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

// TestIrEntityInfra_DerivesUsingSetFromIRRenderSet pins that the per-instance
// FeatureRecipe using-set is derived from the IR render-set (graph.Spec) — every
// attachment entry the renderer renders, keyed by the Manifest-mapped
// FeatureRecipe URI, carrying its own IR-resolved infra. Two instances of one FR on
// different nodes yield two references with distinct infra; a credential attachment
// is walked through the same one bucket as an operational one; an instance whose
// type has no Manifest mapping is skipped. (The IR's five-tier assembly is tested in
// the ir package; this pins that pull reads that render-set rather than a
// tier-short proxy.)
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
	// A credential attachment shares the one bucket, so it is pinned through the
	// same walk as an operational attachment.
	apiFeats.Set("db", entry("core/db-creds", "size", "large"))
	webFeats := types.OrderedMap[types.IRFeatureEntry]{}
	webFeats.Set("tls", entry("core/tls", "chart_version", "4.5.6"))
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiFeats)
	fvm.Set("web", webFeats)

	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: fvm}}

	flatF := &types.MappingTier{}
	flatF.Type.Set("core/tls", "tls-helm:1.0.0")
	flatF.Type.Set("core/db-creds", "db-creds-helm:1.0.0")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Features: flatF}}}

	componentScope := func(_, _ string) (string, bool) { return "component", true }
	out, err := irEntityInfra(graph, manifest, componentScope)
	if err != nil {
		t.Fatalf("irEntityInfra: %v", err)
	}

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
	if len(db) != 1 || db[0].Component != "api.features.db" || db[0].Infra["size"] != "large" {
		t.Errorf("credential using-set not derived from the IR render-set: %v", db)
	}
	if _, ok := out["core/unmapped"]; ok {
		t.Error("a type with no Manifest mapping must not appear in the using-set")
	}
}

