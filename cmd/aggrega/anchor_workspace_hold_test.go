// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestABuildIsHeldToTheRootTheDirectoryItIsRunInPicks drives whole builds whose
// output and trace directories are composed in a directory of their own — the
// directory the command was run in, spelled here as the absolute output and trace
// directories so no test has to move its working directory — over the shapes
// that directory takes against the workspace. One fault per row, beside a control
// building from a plain directory outside the workspace.
//
// A directory outside the workspace is a root of its own: an output or trace
// directory beneath it that is a link leading out of it halts the build, and the
// place the link leads to is left as it was. A directory reached through a link
// leading out of the workspace is followed where it was named. A directory inside
// the workspace holds the build's writes to the workspace root, so an output
// directory linked elsewhere inside the workspace is followed.
func TestABuildIsHeldToTheRootTheDirectoryItIsRunInPicks(t *testing.T) {
	root := t.TempDir()
	releasePath, catalogDir := dryRunFixture(t, root, "")
	primeDryRunLockfile(t, root, releasePath, catalogDir)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	rows := []struct {
		name string
		// arrange returns the directory the build runs in, whether it traces, the
		// place a link leads to that must stay as it was ("" for none), and where
		// the output must land when the row is not refused.
		arrange func(t *testing.T) (runIn string, trace bool, leadsTo, landsIn string)
		refused bool
	}{
		{
			name: "control: a build run in a directory outside the workspace writes its output there",
			arrange: func(t *testing.T) (string, bool, string, string) {
				run := t.TempDir()
				return run, false, "", filepath.Join(run, "dist")
			},
		},
		{
			name: "an output directory that is a link leading out of the directory run in halts the build",
			arrange: func(t *testing.T) (string, bool, string, string) {
				run, elsewhere := t.TempDir(), t.TempDir()
				writeFixture(t, filepath.Join(elsewhere, "boundaries", "old", "sentinel.txt"), "old\n")
				linkOrSkipBuild(t, elsewhere, filepath.Join(run, "dist"))
				return run, false, elsewhere, ""
			},
			refused: true,
		},
		{
			name: "a trace directory that is a link leading out of the directory run in halts the build",
			arrange: func(t *testing.T) (string, bool, string, string) {
				run, elsewhere := t.TempDir(), t.TempDir()
				writeFixture(t, filepath.Join(elsewhere, "sentinel.txt"), "kept\n")
				linkOrSkipBuild(t, elsewhere, filepath.Join(run, buildops.DefaultTraceDir))
				return run, true, elsewhere, ""
			},
			refused: true,
		},
		{
			name: "a directory run in that is reached through a link leading out of the workspace is followed",
			arrange: func(t *testing.T) (string, bool, string, string) {
				target := t.TempDir()
				linked := filepath.Join(root, "run-here")
				linkOrSkipBuild(t, target, linked)
				t.Cleanup(func() { _ = os.Remove(linked) })
				return linked, false, "", filepath.Join(target, "dist")
			},
		},
		{
			name: "beneath a directory run in inside the workspace an output linked elsewhere inside it is followed",
			arrange: func(t *testing.T) (string, bool, string, string) {
				run := filepath.Join(root, "run-inside")
				kept := filepath.Join(root, "kept")
				if err := os.MkdirAll(run, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(kept, 0o755); err != nil {
					t.Fatal(err)
				}
				linkOrSkipBuild(t, kept, filepath.Join(run, "dist"))
				t.Cleanup(func() { _ = os.RemoveAll(run); _ = os.RemoveAll(kept) })
				return run, false, "", kept
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			runIn, trace, leadsTo, landsIn := row.arrange(t)
			before := treeSnapshot(t, leadsTo)

			opts := buildops.RunOptions{
				OutDir:   filepath.Join(runIn, "dist"),
				TraceDir: filepath.Join(runIn, buildops.DefaultTraceDir),
			}
			err := dispatchBuildWithDiag(context.Background(), ws, releasePath, []string{catalogDir}, trace,
				buildDiagOptions{quiet: true}, opts, io.Discard, io.Discard)

			if leadsTo != "" {
				if d := diffSnapshots(before, treeSnapshot(t, leadsTo)); d != "" {
					t.Errorf("the place the link leads to changed:\n%s", d)
				}
			}
			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a build its root holds: %v", err)
			}
			if len(treeSnapshot(t, landsIn)) == 0 {
				t.Errorf("the build wrote nothing beneath %s", landsIn)
			}
		})
	}
}

