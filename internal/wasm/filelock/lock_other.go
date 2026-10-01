// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !unix && !windows

package filelock

// Acquire is the no-op cross-process lock for platforms with no file locking.
// It mirrors the degradation the locking variants already apply to an
// unopenable or unlockable file: the caller proceeds with whatever weaker
// serialization it has rather than failing.
//
// The compiler's host builds lock on unix and on Windows; this variant exists so
// packages that lock can still compile for the wasip1 guest toolchain, which
// pulls them into its dependency graph but performs none of the writes the lock
// guards.
func Acquire(lockPath string) func() {
	return func() {}
}
