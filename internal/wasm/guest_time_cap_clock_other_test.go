// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build !linux

package wasm

import (
	"runtime"
	"testing"
	"time"
)

// SpendHostTime spends d on the per-call ceiling's own clock from inside a host
// function body, standing in for host work a guest has asked for. Off Linux the
// ceiling reads wall time, so a host body that sleeps spends it.
//
// It is exported from a test file only so that this package's external tests can
// drive a host function body with it.
func SpendHostTime(d time.Duration) {
	time.Sleep(d)
}

// TestGuestCeilingIsSpentByProcessorTimeNotStarvation is the Linux proof that the
// per-call ceiling is spent by the guest's processor time. Only a Linux build reads
// a thread's processor time, so on this platform the ceiling is wall time and
// there is nothing for the proof to show.
func TestGuestCeilingIsSpentByProcessorTimeNotStarvation(t *testing.T) {
	t.Skipf("the per-call ceiling reads a thread's processor time only on linux; on %s it is measured in wall time, which a guest waiting for a processor spends by design", runtime.GOOS)
}
