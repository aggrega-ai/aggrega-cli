// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build windows

package filelock

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestLockAndTryLockCreateNoDirectory pins what separates Lock and TryLock from
// Acquire: neither creates the lock file's directory. A lock whose directory is
// missing degrades — Lock to a release that does nothing, TryLock to reporting
// false — and returns at once, leaving the directory absent.
func TestLockAndTryLockCreateNoDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	lockPath := filepath.Join(missing, "guard.lock")

	// Each row takes the lock and reports whether the caller may treat it as
	// held. Lock reports nothing, so its row passes the release through and
	// reports false: a degraded Lock is indistinguishable from no lock.
	cases := []struct {
		name string
		take func() (func(), bool)
	}{
		{"Lock", func() (func(), bool) { return Lock(lockPath), false }},
		{"TryLock", func() (func(), bool) { return TryLock(lockPath) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type result struct {
				release func()
				held    bool
			}
			done := make(chan result, 1)
			go func() {
				release, held := tc.take()
				done <- result{release, held}
			}()
			var r result
			select {
			case r = <-done:
			case <-time.After(5 * time.Second):
				// A liveness ceiling: degrading returns at once, so only a hang
				// reaches it.
				t.Fatal("a lock in a missing directory blocked instead of degrading")
			}
			if r.release == nil {
				t.Fatal("no release function was returned")
			}
			if r.held {
				t.Fatal("reported holding a lock whose file cannot be opened")
			}
			r.release()
			if _, err := os.Stat(missing); !os.IsNotExist(err) {
				t.Fatalf("the lock created its directory (stat: %v); only Acquire may", err)
			}
		})
	}
}

// TestTryLockRefusesWhileHeld proves TryLock never waits: while one handle holds
// the lock a second attempt reports false at once, and after the release a
// further attempt gets the lock. Two handles in one process contend exactly as
// two processes do, because the lock belongs to the handle.
func TestTryLockRefusesWhileHeld(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "guard.lock")

	release, ok := TryLock(lockPath)
	if !ok {
		t.Fatal("TryLock did not get a lock nobody holds")
	}

	done := make(chan bool, 1)
	go func() {
		second, got := TryLock(lockPath)
		if got {
			second()
		}
		done <- got
	}()
	select {
	case got := <-done:
		if got {
			t.Error("a second TryLock got the lock while the first held it")
		}
	case <-time.After(5 * time.Second):
		t.Error("a second TryLock waited instead of reporting that the lock is held")
	}

	release()

	after, ok := TryLock(lockPath)
	if !ok {
		t.Fatal("the lock was not available again after its release")
	}
	after()
}

// TestLockSharedAdmitsReadersTogetherAndExcludesAWriter pins the two halves of
// a shared lock: two shared holders hold it at once, an exclusive holder cannot
// while either does, and a shared holder waits for an exclusive one to release.
//
// The last half is decided by ORDER, not by time. The exclusive holder sets a
// witness immediately before it releases, and the shared taker reports whether
// the witness was set when its lock was granted: a shared lock that waits always
// reports true, and one that did not wait reports false however fast the machine
// is. The short pause before the release only widens the window in which a lock
// that does not wait is caught; it is not an assertion.
func TestLockSharedAdmitsReadersTogetherAndExcludesAWriter(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "guard.lock")

	first := LockShared(lockPath)
	secondDone := make(chan func(), 1)
	go func() { secondDone <- LockShared(lockPath) }()
	var second func()
	select {
	case second = <-secondDone:
	case <-time.After(5 * time.Second):
		// A liveness ceiling: a shared lock granted beside another returns at
		// once, so only a lock that excludes its own kind reaches it.
		first()
		t.Fatal("a second shared holder waited for the first; shared holders must hold the lock together")
	}

	if release, got := TryLock(lockPath); got {
		release()
		t.Fatal("an exclusive lock was granted while two shared holders held the lock")
	}
	first()
	if release, got := TryLock(lockPath); got {
		release()
		t.Fatal("an exclusive lock was granted while one shared holder still held the lock")
	}
	second()

	exclusive, ok := TryLock(lockPath)
	if !ok {
		t.Fatal("the exclusive lock was not available after every shared holder released")
	}
	var released atomic.Bool
	granted := make(chan bool, 1)
	go func() {
		release := LockShared(lockPath)
		granted <- released.Load()
		release()
	}()
	time.Sleep(300 * time.Millisecond)
	released.Store(true)
	exclusive()
	select {
	case afterRelease := <-granted:
		if !afterRelease {
			t.Fatal("a shared lock was granted while an exclusive holder still held the lock")
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("a shared lock was never granted after the exclusive holder released (liveness ceiling)")
	}
}
