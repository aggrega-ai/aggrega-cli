// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestRunBuild_Step4bAssertionFailureHaltsCLI pins the CLI-level
// wire-in for Step 4b (Component/Feature assertion evaluation). The
// test invokes runBuild with a minimal fixture whose Component
// carries an always-firing failing assertion and asserts the build
// halts with ErrAssertionFailed in the surfaced error chain. A
// regression that deletes the EvaluateComponentAndFeatureAssertions
// call from build.go would let runBuild complete past Step 4b into
// Step 5 (Render); this test fails such a regression because the
// expected sentinel and message stop being present in the chain.
func TestRunBuild_Step4bAssertionFailureHaltsCLI(t *testing.T) {
	// Not parallel: mutates process-global catalogDirs, releasePath seams other tests read.
	root := t.TempDir()

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: cli-smoke
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
  name: cli-smoke-release
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
  name: cli-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)

	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
  assertions:
    smoke_assert: '{{ fail "cli-step4b-smoke-fired" }}'
`)

	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))

	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
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

	releasePath := filepath.Join(root, "release.yaml")
	catalogDirs := []string{catalogRoot}
	traceEnabled := false

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
	// Build is read-only: prime the lockfile with an explicit pull. The assertion
	// failure surfaces later, at the render step, which the pull does not run.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catalogDirs, ws: ws}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	err := dispatchBuild(context.Background(), ws, releasePath, catalogDirs, traceEnabled, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("build returned nil; expected assertion failure")
	}
	if !errors.Is(err, renderer.ErrAssertionFailed()) {
		t.Errorf("expected ErrAssertionFailed in chain; got %v", err)
	}
	if !chainContains(err, "cli-step4b-smoke-fired") {
		t.Errorf("expected fail-call message in chain; got %v", err)
	}
}

// TestRunBuild_VerifyDeterminismSucceedsOverDeterministicGuest pins the CLI-level
// wire-in of the verify-determinism build mode over a real, deterministic provider
// guest. The build renders a workload-synthesis node through the embedded helm
// guest with the verify-determinism dial on, so every node renders twice and the
// emitted bytes are compared. The shipped helm guest is deterministic — identical
// input yields byte-identical output — so the second render matches the first and
// the build completes. A regression that made the guest (or the reused-instance
// path) non-deterministic would surface here as ErrRenderNondeterministic.
func TestRunBuild_VerifyDeterminismSucceedsOverDeterministicGuest(t *testing.T) {
	root := t.TempDir()

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: verify-smoke
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
  name: verify-smoke-release
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
  name: verify-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)

	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)

	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))

	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
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

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
	// Build is read-only: prime the lockfile with an explicit pull, then build reads it.
	if err := pullRun(pullConfig{ReleasePath: filepath.Join(root, "release.yaml"), CatalogDirs: []string{catalogRoot}, ws: ws}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	err := dispatchBuildWithDiag(context.Background(),
		ws,
		filepath.Join(root, "release.yaml"),
		[]string{catalogRoot},
		false,
		buildDiagOptions{},
		buildops.RunOptions{VerifyDeterminism: true, OutDir: filepath.Join(root, "out")},
		os.Stderr, os.Stdout,
	)
	if err != nil {
		t.Fatalf("verify-determinism build over the deterministic helm guest failed: %v", err)
	}
	if errors.Is(err, renderer.ErrRenderNondeterministic()) {
		t.Fatalf("deterministic guest incorrectly flagged non-deterministic: %v", err)
	}
}

// writeHelmRendererPlugin writes the helm provider Plugin entity into
// providersDir, binding the helm provider to the embedded helm rendering guest.
// Its identity mirrors the shipped core plugin (metadata.name provider/helm), so
// a catalog that also carries the shipped entity resolves both to one plugin
// rather than two competing ones. The .wasm artifact reference is derived from
// that bare identity and resolves through the plugin-resolution cascade's embed
// tier (the exact bytes compiled into the binary), so the build's render step
// routes the helm provider to the shipped guest with no cache or lockfile.
//
// The entity written is the embedded one verbatim, which keeps its version equal
// to the version capability resolution selects; see the seeding notes in
// embedded_plugin_seed_test.go for why that equality is what keeps a fixture
// build off the network.
func writeHelmRendererPlugin(t *testing.T, providersDir string) {
	t.Helper()
	seedEmbeddedPlugins(t, providersDir, "provider/helm")
}

// writeFixture writes content to path, creating parent directories
// as needed. Fails the test on any I/O error.
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// chainContains walks err via errors.Unwrap and returns true when any
// link's Error() contains needle. cerrors.Wrap flattens the surface
// presentation, so verifying inner-cause text requires chain traversal.
func chainContains(err error, needle string) bool {
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(cause.Error(), needle) {
			return true
		}
	}
	return false
}

// providesContractFixture writes a complete build workspace (Aggregate, Release,
// Manifest, catalog) whose Component core/store declares a two-key provides
// output contract (endpoint, dsn). The bound Recipe's provides block is supplied
// by recipeProvides so a caller can under-deliver (omit a declared key) or
// over-deliver (superset). Returns the workspace root.
func providesContractFixture(t *testing.T, recipeProvides string) string {
	t.Helper()
	root := t.TempDir()

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: provides-smoke
  version: "1.0.0"
spec:
  components:
    only:
      type: core/store
      values:
        port: 8080
`)

	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: provides-smoke-release
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
  name: provides-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/store: store-recipe:1.0.0
