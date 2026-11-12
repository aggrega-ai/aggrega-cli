// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// sampleLockfile returns a minimal but non-trivial lockfile carrying the given
// generated_at stamp; entityDigest lets a test vary the content (a pin) while
// holding the stamp, or vice versa.
func sampleLockfile(stamp, entityDigest string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: stamp,
		Entities: []lockfile.Entity{
			{Kind: "component", URI: "server", ResolvedVersion: "1.0.0", Digest: entityDigest},
		},
	}
}

// TestWriteLockfilePreservingTimestamp covers the byte-idempotence mechanism:
// an unchanged re-write reuses the prior stamp (byte-identical), a real content
// change re-stamps, and a first write (no prior file) keeps the lockfile's own
// stamp.
func TestWriteLockfilePreservingTimestamp(t *testing.T) {
	const stampA = "2026-06-17T08:00:00Z"
	const stampB = "2026-06-17T09:30:00Z"
	const digest = "sha256:aaaa"

	t.Run("unchanged content reuses the prior stamp (byte-identical)", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "release.lock")
		if err := lockfile.WriteLockfile(path, sampleLockfile(stampA, digest)); err != nil {
			t.Fatalf("seed write: %v", err)
		}
		first, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read seed: %v", err)
		}

		// Same content, different stamp, via the preserving writer.
		if err := lockfile.WriteLockfilePreservingTimestamp(path, sampleLockfile(stampB, digest)); err != nil {
			t.Fatalf("preserving write: %v", err)
		}
		second, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read second: %v", err)
		}
		if string(first) != string(second) {
			t.Errorf("unchanged re-write was not byte-identical\n--- first ---\n%s\n--- second ---\n%s", first, second)
		}
		if !strings.Contains(string(second), "generated_at: \""+stampA+"\"") {
			t.Errorf("re-write did not preserve the prior stamp %q; got:\n%s", stampA, second)
		}
	})

	t.Run("changed content re-stamps", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "release.lock")
		if err := lockfile.WriteLockfile(path, sampleLockfile(stampA, digest)); err != nil {
			t.Fatalf("seed write: %v", err)
		}
		// A real pin change: different digest carried with the fresh stamp.
		if err := lockfile.WriteLockfilePreservingTimestamp(path, sampleLockfile(stampB, "sha256:bbbb")); err != nil {
			t.Fatalf("preserving write: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !strings.Contains(string(got), "generated_at: \""+stampB+"\"") {
			t.Errorf("a real change must re-stamp to %q; got:\n%s", stampB, got)
		}
		if !strings.Contains(string(got), "sha256:bbbb") {
			t.Errorf("the changed pin was not written; got:\n%s", got)
		}
	})

	t.Run("no prior file keeps the lockfile's own stamp", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "release.lock")
		if err := lockfile.WriteLockfilePreservingTimestamp(path, sampleLockfile(stampB, digest)); err != nil {
			t.Fatalf("preserving write: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !strings.Contains(string(got), "generated_at: \""+stampB+"\"") {
			t.Errorf("first write must keep the lockfile's own stamp %q; got:\n%s", stampB, got)
		}
	})
}
