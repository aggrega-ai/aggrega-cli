// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
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
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteAggregateFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}
	relocateAggregateToCache(t, root, cacheDir, "mysub")

	build := func() string {
		if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("dispatchBuild: %v", err)
		}
		return readDistTree(t, filepath.Join(root, "dist"))
	}

	first := build()
	if err := os.RemoveAll(filepath.Join(root, "dist")); err != nil {
		t.Fatalf("clean dist: %v", err)
	}
	second := build()

	if first != second {
		t.Fatalf("build output not byte-identical across two runs:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
