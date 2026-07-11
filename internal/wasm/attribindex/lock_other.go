// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !unix

package attribindex

// flockIndex is the no-op cross-process lock for platforms without POSIX
// advisory file locking. It mirrors the provenance index's non-unix variant:
// the compiler's host builds run on unix, and this variant exists so the
// package still compiles for the wasip1 guest toolchain, which never writes the
// index.
func flockIndex(dir string) func() {
	return func() {}
}
