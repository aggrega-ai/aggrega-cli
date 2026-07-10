// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build unix

package provindex

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
// It deliberately mirrors the cross-process compile-lock discipline: a single
// exclusive flock over a sidecar lock file, best-effort so any failure to open
// or lock degrades to no serialization rather than failing the caller. The two
// are kept as separate small helpers rather than one shared primitive because
// their lock-path derivations differ (per-guest digest there, one fixed index
// lock here); do not "unify" them into a single call without reconciling that.
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
