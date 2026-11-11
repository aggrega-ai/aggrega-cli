// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package retry calls an operation again when it fails in a way a later attempt
// can succeed, with a small fixed budget and an exponential backoff.
//
// It holds the one retry loop the publication path and the registry index walk
// use. Which failures are worth another attempt is not decided here: the caller
// passes the classifier, because that answer belongs to whoever knows what the
// operation talks to.
package retry

import (
	"context"
	"time"
)

// MaxAttempts bounds how many times Transient calls an operation before it
// returns the operation's last error: the first call plus at most two retries.
// A publication is a timeout-bounded job, so the budget is small and fixed; the
// registry index walk reads under the same budget, so one bound says how often
// any registry request is attempted after a transient failure. A registry asking
// to slow down is not such a failure and spends none of it.
const MaxAttempts = 3

// BaseDelay is the first backoff interval; each later retry doubles it, so an
// operation that fails every attempt waits BaseDelay and then twice BaseDelay
// before its last error is returned. It is a variable rather than a constant so
// a test can shrink it and not sleep for real seconds; production leaves it at
// the default.
var BaseDelay = 500 * time.Millisecond

// Transient calls fn up to MaxAttempts times and returns its first success.
//
// When fn fails, transient decides what happens next. An error it reports as
// not transient is returned at once, so a hopeless failure does not spend a
// timeout-bounded job's budget. A transient error is followed by a backoff and
// another call. After the last attempt the last error is returned unwrapped, so
// the caller's own wrapping applies to the underlying failure.
//
// It waits its own backoff and nothing else. A registry asking to be left alone
// for a while is answered before any failure reaches this loop, by the registry
// client's handling of a registry that is too busy, so a wait the registry asked
// for is never taken here as well.
//
// A context that is cancelled or past its deadline during a backoff ends the
// wait at once: Transient returns the context's error rather than sleeping out
// the interval or making another call.
func Transient[T any](ctx context.Context, fn func() (T, error), transient func(error) bool) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt < MaxAttempts; attempt++ {
		v, err := fn()
		if err == nil {
			return v, nil
		}
		lastErr = err
		if !transient(err) {
			return zero, err
		}
		if attempt == MaxAttempts-1 {
			break
		}
		timer := time.NewTimer(BaseDelay << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, lastErr
}
