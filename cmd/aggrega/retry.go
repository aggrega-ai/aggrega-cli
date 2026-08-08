// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// maxPublishAttempts bounds how many times a retryable publish step is tried
// before its last error is returned. A publish is a timeout-bounded job, so the
// retry budget is small and fixed: the first call plus at most two retries.
const maxPublishAttempts = 3

// publishRetryBaseDelay is the first backoff interval; each subsequent retry
// doubles it (base, 2x, 4x). It is a package variable rather than a constant so
// tests can shrink it to a sub-millisecond value and not sleep for real
// seconds; production leaves it at the default.
var publishRetryBaseDelay = 500 * time.Millisecond

// fulcioTokenTransientMarker is the substring the keyless certificate
// authority emits when it transiently fails to process an identity token. The
// authority surfaces this as a plain string-wrapped 400 with no typed signal,
// so the only available classification is a defensive substring match — the
// same approach the subject-failure classification uses. A match means a fresh
// token acquisition on the next attempt can succeed, so the step is retryable.
const fulcioTokenTransientMarker = "There was an error processing the identity token"

// signBundle packs the two success values of a signing call — the bundle bytes
// and the referrer artifactType — into one value so the signing retry can flow
// through the single-value generic retry helper.
type signBundle struct {
	bytes        []byte
	artifactType string
}

// retryTransient calls fn up to maxPublishAttempts times, returning its first
// success. When fn fails, retryable decides the disposition: a terminal error
// returns immediately (failing fast protects a timeout-bounded job from
// wasting its budget on a hopeless error), while a retryable error sleeps an
// exponential backoff and tries again. After the final attempt the last error
// is returned unwrapped, so the caller's existing error-wrapping applies to the
// underlying failure. Context cancellation between attempts is honored: a
// cancelled or deadline-exceeded context during a backoff returns the context
// error rather than sleeping out the interval.
func retryTransient[T any](ctx context.Context, fn func() (T, error), retryable func(error) bool) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt < maxPublishAttempts; attempt++ {
		v, err := fn()
		if err == nil {
			return v, nil
		}
		lastErr = err
		if !retryable(err) {
			return zero, err
		}
		if attempt == maxPublishAttempts-1 {
			break
		}
		backoff := publishRetryBaseDelay << attempt
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, lastErr
}

// isRetryablePublishError classifies a sign-path or registry-push failure as
// transient (worth another attempt) or terminal (fail fast). It is the single
// classifier both retry sites consult, so the sign retry and the push retry
// never disagree about which errors are worth retrying.
//
// Terminal (returns false): a nil error; an immutable-tag conflict; an
// authentication denial; a caller cancellation; and, by default, any error not
// positively recognized as transient. Transient (returns true): a registry
// server error (HTTP 5xx); a network timeout; a request-scoped deadline that is
// not the caller's own cancellation; and the keyless authority's transient
// token-processing failure.
func isRetryablePublishError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, oci.ErrImmutableTagConflict()) || errors.Is(err, oci.ErrFetchUnauthorized()) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var resp *errcode.ErrorResponse
	if errors.As(err, &resp) && resp.StatusCode >= 500 {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if strings.Contains(err.Error(), fulcioTokenTransientMarker) {
		return true
	}
	return false
}
