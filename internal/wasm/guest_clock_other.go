// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !linux

package wasm

// startExecutionClock returns the clock one export invocation measures its
// guest's execution in. Only a Linux build reads a thread's processor time, so
// here the guest is measured in the wall time elapsed from this moment, with the
// time spent inside host functions still subtracted. A guest waiting for a
// processor on a busy machine therefore spends its budget on this platform
// while it waits.
func startExecutionClock() executionClock {
	return newWallClock()
}
