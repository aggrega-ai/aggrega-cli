// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// writeValidWorkspace lays down a minimal buildable workspace under root: an
// Aggregate using core/server, its Release + Manifest, and a catalog with the
// core/server Component, its helm Recipe, and the helm Provider. The closure of
// the Release is exactly {core/server, server-recipe, helm}. Returns the release
// and catalog paths.
func writeValidWorkspace(t *testing.T, root string) (releasePath, catalogRoot string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: iso-smoke
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: iso-smoke-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        only:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: iso-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	catalogRoot = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeFixture(t, filepath.Join(catalogRoot, "base", "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  target: helm
  implements: core/server
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
	return filepath.Join(root, "release.yaml"), catalogRoot
}

// writeBrokenOutOfClosureEntities drops three broken entities into the catalog
// that NO Release in the workspace references: an unparseable-by-shape Recipe, a
// SchemaLibrary with a forbidden keyword, and a Component whose schema $ref names
// a nonexistent library. All three would fail a whole-catalog load or whole-catalog
// validation; none is in the closure of the iso-smoke Release.
func writeBrokenOutOfClosureEntities(t *testing.T, catalogRoot string) {
	t.Helper()
	// Broken Recipe: helm provider with no implements and no input kind — fails
	// ValidateRecipe, so a tolerant Pass-1 skips it.
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "broken.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: broken-recipe
  version: "1.0.0"
spec:
  target: helm
`)
	// Broken SchemaLibrary: forbidden $dynamicAnchor — fails the per-entity resolve.
	writeFixture(t, filepath.Join(catalogRoot, "base", "libs", "broken-lib.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BrokenLib
  version: "1.0.0"
spec:
  schemas:
    Broken:
      $dynamicAnchor: forbidden
      type: string
`)
	// Broken Component: a dangling cross-document $ref. Parses at Pass-1 (kept), but
	// would fail closure-scoped Pass-2 AND library discovery if it were in scope.
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "broken-comp.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/broken
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
`)
}

// readTree reads every file under dir into a path→content map (paths relative to
// dir), for byte-identity comparison of build output.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("readTree %s: %v", dir, err)
	}
	return out
}

// TestBuild_BrokenOutOfClosureIsolation is THE robustness headline: a catalog
// carrying a broken recipe, a broken library, and a broken component — all OUTSIDE
// the Release's closure — builds successfully and BYTE-IDENTICALLY to the same
// catalog without the broken entities. One unused-broken entity no longer fails an
// unrelated build.
func TestBuild_BrokenOutOfClosureIsolation(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeValidWorkspace(t, root)

	if err := dispatchBuild(nil, releasePath, []string{catalogRoot}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("clean build should succeed: %v", err)
	}
	clean := readTree(t, filepath.Join(root, "dist"))
	if len(clean) == 0 {
		t.Fatal("clean build produced no artifacts; the byte-identity comparison would be vacuous")
	}

	// Add broken entities OUTSIDE the closure; the closure (and lockfile) are
	// unchanged, so the pre-existing lockfile still verifies.
	writeBrokenOutOfClosureEntities(t, catalogRoot)
	if err := dispatchBuild(nil, releasePath, []string{catalogRoot}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("build must succeed despite broken out-of-closure entities (blast-radius isolation): %v", err)
	}
	dirty := readTree(t, filepath.Join(root, "dist"))

	if len(dirty) != len(clean) {
		t.Fatalf("artifact set changed: clean=%d dirty=%d", len(clean), len(dirty))
	}
	for path, want := range clean {
		if got, ok := dirty[path]; !ok || got != want {
			t.Errorf("artifact %q diverged after adding broken out-of-closure entities", path)
		}
	}
}

// TestBuild_InClosureSchemaInvalidStillFails proves closure-scoping does NOT weaken
// in-closure validation: an in-closure Component whose schema $ref is unresolvable
// fails the build loudly, exactly as a whole-catalog Pass-2 would.
func TestBuild_InClosureSchemaInvalidStillFails(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeValidWorkspace(t, root)

	// Corrupt the IN-closure core/server component with a dangling $ref.
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
`)
	if err := dispatchBuild(nil, releasePath, []string{catalogRoot}, false, os.Stderr, os.Stdout); err == nil {
		t.Fatal("build must fail when an in-closure component's $ref is unresolvable (closure-scoped Pass-2 must still reject)")
	}
}

// TestBuild_InClosureUnparseableReplaysSkip proves the F3 guard for the tolerant
// load: an UNPARSEABLE in-closure recipe is skipped at load, so the closure cannot
// resolve it — and the failure replays the skip context (WHY it went missing),
// never a bare "not found".
func TestBuild_InClosureUnparseableReplaysSkip(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeValidWorkspace(t, root)

	// Replace the in-closure recipe with one that fails load-time shape validation
	// (helm provider, no implements, no input kind) — skipped at tolerant Pass-1.
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  target: helm
`)
	err := dispatchBuild(nil, releasePath, []string{catalogRoot}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("build must fail when the in-closure recipe was skipped at load")
	}
	if !chainContains(err, "skipped at load") {
		t.Errorf("in-closure failure must replay the skip context (WHY it went missing); got: %v", err)
	}
}

// lockfileEntityURIs reads the lockfile for releasePath and returns its sorted
// entity + library URIs — the closure-relevant content, excluding the
// GeneratedAt timestamp so two pulls of the same closure compare equal.
func lockfileEntityURIs(t *testing.T, releasePath string) []string {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	var uris []string
	for _, e := range lf.Entities {
		uris = append(uris, "entity:"+e.URI)
	}
	for _, l := range lf.SchemaLibraries {
		uris = append(uris, "schemalib:"+l.URI)
	}
	for _, l := range lf.MappingLibraries {
		uris = append(uris, "mappinglib:"+l.URI)
	}
	sort.Strings(uris)
	return uris
}

// TestPull_BrokenOutOfClosureIsolation is the pull-side headline: a broken recipe,
// library, and component OUTSIDE the closure do not fail `aggrega pull`, and the
// lockfile's closure content (entities + libraries) is identical to the clean
// catalog's — out-of-closure breakage never leaks into the lockfile.
func TestPull_BrokenOutOfClosureIsolation(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeValidWorkspace(t, root)

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}}); err != nil {
		t.Fatalf("clean pull should succeed: %v", err)
	}
	clean := lockfileEntityURIs(t, releasePath)
	if len(clean) == 0 {
		t.Fatal("clean pull produced an empty closure; the comparison would be vacuous")
	}

	writeBrokenOutOfClosureEntities(t, catalogRoot)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}, Regenerate: true}); err != nil {
		t.Fatalf("pull must succeed despite broken out-of-closure entities: %v", err)
	}
	dirty := lockfileEntityURIs(t, releasePath)

	if len(dirty) != len(clean) {
		t.Fatalf("lockfile closure changed: clean=%v dirty=%v", clean, dirty)
	}
	for i := range clean {
		if clean[i] != dirty[i] {
			t.Errorf("lockfile closure entry %d diverged: clean=%q dirty=%q", i, clean[i], dirty[i])
		}
	}
}

// TestPull_InClosureUnparseableFails proves pull, like build, fails loudly when an
// IN-closure entity was skipped at the tolerant load, replaying the skip context.
func TestPull_InClosureUnparseableFails(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeValidWorkspace(t, root)

	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  target: helm
`)
	err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}})
	if err == nil {
		t.Fatal("pull must fail when the in-closure recipe was skipped at load")
	}
	if !chainContains(err, "skipped at load") {
		t.Errorf("in-closure pull failure must replay the skip context; got: %v", err)
	}
}
