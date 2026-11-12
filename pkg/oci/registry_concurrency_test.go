// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// eachAtOnceLiveness separates a run that returns from one that does not. It is
// not a performance budget: every row returns in microseconds where the calls run
// at once as they should, while in a run of one call at a time the first calls
// each wait this long for company that never comes, so the run outlasts the
// bound the test gives EachAtOnce as a whole.
const eachAtOnceLiveness = 30 * time.Second

// TestEachAtOnceCallsEveryIndexOnceWithAtMostTheLimitInFlight runs EachAtOnce over
// rows of n and limit, with a counting call and no clock: each of the first
// min(n, limit) calls waits until that many are in flight at once, which only a run
// keeping that many in flight ever reaches. Every index must be called exactly
// once, the most calls ever in flight must equal min(n, limit), and every call's
// context must state that number as the registry allowance's ceiling.
//
// Planted defects and the rows they fail:
//   - calls made one at a time never reach two in flight, so the first calls each
//     wait out the liveness bound and "more entities than the bound" fails, on the
//     run's own bound and on its peak;
//   - no bound at all lets more than the limit in flight and fails the peak check of
//     "more entities than the bound";
//   - an index skipped or called twice fails the count of every row with n > 0;
//   - a ceiling not stated, or stated as the limit rather than the calls that can
//     run, fails the ceiling check of "fewer entities than the bound".
func TestEachAtOnceCallsEveryIndexOnceWithAtMostTheLimitInFlight(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name     string
		n, limit int
	}{
		{name: "more entities than the bound", n: 3*RegistryRequestsAtOnce + 1, limit: RegistryRequestsAtOnce},
		{name: "fewer entities than the bound", n: 3, limit: RegistryRequestsAtOnce},
		{name: "a bound of one", n: 5, limit: 1},
		{name: "a single entity", n: 1, limit: RegistryRequestsAtOnce},
		{name: "no entities", n: 0, limit: RegistryRequestsAtOnce},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			want := max(1, min(row.n, row.limit))
			var (
				mu       sync.Mutex
				inFlight int
				peak     int
				full     = make(chan struct{})
				fullOnce sync.Once
				calls    = make([]int, row.n)
				ceilings = make([]int, row.n)
			)
			done := make(chan struct{})
			go func() {
				defer close(done)
				EachAtOnce(context.Background(), row.n, row.limit, func(ctx context.Context, i int) {
					mu.Lock()
					inFlight++
					peak = max(peak, inFlight)
					calls[i]++
					ceilings[i] = ceilingFrom(ctx)
					if inFlight == want {
						fullOnce.Do(func() { close(full) })
					}
					mu.Unlock()
					if i < want {
						select {
						case <-full:
						case <-time.After(eachAtOnceLiveness):
						}
					}
					mu.Lock()
					inFlight--
					mu.Unlock()
				})
			}()
			select {
			case <-done:
			case <-time.After(2 * eachAtOnceLiveness):
				t.Fatal("EachAtOnce did not return")
			}
			for i, c := range calls {
				if c != 1 {
					t.Errorf("index %d was called %d times; want once", i, c)
				}
				if ceilings[i] != want {
					t.Errorf("index %d was called under a ceiling of %d; want %d", i, ceilings[i], want)
				}
			}
			if row.n > 0 && peak != want {
				t.Errorf("the most calls in flight at once was %d; want %d", peak, want)
			}
			select {
			case <-full:
			default:
				if row.n > 0 {
					t.Errorf("the calls never reached %d in flight at once", want)
				}
			}
		})
	}
}

// TestAPanickingCallIsRaisedAgainOnTheCallersGoroutine plants panicking calls and
// requires the caller's own recover to see the panic of the lowest index that
// panicked, with the value it was raised with, whichever panicked first. Where two
// calls panic at once, the earlier index panics only after the later one has, so a
// run raising the first panic in time raises the wrong one.
//
// Planted defects and the rows they fail:
//   - a call's panic left on the goroutine EachAtOnce started: the test binary
//     itself dies on "two calls panic, the later index first", since nothing the
//     caller runs can recover it;
//   - the panic raised again is the first in time rather than the lowest index:
//     "two calls panic, the later index first";
//   - a panic that stops the other calls at once: "two calls panic, the later index
//     first", whose other indices must each still be called once;
//   - a bound of one that keeps calling after the panicking index: "a bound of one".
func TestAPanickingCallIsRaisedAgainOnTheCallersGoroutine(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name     string
		n, limit int
		// panics are the indices whose call panics, the later one first where there
		// are two.
		panics []int
		want   string
	}{
		{name: "two calls panic, the later index first", n: 3 * RegistryRequestsAtOnce, limit: RegistryRequestsAtOnce,
			panics: []int{5, 9}, want: "panic at index 5"},
		{name: "a bound of one", n: 5, limit: 1, panics: []int{2}, want: "panic at index 2"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			laterPanicked := make(chan struct{})
			var mu sync.Mutex
			calls := make([]int, row.n)
			got := func() (raised any) {
				defer func() { raised = recover() }()
				EachAtOnce(context.Background(), row.n, row.limit, func(_ context.Context, i int) {
					mu.Lock()
					calls[i]++
					mu.Unlock()
					switch {
					case len(row.panics) == 2 && i == row.panics[1]:
						close(laterPanicked)
						panic(fmt.Sprintf("panic at index %d", i))
					case i == row.panics[0]:
						if len(row.panics) == 2 {
							select {
							case <-laterPanicked:
							case <-time.After(eachAtOnceLiveness):
							}
						}
						panic(fmt.Sprintf("panic at index %d", i))
					}
				})
				return nil
			}()
			if got != row.want {
				t.Fatalf("the caller recovered %v; want %q", got, row.want)
			}
			for i, c := range calls {
				want := 1
				if row.limit == 1 && i > row.panics[0] {
					want = 0
				}
				if c != want {
					t.Errorf("index %d was called %d times; want %d", i, c, want)
				}
			}
		})
	}
}
