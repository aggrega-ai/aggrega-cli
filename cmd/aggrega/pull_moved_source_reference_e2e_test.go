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

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// What this file proves, and where the seam sits.
//
// A source fetched from a git repository is pinned at the commit its authored
// reference resolved to. Move that reference at the remote and pull again: the
// question is whether the run adopts the new commit or refuses it. The product's
// own resolver states the answer — only a commit is immutable, a re-pushed
// reference is drift at the next pull, and an upgrade is the remediation — so
// what is under test is whether the code keeps that sentence.
//
// The whole run is real: the command's own entry point, a real workspace, the
// real closure walk, the real lockfile writer and the real recipe freeze. The one
// thing stubbed is the transport, through the seam the operation declares for it,
// and the stub is what MOVES the reference — a reference that resolves to one
// commit and then to another is exactly a force-pushed tag, and driving it from a
// field makes the move the single variable between the cases below.
//
// The three cases are chosen to disagree with each other. A refusal that refused
// everything would pass the moved case and fail the unmoved one; a refusal keyed
// on nothing would pass both and fail the upgrade.

const (
	movedRefCommitBefore = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	movedRefCommitAfter  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// movingReferenceResolver answers every source resolution with whatever commit
// its field currently names, which is how this file moves a reference upstream
// without a remote. The digest is derived from that commit, so the recorded
// digest moves with the pinned version exactly as a real fetch's would — which is
// the property that makes the build-time digest gate useless against this defect
// and is therefore the property the fixture has to reproduce.
type movingReferenceResolver struct {
	commit string
	calls  int
}

func (r *movingReferenceResolver) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	r.calls++
	return &closure.ResolvedSource{
		Kind:           spec.Kind,
		URL:            spec.URL,
		Chart:          spec.Chart,
		ModuleSubdir:   spec.ModuleSubdir,
		PinnedVersion:  r.commit,
		Digest:         digest.ContentDigest([]byte(r.commit)),
		Mediatype:      "application/gzip",
		OriginalSource: spec.OriginalSource,
	}, nil
}

