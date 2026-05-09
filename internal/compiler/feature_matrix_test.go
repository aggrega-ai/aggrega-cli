// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/analyzer"
	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/cognitio/aggrega-cli/internal/compiler/renderer/compose"
	renderHelm "github.com/cognitio/aggrega-cli/internal/compiler/renderer/helm"
	renderHelmChart "github.com/cognitio/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/internal/compiler/trace"
	"github.com/cognitio/aggrega-cli/pkg/catalog"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
)

// featureMatrixVariant identifies the per-Release fixture: which Release file under
// testdata/feature-matrix/releases/ to load and which catalog/recipes/<variant>/
// sub-directory to load alongside the shared catalog/base/.
type featureMatrixVariant struct {
	name        string // sub-directory under catalog/recipes/ AND under releases/
	releaseFile string // basename under releases/, e.g. "helm-synthesized.yaml"
	// extraCatalogPaths lists additional catalog directories to load (beyond
	// the feature-matrix base + per-variant recipes). Used for cross-fixture
	// catalog reuse: the compose-sourced-with-bridge variant points at the
	// standard catalog's compose-bridge feature + feature-recipe paths so
	// the demo Aggregate's bridge attachment resolves against the standard
	// FR.
	extraCatalogPaths []string
	// traceRoot, when non-empty, enables trace capture into the given directory.
	// Used by the cross-cutting TraceLayout criterion; the per-Release smoke
	// tests leave it empty.
	traceRoot string
}

// runFeatureMatrixVariant executes the full 5-step pipeline for one Release variant
// and returns the rendered file set. Mirrors runExample22Variant's shape with
// per-variant Release path and per-variant catalog directory.
func runFeatureMatrixVariant(t *testing.T, variant featureMatrixVariant) *example22Result {
	t.Helper()

	base := "testdata/feature-matrix/catalog/base"
	recipesDir := filepath.Join("testdata/feature-matrix/catalog/recipes", variant.name)
	dirs := append([]string{base, recipesDir}, variant.extraCatalogPaths...)

	cat, err := catalog.LoadAll(dirs...)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: "testdata/feature-matrix/aggregate.yaml",
		ReleasePath:   filepath.Join("testdata/feature-matrix/releases", variant.releaseFile),
		ManifestPath:  "testdata/feature-matrix/manifest.yaml",
	})
	if err != nil {
		t.Fatalf("Step 1 (resolve): %v", err)
	}

	catalogLookup := cat.CatalogLookup()

	guardErrs := resolver.RunGuards(resolveResult.Aggregate, resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra)
	if len(guardErrs) > 0 {
		for _, e := range guardErrs {
			t.Errorf("guard: %s", e.Error())
		}
		t.FailNow()
	}

	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult:    resolveResult,
		CatalogLookup:    catalogLookup,
		LoadSubAggregate: cat.SubAggregateLoader(),
	})
	if err != nil {
		t.Fatalf("Step 2 (topology): %v", err)
	}

	graph, featureOrder, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   catalogLookup,
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Step 3 (IR): %v", err)
	}

	analysis := analyzer.Analyze(graph, catalogLookup)
	for _, e := range analysis.Errors {
		t.Errorf("selector: %s -> %s (%s): %s", e.Source, e.Target, e.Socket, e.Message)
	}
	if t.Failed() {
		t.FailNow()
	}

	var traceWriter trace.Writer
	if variant.traceRoot != "" {
		traceWriter = trace.New(variant.traceRoot)
		defer func() { _ = traceWriter.Close() }()
	}
	rendererInput := renderer.RendererInput{
		Graph:          graph,
		FeatureOrder:   featureOrder,
		ResolveResult:  resolveResult,
		CatalogLookup:  catalogLookup,
		ProviderLookup: cat.ProviderLookup,
		Trace:          traceWriter,
	}
	engineFactory := func(providerName string) (renderer.Engine, error) {
		switch providerName {
		case "helm", "":
			return &renderHelm.HelmEngine{}, nil
		case "helm-chart":
			return &renderHelmChart.HelmEngine{}, nil
		case "compose":
			return &renderCompose.ComposeEngine{}, nil
		default:
			return nil, nil
		}
	}

	renderResult, err := renderer.RenderAll(
		context.Background(),
		rendererInput,
		cat.RecipeLookup(),
		engineFactory,
	)
	if err != nil {
		t.Fatalf("Step 5 (render): %v", err)
	}
	return &example22Result{Files: renderResult.Files, Warnings: renderResult.Warnings}
}

