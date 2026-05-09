// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/analyzer"
	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/cognitio/aggrega-cli/internal/compiler/renderer/compose"
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

// variantFixture identifies the catalog sub-directory plus optional
// per-variant overrides for the Aggregate / Release / Manifest paths.
// Empty path fields default to the example22 root files; variants requiring
// distinct authoring shapes (compose-only Aggregates, artifacts-only topology)
// supply their own paths under testdata/example22/aggregates/ etc.
type variantFixture struct {
	name          string
	aggregatePath string
	releasePath   string
	manifestPath  string
}

// runExample22 executes the full 5-step pipeline for one variant and returns
// the rendered file set, sharing the root Aggregate / Release / Manifest.
// Thin wrapper over runExample22Variant for variants that don't override.
func runExample22(t *testing.T, variant string) *example22Result {
	t.Helper()
	return runExample22Variant(t, variantFixture{name: variant})
}

// runExample22Variant executes the full 5-step pipeline for a per-variant
// fixture. Path fields on fx default to the example22 root files when empty;
// variants whose authoring shape diverges from the shared Aggregate / Release
// / Manifest pass explicit paths.
func runExample22Variant(t *testing.T, fx variantFixture) *example22Result {
	t.Helper()

	base := "testdata/example22/catalog/base"
	recipesDir := filepath.Join("testdata/example22/catalog/recipes", fx.name)
	if fx.aggregatePath == "" {
		fx.aggregatePath = "testdata/example22/aggregate.yaml"
	}
	if fx.releasePath == "" {
		fx.releasePath = "testdata/example22/release.yaml"
	}
	if fx.manifestPath == "" {
		fx.manifestPath = "testdata/example22/manifest.yaml"
	}

	cat, err := catalog.LoadAll(base, recipesDir)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: fx.aggregatePath,
		ReleasePath:   fx.releasePath,
		ManifestPath:  fx.manifestPath,
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
		case "helm", "helm-raw", "":
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

func TestExample22_HelmSourced(t *testing.T) {
	t.Parallel()
	res := runExample22(t, "helm-sourced")
	assertCommonProof(t, res, modeHelmFlat)
}

func TestExample22_HelmSynthesized(t *testing.T) {
	t.Parallel()
	res := runExample22(t, "helm-synthesized")
	assertCommonProof(t, res, modeHelmFlat)
	assertProvenanceLabels(t, res.Files, nil)
	assertDistMatches(t, res.Files, "testdata/example22/expected/helm-synthesized", "dist/")
}

func TestExample22_HelmArtifacts(t *testing.T) {
	t.Parallel()
	res := runExample22Variant(t, variantFixture{
		name:          "helm-artifacts",
		aggregatePath: "testdata/example22/aggregates/helm-artifacts.yaml",
		releasePath:   "testdata/example22/releases/helm-artifacts.yaml",
		manifestPath:  "testdata/example22/manifests/helm-artifacts.yaml",
	})
	assertProvenanceLabels(t, res.Files, nil)
	assertDistMatches(t, res.Files, "testdata/example22/expected/helm-artifacts", "dist/")
}

func TestExample22_ComposeSynthesized(t *testing.T) {
	t.Parallel()
	res := runExample22Variant(t, variantFixture{
		name:          "compose-synthesized",
		aggregatePath: "testdata/example22/aggregates/compose-synthesized.yaml",
		releasePath:   "testdata/example22/releases/compose-synthesized.yaml",
		manifestPath:  "testdata/example22/manifests/compose-synthesized.yaml",
	})
	assertDistMatches(t, res.Files, "testdata/example22/expected/compose-synthesized", "dist/")
}

func TestExample22_ComposeSourced(t *testing.T) {
	t.Parallel()
	res := runExample22Variant(t, variantFixture{
		name:          "compose-sourced",
		aggregatePath: "testdata/example22/aggregates/compose-synthesized.yaml",
		releasePath:   "testdata/example22/releases/compose-synthesized.yaml",
		manifestPath:  "testdata/example22/manifests/compose-synthesized.yaml",
	})
	assertDistMatches(t, res.Files, "testdata/example22/expected/compose-sourced", "dist/")
}

func TestExample22_HelmSourcedWithArtifacts(t *testing.T) {
	t.Parallel()
	res := runExample22(t, "helm-sourced-with-artifacts")
	assertCommonProof(t, res, modeHelmFlat)
	assertProvenanceLabels(t, res.Files, nil)
	assertDistMatches(t, res.Files, "testdata/example22/expected/helm-sourced-with-artifacts", "dist/")
}

func TestExample22_HelmSynthesizedWithArtifacts(t *testing.T) {
	t.Parallel()
	res := runExample22(t, "helm-synthesized-with-artifacts")
	assertCommonProof(t, res, modeHelmFlat)
	assertProvenanceLabels(t, res.Files, nil)
	assertDistMatches(t, res.Files, "testdata/example22/expected/helm-synthesized-with-artifacts", "dist/")
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

// assertDistMatches walks expectedDir and byte-compares every file there
// against the corresponding entry in actual. The deterministic-output
// invariant requires byte-identical output across runs of the same input,
// so byte-compare (not structural compare) is the right level — any whitespace
// or key-ordering shift surfaces as a test failure for investigation.
//
// stripPrefix is removed from actual file keys before lookup against
// expectedDir's relative paths. The repo-level .gitignore excludes `dist/`,
// so goldens land under expectedDir without the leading `dist/` segment;
// pass "dist/" as stripPrefix to map between the actual files map's
// `dist/<boundary>/...` keys and the goldens' `<boundary>/...` paths.
// Pass "" to compare unmapped (the helper is general).
//
// Comparison rules:
//   - Every file under expectedDir MUST appear in actual at
//     stripPrefix+<rel-path>. Missing-from-actual is a fatal failure.
//   - Every file in actual whose key starts with stripPrefix MUST appear
//     under expectedDir at the post-strip path. Present-in-actual-but-
//     missing-from-expected is a fatal failure (the variant grew an
//     unexpected emission).
//   - Files outside the variant's stripPrefix are ignored (other variants'
//     output, not this variant's surface).
//
// Set AGGREGA_UPDATE_GOLDENS=1 to (re)write the goldens under expectedDir
// from the actual file set; the assertions then pass trivially. Use only
// when intentionally regenerating goldens after inspection.
func assertDistMatches(t *testing.T, actual map[string][]byte, expectedDir, stripPrefix string) {
	t.Helper()

	if os.Getenv("AGGREGA_UPDATE_GOLDENS") == "1" {
		if err := os.RemoveAll(expectedDir); err != nil {
			t.Fatalf("remove %s: %v", expectedDir, err)
		}
		for path, body := range actual {
			if stripPrefix != "" && !strings.HasPrefix(path, stripPrefix) {
				continue
			}
			rel := strings.TrimPrefix(path, stripPrefix)
			full := filepath.Join(expectedDir, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
			}
			if err := os.WriteFile(full, body, 0o644); err != nil {
				t.Fatalf("write %s: %v", full, err)
			}
		}
		t.Logf("AGGREGA_UPDATE_GOLDENS=1 — wrote goldens under %s", expectedDir)
		return
	}

	expected := map[string][]byte{}
	err := filepath.Walk(expectedDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(expectedDir, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		expected[filepath.ToSlash(rel)] = body
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", expectedDir, err)
	}

	for rel, want := range expected {
		actualKey := stripPrefix + rel
		got, ok := actual[actualKey]
		if !ok {
			t.Errorf("missing from actual: %s (expected %d bytes)", actualKey, len(want))
			continue
		}
		if string(got) != string(want) {
			t.Errorf("byte mismatch at %s\n--- want ---\n%s\n--- got ---\n%s", actualKey, string(want), string(got))
		}
	}

	for path := range actual {
		if stripPrefix != "" && !strings.HasPrefix(path, stripPrefix) {
			continue
		}
		rel := strings.TrimPrefix(path, stripPrefix)
		if _, ok := expected[rel]; !ok {
			t.Errorf("present in actual but missing from expected: %s", path)
		}
	}
}

// assertProvenanceLabels verifies the canonical aggrega.ai/* label set is
// present on K8s-shaped manifests in actual. The helm engine's
// buildAggreraLabels emits component-id + boundary-path on every K8s
// manifest; aggrega.ai/recipe-uri is emitted by the artifacts evaluator
// path only — the helm and helm-chart engines do not yet thread Recipe
// identity onto NodeRenderContext, so recipe-uri is asserted only when
// requested explicitly via requiredLabels.
//
// Files without metadata.labels structure (compose docker-compose.yaml,
// chart Chart.yaml, values.yaml, sub-chart Chart.yaml, the helm-chart
// umbrella's own non-templated files) are skipped — the label contract
// applies to K8s manifests, not chart packaging or compose service files.
//
// requiredLabels is the exact set of label keys that must appear; pass nil
// for the helm-engine-shipped default (component-id, boundary-path).
func assertProvenanceLabels(t *testing.T, actual map[string][]byte, requiredLabels []string) {
	t.Helper()

	if requiredLabels == nil {
		requiredLabels = []string{
			"aggrega.ai/component-id",
			"aggrega.ai/boundary-path",
		}
	}

	paths := make([]string, 0, len(actual))
	for p := range actual {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		body := string(actual[path])
		base := filepath.Base(path)
		// Skip files that don't carry K8s metadata.labels structure.
		switch base {
		case "Chart.yaml", "values.yaml", "docker-compose.yaml":
			continue
		}
		// Skip helm-chart sub-chart templates: they remain `{{ .Values.* }}`
		// templated and do not carry rendered labels.
		if strings.Contains(path, "/charts/") && strings.Contains(path, "/templates/") {
			continue
		}
		// Skip files without a metadata block at all (raw artifacts may be
		// non-K8s shapes — apply the contract only when metadata: appears).
		if !strings.Contains(body, "metadata:") {
			continue
		}
		for _, lbl := range requiredLabels {
			if !strings.Contains(body, lbl+":") {
				t.Errorf("provenance label %s missing from %s:\n%s", lbl, path, body)
			}
		}
	}
}
