// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !unix && !windows

package provindex

// flockIndex is the no-op cross-process lock for platforms with no file
// locking. It mirrors the compile-lock package's no-op variant: the compiler's
// host builds lock on unix and on Windows, and this variant exists so the
// package still compiles for the wasip1 guest toolchain, which pulls it into its
// dependency graph but never writes the index.
func flockIndex(dir string) func() {
	return func() {}
}

// readLockIndex is the reader's half of the index lock, a no-op here for the
// same reason flockIndex is.
func readLockIndex(dir string) func() {
	return func() {}
}
