// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// authoringFixture writes the minimal multi-file fixture every pull /
// build integration test consumes. Returns the absolute path to the
// Release file the tests dispatch against.
//
// Naming convention: the Aggregate / Manifest / Component / Recipe
// metadata.name fields match both their file stems (so resolver.Load*
// joins releaseDir + name + ".yaml" correctly) AND the Release's
// spec.aggregate / spec.manifest URI strings (so the closure walker
// finds them in the in-memory catalog at Tier 1).
func authoringFixture(t *testing.T, root string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(catalogRoot, "aggregates", "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: smoke-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server
`)
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      server:
        image: test-server:latest
        ports:
          web: 8080
    services:
      svc:
        type: ClusterIP
        ports:
          web: 8080
`)
	return filepath.Join(root, "release.yaml")
}

func TestPullEndToEndRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
	}); err != nil {
		t.Fatalf("pullRun: %v", err)
	}

	lockfilePath := filepath.Join(root, "release.lock")
	data, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "entities:") {
		t.Errorf("lockfile missing entities: section\n%s", text)
	}
	if !strings.Contains(text, "uri: server") {
		t.Errorf("lockfile missing server entity entry\n%s", text)
	}
}

func TestPullProducesByteIdenticalLockfileAcrossRuns(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read first lockfile: %v", err)
	}

	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		Upgrade:     true,
	}); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read second lockfile: %v", err)
	}

	// generated_at carries wall-clock provenance and intentionally
	// differs between runs; strip the generated_at line for comparison.
	firstStripped := stripGeneratedAt(first)
	secondStripped := stripGeneratedAt(second)
	if !bytes.Equal(firstStripped, secondStripped) {
		t.Errorf("consecutive pulls produced different lockfiles (excluding generated_at)\n--- first ---\n%s\n--- second ---\n%s", firstStripped, secondStripped)
	}
}

// TestPullDefaultRerunIsByteIdentical proves the honoring default is idempotent:
// a second plain pull (no --upgrade) over an unchanged closure honors the pins
// the first pull recorded and rewrites a byte-identical lockfile (modulo the
// generated_at provenance line). This is the GitOps-determinism guarantee of the
// honoring reconcile — re-running pull does not churn the committed lockfile.
func TestPullDefaultRerunIsByteIdentical(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read first lockfile: %v", err)
	}

	// Second pull WITHOUT --upgrade: the honoring reconcile path.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("second pull (honoring rerun): %v", err)
	}
	second, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read second lockfile: %v", err)
	}

	if !bytes.Equal(stripGeneratedAt(first), stripGeneratedAt(second)) {
		t.Errorf("default honoring rerun churned the lockfile (excluding generated_at)\n--- first ---\n%s\n--- second ---\n%s", stripGeneratedAt(first), stripGeneratedAt(second))
	}
}

// TestPullByteIdenticalIncludingGeneratedAt is the load-bearing idempotence
// proof: a second plain pull over an unchanged closure produces a lockfile
// byte-identical to the first INCLUDING the generated_at line — no stripping.
// The committed-lockfile (GitOps) workflow therefore sees zero churn on re-pull.
func TestPullByteIdenticalIncludingGeneratedAt(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read first lockfile: %v", err)
	}

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read second lockfile: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("re-pull was not byte-identical including generated_at\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestPullUpgradeDiscardsExistingLockfile(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	stalePath := filepath.Join(root, "release.lock")
	if err := os.WriteFile(stalePath, []byte("# stale lockfile content\n"), 0o644); err != nil {
		t.Fatalf("seed stale lockfile: %v", err)
	}

	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		Upgrade:     true,
	}); err != nil {
		t.Fatalf("pullRun --upgrade: %v", err)
	}

	got, err := os.ReadFile(stalePath)
	if err != nil {
		t.Fatalf("read lockfile after upgrade: %v", err)
	}
	if strings.Contains(string(got), "stale") {
		t.Errorf("--upgrade did not discard the stale lockfile content")
	}
}

