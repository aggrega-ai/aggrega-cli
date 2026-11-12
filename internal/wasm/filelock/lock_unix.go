// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build unix

package filelock

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Acquire takes an exclusive advisory lock on lockPath and returns a release
// function the caller invokes once the work the lock guards has finished. The
// acquire BLOCKS until the lock is available, so a second holder waits its turn
// rather than proceeding unprotected.
//
// The lock file's parent directory is created if it does not exist, so a caller
// can name a lock inside a scratch area that no build has populated yet.
//
// It is best-effort: on any failure to create the directory, open the file or
// take the lock it returns a release that does nothing, degrading to no
// cross-process serialization rather than failing the caller.
//
// The lock is advisory, so it serializes only participants that ask for it. It
// therefore protects a directory from concurrent writers that all route through
// this helper, and not from an unrelated process writing the same directory
// directly.
func Acquire(lockPath string) func() {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return func() {}
	}
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
