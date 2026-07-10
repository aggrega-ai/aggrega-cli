// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !unix

package provindex

// flockIndex is the no-op cross-process lock for platforms without POSIX
// advisory file locking. It mirrors the compile-lock package's non-unix variant:
// the compiler's host builds run on unix, and this variant exists so the package
// still compiles for the wasip1 guest toolchain, which pulls it into its
// dependency graph but never writes the index.
func flockIndex(dir string) func() {
	return func() {}
}
