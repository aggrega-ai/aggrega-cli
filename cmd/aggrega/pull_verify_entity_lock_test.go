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
// side of it: the intact lock verifies, the drifted one is refused under the
// condition a build halts with, and the restored one verifies again. A verify that
// refused everything would fail the controls; one that refused nothing would fail
// the plant.

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
// itself edited in the lock.
func TestPullVerifyRefusesAnImportedFileThatNoLongerMatchesItsPin(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := fullURICollisionFixture(t, root)
	catalogDir := filepath.Join(root, "catalog")
	recipePath := filepath.Join(catalogDir, "recipes", "server.yaml")
	importPath := filepath.Join(catalogDir, "recipes", "extra.yaml")

	pullWriting(t, root, releasePath, catalogDir)
	lock := readLockForTest(t, recipePath)
	if len(lock.ArtifactDigests) != 1 {
		t.Fatalf("the fixture must pin exactly one imported file; the lock records %d", len(lock.ArtifactDigests))
	}
	recordedPin := lock.ArtifactDigests[0].Digest
	lockPath := recipepin.LockPath(recipePath)
	lockBytes := mustReadFile(t, lockPath)
	importBytes := mustReadFile(t, importPath)

	if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
		t.Fatalf("verify must pass over the lock the pull just wrote, or its refusals below prove nothing: %v", err)
	}

	// Plant one: the imported file edited after the pull that pinned it.
	edited := []byte(strings.Replace(string(importBytes), "origin: imported", "origin: edited", 1))
	if string(edited) == string(importBytes) {
		t.Fatalf("could not edit the imported file; the plant would change nothing")
	}
	if err := os.WriteFile(importPath, edited, 0o644); err != nil {
		t.Fatalf("edit the imported file: %v", err)
	}
	err := verifyPull(t, root, releasePath, catalogDir, nil)
	if err == nil {
		t.Fatal("verify reported a match over an imported file a build of this Release refuses")
	}
	if !errors.Is(err, renderer.ErrArtifactContentMismatch()) {
		t.Fatalf("the refusal must carry the condition a build halts with for the same file; got: %v", err)
	}
	if errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Errorf("the lockfile did not drift, so the refusal must not report that it did: %v", err)
	}
	for _, want := range []string{"extra.yaml", recordedPin, digest.ContentDigest(edited)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so a reader cannot tell which file moved: %v", want, err)
		}
	}
	if got := mustReadFile(t, lockPath); string(got) != string(lockBytes) {
		t.Error("a verify rewrote the lock; verify writes nothing")
	}

	if err := os.WriteFile(importPath, importBytes, 0o644); err != nil {
		t.Fatalf("restore the imported file: %v", err)
	}
	if err := verifyPull(t, root, releasePath, catalogDir, nil); err != nil {
		t.Fatalf("verify must pass again once the file is restored: %v", err)
	}

	// Plant two: the pin edited in the lock, the imported file untouched.
	drifted := []types.ArtifactDigest{{Name: lock.ArtifactDigests[0].Name, Digest: "sha256:" + strings.Repeat("0", 64)}}
	if _, err := recipepin.WriteLock(recipePath, lock.SourceDigests, drifted); err != nil {
		t.Fatalf("plant a drifted pin: %v", err)
	}
	err = verifyPull(t, root, releasePath, catalogDir, nil)
	if !errors.Is(err, renderer.ErrArtifactContentMismatch()) {
		t.Fatalf("a drifted pin in the lock must be refused under the same condition; got: %v", err)
	}
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
// acquired. A build compares it with the digest the lockfile records for the same
// source and halts on a disagreement, so a verify must too.
func TestPullVerifyRefusesASourcePinTheLockfileDisagreesWith(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogDir := writeValidWorkspace(t, root)
	recipePath := filepath.Join(catalogDir, "recipes", "helm", "server.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1alpha1
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
	res := verbatimSourceResolver{}

	if err := pullRun(context.Background(), pullConfig{
		ws:             resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath:    releasePath,
		CatalogDirs:    []string{catalogDir},
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
	lockBytes := mustReadFile(t, lockPath)

	if err := verifyPull(t, root, releasePath, catalogDir, res); err != nil {
		t.Fatalf("verify must pass over the lock the pull just wrote, or its refusal below proves nothing: %v", err)
	}

	drifted := append([]types.SourceDigest(nil), lock.SourceDigests...)
	drifted[0].Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err := recipepin.WriteLock(recipePath, drifted, lock.ArtifactDigests); err != nil {
		t.Fatalf("plant a drifted source pin: %v", err)
	}
	plantedBytes := mustReadFile(t, lockPath)
	err = verifyPull(t, root, releasePath, catalogDir, res)
	if err == nil {
		t.Fatal("verify reported a match over a source pin a build of this Release refuses")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("the refusal must carry the condition a build halts with for the same pin; got: %v", err)
	}
	if errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Errorf("the lockfile did not drift, so the refusal must not report that it did: %v", err)
	}
	for _, want := range []string{drifted[0].Digest, lf.SourceArtifacts[0].Digest, recipepin.LockFileName} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so a reader cannot tell which record moved: %v", want, err)
		}
	}
	if got := mustReadFile(t, lockPath); string(got) != string(plantedBytes) {
		t.Error("a verify rewrote the lock; verify writes nothing")
	}

	if err := os.WriteFile(lockPath, lockBytes, 0o644); err != nil {
		t.Fatalf("restore the lock: %v", err)
	}
	if err := verifyPull(t, root, releasePath, catalogDir, res); err != nil {
		t.Fatalf("verify must pass again once the lock is restored: %v", err)
	}
}
