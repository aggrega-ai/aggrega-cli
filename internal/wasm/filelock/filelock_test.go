// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package filelock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAcquireSerializesHolders proves the lock is exclusive and that a second
// holder WAITS rather than proceeding unprotected. The waiting is the whole
// point: a caller that guards the production of bytes has to take its turn,
// where a caller guarding an efficiency measure could skip instead.
//
// The two holders are goroutines opening the lock file separately. An advisory
// lock is associated with the open file description rather than the process, so
// two independent opens contend exactly as two processes do.
func TestAcquireSerializesHolders(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "guard.lock")

	release := Acquire(lockPath)
	if !locksAvailable(t, lockPath) {
		release()
		t.Skip("this platform has no advisory file locking, so builds serialize in-process only by design")
	}

	secondHeld := make(chan struct{})
	go func() {
		secondRelease := Acquire(lockPath)
		close(secondHeld)
		secondRelease()
	}()

	select {
	case <-secondHeld:
		t.Fatal("a second holder took the lock while the first still held it")
	case <-time.After(250 * time.Millisecond):
		// Still waiting, which is the required behaviour.
	}

	release()

	select {
	case <-secondHeld:
	case <-time.After(5 * time.Second):
		t.Fatal("the second holder never acquired the lock after it was released")
	}
}

// TestAcquireDegradesWhenLockPathUnusable exercises the unavailable-primitive
// convention on the path that can be reached on any platform: the lock file
// cannot be created at all. The helper must hand back a usable release and let
// the caller proceed, because refusing to build on a machine that cannot lock
// would trade a rare wrong artifact for a certain failure.
func TestAcquireDegradesWhenLockPathUnusable(t *testing.T) {
	// A regular file stands where the lock's parent directory would go, so the
	// directory cannot be created and the lock file cannot be opened.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed the blocking file: %v", err)
	}
	lockPath := filepath.Join(blocker, "nested", "guard.lock")

	done := make(chan func(), 1)
	go func() { done <- Acquire(lockPath) }()

	var release func()
	select {
	case release = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an unusable lock path blocked the caller instead of degrading to no serialization")
	}
	if release == nil {
		t.Fatal("no release function was returned for an unusable lock path")
	}

	// Releasing a lock that was never taken is safe and repeatable, so a
	// deferred release needs no special case for the degraded path.
	release()
	release()

	if _, err := os.Stat(lockPath); err == nil {
		t.Fatal("a lock file was created under a path that cannot hold one")
	}
}

// TestAcquireReleasesForReuse confirms the lock is reacquirable, so a queue of
// builders drains instead of stalling after the first one finishes.
func TestAcquireReleasesForReuse(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "guard.lock")
	for i := 0; i < 3; i++ {
		release := Acquire(lockPath)
		if release == nil {
			t.Fatalf("acquisition %d returned no release function", i)
		}
		release()
	}
}

// locksAvailable reports whether this platform actually took the lock, so the
// exclusivity test skips rather than fails where the primitive is absent. It
// reads the observable consequence — the lock file exists once a lock has been
// taken — rather than the build tag.
func locksAvailable(t *testing.T, lockPath string) bool {
	t.Helper()
	_, err := os.Stat(lockPath)
	return err == nil
}
