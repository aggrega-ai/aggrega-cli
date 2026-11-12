// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

// shrinkDelay sets the backoff base to a sub-millisecond value for the duration
// of a test so retries do not sleep for real seconds, restoring the default
// afterwards.
func shrinkDelay(t *testing.T) {
	t.Helper()
	prev := BaseDelay
	BaseDelay = 1 * time.Millisecond
	t.Cleanup(func() { BaseDelay = prev })
}

var errTransient = errors.New("transient")

// onlyErrTransient classifies errTransient as transient and everything else as
// terminal, isolating the loop's control flow from any production classifier.
func onlyErrTransient(err error) bool { return errors.Is(err, errTransient) }

// TestTheBudgetAndBackoffAreThreeAttemptsFromHalfASecond pins the budget every
// retried step shares: three attempts, the first backoff half a second, doubling.
// A change to either is a change to how long a failing publication waits, so it
// fails here rather than passing unnoticed through every caller.
func TestTheBudgetAndBackoffAreThreeAttemptsFromHalfASecond(t *testing.T) {
	// Not parallel: reads the process-global BaseDelay seam other tests mutate.
	if MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d; want 3", MaxAttempts)
	}
	if BaseDelay != 500*time.Millisecond {
		t.Errorf("BaseDelay = %v; want 500ms", BaseDelay)
	}
}

func TestRetryTransientRetryableThenSuccess(t *testing.T) {
	// Not parallel: mutates the process-global BaseDelay seam other tests read.
	shrinkDelay(t)
	calls := 0
	got, err := Transient(context.Background(), func() (string, error) {
		calls++
		if calls == 1 {
			return "", errTransient
		}
		return "ok", nil
	}, onlyErrTransient)
	if err != nil {
		t.Fatalf("Transient: unexpected error %v", err)
	}
	if got != "ok" {
		t.Errorf("value = %q, want %q", got, "ok")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestRetryTransientExhaustsAttempts(t *testing.T) {
	// Not parallel: mutates the process-global BaseDelay seam other tests read.
	shrinkDelay(t)
	calls := 0
	_, err := Transient(context.Background(), func() (string, error) {
		calls++
		return "", errTransient
	}, onlyErrTransient)
	if !errors.Is(err, errTransient) {
		t.Fatalf("error = %v, want errTransient", err)
	}
	if calls != MaxAttempts {
		t.Errorf("calls = %d, want %d", calls, MaxAttempts)
	}
}

func TestRetryTransientTerminalFailsFast(t *testing.T) {
	// Not parallel: mutates the process-global BaseDelay seam other tests read.
	shrinkDelay(t)
	terminal := errors.New("terminal")
	calls := 0
	_, err := Transient(context.Background(), func() (string, error) {
		calls++
		return "", terminal
	}, onlyErrTransient)
	if !errors.Is(err, terminal) {
		t.Fatalf("error = %v, want terminal", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

// TestRetryTransientCancelledDuringBackoffReturnsAtOnce proves a caller that
// withdraws while a transient failure is backing off gets the context's error at
// once, with no further call. The backoff is set to an hour, so a loop that slept
// it out would never return; the ceiling below is a liveness bound separating
// "returns" from "hangs", not a performance budget.
func TestRetryTransientCancelledDuringBackoffReturnsAtOnce(t *testing.T) {
	// Not parallel: mutates the process-global BaseDelay seam other tests read.
	prev := BaseDelay
	BaseDelay = time.Hour
	t.Cleanup(func() { BaseDelay = prev })

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	done := make(chan error, 1)
	go func() {
		_, err := Transient(ctx, func() (string, error) {
			calls++
			cancel()
			return "", errTransient
		}, onlyErrTransient)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v; want context.Canceled", err)
		}
		if calls != 1 {
			t.Errorf("calls = %d; want 1 (no call after the caller withdrew)", calls)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Transient did not return after the caller withdrew; it slept out the backoff")
	}
}
