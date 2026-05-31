// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestIrEntityInfra_DerivesUsingSetFromIRRenderSet pins Part A: the FeatureRecipe /
// secret per-instance using-set is derived from the IR render-set (graph.Spec) —
// every feature/secret entry the renderer renders, keyed by the Manifest-mapped
// FeatureRecipe URI, carrying its own IR-resolved infra. Two instances of one FR on
// different nodes yield two references with distinct infra; the secret bucket is
// derived alongside features; an instance whose type has no Manifest mapping is
// skipped. (The IR's five-tier assembly is tested in the ir package; this pins that
// pull reads that render-set rather than a tier-short proxy.)
func TestIrEntityInfra_DerivesUsingSetFromIRRenderSet(t *testing.T) {
	t.Parallel()
	entry := func(typ, infraKey, infraVal string) types.IRFeatureEntry {
		e := types.IRFeatureEntry{Type: typ, Infra: types.OrderedMap[any]{}}
		if infraKey != "" {
			e.Infra.Set(infraKey, infraVal)
		}
		return e
	}
	apiFeats := types.OrderedMap[types.IRFeatureEntry]{}
	apiFeats.Set("tls", entry("core/tls", "chart_version", "1.2.3"))
	apiFeats.Set("orphan", entry("core/unmapped", "", "")) // no Manifest mapping
	webFeats := types.OrderedMap[types.IRFeatureEntry]{}
	webFeats.Set("tls", entry("core/tls", "chart_version", "4.5.6"))
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiFeats)
	fvm.Set("web", webFeats)

	apiSecrets := types.OrderedMap[types.IRSecretEntry]{}
	apiSecrets.Set("db", entry("core/db-creds", "size", "large"))
	secs := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
	secs.Set("api", apiSecrets)

	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: fvm, Secrets: secs}}

	flatF := &types.MappingTier{}
	flatF.Type.Set("core/tls", "tls-helm:1.0.0")
	flatF.Type.Set("core/db-creds", "db-creds-helm:1.0.0")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Features: flatF}}}

	out := irEntityInfra(graph, manifest)

	tls := out["tls-helm:1.0.0"]
	if len(tls) != 2 {
		t.Fatalf("tls-helm references: %d; want 2 (one per node instance)", len(tls))
	}
	gotInfra := map[string]any{}
	for _, c := range tls {
		gotInfra[c.Component] = c.Infra["chart_version"]
	}
	if gotInfra["api.features.tls"] != "1.2.3" || gotInfra["web.features.tls"] != "4.5.6" {
		t.Errorf("per-instance infra not carried from the IR: %v", gotInfra)
	}
	db := out["db-creds-helm:1.0.0"]
	if len(db) != 1 || db[0].Component != "api.secrets.db" || db[0].Infra["size"] != "large" {
		t.Errorf("secret using-set not derived from the IR render-set: %v", db)
	}
	if _, ok := out["core/unmapped"]; ok {
		t.Error("a type with no Manifest mapping must not appear in the using-set")
	}
}

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
  aggregate: aggregate
  manifest: manifest
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
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  baseSchema: |
    type: object
    properties:
      image: { type: string }
      replicas: { type: integer, default: 1 }
    required: [image]
    additionalProperties: true
`)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  provider: helm
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

	if err := pullRun(pullConfig{
		ReleasePath: releasePath,
		CatalogDir:  filepath.Join(root, "catalog"),
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

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDir: filepath.Join(root, "catalog")}); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read first lockfile: %v", err)
	}

	if err := pullRun(pullConfig{
		ReleasePath: releasePath,
		CatalogDir:  filepath.Join(root, "catalog"),
		Regenerate:  true,
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

func TestPullRegenerateDiscardsExistingLockfile(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	stalePath := filepath.Join(root, "release.lock")
	if err := os.WriteFile(stalePath, []byte("# stale lockfile content\n"), 0o644); err != nil {
		t.Fatalf("seed stale lockfile: %v", err)
	}

	if err := pullRun(pullConfig{
		ReleasePath: releasePath,
		CatalogDir:  filepath.Join(root, "catalog"),
		Regenerate:  true,
	}); err != nil {
		t.Fatalf("pullRun --regenerate: %v", err)
	}

	got, err := os.ReadFile(stalePath)
	if err != nil {
		t.Fatalf("read lockfile after regenerate: %v", err)
	}
	if strings.Contains(string(got), "stale") {
		t.Errorf("--regenerate did not discard the stale lockfile content")
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
	if err := pullRun(pullConfig{
		ReleasePath: releasePath,
		CatalogDir:  filepath.Join(root, "catalog"),
	}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}

	// Now --verify should succeed: lockfile matches the closure
	// just used to produce it.
	if err := pullRun(pullConfig{
		ReleasePath: releasePath,
		CatalogDir:  filepath.Join(root, "catalog"),
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
	if err := pullRun(pullConfig{
		ReleasePath: releasePath,
		CatalogDir:  filepath.Join(root, "catalog"),
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

	err = pullRun(pullConfig{
		ReleasePath: releasePath,
		CatalogDir:  filepath.Join(root, "catalog"),
		Verify:      true,
	})
	if err == nil {
		t.Fatal("pullRun --verify on drifted lockfile: expected error, got nil")
	}
	if !errors.Is(err, lockfile.ErrLockfileDrift) {
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

	err := pullRun(pullConfig{
		ReleasePath: releasePath,
		CatalogDir:  filepath.Join(root, "catalog"),
		Verify:      true,
	})
	if err == nil {
		t.Fatal("pullRun --verify with no lockfile: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "verify: lockfile") {
		t.Errorf("expected error to name the missing lockfile path; got: %v", err)
	}
}

// TestPullVerifyAndRegenerateMutuallyExclusive covers the flag-
// combination guard. Setting both --verify and --regenerate is a
// configuration error.
func TestPullVerifyAndRegenerateMutuallyExclusive(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)

	// The mutual-exclusion check lives in the cobra RunE wrapper,
	// not in pullRun itself, so this test exercises the cobra
	// command-execution path. Set the flag globals directly.
	pullReleasePath = releasePath
	pullCatalogDir = filepath.Join(root, "catalog")
	pullVerify = true
	pullRegenerate = true
	defer func() {
		pullReleasePath = ""
		pullCatalogDir = "."
		pullVerify = false
		pullRegenerate = false
	}()

	err := pullCmd.RunE(pullCmd, nil)
	if err == nil {
		t.Fatal("expected error for --verify + --regenerate combination, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected error to name the mutual-exclusion; got: %v", err)
	}
}
