// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// shrinkRetryDelay sets the backoff base to a sub-millisecond value for the
// duration of a test so retries do not sleep for real seconds, restoring the
// production default afterward.
func shrinkRetryDelay(t *testing.T) {
	t.Helper()
	prev := publishRetryBaseDelay
	publishRetryBaseDelay = 1 * time.Millisecond
	t.Cleanup(func() { publishRetryBaseDelay = prev })
}

// timeoutError is a net.Error whose Timeout reports true, standing in for a
// transient network timeout in the classifier table.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var errRetryable = errors.New("retryable")

// alwaysRetryable classifies errRetryable as retryable and everything else as
// terminal, isolating retryTransient's loop control from the production
// classifier.
func alwaysRetryable(err error) bool { return errors.Is(err, errRetryable) }

func TestRetryTransientRetryableThenSuccess(t *testing.T) {
	// Not parallel: mutates the process-global publishRetryBaseDelay seam other tests read.
	shrinkRetryDelay(t)
	calls := 0
	got, err := retryTransient(context.Background(), func() (string, error) {
		calls++
		if calls == 1 {
			return "", errRetryable
		}
		return "ok", nil
	}, alwaysRetryable)
	if err != nil {
		t.Fatalf("retryTransient: unexpected error %v", err)
	}
	if got != "ok" {
		t.Errorf("value = %q, want %q", got, "ok")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestRetryTransientExhaustsAttempts(t *testing.T) {
	// Not parallel: mutates the process-global publishRetryBaseDelay seam other tests read.
	shrinkRetryDelay(t)
	calls := 0
	_, err := retryTransient(context.Background(), func() (string, error) {
		calls++
		return "", errRetryable
	}, alwaysRetryable)
	if !errors.Is(err, errRetryable) {
		t.Fatalf("error = %v, want errRetryable", err)
	}
	if calls != maxPublishAttempts {
		t.Errorf("calls = %d, want %d", calls, maxPublishAttempts)
	}
}

func TestRetryTransientTerminalFailsFast(t *testing.T) {
	// Not parallel: mutates the process-global publishRetryBaseDelay seam other tests read.
	shrinkRetryDelay(t)
	terminal := errors.New("terminal")
	calls := 0
	_, err := retryTransient(context.Background(), func() (string, error) {
		calls++
		return "", terminal
	}, alwaysRetryable)
	if !errors.Is(err, terminal) {
		t.Fatalf("error = %v, want terminal", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestIsRetryablePublishError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"immutable-tag conflict", oci.ErrImmutableTagConflict, false},
		{"auth denied", oci.ErrFetchUnauthorized, false},
		{"registry 503", &errcode.ErrorResponse{StatusCode: 503}, true},
		{"registry 403", &errcode.ErrorResponse{StatusCode: 403}, false},
		{"net timeout", timeoutError{}, true},
		{"fulcio token 400", errors.New(`signing: keyless bundle: Fulcio returned 400: {"code":3,"message":"There was an error processing the identity token"}`), true},
		{"arbitrary", errors.New("nope"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryablePublishError(tc.err); got != tc.want {
				t.Errorf("isRetryablePublishError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
