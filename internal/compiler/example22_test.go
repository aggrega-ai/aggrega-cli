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
	renderHelm "github.com/cognitio/aggrega-cli/internal/compiler/renderer/helm"
	renderHelmChart "github.com/cognitio/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/catalog"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
)

// Example22 drives the end-to-end proof across the 4-variant provider matrix:
//
//	                      | generative (sourceless) | rendering (sourced) |
//	--------------------- | ----------------------- | ------------------- |
//	provider: helm        | A                        | B                   |
//	provider: helm-chart  | C                        | D                   |
//
// All 4 variants share the same Aggregate + Release + Manifest; only the Recipe
// layer changes (which `catalog/recipes/<variant>` sub-directory is loaded).
//
// Common assertions (every variant):
//
//  1. API_HOST env on frontend deployment = "backend.backend-zone.svc"  (injectMapping flow)
//  2. Frontend deployment metadata.namespace = "frontend-zone"           (WI-6 annotation injection)
//  3. Backend deployment metadata.namespace = "backend-zone"             (WI-6 annotation injection)
//  4. Frontend container port = 3003                                     (values.port flows from Aggregate)
//  5. Backend replicas = 3                                               (infra.replicas flows from Release)
//  6. Output shape matches the provider mode.

type variantMode int

const (
	modeHelmFlat variantMode = iota
)

type example22Result struct {
	Files    map[string][]byte
	Warnings []string
}