// TestPullUpgradeFailureLeavesPriorLockfileIntact proves a bare --upgrade that
// cannot complete leaves the previous lockfile byte-identical on disk and still
// fails. A full refresh discards the previous pin set in memory; the file itself
// is replaced only by the single write at the end of a pull that got that far,
// so a resolution failure costs the caller nothing it cannot recover.
//
// The bytes are compared, not the file's existence: a refresh that truncated or
// rewrote the lockfile before failing would satisfy an existence check while
// still having destroyed the pins.
func TestPullUpgradeFailureLeavesPriorLockfileIntact(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	catalogDir := filepath.Join(root, "catalog")
	lockfilePath := filepath.Join(root, "release.lock")

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	before, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("read seeded lockfile: %v", err)
	}

	// Break the closure the upgrade must re-resolve: the Recipe the Manifest maps
	// `server` to is no longer in the catalog. The walk is the first step that can
	// fail after the refresh decision is made, which is the window under test.
	if err := os.Remove(filepath.Join(catalogDir, "recipes", "server.yaml")); err != nil {
		t.Fatalf("remove recipe to break the closure: %v", err)
	}

	upgradeErr := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		Upgrade:     true,
	})
	if upgradeErr == nil {
		t.Fatal("upgrade over an unresolvable closure returned nil; the resolution failure must surface")
	}

	after, readErr := os.ReadFile(lockfilePath)
	if readErr != nil {
		t.Fatalf("prior lockfile is gone after a failed upgrade (%v); the refresh destroyed pins it never replaced", readErr)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("failed upgrade did not leave the prior lockfile byte-identical\n--- before (%d bytes) ---\n%s\n--- after (%d bytes) ---\n%s",
			len(before), before, len(after), after)
	}
}

// seedExternalInput loads the lockfile at path, records one external-input pin,
// and rewrites it by hand.
//
// It is written by hand rather than pinned by the product because the fixture
// these cases use renders a Release that asks for no external input at all, so
// the resolution's own discovery has nothing to pin. That is what makes it the
// right fixture here: the pin under test cannot have come from this run, so a
// pin that survives was carried and a pin that vanishes was dropped. The cases
// that watch the resolution DISCOVER a pin use a Release whose render genuinely
// requests one, and observe the fetch at the endpoint.
func seedExternalInput(t *testing.T, path string) lockfile.ExternalInput {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("load lockfile to seed external input: %v", err)
	}
	pin := lockfile.ExternalInput{
		URL:        "https://example.com/schemas/base.json",
		Sha256:     strings.Repeat("a", 64),
		Size:       512,
		RequiredBy: []string{"provider.kubernetes"},
	}
	lf.ExternalInputs = []lockfile.ExternalInput{pin}
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("rewrite lockfile with seeded external input: %v", err)
	}
	return pin
}

// TestPullPreservesExternalInputsWarm proves a warm resolution (no --upgrade)
// carries an already-recorded external-input pin forward.
//
// The lockfile is materialised from the catalog closure, which carries no remote
// inputs, and this Release's render asks for none — so without the carry-forward
// the rewritten lockfile would drop a pin whose bytes are already in the store
// and the next build would refetch them.
func TestPullPreservesExternalInputsWarm(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	lockfilePath := filepath.Join(root, "release.lock")

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	want := seedExternalInput(t, lockfilePath)

	// Warm re-pull (no --upgrade): the honoring reconcile path.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("warm re-pull: %v", err)
	}

	lf, _, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		t.Fatalf("load lockfile after warm re-pull: %v", err)
	}
	if len(lf.ExternalInputs) != 1 {
		t.Fatalf("warm re-pull dropped external_inputs: got %d, want 1: %+v", len(lf.ExternalInputs), lf.ExternalInputs)
	}
	got := lf.ExternalInputs[0]
	if got.URL != want.URL || got.Sha256 != want.Sha256 || got.Size != want.Size {
		t.Errorf("carried external input mismatch: got %+v, want %+v", got, want)
	}
	if len(got.RequiredBy) != 1 || got.RequiredBy[0] != want.RequiredBy[0] {
		t.Errorf("carried external input required_by mismatch: got %v, want %v", got.RequiredBy, want.RequiredBy)
	}
}

