// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// The tests in this file hold what a build tells a user who edited their Release
// after pulling it. The Release is recorded in the lockfile by content, so an
// edit is drift: the build halts at the drift comparison, before anything
// renders, naming the Release file and telling them to pull again — rather than
// reaching the render and reporting whatever the edit makes it fail on, which for
// a changed source version is a fetch failure that points at the network.
//
// Every halting assertion reads the diagnostic, not the exit status: the build
// failed before the Release was recorded too, so a failure alone proves nothing.
// With the comparison removed, the version edit fails as a fetch error instead
// and each assertion that it is drift fails; the comment edit and the parent edit
// build cleanly instead and each assertion that they halt fails.

// verifySharedRecipeRelease runs `aggrega pull --verify --release` over one
// Release of the shared-recipe fixture.
func verifySharedRecipeRelease(t *testing.T, root, catalogDir, releasePath string) error {
	t.Helper()
	return pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		Verify:      true,
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	})
}

// rewriteRelease replaces one exact substring of the Release at path, failing the
// test when the substring is absent so a fixture that stopped carrying it cannot
// turn the edit into a no-op.
func rewriteRelease(t *testing.T, path, from, to string) {
	t.Helper()
	body := string(mustReadFile(t, path))
	if !strings.Contains(body, from) {
		t.Fatalf("test setup: %s does not carry %q", path, from)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(body, from, to, 1)), 0o644); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}
}

// requireReleaseDrift asserts that err is the lockfile-drift condition raised for
// the named Release file, saying that re-resolving the Release clears it, that it
// names no placeholder Release, and that nothing about it is a fetch. It holds on
// every surface that reports the entry, so it asserts what the entry says rather
// than the command a build prints beside it.
func requireReleaseDrift(t *testing.T, err error, step, file, condition string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected the build to halt on the edited Release %s; it succeeded", step, file)
	}
	if !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("%s: expected the lockfile-drift condition naming %s; got %v", step, file, err)
	}
	if errors.Is(err, oci.ErrFetchNotFound()) {
		t.Errorf("%s: the halt still carries a fetch failure: %v", step, err)
	}
	msg := err.Error()
	for _, want := range []string{`release document "` + file + `" ` + condition,
		"re-resolving the Release records the files it reads as they now stand"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%s: diagnostic does not carry %q:\n%s", step, want, msg)
		}
	}
	for _, wrong := range []string{"no lockfile entry", "verify the URL", "offline", "<release>"} {
		if strings.Contains(msg, wrong) {
			t.Errorf("%s: diagnostic still points at %q:\n%s", step, wrong, msg)
		}
	}
}

// TestEditedReleaseHaltsAtTheDriftComparison drives the sequence a user meets:
// pull a Release naming one chart version, build it, edit the version, build
// again. The untouched pull, verify and build must all pass — the comparison
// must not fire on a Release that did not change — and the edited build and
// verify must halt as drift naming the Release. A pull then records the edit and
// the build renders the version the Release now names, which is the remediation
// the diagnostic gives actually working.
func TestEditedReleaseHaltsAtTheDriftComparison(t *testing.T) {
	t.Parallel()
	srv := serveMultiVersionHelmRepo(t, "0.6.0", "0.7.0")
	root := t.TempDir()
	relA, _ := sharedRecipeTwoReleasesFixture(t, root, srv.URL)
	catalogDir := filepath.Join(root, "catalog")
	dist := filepath.Join(root, "dist", "a")

	if err := pullSharedRecipeRelease(t, root, catalogDir, relA, nil); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if err := verifySharedRecipeRelease(t, root, catalogDir, relA); err != nil {
		t.Fatalf("pull --verify over an untouched Release must pass: %v", err)
	}
	if err := buildSharedRecipeRelease(t, root, catalogDir, relA); err != nil {
		t.Fatalf("build over an untouched Release must pass: %v", err)
	}
	if got := renderedChartVersion(t, dist); got != "0.6.0" {
		t.Fatalf("untouched build rendered chart %q, want 0.6.0", got)
	}

	rewriteRelease(t, relA, `chart_version: "0.6.0"`, `chart_version: "0.7.0"`)
	requireReleaseDrift(t, verifySharedRecipeRelease(t, root, catalogDir, relA),
		"pull --verify after the version edit", "release-a.yaml", "was edited after the lockfile was written")
	requireReleaseDrift(t, buildSharedRecipeRelease(t, root, catalogDir, relA),
		"build after the version edit", "release-a.yaml", "was edited after the lockfile was written")

	if err := pullSharedRecipeRelease(t, root, catalogDir, relA, nil); err != nil {
		t.Fatalf("pull after the version edit: %v", err)
	}
	if err := buildSharedRecipeRelease(t, root, catalogDir, relA); err != nil {
		t.Fatalf("build after pulling the edit must pass: %v", err)
	}
	if got := renderedChartVersion(t, dist); got != "0.7.0" {
		t.Errorf("after pulling the edit the build rendered chart %q, want 0.7.0", got)
	}
}

