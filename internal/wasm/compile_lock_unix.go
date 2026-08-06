// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build unix

package wasm

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
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
	lockPath := filepath.Join(dir, ".compile-"+digest.BareHex(key)+".lock")
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

// flockCompileCacheReclaim tries to take an exclusive advisory lock on the
// reclamation lock file under dir, reporting whether it got it. It is the
// cross-process half of holding a shared compiled-module cache to its size cap:
// when several processes start against one cache directory at once, exactly one
// of them walks and trims it and the others get on with their work.
//
// The acquire is NON-BLOCKING, and that is the point. A process that cannot take
// the lock skips reclamation rather than waiting for it, because another process
// is already doing the same job and waiting would buy nothing. This lock is an
// efficiency measure, not a safety one — removing an entry another process wants
// costs that process a recompile and never a failed read — so skipping is always
// a correct outcome.
//
// It is a different lock file from the per-guest compile locks, so reclamation
// and compilation never contend: a trim in progress does not hold up a compile,
// and a compile does not hold up a trim. On any failure to open or lock the file
// it reports false, so an unwritable cache directory means no reclamation rather
// than a failure.
func flockCompileCacheReclaim(dir string) (func(), bool) {
	lockPath := filepath.Join(dir, compileCacheReclaimLockName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, false
	}
	fd := int(f.Fd())
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return func() {}, false
	}
	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = f.Close()
	}, true
}
