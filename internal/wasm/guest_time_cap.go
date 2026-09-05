// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// hostTimeAccount accumulates the wall time one plugin instance spends inside
// host functions during a single export invocation.
//
// It exists because the per-call ceiling bounds GUEST execution while a host
// function body runs HOST code, and the two are not the same measurement. A
// host function resolves a source, serves an external input, or — for the
// producer-consumption seam — loads, describes and runs a second guest before
// returning a tree to the caller. That work is unbounded from the calling
// guest's point of view and is not the guest executing, so charging it to the
// guest's ceiling makes the ceiling report the host's cost under the guest's
// name: the cost grows with machine load, and the guest that asked for the
// service is the one blamed for it.
//
// A guest calls host functions synchronously from its own execution, so the
// account is written from one goroutine at a time. The mutex is what lets the
// watchdog read a consistent total — and any interval still in flight — from
// another. depth tolerates a host body that reaches another host function on
// the same instance: only the outermost interval is counted, so no span of
// time is counted twice.
type hostTimeAccount struct {
	mu    sync.Mutex
	total time.Duration
	depth int
	since time.Time
}

// enter records that a host function body has begun on this instance.
func (a *hostTimeAccount) enter() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.depth == 0 {
		a.since = time.Now()
	}
	a.depth++
}

// leave records that a host function body has returned, folding the outermost
// interval into the running total.
func (a *hostTimeAccount) leave() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.depth--
	if a.depth == 0 {
		a.total += time.Since(a.since)
	}
}

// elapsed reports the host time this instance has accumulated, INCLUDING the
// interval currently in flight.
//
// Counting the in-flight interval is load-bearing rather than a refinement:
// without it a guest blocked in a slow host call reads as a guest that has been
// executing all along, which is exactly the reading this account exists to
// prevent — and it would misfire hardest on the slowest host call, the one case
// where the distinction matters.
func (a *hostTimeAccount) elapsed() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.total
	if a.depth > 0 {
		d += time.Since(a.since)
	}
	return d
}

// reset clears the account so a fresh export invocation starts from zero. One
// plugin instance may serve several invocations in sequence, and the ceiling is
// per-invocation, so an account carried across them would shrink every later
// invocation's budget by whatever the earlier ones spent.
func (a *hostTimeAccount) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.total = 0
	a.depth = 0
}

// callUnderGuestTimeCap invokes call with a context cancelled once the guest has
// EXECUTED for longer than this instance's per-call ceiling, and returns what
// call returns. A zero or negative ceiling runs call under the caller's context
// unchanged.
//
// The budget is spent by guest execution alone. The watchdog subtracts the time
// the instance spends inside host functions from the wall clock, so a guest that
// asks the host for a parsed tree is not charged for the host loading and
// running the parser that produces it. The ceiling therefore measures the thing
// it names — a guest that will not yield — rather than the cost of whatever the
// host does on the guest's behalf.
//
// The watchdog re-arms rather than firing once: each time it wakes it recomputes
// guest time and, when the budget is not yet spent, sleeps for the remainder. It
// converges because host time only ever grows, so each re-arm is for a shorter
// interval than the last unless the guest is inside a host call — and while it
// is, no guest time accrues to spend.
//
// Cancelling the context is what stops a runaway guest: the runtime is
// configured to close a module when its call context is done, so cancellation
// unwinds a guest that would not otherwise return. That configuration and this
// ceiling ship together; neither is a cancellation mechanism on its own.
//
// A call the ceiling cancels is reported as such rather than surfacing the
// runtime's bare cancellation text, which says only that a module closed and
// leaves the author to guess why. The wrap names the ceiling that was spent and
// the field that raises it, and it keeps the underlying error wrapped so a
// caller matching on the runtime's own error still matches.
func (p *Plugin) callUnderGuestTimeCap(ctx context.Context, funcName string, call func(context.Context) error) error {
	if p.callTimeout <= 0 {
		return call(ctx)
	}
	p.hostTime.reset()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var ceilingSpent atomic.Bool
	start := time.Now()
	returned := make(chan struct{})
	defer close(returned)

	go func() {
		timer := time.NewTimer(p.callTimeout)
		defer timer.Stop()
		for {
			select {
			case <-returned:
				return
			case <-timer.C:
				guest := time.Since(start) - p.hostTime.elapsed()
				if guest >= p.callTimeout {
					ceilingSpent.Store(true)
					cancel()
					return
				}
				timer.Reset(p.callTimeout - guest)
			}
		}
	}()

	err := call(ctx)
	if err != nil && ceilingSpent.Load() {
		return fmt.Errorf("wasm: plugin %q call to %s ran past its %s execution ceiling and was cancelled; a plugin that legitimately runs longer raises spec.runtime.timeoutMs: %w",
			p.name, funcName, p.callTimeout, err)
	}
	return err
}
