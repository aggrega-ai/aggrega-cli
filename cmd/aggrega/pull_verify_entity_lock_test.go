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

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// What this file proves.
//
// `aggrega pull --verify` is the command whose job is to say whether the committed
// files still describe what a build of the Release will accept. The pins a pull
// writes into the entity.lock beside a Recipe are among those files, and a build
// halts when one of them disagrees with what it compares it against — so a verify
// that passed over such a workspace would be reporting a match the build then
// refuses.
//
// Each class is driven through the command's own entry point over a real
// workspace, with one planted defect at a time and a positive control on either
// side of it: the intact lock verifies, the drifted one is refused by the verify
// and by a build of the same tree under one condition, and the restored one
// verifies again. A verify that refused everything would fail the controls; one
// that refused nothing would fail the plant.
//
// The condition both meet is the lockfile's: the Recipe's entity row covers every
// file a publication of it carries — the file an artifacts entry imports and the
// lock beside it included — so a change to either moves the row, and the row is
// compared before any single file is. Which file moved is named by the pull that
// holds the lock to the pin step, which is where the per-file detail lives; each
// plant drives that pull too.

// pullHolding drives the pull as it runs when it does not pin, which is the
// command's own default: it holds the lock beside each entity to the pins the step
// would record, and halts on an input the lock does not record at that value,
// naming the entity, the input and its class. These fixtures' workspaces may pin at
// a pull, so the run names the default rather than inheriting the setting.
func pullHolding(t *testing.T, root, releasePath, catalogDir string, res closure.SourceResolver) error {
	t.Helper()
	hold := false
	return pullRun(context.Background(), pullConfig{
		ws:             resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath:    releasePath,
		CatalogDirs:    []string{catalogDir},
		PinSources:     &hold,
		sourceResolver: res,
		DiagOut:        io.Discard,
		Out:            io.Discard,
		diag:           buildDiagOptions{quiet: true},
	})
}

// entityRow returns the digest the lockfile written for releasePath records for
// the Recipe uri, failing the test where it records none. It reads the Recipe's
// own row: a Component sharing the Recipe's name has a row of its own.
func entityRow(t *testing.T, releasePath, uri string) string {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	for _, e := range lf.Entities {
		if e.URI == uri && e.Kind == "recipe" {
			return e.Digest
		}
	}
	t.Fatalf("the lockfile records no Recipe row for %q: %+v", uri, lf.Entities)
	return ""
}

// assertRowRefusal holds a verify and a build of one tree to one refusal: the
// lockfile-drift condition, naming the entity whose row moved with the recorded and
// the current digest, in the same words on both surfaces. before is the condition a
// comparison of a single file raises, which must not be what either surface met: the
// row is compared first, so meeting the file's condition instead would mean a
// surface reached the file before the record that covers it.
func assertRowRefusal(t *testing.T, verifyErr, buildErr error, uri, recorded, current string, before error) {
	t.Helper()
	// Every caller hands a Recipe's reference: the lock beside a Recipe is what moves.
	want := `entity "` + uri + `" (recipe) changed: lockfile=(1.0.0,` + recorded + `) current=(1.0.0,` + current + `)`
	for _, s := range []struct {
		name string
		err  error
	}{{"pull --verify", verifyErr}, {"build", buildErr}} {
		if s.err == nil {
			t.Errorf("%s reported a match over a tree whose entity row moved", s.name)
			continue
		}
		if !errors.Is(s.err, lockfile.ErrLockfileDrift()) {
			t.Errorf("%s: want the lockfile-drift condition, which a build and a verify both halt with; got %v", s.name, s.err)
		}
		if errors.Is(s.err, before) {
			t.Errorf("%s met the single file's condition rather than the row that covers it: %v", s.name, s.err)
		}
		if !strings.Contains(s.err.Error(), want) {
			t.Errorf("%s: the refusal does not name the entity and both digests; want %q in:\n%v", s.name, want, s.err)
		}
	}
}

// assertHoldingPullNames holds the pull that does not pin to naming what moved: the
// unpinned-entity condition, naming the input whose recorded pin no longer matches,
// and leaving the lock and the lockfile as they were.
func assertHoldingPullNames(t *testing.T, err error, lockPath, lockfilePath string, lockBefore, lockfileBefore []byte, input string) {
	t.Helper()
	if !errors.Is(err, recipepin.ErrEntityNotPinned()) {
		t.Fatalf("the pull that holds the lock must refuse it under the unpinned-entity condition; got %v", err)
	}
	for _, want := range []string{input, "the recorded pin no longer matches it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the pull does not name %q, so a reader cannot tell which input moved: %v", want, err)
		}
	}
	if got := mustReadFile(t, lockPath); string(got) != string(lockBefore) {
		t.Error("the pull that holds the lock rewrote it")
	}
	if got := mustReadFile(t, lockfilePath); string(got) != string(lockfileBefore) {
		t.Error("the pull that halted rewrote the lockfile")
	}
}

