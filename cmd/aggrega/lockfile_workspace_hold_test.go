// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestPullHoldsTheLockfileToTheWorkspace drives a whole pull over the authoring
// fixture with the Release's lockfile being, one row at a time, absent, a link to
// a file outside the workspace, and a link leading nowhere outside.
//
// A refused row is held to the file the link leads to as well as to the
// condition: the file outside must hold what it held, and a file that was never
// there must still not be there. A lockfile that is a link to a lockfile outside
// is refused where the pull first reads it for its prior pins, before anything is
// resolved, so its condition is the read's; the last row is the shape the
// definition of inside passes for a read — a link leading nowhere is passed over
// for its directory, which is inside, and there is nothing to read — and a plain
// write through it would create the file it points at, so its condition is the
// write's.
func TestPullHoldsTheLockfileToTheWorkspace(t *testing.T) {
	rows := []struct {
		name   string
		layout func(t *testing.T, lock, outside string, pull func() error) string
		// refusedWith is the condition a refused row halts with; nil for a row
		// that completes.
		refusedWith error
	}{
		{name: "control: no lockfile yet", layout: func(*testing.T, string, string, func() error) string { return "" }},
		{name: "a lockfile that is a link to a lockfile outside", refusedWith: readscope.ErrReadLeadsOutOfWorkspace(), layout: func(t *testing.T, lock, outside string, pull func() error) string {
			// A real lockfile, so the pull reads it as one and reaches its write;
			// it is then moved outside and the workspace keeps a link to it.
			if err := pull(); err != nil {
				t.Fatalf("seeding pull: %v", err)
			}
			target := filepath.Join(outside, "release.lock")
			if err := os.Rename(lock, target); err != nil {
				t.Fatal(err)
			}
			// A byte the next pull would rewrite, so a write through the link is
			// visible as a change to the file outside.
			if err := os.WriteFile(target, append(mustReadFile(t, target), []byte("# not the workspace's\n")...), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, lock); err != nil {
				t.Skipf("symbolic links are not available here: %v", err)
			}
			return target
		}},
		{name: "a lockfile that is a link leading nowhere outside", refusedWith: writescope.ErrWriteLeadsOutOfWorkspace(), layout: func(t *testing.T, lock, outside string, _ func() error) string {
			target := filepath.Join(outside, "absent.lock")
			if err := os.Symlink(target, lock); err != nil {
				t.Skipf("symbolic links are not available here: %v", err)
			}
			return target
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			t.Chdir(root)
			releasePath := authoringFixture(t, root)
			pull := func() error {
				return pullRun(context.Background(), pullConfig{
					ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					ReleasePath: releasePath,
					CatalogDirs: []string{filepath.Join(root, "catalog")},
				})
			}
			target := row.layout(t, filepath.Join(root, "release.lock"), outside, pull)
			var before []byte
			if target != "" {
				before, _ = os.ReadFile(target)
			}

			err := pull()
			if row.refusedWith == nil {
				if err != nil {
					t.Fatalf("refused a lockfile the workspace holds: %v", err)
				}
				return
			}
			if !errors.Is(err, row.refusedWith) {
				t.Fatalf("want the %v halt, got %v", row.refusedWith, err)
			}
			after, aerr := os.ReadFile(target)
			switch {
			case before == nil && !os.IsNotExist(aerr):
				t.Errorf("a lockfile was created outside the workspace at %s", target)
			case before != nil && string(after) != string(before):
				t.Errorf("the file outside the workspace was rewritten:\n%s", after)
			}
		})
	}
}