// runExample22 executes the full 5-step pipeline for one variant and returns
// the rendered file set. The `variant` sub-directory under catalog/recipes/
// drives the provider + source choice.
func runExample22(t *testing.T, variant string) *example22Result {
	t.Helper()

	base := "testdata/example22/catalog/base"
	recipesDir := filepath.Join("testdata/example22/catalog/recipes", variant)

	cat, err := catalog.LoadAll(base, recipesDir)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: "testdata/example22/aggregate.yaml",
		ReleasePath:   "testdata/example22/release.yaml",
		ManifestPath:  "testdata/example22/manifest.yaml",
	})
	if err != nil {
		t.Fatalf("Step 1 (resolve): %v", err)
	}

	catalogLookup := cat.CatalogLookup()

	// Step 1a: Guards — MUST pass for the end-to-end proof.
	guardErrs := resolver.RunGuards(resolveResult.Aggregate, resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra)
	if len(guardErrs) > 0 {
		for _, e := range guardErrs {
			t.Errorf("guard: %s", e.Error())
		}
		t.FailNow()
	}

	// Step 2: Topology
	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult:    resolveResult,
		CatalogLookup:    catalogLookup,
		LoadSubAggregate: cat.SubAggregateLoader(),
	})
	if err != nil {
		t.Fatalf("Step 2 (topology): %v", err)
	}

	// Step 3: IR
	graph, featureOrder, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   catalogLookup,
		CompilerVersion: "0.1.0-m1",
	})
	if err != nil {
		t.Fatalf("Step 3 (IR): %v", err)
	}

	// Step 4: Analyze
	analysis := analyzer.Analyze(graph, catalogLookup)
	for _, e := range analysis.Errors {
		t.Errorf("selector: %s → %s (%s): %s", e.Source, e.Target, e.Socket, e.Message)
	}
	if t.Failed() {
		t.FailNow()
	}

	// Step 5: Render — provider per-node via factory.
	providerLookup := cat.ProviderLookup
	rendererInput := renderer.RendererInput{
		Graph:          graph,
		FeatureOrder:   featureOrder,
		ResolveResult:  resolveResult,
		CatalogLookup:  catalogLookup,
		ProviderLookup: providerLookup,
	}
	engineFactory := func(providerName string) (renderer.Engine, error) {
		switch providerName {
		case "helm", "":
			return &renderHelm.HelmEngine{}, nil
		case "helm-chart":
			return &renderHelmChart.HelmEngine{}, nil
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

// assertCommonProof asserts the invariants for variants whose output is
// pre-rendered K8s YAML (helm flat in both modes, and helm-chart generative
// whose templates have values substituted by our own engine). The helm-chart
// sourced variant uses `assertSourcedUmbrellaProof` instead, because its
// sub-chart templates come through unrendered — values propagate through the
// umbrella's values.yaml and are substituted at `helm install` time.
func assertCommonProof(t *testing.T, res *example22Result, mode variantMode) {
	t.Helper()

	// example22's release.yaml authors `directory: ./dist/<name>` per
	// boundary, so paths are `dist/<name>/<provider>/...`. Search by the
	// authored directory segment plus the deployment basename.
	frontendDep, frontendDepPath := findFile(res.Files, "dist/frontend/", "frontend", "deployment")
	backendDep, backendDepPath := findFile(res.Files, "dist/backend/", "backend", "deployment")

	if frontendDep == "" {
		t.Fatalf("frontend deployment not found; files: %v", sortedKeys(res.Files))
	}
	if backendDep == "" {
		t.Fatalf("backend deployment not found; files: %v", sortedKeys(res.Files))
	}

	// 1. API_HOST injection via k8s-dns-patcher feature recipe
	if !strings.Contains(frontendDep, "backend.backend-zone.svc") {
		t.Errorf("[%s] API_HOST not rewritten in frontend deployment (%s):\n%s", tagFor(mode), frontendDepPath, frontendDep)
	}

	// 2 + 3. Namespace injection (WI-6)
	if !strings.Contains(frontendDep, "namespace: frontend-zone") {
		t.Errorf("[%s] frontend deployment missing metadata.namespace frontend-zone (%s):\n%s", tagFor(mode), frontendDepPath, frontendDep)
	}
	if !strings.Contains(backendDep, "namespace: backend-zone") {
		t.Errorf("[%s] backend deployment missing metadata.namespace backend-zone (%s):\n%s", tagFor(mode), backendDepPath, backendDep)
	}

	// 4. Frontend port 3003 (from Aggregate .values.port=3003)
	if !strings.Contains(frontendDep, "3003") {
		t.Errorf("[%s] frontend deployment missing port 3003 (%s):\n%s", tagFor(mode), frontendDepPath, frontendDep)
	}

	// 5. Backend replicas 3 (from Release .infra.replicas=3)
	if !strings.Contains(backendDep, "replicas: 3") {
		t.Errorf("[%s] backend deployment missing replicas: 3 (%s):\n%s", tagFor(mode), backendDepPath, backendDep)
	}

	// 6. Output shape per provider mode
	hasBoundaryChart := containsAny(res.Files, "dist/frontend/helm/Chart.yaml")
	hasSubChart := containsAny(res.Files, "dist/frontend/helm/charts/")
	switch mode {
	case modeHelmFlat:
		if hasBoundaryChart {
			t.Errorf("[helm] unexpected umbrella Chart.yaml at boundary root; files: %v", sortedKeys(res.Files))
		}
		if hasSubChart {
			t.Errorf("[helm] unexpected charts/<instance_id>/ nesting; files: %v", sortedKeys(res.Files))
		}
	}
}

// findFile returns (contents, path) of the first file whose path contains ALL substrings.
func findFile(files map[string][]byte, parts ...string) (string, string) {
	for path, content := range files {
		matched := true
		for _, p := range parts {
			if !strings.Contains(path, p) {
				matched = false
				break
			}
		}
		if matched {
			return string(content), path
		}
	}
	return "", ""
}

func containsAny(files map[string][]byte, prefix string) bool {
	for path := range files {
		if strings.Contains(path, prefix) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func tagFor(m variantMode) string {
	if m == modeHelmFlat {
		return "helm"
	}
	return "helm-chart"
}

// --- Variant tests ---------------------------------------------------------

func TestExample22_HelmGenerative(t *testing.T) {
	t.Parallel()
	res := runExample22(t, "helm-generative")
	assertCommonProof(t, res, modeHelmFlat)
}

func TestExample22_HelmSourced(t *testing.T) {
	t.Parallel()
	res := runExample22(t, "helm-sourced")
	assertCommonProof(t, res, modeHelmFlat)
}

func TestExample22_HelmChartSourced(t *testing.T) {
	t.Parallel()
	res := runExample22(t, "helm-chart-sourced")
	assertSourcedUmbrellaProof(t, res)
}

// assertSourcedUmbrellaProof covers the helm-chart sourced variant. The user's
// chart is placed under charts/<instance_id>/ verbatim, so templates still
// reference {{ .Values.* }}. We verify propagation through umbrella files:
//
//   - umbrella Chart.yaml declares dependencies using the user's chart name
//     with alias=<instance_id>
//   - sub-chart Chart.yaml preserves the user's original chart name
//   - umbrella values.yaml scopes per-instance values (replicas, port,
//     environment.API_HOST) under the instance alias
//
// Namespace injection is out of scope here: in sourced mode the user's chart
// controls metadata.namespace (typically via .Release.Namespace), which the
// deploying tool sets at `helm install --namespace=<boundary-ns>`.
func assertSourcedUmbrellaProof(t *testing.T, res *example22Result) {
	t.Helper()

	// Umbrella shape exists
	if !containsAny(res.Files, "dist/frontend/helm-chart/Chart.yaml") {
		t.Fatalf("frontend umbrella Chart.yaml missing; files: %v", sortedKeys(res.Files))
	}
	if !containsAny(res.Files, "dist/frontend/helm-chart/charts/") {
		t.Fatalf("frontend sub-chart nesting missing; files: %v", sortedKeys(res.Files))
	}

	// Sub-chart preserves user's original chart name
	subChart, subPath := findFile(res.Files, "dist/frontend/helm-chart/charts/frontend/Chart.yaml")
	if subChart == "" {
		t.Fatalf("frontend sub-chart Chart.yaml not found; files: %v", sortedKeys(res.Files))
	}
	if !strings.Contains(subChart, "name: web-ui") {
		t.Errorf("sourced sub-chart did not preserve user's chart name `web-ui` (%s):\n%s", subPath, subChart)
	}

	// Umbrella dep uses original chart name with alias = instance_id
	umbrella, umbrellaPath := findFile(res.Files, "dist/frontend/helm-chart/Chart.yaml")
	if !strings.Contains(umbrella, "- name: web-ui") {
		t.Errorf("umbrella dep missing original chart name `web-ui` (%s):\n%s", umbrellaPath, umbrella)
	}
	if !strings.Contains(umbrella, "alias: frontend") {
		t.Errorf("umbrella dep missing alias=frontend (%s):\n%s", umbrellaPath, umbrella)
	}

	// Umbrella values.yaml scopes per-instance values
	frontendValues, fvPath := findFile(res.Files, "dist/frontend/helm-chart/values.yaml")
	if frontendValues == "" {
		t.Fatalf("frontend umbrella values.yaml missing; files: %v", sortedKeys(res.Files))
	}
	if !strings.Contains(frontendValues, "frontend:") {
		t.Errorf("umbrella values.yaml missing `frontend:` alias scope (%s):\n%s", fvPath, frontendValues)
	}
	// Values propagation proof: Aggregate .values.port=3003 → frontend.ports
	if !strings.Contains(frontendValues, "3003:3003") {
		t.Errorf("frontend.ports did not pick up .values.port=3003 (%s):\n%s", fvPath, frontendValues)
	}
	// Inject flow proof: injectMapping produced API_HOST
	if !strings.Contains(frontendValues, "backend.backend-zone.svc") {
		t.Errorf("frontend.environment.API_HOST not rewritten in umbrella values (%s):\n%s", fvPath, frontendValues)
	}

	// Backend boundary: Release .infra.replicas=3 → backend.replicas
	backendValues, bvPath := findFile(res.Files, "dist/backend/helm-chart/values.yaml")
	if backendValues == "" {
		t.Fatalf("backend umbrella values.yaml missing; files: %v", sortedKeys(res.Files))
	}
	if !strings.Contains(backendValues, "replicas: 3") {
		t.Errorf("backend.replicas did not pick up .infra.replicas=3 (%s):\n%s", bvPath, backendValues)
	}
}