`)

	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "store.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/store
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
    provides:
      endpoint: { type: string }
      dsn: { type: string }
`)

	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))

	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "store.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: store-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/store
`+recipeProvides+`
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      store:
        image: test-store:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`)
	return root
}

// TestRunBuild_ProvidesContractUnfulfilledHaltsCLI pins the CLI-level wire-in of
// the Component↔Recipe producer-fulfilment check. The bound Recipe declares only
// the endpoint provides key while its Component declares endpoint AND dsn, so the
// realizer under-delivers its declared output contract. The build must halt at the
// render step with ErrProvidesContractUnfulfilled naming the missing key rather
// than silently dropping it at inject/render time.
func TestRunBuild_ProvidesContractUnfulfilledHaltsCLI(t *testing.T) {
	// Not parallel: mutates process-global catalogDirs, releasePath seams other tests read.
	root := providesContractFixture(t, `  provides:
    endpoint: "{{ .context.instance_id }}.svc.local"`)

	releasePath := filepath.Join(root, "release.yaml")
	catalogDirs := []string{filepath.Join(root, "catalog")}
	traceEnabled := false

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catalogDirs, ws: ws}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	err := dispatchBuild(context.Background(), ws, releasePath, catalogDirs, traceEnabled, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("build returned nil; expected a provides-contract shortfall halt")
	}
	if !errors.Is(err, renderer.ErrProvidesContractUnfulfilled()) {
		t.Errorf("expected ErrProvidesContractUnfulfilled in chain; got %v", err)
	}
	if !chainContains(err, `"dsn"`) {
		t.Errorf("expected the halt to name the missing key \"dsn\"; got %v", err)
	}
	if !chainContains(err, "core/store") || !chainContains(err, "store-recipe") {
		t.Errorf("expected the halt to name the Component and Recipe; got %v", err)
	}
}

// TestRunBuild_ProvidesContractSupersetBuildsCLI pins that a Recipe producing MORE
// provides keys than its Component declares (endpoint, dsn, extra vs endpoint, dsn)
// satisfies the contract — subset-required, superset-permitted — and the build
// completes through the render step.
func TestRunBuild_ProvidesContractSupersetBuildsCLI(t *testing.T) {
	root := providesContractFixture(t, `  provides:
    endpoint: "{{ .context.instance_id }}.svc.local"
    dsn: "postgres://{{ .context.instance_id }}"
    extra: "surplus"`)

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
	if err := pullRun(pullConfig{ReleasePath: filepath.Join(root, "release.yaml"), CatalogDirs: []string{filepath.Join(root, "catalog")}, ws: ws}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	err := dispatchBuildWithDiag(context.Background(),
		ws,
		filepath.Join(root, "release.yaml"),
		[]string{filepath.Join(root, "catalog")},
		false,
		buildDiagOptions{},
		buildops.RunOptions{OutDir: filepath.Join(root, "out")},
		os.Stderr, os.Stdout,
	)
	if err != nil {
		t.Fatalf("a superset-producing Recipe must satisfy the contract and build clean; got %v", err)
	}
}

// featureProvidesContractFixture writes a complete build workspace whose component
// core/store (no provides of its own) attaches a non-credential Feature core/metrics
// that declares a two-key provides output contract (endpoint, dsn). The bound
// FeatureRecipe's provides block is supplied by frProvides so a caller can
// under-deliver or over-deliver. Returns the workspace root.
func featureProvidesContractFixture(t *testing.T, frProvides string) string {
	t.Helper()
	root := t.TempDir()

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: feat-provides-smoke
  version: "1.0.0"
spec:
  components:
    only:
      type: core/store
      values:
        port: 8080
      features:
        obs:
          type: core/metrics
`)

	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: feat-provides-smoke-release
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
  name: feat-provides-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/store: store-recipe:1.0.0
    features:
      type:
        core/metrics: metrics-fr:1.0.0
