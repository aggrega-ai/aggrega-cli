// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// What this file proves.
//
// A source pin recorded beside an entity is compared with the lockfile by two
// commands: a build, before it reads the cached archive, and a verifying pull. The
// two must compare in the same cells and stand down in the same cells, and where
// they refuse they must say the same thing — otherwise a verify passes a workspace
// its build refuses, or refuses one its build accepts.
//
// Two cells, each driven through BOTH commands over one workspace, with one planted
// defect and a control on either side of it:
//
//   - a source pinned at the version its author wrote, where both commands compare:
//     a drifted pin is refused by both, under one condition and in one sentence;
//   - a source written at a git reference, where the pull records the pin under the
//     commit the reference resolved to while a build looks it up under the version
//     the entity names: neither compares, so the same plant is accepted by both.
//
// The second cell is what makes the first attributable. A verify that refused every
// drifted pin would pass the first cell and fail the second; one that refused none
// would fail the first.

// buildOver drives the command's own build entry over a workspace, writing the
// rendered tree under root.
func buildOver(t *testing.T, root, releasePath, catalogDir string) error {
	t.Helper()
	return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath, []string{catalogDir}, false, io.Discard, io.Discard)
}

// plantDriftedSourcePin rewrites the lock beside recipePath with its one source
// pin's content digest replaced, leaving every other row as the pull wrote it.
func plantDriftedSourcePin(t *testing.T, recipePath string) []types.SourceDigest {
	t.Helper()
	lock := readLockForTest(t, recipePath)
	if len(lock.SourceDigests) != 1 {
		t.Fatalf("the fixture must record exactly one source pin; the lock records %d", len(lock.SourceDigests))
	}
	drifted := append([]types.SourceDigest(nil), lock.SourceDigests...)
	drifted[0].Digest = "sha256:" + strings.Repeat("0", 64)
	planted := lock.Pins()
	planted.SourceDigests = drifted
	if _, err := recipepin.WriteLock(recipePath, planted); err != nil {
		t.Fatalf("plant a drifted source pin: %v", err)
	}
	return drifted
}

// alignRecipeRow rewrites the lockfile's row of the Recipe uri to the digest the
// Recipe document at recipePath now carries, the lock beside it included.
//
// A planted source pin is a change to that lock, and the Recipe's lockfile row
// covers the lock, so without this both commands would halt on the moved row before
// either reached the source pin — a second fault in a fixture meant to carry one.
// Aligning the row leaves the planted source pin, compared against the lockfile's
// source-artifact row, as the only disagreement, which is the comparison these two
// cells are about.
func alignRecipeRow(t *testing.T, releasePath, recipePath, uri string) {
	t.Helper()
	path := lockfile.PathFor(releasePath)
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	aligned := false
	for i := range lf.Entities {
		if lf.Entities[i].URI == uri && lf.Entities[i].Kind == "recipe" {
			lf.Entities[i].Digest = rowDigestAt(t, recipePath)
			aligned = true
		}
	}
	if !aligned {
		t.Fatalf("the lockfile records no Recipe row for %q: %+v", uri, lf.Entities)
	}
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("write the aligned lockfile: %v", err)
	}
}

// TestBuildAndVerifyRefuseADriftedPinAtAnAuthoredVersionInOneSentence is the cell
// where both commands compare.
func TestBuildAndVerifyRefuseADriftedPinAtAnAuthoredVersionInOneSentence(t *testing.T) {
	t.Parallel()
	blob := pinChartTgz(t)
	srv := serveChart(t, blob)
	root := t.TempDir()
	releasePath := sourcedPinFixture(t, root, srv.URL+"/pinchart.tgz")
	catalogDir := filepath.Join(root, "catalog")
	recipePath := filepath.Join(catalogDir, "recipes", "server.yaml")
	lockPath := recipepin.LockPath(recipePath)

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("the seed pull must record the pin, or there is nothing to compare: %v", err)
	}
	intact := mustReadFile(t, lockPath)
	intactLockfile := mustReadFile(t, lockfile.PathFor(releasePath))
	if err := buildOver(t, root, releasePath, catalogDir); err != nil {
		t.Fatalf("build must accept the lock the pull just wrote, or its refusal below proves nothing: %v", err)
	}
	if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
		t.Fatalf("verify must accept the lock the pull just wrote, or its refusal below proves nothing: %v", err)
	}

	drifted := plantDriftedSourcePin(t, recipePath)
	alignRecipeRow(t, releasePath, recipePath, "server")
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.SourceArtifacts) != 1 {
		t.Fatalf("the fixture must pin exactly one source; the lockfile records %d", len(lf.SourceArtifacts))
	}
	row := lf.SourceArtifacts[0]
	var want *cerrors.CompilerError
	if !errors.As(recipepin.CheckSourcePin(drifted, lf, lockPath, row.Kind, row.URL, row.Chart, row.ModuleSubdir, row.PinnedVersion, ""), &want) {
		t.Fatal("the planted pin must be one the shared comparison refuses, or the cell is not the one this test is about")
	}

	buildErr := buildOver(t, root, releasePath, catalogDir)
	verifyErr := verifyPull(t, root, releasePath, catalogDir, nil)
	for _, side := range []struct {
		name string
		err  error
		text string
	}{
		{"build", buildErr, cerrors.CauseChain(buildErr)},
		{"verify", verifyErr, cerrors.CauseChain(verifyErr)},
	} {
		if side.err == nil {
			t.Errorf("%s accepted a source pin that disagrees with the lockfile at the version its entity authors", side.name)
			continue
		}
		if !errors.Is(side.err, oci.ErrFetchIntegrity()) {
			t.Errorf("%s refused under the wrong condition: %v", side.name, side.err)
		}
		if !strings.Contains(side.text, want.Message()) {
			t.Errorf("%s does not carry the one sentence both commands share.\nwant: %s\ngot:  %s", side.name, want.Message(), side.text)
		}
	}

	if err := os.WriteFile(lockPath, intact, 0o644); err != nil {
		t.Fatalf("restore the lock: %v", err)
	}
	if err := os.WriteFile(lockfile.PathFor(releasePath), intactLockfile, 0o644); err != nil {
		t.Fatalf("restore the lockfile: %v", err)
	}
	if err := buildOver(t, root, releasePath, catalogDir); err != nil {
		t.Errorf("build must accept the restored lock: %v", err)
	}
	if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
		t.Errorf("verify must accept the restored lock: %v", err)
	}
}

