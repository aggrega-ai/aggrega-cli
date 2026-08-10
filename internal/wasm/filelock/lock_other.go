// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !unix

package filelock

// Acquire is the no-op cross-process lock for platforms without POSIX advisory
// file locking. It mirrors the degradation the unix variant already applies to
// an unopenable or unlockable file: the caller proceeds with whatever weaker
// serialization it has rather than failing.
//
// The compiler's host builds run on unix; this variant exists so packages that
// lock can still compile for the wasip1 guest toolchain, which pulls them into
// its dependency graph but performs none of the writes the lock guards.
func Acquire(lockPath string) func() {
	return func() {}
}