`)

	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "store.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/store
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)

	writeFixture(t, filepath.Join(catalogRoot, "base", "features", "metrics.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/metrics
  version: "1.0.0"
spec:
  schema:
    provides:
      endpoint: { type: string }
      dsn: { type: string }
`)

	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))

	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "store.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: store-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/store
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      store:
        image: test-store:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`)

	writeFixture(t, filepath.Join(catalogRoot, "feature-recipes", "helm", "metrics.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: metrics-fr
  version: "1.0.0"
spec:
  capability: helm
  implements: core/metrics
`+frProvides+`
`)
	return root
}

// TestRunBuild_FeatureProvidesContractUnfulfilledHaltsCLI pins the CLI-level
// wire-in of the Feature↔non-credential-FeatureRecipe producer-fulfilment check.
// The bound FeatureRecipe declares only the endpoint provides key while its Feature
// declares endpoint AND dsn, so the realizer under-delivers its declared output
// contract. The build must halt at render with ErrProvidesContractUnfulfilled
// naming the missing key.
func TestRunBuild_FeatureProvidesContractUnfulfilledHaltsCLI(t *testing.T) {
	// Not parallel: mutates process-global catalogDirs, releasePath seams other tests read.
	root := featureProvidesContractFixture(t, `  provides:
    endpoint: "{{ .context.instance_id }}.metrics"`)

	releasePath := filepath.Join(root, "release.yaml")
	catalogDirs := []string{filepath.Join(root, "catalog")}
	traceEnabled := false

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catalogDirs, ws: ws}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	err := dispatchBuild(context.Background(), ws, releasePath, catalogDirs, traceEnabled, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("build returned nil; expected a feature provides-contract shortfall halt")
	}
	if !errors.Is(err, renderer.ErrProvidesContractUnfulfilled()) {
		t.Errorf("expected ErrProvidesContractUnfulfilled in chain; got %v", err)
	}
	if !chainContains(err, `"dsn"`) {
		t.Errorf("expected the halt to name the missing key \"dsn\"; got %v", err)
	}
	if !chainContains(err, "core/metrics") || !chainContains(err, "metrics-fr") {
		t.Errorf("expected the halt to name the Feature and FeatureRecipe; got %v", err)
	}
}

// TestRunBuild_FeatureProvidesContractFulfilledBuildsCLI pins that a FeatureRecipe
// publishing every key its Feature declares satisfies the contract and the build
// completes.
func TestRunBuild_FeatureProvidesContractFulfilledBuildsCLI(t *testing.T) {
	root := featureProvidesContractFixture(t, `  provides:
    endpoint: "{{ .context.instance_id }}.metrics"
    dsn: "metric://{{ .context.instance_id }}"`)

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
	if err := pullRun(pullConfig{ReleasePath: filepath.Join(root, "release.yaml"), CatalogDirs: []string{filepath.Join(root, "catalog")}, ws: ws}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	err := dispatchBuildWithDiag(context.Background(),
		ws,
		filepath.Join(root, "release.yaml"),
		[]string{filepath.Join(root, "catalog")},
		false,
		buildDiagOptions{},
		buildops.RunOptions{OutDir: filepath.Join(root, "out")},
		os.Stderr, os.Stdout,
	)
	if err != nil {
		t.Fatalf("a FeatureRecipe fulfilling the contract must build clean; got %v", err)
	}
}