// localChartRepository commits a minimal chart under charts/hello to a git
// repository on disk and tags the commit, so a git-sourced Recipe resolves through
// the product's own resolver with no network. The directory's name ends in .git,
// which is what makes the reference read as fetched rather than as a path the
// workspace already holds.
func localChartRepository(t *testing.T, tag string) (url, commit string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "hello.git")
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init repository: %v", err)
	}
	writeFixture(t, filepath.Join(dir, "charts", "hello", "Chart.yaml"), "apiVersion: v2\nname: hello\nversion: 0.1.0\n")
	writeFixture(t, filepath.Join(dir, "charts", "hello", "templates", "cm.yaml"),
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-hello\ndata:\n  origin: git\n")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add("charts"); err != nil {
		t.Fatalf("stage the chart: %v", err)
	}
	hash, err := wt.Commit("chart", &git.CommitOptions{
		Author: &object.Signature{Name: "fixture", Email: "fixture@example.invalid", When: time.Unix(0, 0).UTC()},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := repo.CreateTag(tag, hash, nil); err != nil {
		t.Fatalf("tag: %v", err)
	}
	return dir, hash.String()
}

// TestBuildAndVerifyBothStandDownOnAPinRecordedUnderAResolvedReference is the cell
// where neither command compares.
func TestBuildAndVerifyBothStandDownOnAPinRecordedUnderAResolvedReference(t *testing.T) {
	t.Parallel()
	const tag = "hello-0.1.0"
	url, commit := localChartRepository(t, tag)
	root := t.TempDir()
	releasePath := sourcedPinFixture(t, root, "unused")
	catalogDir := filepath.Join(root, "catalog")
	recipePath := filepath.Join(catalogDir, "recipes", "server.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source:
    kind: git-repository
    url: `+url+`
    ref: `+tag+`
    module_subdir: charts/hello
`)

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("the seed pull must resolve the reference and record its pin: %v", err)
	}
	// The cell's premise, asserted rather than assumed: the pin is recorded under
	// the commit, and the lockfile reaches the row through the authored reference.
	lock := readLockForTest(t, recipePath)
	if len(lock.SourceDigests) != 1 || lock.SourceDigests[0].Version != commit {
		t.Fatalf("the pull must record one pin under the commit %s the reference resolved to; the lock records %+v", commit, lock.SourceDigests)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.SourceArtifacts) != 1 || len(lf.SourceArtifacts[0].ReferencedBy) == 0 || lf.SourceArtifacts[0].ReferencedBy[0].RequestedRef != tag {
		t.Fatalf("the lockfile must record the authored reference %q beside the row; it records %+v", tag, lf.SourceArtifacts)
	}
	if err := buildOver(t, root, releasePath, catalogDir); err != nil {
		t.Fatalf("build must accept the lock the pull just wrote: %v", err)
	}
	if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
		t.Fatalf("verify must accept the lock the pull just wrote: %v", err)
	}

	plantDriftedSourcePin(t, recipePath)
	alignRecipeRow(t, releasePath, recipePath, "server")
	buildErr := buildOver(t, root, releasePath, catalogDir)
	verifyErr := verifyPull(t, root, releasePath, catalogDir, nil)
	if (buildErr == nil) != (verifyErr == nil) {
		t.Fatalf("the two commands disagree about a pin recorded under a resolved reference.\nbuild:  %v\nverify: %v", buildErr, verifyErr)
	}
	if buildErr != nil {
		t.Fatalf("both commands refused a pin a build's own lookup does not reach; a build compares nothing there.\nbuild:  %v\nverify: %v", buildErr, verifyErr)
	}
}
