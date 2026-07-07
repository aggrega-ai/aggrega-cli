// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build unix

package wasm

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// flockGuestCompile takes an exclusive advisory lock on a per-guest lock file
// under dir and returns a release function the caller invokes once
// compilation/instantiation has returned. It backs the cross-process layer of
// Loader.lockGuestCompile: when several processes share one on-disk compilation
// cache directory, the file lock keeps them from compiling the same guest into a
// torn cache entry at once, while distinct guests (distinct lock files) still
// proceed in parallel.
//
// It is best-effort: on any failure to open or lock the file it returns a no-op
// release, degrading to the in-process-only serialization the in-process mutex
// already provides rather than failing the load. The lock file name is derived
// from the guest content digest with the algorithm prefix stripped so it is a
// plain filesystem-safe token.
func flockGuestCompile(dir, key string) func() {
	lockPath := filepath.Join(dir, ".compile-"+strings.TrimPrefix(key, "sha256:")+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	fd := int(f.Fd())
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		_ = f.Close()
		return func() {}
	}
	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = f.Close()
	}
}
