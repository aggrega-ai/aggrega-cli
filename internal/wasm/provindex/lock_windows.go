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

// readLockIndex takes the same index lock SHARED for a reader and returns the
// release the reader invokes once it has closed the index file. Windows refuses
// to replace a file another handle holds open, so a reader holding the index
// across a writer's write-temp-rename would make the writer's rename fail, and a
// reader opening the index while that rename holds it would itself be refused.
// Holding the lock shared keeps every reader out of that window: the writer holds
// it exclusively from before it writes until after it renames, and readers still
// read side by side.
//
// Like flockIndex it blocks, creates no directory, and degrades to no lock on any
// failure, in which case the reader reads unlocked.
func readLockIndex(dir string) func() {
	return filelock.LockShared(filepath.Join(dir, lockFileName))
}
