// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !unix

package wasm

// flockGuestCompile is the no-op cross-process lock for platforms without POSIX
// advisory file locking. The compiler's host builds run on unix; this variant
// exists so the package still compiles for the wasip1 guest toolchain, which
// pulls this package into its dependency graph but never constructs a Plugin.
// Same-guest compilation is still serialized in-process by Loader.lockGuestCompile's
// per-guest mutex; only the cross-process layer is absent here.
func flockGuestCompile(dir, key string) func() {
	return func() {}
}
