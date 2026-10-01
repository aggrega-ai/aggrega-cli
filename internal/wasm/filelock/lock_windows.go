// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build windows

package filelock

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// allBytes is the length, in both halves of the 64-bit range, that the lock
// covers from offset zero: the whole file at any length. Every holder names
// the same range, so every holder contends for the same lock.
const allBytes = ^uint32(0)

// Acquire takes an exclusive lock on lockPath and returns a release function
// the caller invokes once the work the lock guards has finished. The acquire
// BLOCKS until the lock is available, so a second holder waits its turn rather
// than proceeding unprotected.
//
// The lock file's parent directory is created if it does not exist, so a caller
// can name a lock inside a scratch area that no build has populated yet.
//
// It is best-effort: on any failure to create the directory, open the file or
// take the lock it returns a release that does nothing, degrading to no
// cross-process serialization rather than failing the caller.
func Acquire(lockPath string) func() {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return func() {}
	}
	return Lock(lockPath)
}

// Lock is Acquire without the directory creation: it BLOCKS until it holds an
// exclusive lock on lockPath and returns the release function, and it creates
// the lock file but never a directory. A lock file whose directory does not
// exist cannot be opened, so the lock degrades to a release that does nothing.
// Callers whose lock lives in a directory they have already created, or whose
// absence should mean no lock rather than a new directory, use this form.
//
// It is best-effort in the same way as Acquire: any failure to open the file or
// take the lock returns a release that does nothing.
func Lock(lockPath string) func() {
	release, _ := lock(lockPath, windows.LOCKFILE_EXCLUSIVE_LOCK)
	return release
}

// LockShared takes a SHARED lock on lockPath and returns the release function.
// Any number of shared holders hold the lock at once; none of them can while an
// exclusive holder (Acquire, Lock or TryLock) has it, and an exclusive holder
// waits until every shared holder has released. It blocks until it holds the
// lock, and like Lock it creates the lock file but never a directory.
//
// It is for a reader of a file whose writers hold the exclusive lock across
// replacing that file. Windows refuses to replace or remove a file another
// handle holds open, so a reader that kept the file open across a writer's
// replacement would make the writer fail, and a reader opening it during the
// replacement would itself be refused. Taking the lock shared keeps every reader
// out of that window without serializing the readers among themselves.
//
// It is best-effort in the same way as Lock: any failure to open the file or take
// the lock returns a release that does nothing, and the reader proceeds unlocked.
func LockShared(lockPath string) func() {
	release, _ := lock(lockPath, 0)
	return release
}

// TryLock takes an exclusive lock on lockPath WITHOUT waiting for it and
// reports whether it did. When another holder has the lock it returns at once,
// reporting false, so a caller guarding work that only one participant needs to
// do can skip it rather than queue behind it. Like Lock, it creates the lock
// file but never a directory.
//
// Any failure to open the file or take the lock also reports false, with a
// release that does nothing, so the caller's skip path covers an unusable lock
// file as well as a held one.
func TryLock(lockPath string) (func(), bool) {
	return lock(lockPath, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY)
}

// lock is the one place a lock is taken on Windows. It opens lockPath, creating
// the file if needed, and locks the file's whole byte range with flags, which
// choose between an exclusive and a shared lock and between waiting for the lock
// and failing at once when it is held. On any failure it returns a release that
// does nothing and false.
//
// The release unlocks the range before closing the file rather than leaving the
// close to drop the lock, because the operating system frees a lock left on a
// closed handle only eventually, and a waiting holder would wait that long.
//
// The lock belongs to the open handle, so two handles contend for it whether
// they are held by two processes or by one. The operating system enforces the
// lock on reads and writes of the locked range; nothing reads or writes a lock
// file, so in practice it serializes exactly the participants that ask for it.
func lock(lockPath string, flags uint32) (func(), bool) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, false
	}
	h := windows.Handle(f.Fd())
	if err := windows.LockFileEx(h, flags, 0, allBytes, allBytes, new(windows.Overlapped)); err != nil {
		_ = f.Close()
		return func() {}, false
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, allBytes, allBytes, new(windows.Overlapped))
		_ = f.Close()
	}, true
}
