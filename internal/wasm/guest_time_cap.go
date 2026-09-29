// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// executionClock is the clock one export invocation measures its guest's
// execution in; startExecutionClock, declared once per platform, decides which
// clock that is. A reading is the time elapsed on the clock since it was
// started, so only the difference between two readings of one clock means
// anything.
//
// It is read from two goroutines at once: the guest's own, which brackets every
// host-function body with a reading, and the ceiling's watchdog, which reads it
// while the guest is still running. An implementation is safe for concurrent
// use.
type executionClock interface {
	now() time.Duration
}

// wallClock is the wall time elapsed since it was started. It is the execution
// clock wherever the loader does not read a thread's processor time.
type wallClock struct {
	origin time.Time
}

// newWallClock starts a wall clock at the current instant.
func newWallClock() wallClock {
	return wallClock{origin: time.Now()}
}

func (c wallClock) now() time.Duration {
	return time.Since(c.origin)
}

// hostTimeAccount accumulates the time one plugin instance spends inside host
// functions during a single export invocation, read on that invocation's
// execution clock.
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
// The account reads the clock the ceiling spends, because a subtraction across
// two clocks measures nothing. Where that clock is the processor time of the
// guest's thread, a host body that waits — on a file, on a lock, on a slow
// seam — advances neither side, and what the account removes is the processor
// time a host body spends on that thread, including a second guest's execution
// run through the producer-consumption seam, which runs on the same thread.
//
// A guest calls host functions synchronously from its own execution, so the
// account is written from one goroutine at a time. The mutex is what lets the
// watchdog read a consistent total — and any interval still in flight — from
// another. depth tolerates a host body that reaches another host function on
// the same instance: only the outermost interval is counted, so no span of
// time is counted twice.
//
// An invocation with no ceiling starts no clock, and there is nothing to
// account: with a nil clock every method leaves the account untouched.
type hostTimeAccount struct {
	mu    sync.Mutex
	clock executionClock
	total time.Duration
	depth int
	since time.Duration
}

// enter records that a host function body has begun on this instance.
func (a *hostTimeAccount) enter() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.clock == nil {
		return
	}
	if a.depth == 0 {
		a.since = a.clock.now()
	}
	a.depth++
}

// leave records that a host function body has returned, folding the outermost
// interval into the running total.
func (a *hostTimeAccount) leave() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.clock == nil {
		return
	}
	a.depth--
	if a.depth == 0 {
		a.total += a.clock.now() - a.since
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
	if a.depth > 0 && a.clock != nil {
		d += a.clock.now() - a.since
	}
	return d
}

// reset clears the account so a fresh export invocation starts from zero, on the
// clock that invocation is measured in. One plugin instance may serve several
// invocations in sequence, and the ceiling is per-invocation, so an account
// carried across them would shrink every later invocation's budget by whatever
// the earlier ones spent.
func (a *hostTimeAccount) reset(clock executionClock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clock = clock
	a.total = 0
	a.depth = 0
}

// callUnderGuestTimeCap invokes call with a context cancelled once the guest has
// EXECUTED for longer than this instance's per-call ceiling, and returns what
// call returns. A zero or negative ceiling runs call under the caller's context
// unchanged.
//
// The budget is spent by guest execution alone, read on one clock for the whole
// invocation (see startExecutionClock). The calling goroutine is locked to its
// OS thread for the call, and guest code runs on the goroutine that invokes it,
// so that thread is where the guest executes. On Linux the clock is that
// thread's processor time; on every other platform it is wall time. On both,
// the watchdog subtracts the time the instance spends inside host functions, so
// a guest that asks the host for a parsed tree is not charged for the host
// loading and running the parser that produces it.
//
// Processor time is what keeps a busy machine from spending the budget. A guest
// waiting for a processor — because other work in the process or on the machine
// holds it, at whatever scheduling priority — spends no processor time, so the
// ceiling measures the thing it names, a guest that will not yield, rather than
// the load it happened to run under. A guest that will not yield still spends
// the budget without pause: outside a host function it has nowhere to wait,
// since the runtime's sleep and yield return at once (see
// deterministicModuleConfig). Where wall time is the measure, the guest is
// charged for waiting it did not cause, and a machine loaded heavily enough can
// spend the budget of a guest that executed for a fraction of it.
//
// The first call on an instance also runs the guest language runtime's one-time
// initialisation, which the runtime performs lazily inside that call. That is
// guest execution, and it is charged to that call's budget.
//
// The watchdog re-arms rather than firing once: each time it wakes it recomputes
// guest time and, when the budget is not yet spent, sleeps for the remainder in
// wall time. That sleep can never outlast the budget: the guest executes on one
// thread, so its clock advances no faster than wall time does, and it advances
// more slowly, or not at all, while the guest waits for a processor or runs a
// host call.
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
	// Locking nests, so a guest call a host function makes on this goroutine
	// keeps the thread locked for the call that is still running around it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	clock := startExecutionClock()
	p.hostTime.reset(clock)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var ceilingSpent atomic.Bool
	start := clock.now()
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
				guest := clock.now() - start - p.hostTime.elapsed()
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
