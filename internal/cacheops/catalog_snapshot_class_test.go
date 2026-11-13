// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cacheops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// snapshotDigest is a manifest digest a catalog snapshot slot is laid under here.
const snapshotDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// layCatalogSnapshotSlot lays a catalog snapshot slot under wsRoot's cache as an
// import keeps one — the archive, its manifest, a file of the tree it is laid out
// as — and the file naming it as the snapshot the workspace reads, and returns the
// slot.
func layCatalogSnapshotSlot(t *testing.T, wsRoot string) string {
	t.Helper()
	slot, err := oci.CatalogSnapshotSlot(wsRoot, testCacheDir, snapshotDigest)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		oci.SlotBlobFile:     "archive-bytes",
		oci.SlotManifestFile: "manifest-bytes",
	} {
		if err := oci.WriteSlotFile(wsRoot, testCacheDir, slot, name, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tree := filepath.Join(slot, "extracted", "tree", "component")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "component.yaml"), []byte("kind: Component\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := oci.WriteSlotFile(wsRoot, testCacheDir, oci.CatalogSnapshotsRoot(wsRoot, testCacheDir),
		oci.CatalogSnapshotCurrentFile, []byte(snapshotDigest+"\n")); err != nil {
		t.Fatal(err)
	}
	return slot
}

// TestTheCatalogSnapshotCacheIsListedClearedAndNotPruned lays one catalog snapshot
// slot in a workspace's cache and holds the three cache operations to it: the
// inventory lists the slot under its class, labelled with the snapshot's digest and
// sized whole; a prune removes nothing from it and reports it as reclaimed by a
// clear alone, since no lockfile pins a snapshot; and a clear removes the slot and
// the file naming the snapshot the workspace reads.
//
// 🔴 PLANTED DEFECTS: leave the class out of workspaceClasses and the listing and
// clear rows fail; mark it Prunable and the prune row finds it removed.
func TestTheCatalogSnapshotCacheIsListedClearedAndNotPruned(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		wsRoot := t.TempDir()
		slot := layCatalogSnapshotSlot(t, wsRoot)
		report, err := Status(t.Context(), Locations{WorkspaceRoot: wsRoot, CacheDir: testCacheDir})
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		var listed []Element
		for _, cl := range report.Classes {
			if cl.Name == CatalogSnapshotClassName {
				listed = cl.Elements
				if cl.Reach != ReachWorkspace || cl.Prunable {
					t.Errorf("the class is %q and prunable %v; want workspace and not prunable", cl.Reach, cl.Prunable)
				}
			}
		}
		if len(listed) != 1 || listed[0].Label != snapshotDigest || listed[0].Path != slot || listed[0].Size <= 0 {
			t.Fatalf("the class lists %+v; want the one slot %s labelled %s with its size", listed, slot, snapshotDigest)
		}
	})
	t.Run("prune", func(t *testing.T) {
		wsRoot := t.TempDir()
		slot := layCatalogSnapshotSlot(t, wsRoot)
		// A prune refuses a workspace no pull has pinned anything in, so the
		// workspace carries an empty lockfile: nothing is pinned, and still the
		// snapshot slot is not removed.
		if err := lockfile.WriteLockfile(filepath.Join(wsRoot, "release.lock"), &lockfile.Lockfile{
			Version: 1, Release: "release.yaml", GeneratedAt: "2026-01-01T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
		res, err := Prune(t.Context(), Locations{WorkspaceRoot: wsRoot, CacheDir: testCacheDir})
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if _, err := os.Stat(slot); err != nil {
			t.Errorf("the prune removed the snapshot slot: %v", err)
		}
		reported := false
		for _, s := range res.NotPruneEligible {
			reported = reported || (s.Class == CatalogSnapshotClassName && s.Count == 1)
		}
		if !reported {
			t.Errorf("the prune does not report the snapshot slot as reclaimed by a clear alone: %+v", res.NotPruneEligible)
		}
	})
	t.Run("clear", func(t *testing.T) {
		wsRoot := t.TempDir()
		slot := layCatalogSnapshotSlot(t, wsRoot)
		if _, err := Clear(t.Context(), Locations{WorkspaceRoot: wsRoot, CacheDir: testCacheDir}); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		for _, gone := range []string{slot, oci.CatalogSnapshotCurrentPath(wsRoot, testCacheDir)} {
			if _, err := os.Stat(gone); !os.IsNotExist(err) {
				t.Errorf("the clear left %s (stat: %v)", gone, err)
			}
		}
	})
}