// tryRunFeatureMatrixVariant mirrors runFeatureMatrixVariant but returns the
// first pipeline error encountered instead of calling t.Fatalf. Used by
// negative-path cross-cutting sub-tests that want to assert on the
// compile-time error instead of treating any failure as a test fault.
func tryRunFeatureMatrixVariant(t *testing.T, variant featureMatrixVariant) (*example22Result, error) {
	t.Helper()

	base := "testdata/feature-matrix/catalog/base"
	recipesDir := filepath.Join("testdata/feature-matrix/catalog/recipes", variant.name)
	dirs := append([]string{base, recipesDir}, variant.extraCatalogPaths...)

	cat, err := catalog.LoadAll(dirs...)
	if err != nil {
		return nil, err
	}

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: "testdata/feature-matrix/aggregate.yaml",
		ReleasePath:   filepath.Join("testdata/feature-matrix/releases", variant.releaseFile),
		ManifestPath:  "testdata/feature-matrix/manifest.yaml",
	})
	if err != nil {
		return nil, err
	}

	catalogLookup := cat.CatalogLookup()
	if guardErrs := resolver.RunGuards(resolveResult.Aggregate, resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra); len(guardErrs) > 0 {
		return nil, guardErrs[0]
	}

	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult:    resolveResult,
		CatalogLookup:    catalogLookup,
		LoadSubAggregate: cat.SubAggregateLoader(),
	})
	if err != nil {
		return nil, err
	}

	graph, featureOrder, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   catalogLookup,
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		return nil, err
	}

	analysis := analyzer.Analyze(graph, catalogLookup)
	if len(analysis.Errors) > 0 {
		return nil, analysis.Errors[0]
	}

	rendererInput := renderer.RendererInput{
		Graph:          graph,
		FeatureOrder:   featureOrder,
		ResolveResult:  resolveResult,
		CatalogLookup:  catalogLookup,
		ProviderLookup: cat.ProviderLookup,
	}
	engineFactory := func(providerName string) (renderer.Engine, error) {
		switch providerName {
		case "helm", "":
			return &renderHelm.HelmEngine{}, nil
		case "helm-chart":
			return &renderHelmChart.HelmEngine{}, nil
		case "compose":
			return &renderCompose.ComposeEngine{}, nil
		default:
			return nil, nil
		}
	}

	renderResult, err := renderer.RenderAll(
		context.Background(),
		rendererInput,
		cat.RecipeLookup(),
		engineFactory,
	)
	if err != nil {
		return nil, err
	}
	return &example22Result{Files: renderResult.Files, Warnings: renderResult.Warnings}, nil
}

// assertFeatureMatrixFilePresent checks at least one file in res whose path contains
// each substring from parts (substring AND across parts). Returns the matched
// content for further inspection by the caller.
func assertFeatureMatrixFilePresent(t *testing.T, res *example22Result, parts ...string) string {
	t.Helper()
	for path, content := range res.Files {
		matched := true
		for _, p := range parts {
			if !strings.Contains(path, p) {
				matched = false
				break
			}
		}
		if matched {
			return string(content)
		}
	}
	t.Errorf("expected file matching %v not found; files: %v", parts, sortedKeys(res.Files))
	return ""
}

// --- feature matrix variant tests ---

// TestFeatureMatrix_HelmSynthesized exercises the helm provider's workload-mode
// path: both api and db Recipes declare workload: only. Expected output:
// per-component Deployment + Service at dist/app/helm/.
func TestFeatureMatrix_HelmSynthesized(t *testing.T) {
	t.Parallel()
	res := runFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "helm-synthesized",
		releaseFile: "helm-synthesized.yaml",
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files")
	}
	apiDep := assertFeatureMatrixFilePresent(t, res, "dist/app/helm/", "api", "deployment")
	if !strings.Contains(apiDep, "namespace: feature-matrix") {
		t.Errorf("api deployment missing namespace feature-matrix:\n%s", apiDep)
	}
	if !strings.Contains(apiDep, "aggrega.ai/component-id: api") {
		t.Errorf("api deployment missing provenance label aggrega.ai/component-id:\n%s", apiDep)
	}
	assertFeatureMatrixFilePresent(t, res, "dist/app/helm/", "db", "deployment")
}

