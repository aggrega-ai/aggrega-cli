// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestTheConventionalCatalogDirectoryIsFollowedOnlyWhereItIsNamed drives a whole
// build — its catalog load, the dependency resolution it fills the lockfile with,
// and the render — over a workspace whose only catalog is the conventional
// `catalog` directory at its root, and reads back whether the build resolved the
// Release's entities from it or halted.
//
// The rows differ in two things only: whether `catalog` is a link leading out of
// the workspace, and whether anybody named it — the configuration listing it, or
// the directory passed with --catalog, spelled as a reader types it from the root.
// A directory somebody named is followed where it leads, like every other
// directory named; the conventional directory read only because it is there is the
// workspace's own, so a link standing at it and leading out halts the build naming
// the link, before anything is resolved or written. Whether it was named is a
// question of the place it leads to: the configuration listing that place in its
// own spelling, or the directory holding it, reaches the link, so what lies there is
// read once, through the listed directory, and the build completes.
//
// The control is first: the conventional directory a plain directory, named by
// nobody, builds — which is what makes a halting row's halt the link's doing.
//
// A build that fills its lockfile renders as part of the fill, so it never reads
// through the lookup a build builds for its own render. The row whose lockfile is
// already filled builds a second time with nothing to fill, which is the build that
// renders through its own lookup — so that lookup is held to the named part of the
// stack as well, and a listed `catalog` link is followed there too.
func TestTheConventionalCatalogDirectoryIsFollowedOnlyWhereItIsNamed(t *testing.T) {
	for _, row := range []struct {
		name string
		// linked moves the fixture's catalog outside the workspace and leaves a
		// link to it standing at `catalog`.
		linked bool
		// workspace is the configuration's `workspace:` block.
		workspace string
		// listsPlace lists, as the configuration's one catalog directory, the
		// place the link leads to in that place's own spelling — or, with
		// listsAbove, the directory holding it — in place of workspace.
		listsPlace, listsAbove bool
		// flag passes `catalog` with --catalog, from the workspace root.
		flag bool
		// refilled builds once to fill the lockfile, then again with auto_pull off,
		// so the second build renders through its own lookup rather than the fill's.
		refilled bool
		halts    bool
	}{
		{name: "control: the conventional directory a plain directory, named by nobody, builds",
			workspace: "workspace: {}"},
		{name: "listed in the configuration, a link leading out is followed and its entities resolve",
			linked: true, workspace: "workspace:\n  catalog_dirs:\n    - ./catalog"},
		{name: "named by nobody, a link leading out halts the build naming the link",
			linked: true, workspace: "workspace: {}", halts: true},
		{name: "passed with --catalog, a link leading out is followed and its entities resolve",
			linked: true, workspace: "workspace: {}", flag: true},
		{name: "listed, a link leading out is followed by a build rendering through its own lookup, the lockfile already filled",
			linked: true, workspace: "workspace:\n  catalog_dirs:\n    - ./catalog", refilled: true},
		{name: "the place the link leads to listed in its own spelling, the link is reached by the listing: read once, it builds",
			linked: true, listsPlace: true},
		{name: "the directory holding the place the link leads to listed, the link is reached by the listing: it builds",
			linked: true, listsPlace: true, listsAbove: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			releasePath := authoringFixture(t, root)
			conventional := filepath.Join(root, "catalog")
			target := filepath.Join(t.TempDir(), "kept-elsewhere")
			if row.linked {
				if err := os.Rename(conventional, target); err != nil {
					t.Fatalf("move the catalog outside the workspace: %v", err)
				}
				if err := os.Symlink(target, conventional); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}
			if row.listsPlace {
				listed := target
				if row.listsAbove {
					listed = filepath.Dir(target)
				}
				row.workspace = "workspace:\n  catalog_dirs:\n    - " + listed
			}
			writeWorkspaceConfig(t, root, "auto_pull: true\n"+row.workspace)
			rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
			var flags []string
			if row.flag {
				t.Chdir(root)
				flags = []string{"catalog"}
			}

			var diag bytes.Buffer
			err := dispatchBuildInRoot(t, rw, releasePath, flags, false, &diag, io.Discard)
			if row.refilled {
				if err != nil {
					t.Fatalf("the build filling the lockfile did not complete: %v\n%s", err, diag.String())
				}
				writeWorkspaceConfig(t, root, row.workspace)
				rw = resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
				diag.Reset()
				err = dispatchBuildInRoot(t, rw, releasePath, flags, false, &diag, io.Discard)
			}

			lock, lockErr := os.ReadFile(lockfile.PathFor(releasePath))
			if !row.halts {
				if err != nil {
					t.Fatalf("the build did not complete: %v\n%s", err, diag.String())
				}
				if lockErr != nil {
					t.Fatalf("the build filled no lockfile: %v", lockErr)
				}
				if !strings.Contains(string(lock), "uri: server") {
					t.Errorf("the lockfile pins no server Component, so the catalog's entities did not resolve:\n%s", lock)
				}
				if renderedFiles(t, filepath.Join(root, "dist")) == 0 {
					t.Error("the build rendered nothing")
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
			}
			if !strings.Contains(err.Error(), "the link "+conventional+" leads to ") {
				t.Errorf("the halt must name the link %s: %v", conventional, err)
			}
			if !errors.Is(lockErr, fs.ErrNotExist) {
				t.Errorf("a build that halted on the catalog wrote a lockfile (read: %v)", lockErr)
			}
		})
	}
}

// renderedFiles counts the files beneath dir, zero where it does not exist.
func renderedFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}