// TestPullHoldsTheLockfileBesideAReleaseToTheRootItsDirectoryPicks drives whole
// pulls of a Release whose lockfile is a link, one shape per row beside a control
// pulling a Release that lies outside the workspace and has no lockfile yet.
//
// A Release outside the workspace — one typed on the command line, as here, or
// named by the configuration — makes its directory a root of its own: a lockfile
// that is a link leading out of it is refused, and one leading elsewhere inside
// it is followed. A Release reached through a link leading out of the workspace
// is followed where it was named. A Release inside the workspace holds its
// lockfile to the workspace root, so a lockfile linked elsewhere in the workspace,
// outside the Release's own directory, is followed.
//
// Every link leads nowhere yet, so a row is held to a file being created or not
// created where the link points, and the pull reads no lockfile before it writes.
func TestPullHoldsTheLockfileBesideAReleaseToTheRootItsDirectoryPicks(t *testing.T) {
	rows := []struct {
		name string
		// arrange lays the Release out against the workspace at ws and returns the
		// Release, its catalog directory, and the place its lockfile must land at —
		// or, for a refused row, must not be created at.
		arrange func(t *testing.T, ws string) (release, catalog, landsAt string)
		refused bool
	}{
		{
			name: "control: a Release outside the workspace has its lockfile written beside it",
			arrange: func(t *testing.T, _ string) (string, string, string) {
				dir := t.TempDir()
				release := authoringFixture(t, dir)
				return release, filepath.Join(dir, "catalog"), lockfile.PathFor(release)
			},
		},
		{
			name: "a lockfile that is a link leading out of the directory of a Release outside the workspace is refused",
			arrange: func(t *testing.T, _ string) (string, string, string) {
				dir, elsewhere := t.TempDir(), t.TempDir()
				release := authoringFixture(t, dir)
				target := filepath.Join(elsewhere, "release.lock")
				linkOrSkipBuild(t, target, lockfile.PathFor(release))
				return release, filepath.Join(dir, "catalog"), target
			},
			refused: true,
		},
		{
			name: "a lockfile that is a link to elsewhere inside the directory of a Release outside the workspace is followed",
			arrange: func(t *testing.T, _ string) (string, string, string) {
				dir := t.TempDir()
				release := authoringFixture(t, dir)
				target := filepath.Join(dir, "kept", "release.lock")
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				linkOrSkipBuild(t, target, lockfile.PathFor(release))
				return release, filepath.Join(dir, "catalog"), target
			},
		},
		{
			name: "a Release reached through a link leading out of the workspace is followed where it was named",
			arrange: func(t *testing.T, ws string) (string, string, string) {
				dir := t.TempDir()
				authoringFixture(t, dir)
				linked := filepath.Join(ws, "linked")
				linkOrSkipBuild(t, dir, linked)
				return filepath.Join(linked, "release.yaml"), filepath.Join(linked, "catalog"), filepath.Join(dir, "release.lock")
			},
		},
		{
			name: "a lockfile beside a Release inside the workspace linked elsewhere inside it is followed",
			arrange: func(t *testing.T, ws string) (string, string, string) {
				dir := filepath.Join(ws, "rel")
				release := authoringFixture(t, dir)
				target := filepath.Join(ws, "other", "release.lock")
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				linkOrSkipBuild(t, target, lockfile.PathFor(release))
				return release, filepath.Join(dir, "catalog"), target
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})
			release, catalog, landsAt := row.arrange(t, ws)

			err := pullRun(context.Background(), pullConfig{ws: rw, ReleasePath: release, CatalogDirs: []string{catalog}})

			_, statErr := os.Stat(landsAt)
			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				if statErr == nil {
					t.Errorf("a lockfile was created where the link leads, at %s", landsAt)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a lockfile its root holds: %v", err)
			}
			if statErr != nil {
				t.Errorf("the lockfile did not land at %s: %v", landsAt, statErr)
			}
		})
	}
}

