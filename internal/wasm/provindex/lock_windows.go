// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build windows

package provindex

import (
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/filelock"
)

// flockIndex takes an exclusive lock on the index lock file under dir and
// returns a release function the caller invokes once the read-modify-write of
// the index has returned. It keeps parallel CLI invocations that share one
// on-disk index from writing a torn file at once.
//
// The acquire blocks until the lock is available. It is best-effort, so any
// failure to open or lock degrades to no serialization rather than failing the
// caller, and it creates no directory: the caller creates the index directory
// before it locks. The lock path is derived here and only the locking is
// shared, because each package that locks keys its lock file differently.
func flockIndex(dir string) func() {
	return filelock.Lock(filepath.Join(dir, lockFileName))
}
