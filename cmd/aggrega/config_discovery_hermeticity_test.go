// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestConfigDiscoveryIsHermeticAgainstAncestorConfig proves that seeding a
// `workspace: {}` marker at the resolution start directory caps workspace
// discovery there, so an ancestor configuration — the stand-in for a
// developer's ambient home-level `.aggrega-config.yaml` sitting above the
// repository checkout — can never anchor the resolved root or inject its
// catalog directories into a test.
//
// The negative control resolves from a markerless descendant of the same
// planted ancestor through the unbounded upward walk and asserts the ancestor
// IS adopted. The contrast is load-bearing: it is the planted ancestor's
// reachability that makes the hermetic arm's isolation meaningful. If discovery
// semantics ever changed so the seeded `workspace: {}` marker no longer capped
// the walk, the negative control would flip and this test would fail loudly
// rather than letting the isolation rot silently.
//
// Both arms call the same production workspaceconfig.Resolve; only the start
// directory and the marker topology differ. No path escapes the test's temp
// directories and no $HOME or current-directory state is consulted (every
// resolve passes an explicit start directory).
func TestConfigDiscoveryIsHermeticAgainstAncestorConfig(t *testing.T) {
	t.Parallel()

	const plantedCatalog = "/bogus/planted/catalog"

	ancestor := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(ancestor, workspaceconfig.ConfigFilename),
		[]byte("workspace:\n  catalog_dirs:\n    - "+plantedCatalog+"\n"),
		0o644,
	); err != nil {
		t.Fatalf("plant ancestor config: %v", err)
	}

	// Hermetic arm: a child carrying its own `workspace: {}` marker.
	child := filepath.Join(ancestor, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(child, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\n"),
		0o644,
	); err != nil {
		t.Fatalf("seed child config: %v", err)
	}

	hermetic, err := workspaceconfig.Resolve(child, "")
	if err != nil {
		t.Fatalf("hermetic Resolve: %v", err)
	}
	if hermetic.Root() != child {
		t.Errorf("hermetic root = %q; want the child's own dir %q (the seeded marker must cap discovery)", hermetic.Root(), child)
	}
	for _, dir := range hermetic.Config().Workspace.CatalogDirs {
		if dir == plantedCatalog {
			t.Errorf("hermetic resolution leaked the planted ancestor's catalog dir %q; ambient config must not reach a marked workspace", plantedCatalog)
		}
	}

	// Negative control: a markerless grandchild whose nearest `workspace:`
	// config is the planted ancestor. The unbounded upward walk must reach it.
	grand := filepath.Join(child, "deep", "nested")
	if err := os.MkdirAll(grand, 0o755); err != nil {
		t.Fatalf("mkdir grand: %v", err)
	}
	// Remove the child marker so nothing caps the walk between grand and the
	// planted ancestor.
	if err := os.Remove(filepath.Join(child, workspaceconfig.ConfigFilename)); err != nil {
		t.Fatalf("remove child marker: %v", err)
	}

	unbounded, err := workspaceconfig.Resolve(grand, "")
	if err != nil {
		t.Fatalf("unbounded Resolve: %v", err)
	}
	if unbounded.Root() != ancestor {
		t.Errorf("unbounded root = %q; want the planted ancestor %q (the walk must reach it, else the negative control no longer exercises the unbounded path)", unbounded.Root(), ancestor)
	}
	foundPlanted := false
	for _, dir := range unbounded.Config().Workspace.CatalogDirs {
		if dir == plantedCatalog {
			foundPlanted = true
		}
	}
	if !foundPlanted {
		t.Errorf("unbounded resolution did NOT pick up the planted catalog dir %q; the negative control is not exercising the ancestor-anchoring path it is meant to guard", plantedCatalog)
	}
}