// verifyPull drives the command's verify over a workspace, with the source
// resolver the seed pull used so the two runs resolve through the same seam.
func verifyPull(t *testing.T, root, releasePath, catalogDir string, res closure.SourceResolver) error {
	t.Helper()
	return pullRun(context.Background(), pullConfig{
		ws:             resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath:    releasePath,
		CatalogDirs:    []string{catalogDir},
		Verify:         true,
		sourceResolver: res,
		DiagOut:        io.Discard,
		Out:            io.Discard,
		diag:           buildDiagOptions{quiet: true},
	})
}

// readLockForTest reads the lock beside an entity, failing the test where there is
// none — every assertion below is about a pin the lock records, so a missing lock
// would make them pass for the wrong reason.
func readLockForTest(t *testing.T, entityPath string) recipepin.Lock {
	t.Helper()
	lock, present, err := recipepin.ReadLock(entityPath)
	if err != nil {
		t.Fatalf("read the lock beside %s: %v", entityPath, err)
	}
	if !present {
		t.Fatalf("the seed pull wrote no lock beside %s, so nothing below is about a recorded pin", entityPath)
	}
	return lock
}

// TestPullVerifyRefusesAnImportedFileThatNoLongerMatchesItsPin covers the pin a
// never-published workspace has no other cover for: the digest of a file an
// artifacts entry imports. It is planted two ways, because either file can be the
// one that moved — the imported file edited with no pull afterwards, and the pin
// itself edited in the lock. Each is refused by the verify and by a build of the
// same tree under one condition, and named, file by file, by the pull that holds
// the lock.
func TestPullVerifyRefusesAnImportedFileThatNoLongerMatchesItsPin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath := fullURICollisionFixture(t, root)
	catalogDir := filepath.Join(root, "catalog")
	recipePath := filepath.Join(catalogDir, "recipes", "server.yaml")
	importPath := filepath.Join(catalogDir, "recipes", "extra.yaml")
	const recipeURI = "helm/server:1.0.0"

	pullWriting(t, root, releasePath, catalogDir)
	lock := readLockForTest(t, recipePath)
	if len(lock.ArtifactDigests) != 1 {
		t.Fatalf("the fixture must pin exactly one imported file; the lock records %d", len(lock.ArtifactDigests))
	}
	lockPath := recipepin.LockPath(recipePath)
	lockfilePath := lockfile.PathFor(releasePath)
	lockBytes := mustReadFile(t, lockPath)
	importBytes := mustReadFile(t, importPath)
	recorded := entityRow(t, releasePath, recipeURI)
	if got := rowDigestAt(t, recipePath); got != recorded {
		t.Fatalf("the lockfile records %s for %s, not the row %s the Recipe digests to, so the plants below are not about that row", recorded, recipeURI, got)
	}

	if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
		t.Fatalf("verify must pass over the lock the pull just wrote, or its refusals below prove nothing: %v", err)
	}
	if err := buildOver(t, root, releasePath, catalogDir); err != nil {
		t.Fatalf("a build must pass over the lock the pull just wrote, or its refusals below prove nothing: %v", err)
	}

	// Plant one: the imported file edited after the pull that pinned it.
	edited := []byte(strings.Replace(string(importBytes), "origin: imported", "origin: edited", 1))
	if string(edited) == string(importBytes) {
		t.Fatalf("could not edit the imported file; the plant would change nothing")
	}
	if err := os.WriteFile(importPath, edited, 0o644); err != nil {
		t.Fatalf("edit the imported file: %v", err)
	}
	lockfileBytes := mustReadFile(t, lockfilePath)
	assertRowRefusal(t, verifyPull(t, root, releasePath, catalogDir, nil), buildOver(t, root, releasePath, catalogDir),
		recipeURI, recorded, rowDigestAt(t, recipePath), renderer.ErrArtifactContentMismatch())
	if got := mustReadFile(t, lockPath); string(got) != string(lockBytes) {
		t.Error("a verify rewrote the lock; verify writes nothing")
	}
	assertHoldingPullNames(t, pullHolding(t, root, releasePath, catalogDir, nil), lockPath, lockfilePath, lockBytes, lockfileBytes, "extra.yaml")

	if err := os.WriteFile(importPath, importBytes, 0o644); err != nil {
		t.Fatalf("restore the imported file: %v", err)
	}
	if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
		t.Fatalf("verify must pass again once the file is restored: %v", err)
	}

	// Plant two: the pin edited in the lock, the imported file untouched.
	drifted := []types.ArtifactDigest{{Name: lock.ArtifactDigests[0].Name, Digest: "sha256:" + strings.Repeat("0", 64)}}
	plantedPins := lock.Pins()
	plantedPins.ArtifactDigests = drifted
	if _, err := recipepin.WriteLock(recipePath, plantedPins); err != nil {
		t.Fatalf("plant a drifted pin: %v", err)
	}
	plantedBytes := mustReadFile(t, lockPath)
	assertRowRefusal(t, verifyPull(t, root, releasePath, catalogDir, nil), buildOver(t, root, releasePath, catalogDir),
		recipeURI, recorded, rowDigestAt(t, recipePath), renderer.ErrArtifactContentMismatch())
	assertHoldingPullNames(t, pullHolding(t, root, releasePath, catalogDir, nil), lockPath, lockfilePath, plantedBytes, lockfileBytes, "extra.yaml")
	if err := os.WriteFile(lockPath, lockBytes, 0o644); err != nil {
		t.Fatalf("restore the lock: %v", err)
	}
	if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
		t.Fatalf("verify must pass again once the lock is restored: %v", err)
	}
}

