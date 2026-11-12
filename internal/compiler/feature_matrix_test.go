// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
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
	// includeTrace, when true, turns on per-pass trace collection; the
	// rendered result's Trace map then carries the per-node trace entries.
	// Used by the cross-cutting TraceLayout criterion; the per-Release smoke
	// tests leave it false.
	includeTrace bool
}

// featureMatrixInput builds the harness Input for one variant: the per-variant
// Release under releases/, the feature-matrix base catalog plus the variant's
// recipes sub-directory plus any extra catalog paths, and the trace knob. The
// Aggregate and Manifest are derived from the Release's spec pointers.
func featureMatrixInput(variant featureMatrixVariant) pipelinetest.Input {
	base := "testdata/feature-matrix/catalog/base"
	recipesDir := filepath.Join("testdata/feature-matrix/catalog/recipes", variant.name)
	return pipelinetest.Input{
		ReleasePath:  filepath.Join("testdata/feature-matrix/releases", variant.releaseFile),
		CatalogPaths: append([]string{base, recipesDir}, variant.extraCatalogPaths...),
		IncludeTrace: variant.includeTrace,
	}
}

// runFeatureMatrixVariant compiles one Release variant through the build-
// faithful harness (pipelinetest.Build) and returns the rendered file set
// (plus the trace map when includeTrace is set).
func runFeatureMatrixVariant(t *testing.T, variant featureMatrixVariant) *fullstackAppResult {
	t.Helper()
	result := pipelinetest.Build(t, featureMatrixInput(variant))
	return &fullstackAppResult{Files: result.Artifacts, Trace: result.Trace}
}

// tryRunFeatureMatrixVariant mirrors runFeatureMatrixVariant but returns the
// first build error encountered instead of failing the test, via the harness's
// error-returning sibling. Used by negative-path cross-cutting sub-tests that
// assert on the error a real build produces rather than treating any failure as
// a test fault. It runs the full build-faithful pipeline, so the error surfaces
// in the same place a real build would surface it.
func tryRunFeatureMatrixVariant(t *testing.T, variant featureMatrixVariant) (*fullstackAppResult, error) {
	t.Helper()
	result, err := pipelinetest.BuildE(t, featureMatrixInput(variant))
	if err != nil {
		return nil, err
	}
	return &fullstackAppResult{Files: result.Artifacts, Trace: result.Trace}, nil
}

// assertFeatureMatrixFilePresent checks at least one file in res whose path contains
// each substring from parts (substring AND across parts). Returns the matched
// content for further inspection by the caller.
func assertFeatureMatrixFilePresent(t *testing.T, res *fullstackAppResult, parts ...string) string {
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
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge/1.0.0/feature.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
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
	// The consumer's connection delivers both DB_HOST (from the address field) and
	// DB_PORT (from port), so the bridge FeatureRecipe's injectMapping rewrites the
	// two together: the host variable to the gateway's name alone and the port
	// variable to the gateway's listen port (the db target port, 3306; no
	// collision). Both are asserted because they are one rewrite — a host variable
	// written as "aggrega-gw:3306" beside a port variable would hand the consumer a
	// port twice, and a port variable left at the target's port is wrong as soon
	// as a collision moves the listen port.
	for _, want := range []string{"DB_HOST: aggrega-gw\n", `DB_PORT: "3306"`} {
		if !strings.Contains(dc, want) {
			t.Errorf("merged compose missing %q (edge-derived bridge rewrite):\n%s", want, dc)
		}
	}
}
