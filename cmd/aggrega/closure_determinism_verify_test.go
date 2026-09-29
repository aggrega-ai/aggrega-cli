// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestClosureUnificationByteIdentical proves the shared closure resolver leaves
// the compiler's deterministic-output contract intact: the same Release built
// twice through the in-process build path (which now routes through
// compilerops.ResolveClosure) renders byte-identical artifacts. It reuses the
// remote-pinned-aggregate fixture so both the top-level triad resolution and the
// cache-bridged sub-aggregate exercise the shared resolver.
func TestClosureUnificationByteIdentical(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath := remoteAggregateFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}
	relocateAggregateToCache(t, root, cacheDir, "mysub")

	build := func() string {
		if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("dispatchBuild: %v", err)
		}
		return readDistTree(t, filepath.Join(root, "dist"))
	}

	first := build()
	// The clean's error is checked here, where sibling sites in this package discard
	// it, and the divergence is deliberate: both builds render into this one
	// directory, so a silently failed clean would leave the first build's artifacts
	// in place and the byte-identity comparison below would compare a tree against
	// itself — passing without ever exercising the second build. Discarding it to
	// match the siblings reintroduces exactly that.
	if err := os.RemoveAll(filepath.Join(root, "dist")); err != nil {
		t.Fatalf("clean dist: %v", err)
	}
	second := build()

	if first != second {
		t.Fatalf("build output not byte-identical across two runs:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
