// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// allowanceEvent is one thing a registry does to the allowance of requests in
// flight to it, and the allowance it must leave — zero where no limit is in
// force.
type allowanceEvent struct {
	// slowDown is a "slow down" answer to a request answered while inFlight
	// requests were in flight, itself included. sameWait says the wait the registry
	// asked for with the answer before has not ended yet.
	slowDown bool
	inFlight int
	sameWait bool
	// successes is a run of that many answers that were not "slow down".
	successes int
	want      int
}

// TestTheAllowanceHalvesOnSlowDownAndRisesByOneUpToTheCeiling drives the one
// registry's shared state (busyHost) through the answers its requests get, one
// sequence per row, and requires the exact allowance after each: a "slow down"
// answer halves the number of requests allowed in flight — from the number in
// flight where no limit was in force — down to one, once per wait the registry
// asks for; a run of as many other answers as the allowance raises it by exactly
// one; and reaching the command's stated ceiling, or with none stated the number it
// was lowered from, lifts the limit.
//
// Each row reads the allowance the package keeps, through the real answer path
// (answered), with no production method added for it. The wait each answer opens
// is an hour, and a row ends it by clearing when the registry may be asked again,
// so no row waits and none depends on a clock.
//
// Planted defects and the row each fails:
//   - lowering to `max(current, 1)` rather than halving fails "lowered from eight
//     in flight to four", and "halved again after each wait, down to one";
//   - raising by two after a run fails "raised by exactly one after as many
//     answers as the allowance";
//   - halving on every "slow down" answer of one wait fails "a second slow-down
//     inside the same wait does not halve again";
//   - recovering to the number in flight rather than the stated ceiling fails "the
//     stated ceiling lifts the limit".
func TestTheAllowanceHalvesOnSlowDownAndRisesByOneUpToTheCeiling(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name    string
		ceiling int
		events  []allowanceEvent
	}{
		{
			name:    "lowered from eight in flight to four",
			ceiling: 8,
			events:  []allowanceEvent{{slowDown: true, inFlight: 8, want: 4}},
		},
		{
			name:    "lowered from one in flight stays at one",
			ceiling: 8,
			events:  []allowanceEvent{{slowDown: true, inFlight: 1, want: 1}},
		},
		{
			name:    "a second slow-down inside the same wait does not halve again",
			ceiling: 8,
			events: []allowanceEvent{
				{slowDown: true, inFlight: 8, want: 4},
				{slowDown: true, inFlight: 4, sameWait: true, want: 4},
			},
		},
		{
			name:    "halved again after each wait, down to one",
			ceiling: 16,
			events: []allowanceEvent{
				{slowDown: true, inFlight: 16, want: 8},
				{slowDown: true, inFlight: 8, want: 4},
				{slowDown: true, inFlight: 4, want: 2},
				{slowDown: true, inFlight: 2, want: 1},
				{slowDown: true, inFlight: 1, want: 1},
			},
		},
		{
			name:    "raised by exactly one after as many answers as the allowance",
			ceiling: 16,
			events: []allowanceEvent{
				{slowDown: true, inFlight: 8, want: 4},
				{successes: 3, want: 4},
				{successes: 1, want: 5},
				{successes: 4, want: 5},
				{successes: 1, want: 6},
			},
		},
		{
			name:    "the stated ceiling lifts the limit",
			ceiling: 6,
			events: []allowanceEvent{
				{slowDown: true, inFlight: 8, want: 4},
				{successes: 4, want: 5},
				{successes: 5, want: 0},
			},
		},
		{
			name: "with no ceiling stated, the number lowered from lifts the limit",
			events: []allowanceEvent{
				{slowDown: true, inFlight: 6, want: 3},
				{successes: 3, want: 4},
				{successes: 4, want: 5},
				{successes: 5, want: 0},
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ctx := WithBusyTiming(context.Background(), BusyWaitLimit, time.Hour, 0)
			const host = "registry.example"
			h := &busyHost{changed: make(chan struct{}), ceiling: row.ceiling}
			slowDown := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
			served := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}
			for i, ev := range row.events {
				if ev.slowDown {
					if !ev.sameWait {
						h.resumeAt = time.Time{}
					}
					h.inFlight = ev.inFlight
					if !h.answered(ctx, host, slowDown, nil) {
						t.Fatalf("event %d: a 429 was not read as a registry asking to slow down", i)
					}
				}
				for range ev.successes {
					h.inFlight = 1
					h.answered(ctx, host, served, nil)
				}
				if h.limit != ev.want {
					t.Fatalf("event %d (%+v): the allowance is %d; want %d", i, ev, h.limit, ev.want)
				}
			}
		})
	}
}