// verbatimSourceResolver answers every source resolution at exactly the version
// the Recipe names, which is how a registry-served chart resolves: the pinned
// version is the authored one and no reference is recorded. The digest is derived
// from the identity so the lockfile and the lock both carry a stable value without
// a remote.
type verbatimSourceResolver struct{}

func (verbatimSourceResolver) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	return &closure.ResolvedSource{
		Kind:           spec.Kind,
		URL:            spec.URL,
		Chart:          spec.Chart,
		ModuleSubdir:   spec.ModuleSubdir,
		PinnedVersion:  spec.Version,
		Digest:         digest.ContentDigest([]byte(spec.URL + "@" + spec.Version)),
		Mediatype:      "application/gzip",
		OriginalSource: spec.OriginalSource,
	}, nil
}

// TestPullVerifyRefusesASourcePinTheLockfileDisagreesWith covers the other pin
// class: the digest a pull records beside a Recipe for the remote source it
// acquired. A pin edited in the lock moves the Recipe's entity row, which covers the
// lock, so the verify and a build of the same tree refuse it under one condition;
// the pull that holds the lock names the source whose pin moved.
func TestPullVerifyRefusesASourcePinTheLockfileDisagreesWith(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogDir := writeValidWorkspace(t, root)
	recipePath := filepath.Join(catalogDir, "recipes", "helm", "server.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  source:
    kind: helm-oci-chart
    url: oci://example.invalid/charts/server
    version: "1.2.3"
  workload: |
    apiVersion: aggrega.ai/v1beta1
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
	res := verbatimSourceResolver{}

	// The seed pull runs the pin step, as --pin-sources does, because the pin it
	// records beside the Recipe is what the verify below is held to.
	pinSources := true
	if err := pullRun(context.Background(), pullConfig{
		ws:             resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath:    releasePath,
		CatalogDirs:    []string{catalogDir},
		PinSources:     &pinSources,
		sourceResolver: res,
		DiagOut:        io.Discard,
		Out:            io.Discard,
		diag:           buildDiagOptions{quiet: true},
	}); err != nil {
		t.Fatalf("the seed pull must record the source pin, or there is nothing to verify: %v", err)
	}
	lock := readLockForTest(t, recipePath)
	if len(lock.SourceDigests) != 1 {
		t.Fatalf("the fixture must pin exactly one source; the lock records %d", len(lock.SourceDigests))
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.SourceArtifacts) != 1 || lf.SourceArtifacts[0].Digest != lock.SourceDigests[0].Digest {
		t.Fatalf("the lockfile and the lock must record the same source digest after one pull; lockfile %+v, lock %+v",
			lf.SourceArtifacts, lock.SourceDigests)
	}
	lockPath := recipepin.LockPath(recipePath)
	lockfilePath := lockfile.PathFor(releasePath)
	lockBytes := mustReadFile(t, lockPath)
	const recipeURI = "server-recipe:1.0.0"
	recorded := entityRow(t, releasePath, recipeURI)
	if got := rowDigestAt(t, recipePath); got != recorded {
		t.Fatalf("the lockfile records %s for %s, not the row %s the Recipe digests to, so the plant below is not about that row", recorded, recipeURI, got)
	}

	if err := verifyPull(t, root, releasePath, catalogDir, res); err != nil {
		t.Fatalf("verify must pass over the lock the pull just wrote, or its refusal below proves nothing: %v", err)
	}

	drifted := append([]types.SourceDigest(nil), lock.SourceDigests...)
	drifted[0].Digest = "sha256:" + strings.Repeat("0", 64)
	plantedPins := lock.Pins()
	plantedPins.SourceDigests = drifted
	if _, err := recipepin.WriteLock(recipePath, plantedPins); err != nil {
		t.Fatalf("plant a drifted source pin: %v", err)
	}
	plantedBytes := mustReadFile(t, lockPath)
	lockfileBytes := mustReadFile(t, lockfilePath)
	assertRowRefusal(t, verifyPull(t, root, releasePath, catalogDir, res), buildOver(t, root, releasePath, catalogDir),
		recipeURI, recorded, rowDigestAt(t, recipePath), oci.ErrFetchIntegrity())
	if got := mustReadFile(t, lockPath); string(got) != string(plantedBytes) {
		t.Error("a verify rewrote the lock; verify writes nothing")
	}
	assertHoldingPullNames(t, pullHolding(t, root, releasePath, catalogDir, res), lockPath, lockfilePath, plantedBytes, lockfileBytes,
		lf.SourceArtifacts[0].URL)

	if err := os.WriteFile(lockPath, lockBytes, 0o644); err != nil {
		t.Fatalf("restore the lock: %v", err)
	}
	if err := verifyPull(t, root, releasePath, catalogDir, res); err != nil {
		t.Fatalf("verify must pass again once the lock is restored: %v", err)
	}
}