// TestPullUpgradeDropsAStalePinNoRenderAsksFor proves what a full refresh does
// to a external-input pin nothing needs: it goes. A refresh reads no prior pin
// set, so nothing is carried, and the refreshed set is whatever the render asks
// for — which for this Release is nothing at all.
//
// It is the half a refresh over a Release that DOES ask for a external input
// cannot show, because there the pin reappears and a reader cannot tell a
// carried pin from a re-discovered one. Here there is nothing to re-discover, so
// an empty list is the only outcome consistent with the pin having been dropped.
func TestPullUpgradeDropsAStalePinNoRenderAsksFor(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	lockfilePath := filepath.Join(root, "release.lock")

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	seedExternalInput(t, lockfilePath)

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}, Upgrade: true}); err != nil {
		t.Fatalf("upgrade pull: %v", err)
	}

	lf, _, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		t.Fatalf("load lockfile after upgrade: %v", err)
	}
	if len(lf.ExternalInputs) != 0 {
		t.Errorf("--upgrade did not drop external_inputs: got %d: %+v", len(lf.ExternalInputs), lf.ExternalInputs)
	}
}

func stripGeneratedAt(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimLeft(line, " "), []byte("generated_at:")) {
			continue
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out
}

// TestPullVerifyMatchingLockfileExits0 verifies that
// `aggrega pull --verify` against a workspace where the existing
// lockfile matches the freshly-computed closure returns nil
// (cobra's RunE non-error → exit 0) and writes no disk side
// effects.
func TestPullVerifyMatchingLockfileExits0(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	// First populate the lockfile via the default pull.
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
	}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}

	// Now --verify should succeed: lockfile matches the closure
	// just used to produce it.
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		Verify:      true,
	}); err != nil {
		t.Errorf("pullRun --verify on matching lockfile: got error %v, want nil", err)
	}
}

// TestPullVerifyDriftedLockfileReturnsErr verifies that
// `aggrega pull --verify` against a workspace where the lockfile
// has been mutated returns a wrapped lockfile.ErrLockfileDrift.
func TestPullVerifyDriftedLockfileReturnsErr(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	// Seed lockfile.
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
	}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}

	// Mutate the lockfile to introduce drift: rewrite one of the
	// recorded digests to a wrong value. Hex-replacing a known
	// digest character flips the verification result.
	lockfilePath := filepath.Join(root, "release.lock")
	data, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	mutated := bytes.Replace(data, []byte("digest: sha256:"), []byte("digest: sha256:ff"), 1)
	if bytes.Equal(mutated, data) {
		t.Fatalf("could not locate a digest line to mutate in:\n%s", data)
	}
	if err := os.WriteFile(lockfilePath, mutated, 0o644); err != nil {
		t.Fatalf("write mutated lockfile: %v", err)
	}

	err = pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		Verify:      true,
	})
	if err == nil {
		t.Fatal("pullRun --verify on drifted lockfile: expected error, got nil")
	}
	if !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Errorf("expected ErrLockfileDrift in error chain; got: %v", err)
	}
}

// TestPullVerifyMissingLockfileReturnsErr verifies that
// `aggrega pull --verify` against a workspace with no lockfile is
// a fatal error. The --verify flag presumes a lockfile exists; the
// no-lockfile case is what plain `aggrega pull` addresses.
func TestPullVerifyMissingLockfileReturnsErr(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	// Deliberately do NOT seed the lockfile.

	err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		Verify:      true,
	})
	if err == nil {
		t.Fatal("pullRun --verify with no lockfile: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "verify: lockfile") {
		t.Errorf("expected error to name the missing lockfile path; got: %v", err)
	}
}

// TestPullVerifyAndUpgradeMutuallyExclusive covers the flag-
// combination guard. Setting both --verify and --upgrade is a
// configuration error.
func TestPullVerifyAndUpgradeMutuallyExclusive(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	// The mutual-exclusion check lives in the cobra RunE wrapper,
	// not in pullRun itself, so this test exercises the cobra
	// command-execution path: the flags are parsed into the command's own
	// state exactly as an invocation would, then the wrapper is called.
	cmd := newPullCmd()
	if err := cmd.ParseFlags([]string{
		"--release", releasePath,
		"--catalog", filepath.Join(root, "catalog"),
		"--verify",
		"--upgrade",
	}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for --verify + --upgrade combination, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected error to name the mutual-exclusion; got: %v", err)
	}
}
