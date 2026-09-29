// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build linux

package wasm

import (
	"time"

	"golang.org/x/sys/unix"
)

// startExecutionClock returns the clock one export invocation measures its
// guest's execution in: the processor time the calling OS thread spends from
// this moment on, read through the kernel's CPU clock for that thread.
//
// The caller must have locked its goroutine to its OS thread, and must keep it
// locked until the invocation returns. Guest code runs on the goroutine that
// invokes it and no other goroutine runs on a locked thread, so the lock is what
// makes this thread's processor time the time the guest, and the host functions
// it calls, spend executing.
//
// A thread's CPU clock can be read from any thread of the process, which is what
// lets the watchdog read how much the guest has spent while it is still running,
// the interval in progress included. If the clock cannot be read when the
// invocation starts, the invocation is measured in wall time instead.
func startExecutionClock() executionClock {
	c := threadCPUClock{id: threadCPUClockID(unix.Gettid()), wall: newWallClock()}
	origin, err := c.read()
	if err != nil {
		return c.wall
	}
	c.origin = origin
	return c
}

// threadCPUClock is the processor time one OS thread has spent since the clock
// was started.
type threadCPUClock struct {
	id     int32
	origin time.Duration
	// wall is the wall time since the clock was started. It is read only if the
	// thread's own clock stops being readable, which the thread running the
	// invocation does not do while it runs.
	wall wallClock
}

func (c threadCPUClock) now() time.Duration {
	t, err := c.read()
	if err != nil {
		return c.wall.now()
	}
	return t - c.origin
}

// read returns the thread's processor time since the thread began.
func (c threadCPUClock) read() (time.Duration, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(c.id, &ts); err != nil {
		return 0, err
	}
	return time.Duration(ts.Nano()), nil
}

// threadCPUClockID returns the id of the processor-time clock of thread tid of
// the calling process.
//
// The encoding is the kernel's own for a per-thread CPU clock: the complement of
// the thread id shifted past three low bits, which carry the per-thread flag and
// the clock kind — here the scheduler's running-time account, which counts time
// on a processor in both user and kernel mode. It is the id the C library's
// pthread_getcpuclockid returns, which a build without cgo cannot call. Every
// thread id the kernel issues fits the encoding, and the kernel refuses the id of
// a thread outside the calling process.
func threadCPUClockID(tid int) int32 {
	const (
		perThread      = 4
		schedulerClock = 2
	)
	return int32((^uint32(tid))<<3 | perThread | schedulerClock)
}
