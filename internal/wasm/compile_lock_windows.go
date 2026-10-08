// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build windows

package wasm

import (
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/filelock"
)

// flockGuestCompile takes an exclusive lock on a per-guest lock file under dir
// and returns a release function the caller invokes once the guest's compile
// has returned. It backs Loader.lockGuestFile: when several processes share one
// on-disk compilation cache directory, the file lock keeps them from compiling
// the same guest at the same time, while distinct guests (distinct lock files)
// still proceed in parallel. The cache entry is whole either way — the runtime
// publishes it by rename — so what the lock saves is the duplicated compile.
//
// The acquire blocks until the lock is available. It is best-effort: on any
// failure to open or lock the file it returns a no-op release, and the compile
// proceeds without it rather than failing the load. It creates no directory, so
// a cache directory that does not exist means no lock. The lock file is named by
// guestCompileLockName.
func flockGuestCompile(dir, key string) func() {
	return filelock.Lock(filepath.Join(dir, guestCompileLockName(key)))
}

// flockCompileCacheReclaim tries to take an exclusive lock on the reclamation
// lock file under dir, reporting whether it got it. It is the cross-process half
// of holding a shared compiled-module cache to its size cap: when several
// processes start against one cache directory at once, exactly one of them walks
// and trims it and the others get on with their work.
//
// The acquire is NON-BLOCKING, and that is the point. A process that cannot take
// the lock skips reclamation rather than waiting for it, because another process
// is already doing the same job and waiting would buy nothing. This lock is an
// efficiency measure, not a safety one, so skipping is always a correct outcome.
//
// It is a different lock file from the per-guest compile locks, so reclamation
// and compilation never contend. On any failure to open or lock the file it
// reports false, so an unwritable cache directory means no reclamation rather
// than a failure.
func flockCompileCacheReclaim(dir string) (func(), bool) {
	return filelock.TryLock(filepath.Join(dir, compileCacheReclaimLockName))
}
