// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build windows

package provindex

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// readLockFixtureTime is a fixed instant for the entries these tests record, so
// nothing in them depends on the clock.
var readLockFixtureTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestLoadWaitsForAWriterHoldingTheIndexLock proves a reader never opens the
// index while a writer holds it: Windows refuses to replace a file another handle
// has open, and refuses an open while a replacement holds it, so a reader that
// did not wait could either break the writer or be turned away with an empty
// index.
//
// The outcome is decided by ORDER, not by time. The writer installs a second
// entry while it still holds the lock and only then releases, so a reader that
// waited always sees that entry and one that did not wait sees the first state
// however fast the machine is. The pause before the write only widens the window
// in which a reader that does not wait is caught; it is not an assertion.
func TestLoadWaitsForAWriterHoldingTheIndexLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, indexFileName)
	if err := Upsert(path, "sha256:aa", Identity{URI: "oci://example.test/before", Version: "1"}, 1, readLockFixtureTime); err != nil {
		t.Fatalf("seed the index: %v", err)
	}

	release := flockIndex(dir)
	type result struct {
		ix  Index
		err error
	}
	read := make(chan result, 1)
	go func() {
		ix, err := Load(path)
		read <- result{ix, err}
	}()

	time.Sleep(300 * time.Millisecond)
	ix, err := load(path)
	if err != nil {
		release()
		t.Fatalf("the writer could not read the index it holds: %v", err)
	}
	ix.byDigest["bb"] = Entry{Digest: "bb", FirstSeen: readLockFixtureTime, SizeGuestBytes: 2,
		Identities: []Identity{{URI: "oci://example.test/written-under-the-lock", Version: "1"}}}
	if err := writeAtomic(path, ix); err != nil {
		release()
		t.Fatalf("the writer could not replace the index while holding its lock: %v", err)
	}
	release()

	select {
	case r := <-read:
		if r.err != nil {
			t.Fatalf("the reader was refused instead of waiting for the writer: %v", r.err)
		}
		if _, ok := r.ix.byDigest["bb"]; !ok {
			t.Fatal("the reader read the index while the writer held its lock")
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("the reader never read the index after the writer released it (liveness ceiling)")
	}
}

// TestAWriterReplacesTheIndexOnceItsReadersHaveClosedIt holds the index exactly
// as a reader does in the middle of a read — the shared lock, and the file open —
// and starts a write. On Windows the write's rename cannot replace a file a reader
// holds open, so the writer must wait for the reader rather than fail.
//
// The reader sets a witness immediately before it closes, and the writer reports
// whether the witness was set when it finished, so a writer that waited always
// reports true and succeeds, and one that did not wait reports false or fails its
// rename. The pause only widens the window; it is not an assertion.
func TestAWriterReplacesTheIndexOnceItsReadersHaveClosedIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, indexFileName)
	if err := Upsert(path, "sha256:aa", Identity{URI: "oci://example.test/before", Version: "1"}, 1, readLockFixtureTime); err != nil {
		t.Fatalf("seed the index: %v", err)
	}

	releaseRead := readLockIndex(dir)
	f, err := os.Open(path)
	if err != nil {
		releaseRead()
		t.Fatalf("open the index as a reader: %v", err)
	}

	var closed atomic.Bool
	type result struct {
		err         error
		afterReader bool
	}
	wrote := make(chan result, 1)
	go func() {
		err := Upsert(path, "sha256:bb", Identity{URI: "oci://example.test/after", Version: "1"}, 2, readLockFixtureTime)
		wrote <- result{err, closed.Load()}
	}()

	time.Sleep(300 * time.Millisecond)
	closed.Store(true)
	_ = f.Close()
	releaseRead()

	select {
	case r := <-wrote:
		if r.err != nil {
			t.Fatalf("the write failed instead of waiting for the reader to close the index: %v", r.err)
		}
		if !r.afterReader {
			t.Fatal("the write completed while a reader still held the index open")
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("the write never completed after the reader closed the index (liveness ceiling)")
	}

	ix, err := Load(path)
	if err != nil {
		t.Fatalf("load after the write: %v", err)
	}
	if _, ok := ix.byDigest["bb"]; !ok {
		t.Fatal("the index does not hold the entry the writer recorded")
	}
	if _, ok := ix.byDigest["aa"]; !ok {
		t.Fatal("the write lost the entry the index held before it")
	}
}
