// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build linux

package wasm

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// callingThreadProcessorTime returns the processor time the calling OS thread
// has spent since it began.
//
// It reads the thread's clock on its own rather than through the loader's
// execution clock, and the near-duplicate is deliberate: a check that read the
// clock through the code under test would agree with that code whatever the
// code measured. It panics rather than failing a test because it is also called
// from inside a host function body, where a test cannot be stopped cleanly; the
// runtime turns the panic into the call's error.
func callingThreadProcessorTime() time.Duration {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &ts); err != nil {
		panic("read the calling thread's processor time: " + err.Error())
	}
	return time.Duration(ts.Nano())
}

// SpendHostTime spends d on the per-call ceiling's own clock from inside a host
// function body, standing in for host work a guest has asked for. On Linux the
// ceiling reads the processor time of the thread the guest runs on, and a host
// body runs on that same thread, so this burns d of that thread's processor
// time. A body that slept instead would spend none of it, and a ceiling that
// forgot to subtract host time could not then be caught.
//
// It is exported from a test file only so that this package's external tests can
// drive a host function body with it.
func SpendHostTime(d time.Duration) {
	start := callingThreadProcessorTime()
	for callingThreadProcessorTime()-start < d {
	}
}

// starveProcessor leaves the calling test one processor, shared with spinners
// goroutines that never block, until the test ends.
func starveProcessor(t *testing.T, spinners int) {
	t.Helper()
	prev := runtime.GOMAXPROCS(1)
	var stop atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < spinners; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
			}
		}()
	}
	t.Cleanup(func() {
		stop.Store(true)
		wg.Wait()
		runtime.GOMAXPROCS(prev)
	})
}

// The constants of TestGuestCeilingIsSpentByProcessorTimeNotStarvation, with the
// figures they were chosen against. Measured 2026-09-29 against aggrega-cli
// 67c88d88 with this clock applied, on a 24-processor Linux host under a load
// average near 31, five trials per row, seven spinners on one processor:
//
//	clock                     ceiling  processor time at cancellation  wall time of the call
//	thread processor time     100 ms   1.03-1.11 x the ceiling         11.7-16.3 x the ceiling
//	wall time (defect)        100 ms   0.09-0.23 x the ceiling         1.6-3.3 x the ceiling
//	thread processor time      50 ms   1.04-1.23 x the ceiling         16.9-19.8 x the ceiling
//	wall time (defect)         50 ms   0.22-0.50 x the ceiling         3.3-6.6 x the ceiling
const (
	// starvationCeiling is the guest's budget. Under the wall-time defect the
	// reading at cancellation is the guest's share of the processor plus about one
	// scheduling slice (10 ms) while the cancellation lands, so a larger budget
	// leaves the defect further below it: 100 ms kept the defect at a quarter of
	// the budget or less where 50 ms let it reach half.
	starvationCeiling = 100 * time.Millisecond

	// starvationSpinners holds the guest to about an eighth of the one processor,
	// so the call takes about eight times its budget in wall time on an idle host
	// (a derived figure; the loaded host above measured 11.7 or more).
	starvationSpinners = 7

	// starvationWallFloor is the premise: the call must take at least this many
	// times its budget in wall time, or the contention did not starve the guest
	// and the run cannot tell the two clocks apart. Chosen at three against the
	// derived eight on an idle host.
	starvationWallFloor = 3

	// starvationLiveness separates a call that returns from one that hangs. It is
	// wall time bounding a budget spent in processor time, so it must allow for a
	// thread starved by the host as well as by this test.
	starvationLiveness = 2 * time.Minute
)

// TestGuestCeilingIsSpentByProcessorTimeNotStarvation proves that on Linux the
// per-call ceiling is spent by the guest's processor time and not by the time the
// guest waits for one. A guest that spins forever runs under a 100 ms ceiling
// while seven goroutines spinning in the same process share its only processor
// (GOMAXPROCS=1), so the call takes many times its budget in wall time. It must
// still be cancelled — a guest that will not yield is stopped whatever the load —
// and only once its thread has spent the whole budget executing.
//
// The discriminator is the processor time read around the call, and it is
// structural rather than a timing threshold. A ceiling that spends processor time
// cancels no earlier than the moment the guest's thread has spent 100 ms, so the
// reading is at least that much at any machine speed and under any load. A
// ceiling that spends wall time cancels once 100 ms have passed, of which the
// guest ran for about an eighth, so its reading falls well short of the budget.
// The two designs therefore differ in the value read rather than in how long the
// test took. Switching startExecutionClock back to the wall clock turns this test
// red.
//
// The one wall-time comparison is the premise, checked after the discriminator so
// that the defect is reported as itself: a call shorter than three times its
// budget was not starved, and a pass over it would say nothing.
func TestGuestCeilingIsSpentByProcessorTimeNotStarvation(t *testing.T) {
	ctx := context.Background()
	loader := &Loader{}
	plugin, err := loader.Load(ctx, PluginSpec{
		Name:      "spin-loop",
		Bytes:     spinLoopWasm,
		TimeoutMs: uint64(starvationCeiling / time.Millisecond),
	})
	if err != nil {
		t.Fatalf("Load with an explicit ceiling should succeed; got %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })

	starveProcessor(t, starvationSpinners)

	type outcome struct {
		wall, processor time.Duration
		err             error
	}
	done := make(chan outcome, 1)
	go func() {
		// The call locks its goroutine to the thread itself; locking first as well
		// is what makes the two readings below that same thread's.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		processorBefore := callingThreadProcessorTime()
		wallBefore := time.Now()
		_, err := plugin.Call(ctx, "spin", nil)
		done <- outcome{
			wall:      time.Since(wallBefore),
			processor: callingThreadProcessorTime() - processorBefore,
			err:       err,
		}
	}()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(starvationLiveness):
		t.Fatalf("a spinning guest under a %v ceiling did not return within %v", starvationCeiling, starvationLiveness)
	}

	if got.err == nil {
		t.Fatalf("a spinning guest must be cancelled by its ceiling while starved; Call returned nil error")
	}
	if !strings.Contains(got.err.Error(), "execution ceiling and was cancelled") {
		t.Fatalf("Call error = %v; want the ceiling's cancellation", got.err)
	}
	if got.processor < starvationCeiling {
		t.Fatalf("the guest was cancelled after spending %v of processor time, short of its %v ceiling, in a call that took %v: the budget was spent by the time the guest waited for a processor, not by the guest executing",
			got.processor, starvationCeiling, got.wall)
	}
	if got.wall < starvationWallFloor*starvationCeiling {
		t.Fatalf("test premise broken: the call took %v, less than %d times its %v ceiling, so the contention did not starve the guest and this run cannot tell the two clocks apart",
			got.wall, starvationWallFloor, starvationCeiling)
	}
}
