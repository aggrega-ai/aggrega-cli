// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The external-input store is a workspace's content-addressed cache, so one
// workspace has exactly one of them and where a person happened to be standing
// when they typed the command is not an input to it. This file is that proof: the
// same Release is built twice from two different working directories, and both
// builds must serve the pinned reference from the SAME store.
//
// # How a working directory can reach a store location at all
//
// The configured artifact cache directory is workspace-relative by design — it is
// a setting whose value is a path inside the workspace — so it only names a real
// location once it is resolved against the workspace root. A consumer handed the
// setting without the root cannot resolve it, and a relative path with nothing to
// resolve it against is resolved by the process working directory instead. The
// store then follows the caller from directory to directory.
//
// # Why the assertion is the SAME store rather than a successful build
//
// "It built from both directories" is the weaker claim, and it is satisfied by
// the very resolution this file exists to rule out: a store that follows the
// working directory is found empty in each new place, and any surface able to
// acquire simply refills it and exits zero. Nothing fails, while the workspace
// accumulates one store per directory a command has been run from. So the
// property under test is the IDENTITY of the store, and the negative half below —
// that the second directory holds no store at all — is what makes that identity
// observable rather than merely likely.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
)

// TestBuildFromANestedSubdirectoryServesTheSameWorkspaceStore builds one Release
// from the workspace root and again from a nested subdirectory of it, and asserts
// both renders served the pinned external reference out of the one store beneath
// the workspace root.
//
// It reuses the fetching-provider fixture because the property is only observable
// through a render that REQUESTS an external input: under any other provider
// fixture nothing reads the store, both builds emit identical bytes, and the
// assertions here would hold no matter where the store was looked for.
//
// The fixture pins the reference and seeds its blob before either build runs, and
// neither build may fetch: the store read is the only way to the expected bytes.
// That is what makes "the same store" and "the right bytes" one assertion rather
// than two — a build that resolved the store somewhere else cannot pass by
// refetching, because there is nothing to refetch from.
func TestBuildFromANestedSubdirectoryServesTheSameWorkspaceStore(t *testing.T) {
	// Not parallel: it changes the working directory, which is the variable under
	// test and is process-wide.
	root := t.TempDir()

	// The workspace is written BEFORE any working-directory change, because
	// building the guest walks up from the working directory to find the umbrella
	// checkout and a temporary directory has no checkout above it.
	releasePath := writeFetchingProviderWorkspace(t, root)

	t.Chdir(root)

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	cacheDir := ws.Config().ResolvedArtifactCacheDir()
	seedPinnedExternalInput(t, root, releasePath, root, cacheDir)

	// The one store the workspace has, named from the same derivation production
	// uses rather than spelled here, so a change to the layout moves both sides.
	storeRoot := externalinputs.StoreRoot(root, cacheDir)
	if _, err := os.Stat(storeRoot); err != nil {
		t.Fatalf("the fixture seeded no store at %s, so neither build below could serve a pinned "+
			"reference from one and a passing result would mean nothing: %v", storeRoot, err)
	}

	t.Logf("the workspace's one external-input store: %s", storeRoot)

	want := seamFixtureServedContent()

	nested := filepath.Join(root, "sub", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create the nested build directory: %v", err)
	}

	// buildFrom runs the command line's own build entry point with dir as the
	// working directory and returns the content the render emitted for the
	// fixture's file. The output lands beneath the workspace root whatever dir is,
	// which is where the build puts a render and not an input to it.
	buildFrom := func(dir, surface string) string {
		t.Helper()
		t.Chdir(dir)
		t.Logf("building from %s (%s); the store it must read is %s",
			surface, dir, externalinputs.StoreRoot(root, cacheDir))
		var diag, out bytes.Buffer
		err := dispatchBuild(context.Background(),
			resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			releasePath, nil, false, &diag, &out)
		if err != nil {
			t.Fatalf("the build launched from %s failed: %v\n--- diagnostics ---\n%s\n--- output ---\n%s",
				surface, err, diag.String(), out.String())
		}
		dist := filepath.Join(root, "dist")
		tree := readRenderedTree(t, dist)
		content := renderedFileNamed(t, tree, surface)
		if err := os.RemoveAll(dist); err != nil {
			t.Fatalf("clear the render emitted from %s: %v", surface, err)
		}
		return content
	}

	fromRoot := buildFrom(root, "the workspace root")
	if fromRoot != want {
		t.Errorf("the build launched from the workspace root did not serve the pinned reference:\n got %q\nwant %q",
			fromRoot, want)
	}

	fromNested := buildFrom(nested, "a nested subdirectory")
	if fromNested != want {
		t.Errorf("the build launched from a nested subdirectory did not serve the pinned reference from the "+
			"workspace's store:\n got %q\nwant %q", fromNested, want)
	}

	// The two renders agree, which is the acceptance: one workspace, one store,
	// whatever directory the command was typed in.
	if fromRoot != fromNested {
		t.Errorf("the same Release rendered differently from two working directories:\n from the root %q\n from a nested subdirectory %q",
			fromRoot, fromNested)
	}

	// The negative half. A store resolved against the working directory would have
	// left a second one beside the nested build, and the build would still have
	// looked healthy — so this is the assertion that makes the identity above
	// observable rather than inferred. The instrument is a stat of the exact path
	// the old resolution would have produced, and the stat of the real store above
	// is its needle: a mistyped path would fail there first.
	strayStore := externalinputs.StoreRoot(nested, cacheDir)
	t.Logf("no second store may exist at %s", strayStore)
	if _, err := os.Stat(strayStore); !os.IsNotExist(err) {
		t.Errorf("a second external-input store exists at %s; the store followed the working directory "+
			"instead of the workspace, so this workspace now has more than one", strayStore)
	}
}
