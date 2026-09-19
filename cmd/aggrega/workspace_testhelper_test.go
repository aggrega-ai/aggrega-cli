// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// tempWorkspaceOptions declares what a test's workspace is. It is passed by
// value to resolveTempWorkspace, so the declaration sits at the test's own call
// site rather than in shared setup a reader has to go and find.
//
// The zero value is the minimal-but-real default: a workspace root at a fresh
// temporary directory declaring no catalog directories of its own. The
// `workspace:` block is written because its presence is what anchors the root;
// the catalog-directory key is written only when the caller declares one, which
// is the ordinary shape of a workspace and the one a command meets in
// production.
type tempWorkspaceOptions struct {
	// Root is the directory to declare as the workspace root. Empty means a
	// fresh t.TempDir(). A test that has already built its own temporary tree —
	// commonly with a catalog inside it — names that directory here, so the
	// command resolves the tree the test wrote instead of discovering one.
	//
	// When Root already holds an `.aggrega-config.yaml`, that file is left
	// exactly as the test wrote it; it must carry a `workspace:` block, since
	// nothing else anchors the root at that directory.
	Root string
	// CatalogDirs are declared as the workspace's catalog_dirs, in order. A
	// relative entry resolves against Root, as it would for any config file.
	// It cannot be combined with a Root that already holds a config file,
	// because that would leave two declarations of the same thing and no way to
	// tell which one took effect.
	//
	// Empty declares NOTHING rather than an empty list, and the difference is a
	// behaviour a reader can see: a workspace that declares no catalog
	// directories still reads the conventional `catalog` directory at its root
	// when one exists, while a workspace that declares `catalog_dirs: []` has
	// switched the local catalog off entirely. A test that wants the off-switch
	// authors its own config file at Root and says so.
	CatalogDirs []string
}

// resolveTempWorkspace resolves the isolated workspace the caller declared, and
// returns it for any test that needs one.
//
// Declaring the workspace is what makes a test independent of the machine it runs
// on. Discovery walks the current directory upward looking for the nearest config
// that anchors a root: when the repository checkout lives under a developer's home
// directory and that home holds an `.aggrega-config.yaml` with a `workspace:`
// block, the walk reaches it and adopts its catalog directories — a developer's
// ambient configuration would then leak into the test and resolve catalog paths
// that do not exist on the machine. Writing the declared config at the declared
// root and resolving from there anchors the root on the very first step of
// discovery, so resolution never climbs to any ancestor. The result is hermetic
// against whatever configuration happens to sit above the checkout.
//
// This is the single home for hermetic test-workspace construction. A test that
// needs catalog directories states them in the options rather than mutating the
// resolved result or reaching for a second helper, so there is one shape to read
// and one place resolution happens. It must not be "simplified" back to handing
// the helper no workspace — the isolation is the point.
func resolveTempWorkspace(t *testing.T, opts tempWorkspaceOptions) *workspaceconfig.ResolvedWorkspace {
	t.Helper()

	root := opts.Root
	if root == "" {
		root = t.TempDir()
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve declared workspace root %s: %v", root, err)
	}

	cfgPath := filepath.Join(absRoot, workspaceconfig.ConfigFilename)
	_, statErr := os.Stat(cfgPath)
	authored := statErr == nil

	switch {
	case authored && len(opts.CatalogDirs) > 0:
		t.Fatalf("%s already holds %s: declare catalog directories there or in the options, not both",
			absRoot, workspaceconfig.ConfigFilename)
	case !authored:
		// Encode through the YAML marshaller rather than by concatenating
		// strings, so a directory holding a character that has to be quoted is
		// written as a value the loader reads back unchanged.
		block := map[string]any{}
		if len(opts.CatalogDirs) > 0 {
			block["catalog_dirs"] = opts.CatalogDirs
		}
		doc, merr := yaml.Marshal(map[string]any{"workspace": block})
		if merr != nil {
			t.Fatalf("encode workspace config: %v", merr)
		}
		if werr := os.WriteFile(cfgPath, doc, 0o644); werr != nil {
			t.Fatalf("write workspace config: %v", werr)
		}
	}

	rw, err := workspaceconfig.Resolve(absRoot, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Assert the declaration took effect. A key the loader does not consume
	// would leave the resolved workspace indistinguishable from one that
	// declared nothing at all — which is the shape this helper exists to
	// replace — so the check belongs here, where a misspelling fails loudly
	// instead of degrading in silence.
	if got := rw.Root(); got != absRoot {
		t.Fatalf("declared workspace root %s, resolved %s (a config at the declared root needs a `workspace:` block to anchor it)",
			absRoot, got)
	}
	if !authored {
		want := make([]string, 0, len(opts.CatalogDirs))
		for _, d := range opts.CatalogDirs {
			if filepath.IsAbs(d) {
				want = append(want, d)
				continue
			}
			want = append(want, filepath.Join(absRoot, d))
		}
		got := rw.Config().Workspace.CatalogDirs
		if len(got) != len(want) {
			t.Fatalf("declared %d catalog dir(s) %v, resolved %d (%v)", len(want), want, len(got), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("declared catalog dir %d as %s, resolved %s", i, want[i], got[i])
			}
		}
	}
	return rw
}

// resolveTempWorkspaceAgainst resolves an isolated workspace against look,
// WITHOUT the global layer, for a test whose command is about to create or
// rewrite the very global file look names.
//
// Resolving against the reader the write will use is the point rather than setup
// detail: a config-mutating command resolves its write destination off the
// resolution, so a test that resolved through one view of the environment and
// wrote through another would exercise a seam production does not have — and
// would pass while the write landed in a file the resolution never read.
//
// The global layer is skipped because the file under test frequently does not
// exist yet, and the full cascade rejects an explicit global path that is
// missing. That is the same reason the write-oriented commands resolve this way.
func resolveTempWorkspaceAgainst(t *testing.T, opts tempWorkspaceOptions, look func(string) string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := resolveTempWorkspace(t, opts).Root()
	rw, err := workspaceconfig.ResolveWithoutGlobal("", root, look)
	if err != nil {
		t.Fatalf("resolve workspace without the global layer: %v", err)
	}
	return rw
}