// TestIrEntityInfra_BoundaryScopePinnedOnceFromProjection pins the source-identity
// granularity for a boundary-scope FeatureRecipe. A boundary-scope FR is Tier-4
// folded into every subtree component's FeatureValuesMapping (N per-component
// entries at the wrong granularity for source identity), and ALSO appears once on
// the boundary-feature projection. The pin must come exclusively from the
// projection — one reference keyed by (boundaryPath, instance) carrying the
// boundary's own infra — with the per-component FVM entries skipped, so the
// lock-time source identity matches the renderer's single boundary render. A
// component-scope FR keeps its per-component pins.
func TestIrEntityInfra_BoundaryScopePinnedOnceFromProjection(t *testing.T) {
	t.Parallel()
	fEntry := func(typ, k, v string) types.IRFeatureEntry {
		e := types.IRFeatureEntry{Type: typ, Infra: types.OrderedMap[any]{}}
		if k != "" {
			e.Infra.Set(k, v)
		}
		return e
	}
	// bridge (boundary-scope) folded onto both api and web; tls (component-scope)
	// on api only.
	apiFeats := types.OrderedMap[types.IRFeatureEntry]{}
	apiFeats.Set("bridge", fEntry("core/bridge", "mappedPort", "folded-api"))
	apiFeats.Set("tls", fEntry("core/tls", "chart_version", "1.2.3"))
	apiFeats.Set("legacy", fEntry("core/legacy", "v", "x")) // scope unresolvable → graceful FVM pin
	webFeats := types.OrderedMap[types.IRFeatureEntry]{}
	webFeats.Set("bridge", fEntry("core/bridge", "mappedPort", "folded-web"))
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiFeats)
	fvm.Set("web", webFeats)

	// Boundary-feature projection: bridge attached once at "cluster" with the
	// boundary's authoritative infra.
	bridgeBFInfra := types.OrderedMap[any]{}
	bridgeBFInfra.Set("mappedPort", "boundary-authoritative")
	clusterBF := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBF.Set("bridge", types.IRBoundaryFeature{Type: "core/bridge", Infra: bridgeBFInfra})
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	bfProj.Set("cluster", clusterBF)

	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: fvm, BoundaryFeatures: bfProj}}

	flatF := &types.MappingTier{}
	flatF.Type.Set("core/bridge", "bridge-helm:1.0.0")
	flatF.Type.Set("core/tls", "tls-helm:1.0.0")
	flatF.Type.Set("core/legacy", "legacy-helm:1.0.0")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Features: flatF}}}

	scopeOf := func(_, featType string) (string, bool) {
		switch featType {
		case "core/bridge":
			return "boundary", true
		case "core/legacy":
			return "", false // FR scope unresolvable — must fall through to the per-component pin
		}
		return "component", true
	}
	out, err := irEntityInfra(graph, manifest, scopeOf)
	if err != nil {
		t.Fatalf("irEntityInfra: %v", err)
	}

	// bridge pinned ONCE, from the projection's boundary infra — not twice from
	// the folded per-component FVM entries.
	bridge := out["bridge-helm:1.0.0"]
	if len(bridge) != 1 {
		t.Fatalf("boundary-scope FR references: %d; want 1 (pinned once from the projection)", len(bridge))
	}
	if bridge[0].Component != "cluster.features.bridge" {
		t.Errorf("boundary-scope pin identity = %q; want cluster.features.bridge", bridge[0].Component)
	}
	if bridge[0].Infra["mappedPort"] != "boundary-authoritative" {
		t.Errorf("boundary-scope pin infra = %v; want the boundary projection's infra, not a folded FVM entry", bridge[0].Infra["mappedPort"])
	}
	// component-scope FR keeps its per-component pin.
	if tls := out["tls-helm:1.0.0"]; len(tls) != 1 || tls[0].Component != "api.features.tls" {
		t.Errorf("component-scope FR pin changed: %v", tls)
	}
	// graceful: an FR whose scope is unresolvable is NOT dropped — it keeps
	// the per-component FVM pin (prior behavior), never the projection pin.
	if legacy := out["legacy-helm:1.0.0"]; len(legacy) != 1 || legacy[0].Component != "api.features.legacy" {
		t.Errorf("unresolvable-scope FR must keep its per-component pin (graceful fallback); got %v", legacy)
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
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
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

	if err := pullRun(pullConfig{
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

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read first lockfile: %v", err)
	}

	if err := pullRun(pullConfig{
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

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read first lockfile: %v", err)
	}

	// Second pull WITHOUT --upgrade: the honoring reconcile path.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
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

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("read first lockfile: %v", err)
	}

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
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

	if err := pullRun(pullConfig{
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

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
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

	upgradeErr := pullRun(pullConfig{
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
// and rewrites it — simulating a pin a prior build or acquire wrote. External
// inputs are not part of the catalog closure, so a plain pull never produces
// them; this is the only way to place one for the carry-forward tests.
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

// TestPullPreservesExternalInputsWarm proves a warm pull (no --upgrade)
// carries forward the external-input pins a prior build or acquire recorded.
// The pull path materialises the lockfile from the catalog closure alone, which
// contains no external inputs; without the carry-forward it would rewrite the
// lockfile with an empty external_inputs list, forcing the next build to
// re-acquire from the network.
func TestPullPreservesExternalInputsWarm(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	lockfilePath := filepath.Join(root, "release.lock")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	want := seedExternalInput(t, lockfilePath)

	// Warm re-pull (no --upgrade): the honoring reconcile path.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
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

// TestPullUpgradeDropsExternalInputs proves --upgrade refreshes the pins: a full
// refresh reads no prior pin set, so the carry-forward finds nothing and the
// rewritten lockfile carries an empty external_inputs list. This is the intended
// "re-acquire from scratch" behavior.
func TestPullUpgradeDropsExternalInputs(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	lockfilePath := filepath.Join(root, "release.lock")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	seedExternalInput(t, lockfilePath)

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}, Upgrade: true}); err != nil {
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
	if err := pullRun(pullConfig{
		ReleasePath: releasePath,
		CatalogDirs: []string{filepath.Join(root, "catalog")},
	}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}

	// Now --verify should succeed: lockfile matches the closure
	// just used to produce it.
	if err := pullRun(pullConfig{
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
	if err := pullRun(pullConfig{
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

	err = pullRun(pullConfig{
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

	err := pullRun(pullConfig{
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
	// command-execution path. Construct the command first (its flag
	// binding resets the globals to their defaults), then set the
	// globals the RunE wrapper reads.
	cmd := newPullCmd()
	pullReleasePath = releasePath
	pullCatalogDirs = []string{filepath.Join(root, "catalog")}
	pullVerify = true
	pullUpgrade = true
	defer func() {
		pullReleasePath = ""
		pullCatalogDirs = []string{"."}
		pullVerify = false
		pullUpgrade = false
	}()

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for --verify + --upgrade combination, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected error to name the mutual-exclusion; got: %v", err)
	}
}