// writeGitRefWorkspace lays down the minimal workspace of writeValidWorkspace and
// rewrites its Recipe to source a git repository at a TAG rather than at a
// commit. The authored `version:` and `ref:` differ from the commit the reference
// resolves to, which is the condition under which the lockfile records the
// authored reference at all — and without that record there is nothing for a
// later pull to hold the reference to.
func writeGitRefWorkspace(t *testing.T, root string) (releasePath, catalogRoot string) {
	t.Helper()
	releasePath, catalogRoot = writeValidWorkspace(t, root)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  source:
    kind: git-repository
    url: git://example.invalid/repo
    version: "v1.0.0"
    ref: "v1.0.0"
    module_subdir: modules/alpha
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`)
	return releasePath, catalogRoot
}

// pinnedSourceVersion reads back the one source-artifact pin the fixture
// produces, failing the test rather than returning a zero value when the pin is
// absent — an assertion made against a missing row would pass for the wrong
// reason.
func pinnedSourceVersion(t *testing.T, lockfilePath string) (pinned, requestedRef string) {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		t.Fatalf("load lockfile %s: %v", lockfilePath, err)
	}
	if len(lf.SourceArtifacts) != 1 {
		t.Fatalf("the fixture must pin exactly one source artifact, or nothing below is about a source pin; got %d",
			len(lf.SourceArtifacts))
	}
	sa := lf.SourceArtifacts[0]
	if len(sa.ReferencedBy) != 1 {
		t.Fatalf("the pinned artifact must carry exactly one back-pointer; got %d", len(sa.ReferencedBy))
	}
	return sa.PinnedVersion, sa.ReferencedBy[0].RequestedRef
}

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// movedRefPull drives the command's own resolution entry over the fixture.
//
// It asks the pull to run the pin step, as --pin-sources does: the fixture's
// Recipe carries an authored-identity git source, and by default a pull records
// no pin beside it and halts, which would stop every case here before the
// reference it is about is ever compared.
func movedRefPull(t *testing.T, root, releasePath, catalogRoot string, res *movingReferenceResolver, upgrade, verify bool) error {
	t.Helper()
	pin := true
	return pullRun(context.Background(), pullConfig{
		ws:             resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath:    releasePath,
		CatalogDirs:    []string{catalogRoot},
		Upgrade:        upgrade,
		Verify:         verify,
		PinSources:     &pin,
		sourceResolver: res,
		DiagOut:        io.Discard,
		Out:            io.Discard,
	})
}

// TestPullRefusesAMovedSourceReference is the defect's end-to-end proof. It
// asserts the three things the silent adoption did — rewrote the pin, rewrote the
// recipe file, and exited successfully — are each no longer true, and it asserts
// them on the same fixture that establishes the pin in the first place.
func TestPullRefusesAMovedSourceReference(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeGitRefWorkspace(t, root)
	recipePath := filepath.Join(catalogRoot, "recipes", "helm", "server.yaml")
	lockfilePath := filepath.Join(root, "release.lock")

	res := &movingReferenceResolver{commit: movedRefCommitBefore}
	if err := movedRefPull(t, root, releasePath, catalogRoot, res, false, false); err != nil {
		t.Fatalf("the first pull must establish the pin, or there is nothing to move: %v", err)
	}
	pinned, requestedRef := pinnedSourceVersion(t, lockfilePath)
	if pinned != movedRefCommitBefore {
		t.Fatalf("the first pull must pin the commit the reference resolved to; pinned %q", pinned)
	}
	// Without a recorded reference there is nothing to hold a later pull to, so
	// this is a precondition of the whole file rather than an incidental check.
	if requestedRef != "v1.0.0" {
		t.Fatalf("the lockfile must record the authored reference, otherwise the refusal below "+
			"would have nothing to join on; recorded %q", requestedRef)
	}
	lockBefore := readFileForTest(t, lockfilePath)
	recipeBefore := readFileForTest(t, recipePath)

	// The reference is force-moved at the remote. Nothing in the workspace changes.
	res.commit = movedRefCommitAfter

	err := movedRefPull(t, root, releasePath, catalogRoot, res, false, false)
	if err == nil {
		t.Fatal("a plain pull adopted a moved reference silently; it must refuse it")
	}
	if !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("the refusal must carry the lockfile-drift condition; got: %v", err)
	}
	t.Logf("emitted diagnostic:\n%v", err)
	for _, want := range []string{movedRefCommitBefore, movedRefCommitAfter, "v1.0.0", "--upgrade"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so a reader cannot act on it", want)
		}
	}

	// The refusal has to land BEFORE anything is written. A refusal that halted
	// after the freeze would leave the recipe file carrying a pin nobody accepted.
	if got := readFileForTest(t, lockfilePath); got != lockBefore {
		t.Error("the refused pull rewrote the lockfile; it must leave the workspace as it found it")
	}
	if got := readFileForTest(t, recipePath); got != recipeBefore {
		t.Error("the refused pull rewrote the recipe file; it must leave the workspace as it found it")
	}
}

// TestPullVerifyRefusesAMovedSourceReference covers the surface a reviewer
// actually reaches for. Verify returns before the source descent runs, so its
// closure carries no source artifacts at all — which is why this cannot be an
// axis folded into the closure comparison and has to re-ask the reference where
// it points.
func TestPullVerifyRefusesAMovedSourceReference(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeGitRefWorkspace(t, root)

	res := &movingReferenceResolver{commit: movedRefCommitBefore}
	if err := movedRefPull(t, root, releasePath, catalogRoot, res, false, false); err != nil {
		t.Fatalf("the first pull must establish the pin: %v", err)
	}

	// Positive control: with the reference where it was, verify must still pass.
	// Without this, an assertion that verify fails would be satisfied by a verify
	// that fails on everything.
	if err := movedRefPull(t, root, releasePath, catalogRoot, res, false, true); err != nil {
		t.Fatalf("verify must pass while the reference has not moved, or its refusal below "+
			"proves nothing: %v", err)
	}

	res.commit = movedRefCommitAfter
	err := movedRefPull(t, root, releasePath, catalogRoot, res, false, true)
	if err == nil {
		t.Fatal("verify reported a match while the reference had moved")
	}
	if !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("the verify refusal must carry the lockfile-drift condition; got: %v", err)
	}
	t.Logf("emitted diagnostic:\n%v", err)
}

// TestPullUpgradeAdoptsAMovedSourceReference is the other half of the promise the
// resolver makes. The refusal names the upgrade as the way out, so the upgrade
// has to be one: a refusal with no remedy would have replaced a silent adoption
// with a dead end.
func TestPullUpgradeAdoptsAMovedSourceReference(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeGitRefWorkspace(t, root)
	lockfilePath := filepath.Join(root, "release.lock")

	res := &movingReferenceResolver{commit: movedRefCommitBefore}
	if err := movedRefPull(t, root, releasePath, catalogRoot, res, false, false); err != nil {
		t.Fatalf("the first pull must establish the pin: %v", err)
	}

	res.commit = movedRefCommitAfter
	if err := movedRefPull(t, root, releasePath, catalogRoot, res, true, false); err != nil {
		t.Fatalf("an upgrade must adopt the moved reference: %v", err)
	}
	pinned, _ := pinnedSourceVersion(t, lockfilePath)
	if pinned != movedRefCommitAfter {
		t.Fatalf("the upgrade must pin where the reference now points; pinned %q", pinned)
	}
}

// TestPullStillSucceedsWhileTheReferenceHasNotMoved is the negative control for
// the whole file. Re-pulling an unchanged workspace is the ordinary case, and a
// refusal that caught it would have made every reconcile fail.
func TestPullStillSucceedsWhileTheReferenceHasNotMoved(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeGitRefWorkspace(t, root)
	lockfilePath := filepath.Join(root, "release.lock")

	res := &movingReferenceResolver{commit: movedRefCommitBefore}
	if err := movedRefPull(t, root, releasePath, catalogRoot, res, false, false); err != nil {
		t.Fatalf("the first pull must establish the pin: %v", err)
	}
	if err := movedRefPull(t, root, releasePath, catalogRoot, res, false, false); err != nil {
		t.Fatalf("re-pulling an unmoved reference must still succeed: %v", err)
	}
	pinned, _ := pinnedSourceVersion(t, lockfilePath)
	if pinned != movedRefCommitBefore {
		t.Fatalf("the second pull must leave the pin alone; pinned %q", pinned)
	}
	if res.calls < 2 {
		t.Fatalf("the source was resolved %d time(s): a second pull that never re-resolved "+
			"would satisfy this test without ever comparing anything", res.calls)
	}
}
