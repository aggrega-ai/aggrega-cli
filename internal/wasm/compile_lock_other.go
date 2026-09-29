// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !unix

package wasm

// flockGuestCompile is the no-op cross-process lock for platforms without POSIX
// advisory file locking. The compiler's host builds run on unix; this variant
// exists so the package still compiles for the wasip1 guest toolchain, which
// pulls this package into its dependency graph but never constructs a Plugin.
// Builds of one guest still take turns in-process (see Loader.firstLoadGate);
// only the cross-process layer is absent here.
func flockGuestCompile(dir, key string) func() {
	return func() {}
}

// flockCompileCacheReclaim is the lock-free variant of the compiled-module
// cache's reclamation lock for platforms without POSIX advisory file locking. It
// reports true — reclamation PROCEEDS here — because the lock is an efficiency
// measure rather than a safety one: it keeps several processes from each trimming
// the same directory at once, and without it the worst outcome is that two
// processes both trim and between them remove more than either needed, which
// costs a recompile and never a failed read. Reporting false instead would leave
// the cache uncapped on these platforms, which is the worse trade.
func flockCompileCacheReclaim(dir string) (func(), bool) {
	return func() {}, true
}