// TestInertReleaseEditIsDrift holds the inert-edit rule: a Release is recorded by
// its bytes, with nothing normalised away, so a comment appended to it halts the
// next build exactly as a comment appended to the Aggregate does, and a pull
// clears it.
func TestInertReleaseEditIsDrift(t *testing.T) {
	t.Parallel()
	srv := serveMultiVersionHelmRepo(t, "0.6.0", "0.7.0")
	root := t.TempDir()
	relA, _ := sharedRecipeTwoReleasesFixture(t, root, srv.URL)
	catalogDir := filepath.Join(root, "catalog")

	if err := pullSharedRecipeRelease(t, root, catalogDir, relA, nil); err != nil {
		t.Fatalf("pull: %v", err)
	}
	body := mustReadFile(t, relA)
	if err := os.WriteFile(relA, append(body, []byte("# an inert comment\n")...), 0o644); err != nil {
		t.Fatalf("append comment: %v", err)
	}
	requireReleaseDrift(t, buildSharedRecipeRelease(t, root, catalogDir, relA),
		"build after a comment edit", "release-a.yaml", "was edited after the lockfile was written")
	if err := pullSharedRecipeRelease(t, root, catalogDir, relA, nil); err != nil {
		t.Fatalf("pull after the comment edit: %v", err)
	}
	if err := buildSharedRecipeRelease(t, root, catalogDir, relA); err != nil {
		t.Errorf("build after pulling the comment edit must pass: %v", err)
	}
}

// TestEditedExtendedReleaseIsDrift holds that the Release a Release extends is
// recorded too: editing the parent is drift for every Release composed from it,
// and the entry names the parent file, which is the one the user edited.
func TestEditedExtendedReleaseIsDrift(t *testing.T) {
	srv := serveMultiVersionHelmRepo(t, "0.6.0", "0.7.0")
	root := t.TempDir()
	t.Chdir(root)
	relA, _ := sharedRecipeTwoReleasesFixture(t, root, srv.URL)
	catalogDir := filepath.Join(root, "catalog")
	child := filepath.Join(root, "release-child.yaml")
	writeFixture(t, child, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: release-child
  environment: testing
  version: "1.0.0"
spec:
  extends: ./release-a.yaml
`)

	if err := pullSharedRecipeRelease(t, root, catalogDir, child, nil); err != nil {
		t.Fatalf("pull the child: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(child))
	if err != nil {
		t.Fatalf("load the child's lockfile: %v", err)
	}
	var paths []string
	for _, d := range lf.ReleaseDocuments {
		paths = append(paths, d.Path)
	}
	if strings.Join(paths, ",") != "release-child.yaml,release-a.yaml" {
		t.Fatalf("the child's lockfile records release documents %v, want [release-child.yaml release-a.yaml]", paths)
	}
	if err := buildSharedRecipeRelease(t, root, catalogDir, child); err != nil {
		t.Fatalf("build the untouched child: %v", err)
	}

	rewriteRelease(t, relA, `chart_version: "0.6.0"`, `chart_version: "0.7.0"`)
	requireReleaseDrift(t, buildSharedRecipeRelease(t, root, catalogDir, child),
		"build the child after its parent was edited", "release-a.yaml", "was edited after the lockfile was written")
}

// TestLockfileWithoutReleaseDocumentsTellsTheUserToPull holds what a user holding
// a lockfile that records no Release file meets: not a crash and not a pass, but
// the drift condition naming the Release as not recorded and telling them to
// pull, after which the build passes.
func TestLockfileWithoutReleaseDocumentsTellsTheUserToPull(t *testing.T) {
	t.Parallel()
	srv := serveMultiVersionHelmRepo(t, "0.6.0", "0.7.0")
	root := t.TempDir()
	relA, _ := sharedRecipeTwoReleasesFixture(t, root, srv.URL)
	catalogDir := filepath.Join(root, "catalog")

	if err := pullSharedRecipeRelease(t, root, catalogDir, relA, nil); err != nil {
		t.Fatalf("pull: %v", err)
	}
	lockPath := lockfile.PathFor(relA)
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.ReleaseDocuments) == 0 {
		t.Fatal("precondition: a pull must record the Release it resolved")
	}
	lf.ReleaseDocuments = nil
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("write the lockfile without the section: %v", err)
	}
	requireReleaseDrift(t, buildSharedRecipeRelease(t, root, catalogDir, relA),
		"build over a lockfile recording no Release file", "release-a.yaml", "is not recorded in the lockfile")
	if err := pullSharedRecipeRelease(t, root, catalogDir, relA, nil); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if err := buildSharedRecipeRelease(t, root, catalogDir, relA); err != nil {
		t.Errorf("build after the pull must pass: %v", err)
	}
}