// TestFeatureMatrix_HelmSourcedWithArtifacts exercises the helm provider's
// sourced + artifacts path: api Recipe carries source: + valuesMapping: +
// artifacts: (ServiceMonitor); db Recipe carries workload: only. Expected
// output: chart-rendered manifests + ServiceMonitor + db deployment.
func TestFeatureMatrix_HelmSourcedWithArtifacts(t *testing.T) {
	t.Parallel()
	res := runFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "helm-sourced-with-artifacts",
		releaseFile: "helm-sourced-with-artifacts.yaml",
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files")
	}
	// Chart-rendered api deployment.
	assertFeatureMatrixFilePresent(t, res, "dist/app/helm/", "api-deployment")
	// Artifacts ServiceMonitor.
	sm := assertFeatureMatrixFilePresent(t, res, "dist/app/helm/", "servicemonitor")
	if !strings.Contains(sm, "ServiceMonitor") {
		t.Errorf("servicemonitor.yaml missing kind ServiceMonitor:\n%s", sm)
	}
	if !strings.Contains(sm, "api-metrics") {
		t.Errorf("servicemonitor.yaml missing api-metrics name:\n%s", sm)
	}
	// db Deployment from workload synth.
	assertFeatureMatrixFilePresent(t, res, "dist/app/helm/", "db-deployment")
}

// TestFeatureMatrix_ComposeSynthesized exercises the compose provider's
// workload-mode path: both Recipes declare workload: only. Expected output:
// one merged docker-compose.yaml at dist/app/compose/.
func TestFeatureMatrix_ComposeSynthesized(t *testing.T) {
	t.Parallel()
	res := runFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "compose-synthesized",
		releaseFile: "compose-synthesized.yaml",
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files")
	}
	dc := assertFeatureMatrixFilePresent(t, res, "dist/app/compose/docker-compose.yaml")
	for _, want := range []string{"api:", "db:", "image: api-server:1.0.0", "image: mariadb:11"} {
		if !strings.Contains(dc, want) {
			t.Errorf("docker-compose.yaml missing %q:\n%s", want, dc)
		}
	}
}

// TestFeatureMatrix_ComposeSourcedWithBridge exercises the compose provider's
// sourced path PLUS a boundary-scope FR (core/compose-bridge) attached at
// the parent boundary, with two child boundaries declaring
// `directory: "{{ .parent_boundary.directory }}"` so all three contributions
// (api + db + bridge service) merge to ONE docker-compose.yaml at the
// parent's evaluated path. Expected: per-(path, provider) merged-path
// collapse with aggrega-gw service present alongside api + db.
func TestFeatureMatrix_ComposeSourcedWithBridge(t *testing.T) {
	t.Parallel()
	res := runFeatureMatrixVariant(t, featureMatrixVariant{
		name:        "compose-sourced-with-bridge",
		releaseFile: "compose-sourced-with-bridge.yaml",
		extraCatalogPaths: []string{
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
		},
	})
	if len(res.Files) == 0 {
		t.Fatalf("renderer produced no files")
	}
	dc := assertFeatureMatrixFilePresent(t, res, "dist/cluster/compose/docker-compose.yaml")
	for _, want := range []string{"api:", "db:", "aggrega-gw:"} {
		if !strings.Contains(dc, want) {
			t.Errorf("merged docker-compose.yaml missing service %q:\n%s", want, dc)
		}
	}
	// Bridge injectMapping should rewrite api's DB_HOST to aggrega-gw:13306.
	if !strings.Contains(dc, "aggrega-gw:13306") {
		t.Errorf("merged compose missing aggrega-gw:13306 (bridge injectMapping rewrite):\n%s", dc)
	}
}