// TestAPullThatPinsHoldsEachEntityLockToTheCatalogDirectoryItWasFoundIn drives
// whole pulls asked to pin, over a Release whose Recipe sits in a catalog
// directory outside the workspace, named with --catalog. One shape per row
// beside a control whose lock is not yet written: a lock that is a link leading
// nowhere out of that directory, and the directory itself named through a link
// leading out of the workspace.
//
// A catalog directory named outside the workspace is a root of its own, so the
// lock beside an entity in it is held to it: a lock leading out of it is refused
// and not created where the link points. A directory named through a link
// leading out of the workspace is followed where it was named, so its lock is
// written where the link leads.
//
// The refused row's link leads nowhere, and that is what makes it a row about
// this hold. A lock that is a link to a file is one of the files the entity
// carries, and every reading of those files refuses a link among them — so a
// pull over such a lock halts on the link, whatever the hold would say, and a
// lock that is a link never completes a pull at all, wherever it leads.
func TestAPullThatPinsHoldsEachEntityLockToTheCatalogDirectoryItWasFoundIn(t *testing.T) {
	rows := []struct {
		name string
		// arrange lays the row out against the workspace at ws and the directory
		// the fixture was authored in, dir, and returns the catalog directory the
		// pull names and the place the lock must land at — or, for a refused row,
		// must not be created at.
		arrange func(t *testing.T, ws, dir string) (catalog, landsAt string)
		refused bool
	}{
		{
			name: "control: a lock not yet written beside an entity in a named directory outside the workspace",
			arrange: func(t *testing.T, _, dir string) (string, string) {
				catalog := filepath.Join(dir, "catalog")
				return catalog, filepath.Join(catalog, "recipes", "entity.lock")
			},
		},
		{
			name: "a lock that is a link leading nowhere out of the named catalog directory is refused",
			arrange: func(t *testing.T, _, dir string) (string, string) {
				catalog := filepath.Join(dir, "catalog")
				target := filepath.Join(t.TempDir(), "entity.lock")
				linkOrSkipBuild(t, target, filepath.Join(catalog, "recipes", "entity.lock"))
				return catalog, target
			},
			refused: true,
		},
		{
			name: "a catalog directory named through a link leading out of the workspace has its lock written where the link leads",
			arrange: func(t *testing.T, ws, dir string) (string, string) {
				linked := filepath.Join(ws, "linked")
				linkOrSkipBuild(t, dir, linked)
				return filepath.Join(linked, "catalog"), filepath.Join(dir, "catalog", "recipes", "entity.lock")
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			releasePath, catalogDir := dryRunFixture(t, dir, "")
			// The fixture pins its Recipe; the lock is taken away so the pull's pin
			// step has one to write.
			if err := os.Remove(filepath.Join(catalogDir, "recipes", "entity.lock")); err != nil {
				t.Fatalf("remove the fixture's lock: %v", err)
			}
			ws := t.TempDir()
			catalog, landsAt := row.arrange(t, ws, dir)

			pin := true
			err := pullRun(context.Background(), pullConfig{
				ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
				ReleasePath: releasePath,
				CatalogDirs: []string{catalog},
				PinSources:  &pin,
			})

			_, statErr := os.Stat(landsAt)
			if row.refused {
				if statErr == nil {
					t.Errorf("a lock was created where the link leads, at %s", landsAt)
				}
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a lock its root holds: %v", err)
			}
			if statErr != nil {
				t.Errorf("the lock did not land at %s: %v", landsAt, statErr)
			}
		})
	}
}

func linkOrSkipBuild(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
