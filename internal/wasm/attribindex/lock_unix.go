// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build unix

package attribindex

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// flockIndex takes an exclusive advisory lock on the index lock file under dir
// and returns a release function the caller invokes once the read-modify-write
// of the index has returned. It keeps parallel CLI invocations that share one
// on-disk index from writing a torn file at once.
//
// It mirrors the provenance index's and the compile lock's cross-process
// discipline: a single exclusive flock over a sidecar lock file, best-effort so
// any failure to open or lock degrades to no serialization rather than failing
// the caller.
func flockIndex(dir string) func() {
	lockPath := filepath.Join(dir, lockFileName)
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
