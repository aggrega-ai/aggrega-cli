// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package provindex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixedTime is a stable clock so the on-disk FirstSeen value — and therefore the
// serialized bytes — is reproducible across a test's writes.
var fixedTime = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

// TestUpsertIdempotent verifies that re-upserting the same {digest, identity}
// leaves exactly one identity and preserves FirstSeen, even when a later upsert
// supplies a different clock value — the digest's first-seen instant is set once.
func TestUpsertIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), indexFileName)
	dg := "sha256:aaaa"
	id := Identity{URI: "oci://hub.aggrega.ai/plugins/core/helm", Version: "1.0.0"}

	if err := Upsert(path, dg, id, 42, fixedTime); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	later := fixedTime.Add(time.Hour)
	if err := Upsert(path, dg, id, 42, later); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	ix, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	entries := ix.Entries()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(entries), entries)
	}
	e := entries[0]
	if len(e.Identities) != 1 {
		t.Fatalf("want 1 identity, got %d: %+v", len(e.Identities), e.Identities)
	}
	if !e.FirstSeen.Equal(fixedTime) {
		t.Fatalf("FirstSeen changed across upserts: want %s, got %s", fixedTime, e.FirstSeen)
	}
	if e.Digest != "aaaa" {
		t.Fatalf("digest not normalized: got %q", e.Digest)
	}
}

// TestUpsertSharedDigest is the strange case: two DISTINCT URIs resolved to the
// SAME content digest collapse to one entry whose identity set holds both, and a
// read surfaces them so a caller can report "shared by 2".
func TestUpsertSharedDigest(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), indexFileName)
	dg := "sha256:beef"
	idA := Identity{URI: "oci://hub.aggrega.ai/plugins/core/helm", Version: "1.0.0"}
	idB := Identity{URI: "oci://hub.aggrega.ai/plugins/team/helm-fork", Version: "2.0.0"}

	if err := Upsert(path, dg, idA, 100, fixedTime); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	if err := Upsert(path, dg, idB, 100, fixedTime); err != nil {
		t.Fatalf("upsert B: %v", err)
	}

	ix, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	entries := ix.Entries()
	if len(entries) != 1 {
		t.Fatalf("want 1 digest entry, got %d: %+v", len(entries), entries)
	}
	got := entries[0].Identities
	if len(got) != 2 {
		t.Fatalf("want identity set of 2 (shared by 2), got %d: %+v", len(got), got)
	}
	// Deterministic identity order: sorted by URI. helm-fork's URI sorts before
	// helm because "team" < the "core" segment? Assert set membership rather than
	// a brittle positional check.
	seen := map[Identity]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen[idA] || !seen[idB] {
		t.Fatalf("identity set missing a source: %+v", got)
	}
}

// TestOnDiskByteIdentical verifies the index file is byte-identical across two
// writes of identical state — the determinism the file-diffing tools downstream
// rely on. The second write re-upserts the same record with a later clock; since
// FirstSeen is preserved and ordering is fixed, the bytes must not move.
func TestOnDiskByteIdentical(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), indexFileName)
	idA := Identity{URI: "oci://hub.aggrega.ai/plugins/core/kustomize", Version: "3.1.0"}
	idB := Identity{URI: "oci://hub.aggrega.ai/plugins/core/helm", Version: "1.0.0"}

	// Seed two digests with two identities on one of them, in mixed order, so
	// the byte-identity check also exercises the sort.
	if err := Upsert(path, "sha256:CCCC", idA, 10, fixedTime); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if err := Upsert(path, "sha256:aaaa", idB, 20, fixedTime); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	if err := Upsert(path, "sha256:aaaa", idA, 20, fixedTime); err != nil {
		t.Fatalf("seed 3: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}

	// Re-write the identical state with a later clock.
	later := fixedTime.Add(24 * time.Hour)
	if err := Upsert(path, "sha256:aaaa", idB, 20, later); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}

	if string(first) != string(second) {
		t.Fatalf("on-disk index not byte-identical across writes:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestLoadMissingIsEmpty confirms a first-ever run (no file yet) loads clean
// rather than erroring.
func TestLoadMissingIsEmpty(t *testing.T) {
	t.Parallel()
	ix, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if len(ix.Entries()) != 0 {
		t.Fatalf("want empty index, got %+v", ix.Entries())
	}
}
