// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// hostPatchHelmchartFixtureInput names the host-patch-helmchart fixture: a
// single helm-chart-sourced component (the host) with a component-scope
// FeatureRecipe attached whose pipeline runs a /host PATCH step. The step
// content-selects the host chart's values.yaml and injects an env var into it.
// Because helm-chart is a nesting BoundaryAssembler (it places each node's chart
// under its own umbrella position rather than merging fragments), its per-node
// chart source is discrete (not a pre-assembly fragment), so the /host PATCH runs
// per-node through the helm-chart codec — no post-assembly phase. The step references a Plugin
// ENTITY URI; the entity's spec.plugin .wasm artifact is built on demand from the
// Rust PDK example and staged co-located beside its Plugin entity in the catalog so
// the build's entity-front resolves it. When the wasm toolchain is absent the
// staging build skips the calling test.
func hostPatchHelmchartFixtureInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	catalogPaths := stageColocatedCatalog(t, []string{
		"testdata/host-patch-helmchart/catalog/base",
		"testdata/host-patch-helmchart/catalog/recipes/helm-chart",
	}, map[string]string{
		"pipeline-step/host-patch": "frp-step-host-patch-rust.wasm",
	})
	return pipelinetest.Input{
		ReleasePath:  "testdata/host-patch-helmchart/release.yaml",
		CatalogPaths: catalogPaths,
	}
}

// findArtifactBySuffix returns the single artifact whose key ends with suffix,
// failing the test when zero or more than one match. The dispatch-path prefix
// (directory + provider sub-directory) is operator-shaped, so the test addresses
// the host chart files by their stable charts/<node>/<file> tail rather than the
// full prefix.
func findArtifactBySuffix(t *testing.T, artifacts map[string][]byte, suffix string) []byte {
	t.Helper()
	var found []byte
	var matchedKey string
	for k, v := range artifacts {
		if strings.HasSuffix(k, suffix) {
			if matchedKey != "" {
				t.Fatalf("expected exactly one artifact ending in %q; matched both %q and %q", suffix, matchedKey, k)
			}
			matchedKey = k
			found = v
		}
	}
	if matchedKey == "" {
		t.Fatalf("no artifact ending in %q; got keys %v", suffix, artifactKeys(artifacts))
	}
	return found
}

// TestHostPatchHelmchart_PerNodeHostPatch compiles the host-patch-helmchart
// fixture and asserts the cross-entity PATCH ran per-node against the
// helm-chart host: the host chart's values.yaml carries the injected env var,
// the surgical node-span edit preserved the file's comment and sibling keys
// byte-for-byte, and the FeatureRecipe's own output rendered alongside. A
// helm-chart host is patched per-node (its chart source is discrete), so this
// exercises the per-node /host path through the helm-chart codec — no
// post-assembly phase.
func TestHostPatchHelmchart_PerNodeHostPatch(t *testing.T) {
	in := hostPatchHelmchartFixtureInput(t)
	t.Parallel()

	result := pipelinetest.Build(t, in)

	values := findArtifactBySuffix(t, result.Artifacts, "charts/api/values.yaml")
	got := string(values)

	// PATCH landed: the guest content-selected the host values.yaml and added
	// the env var under the environment block.
	if !strings.Contains(got, "INJECTED_BY_FEATURE: frp-step-host-patch") {
		t.Errorf("host values.yaml missing the injected env var; got:\n%s", got)
	}
	// Byte-faithful node-span surgery: the leading comment and the pre-existing
	// sibling key (with its single-quoted scalar style) survive verbatim.
	if !strings.Contains(got, "# Chart values for the api-server host component.") {
		t.Errorf("host values.yaml lost its leading comment after PATCH; got:\n%s", got)
	}
	if !strings.Contains(got, "EXISTING_VAR: 'keep-me'") {
		t.Errorf("host values.yaml lost a pre-existing env var (or its scalar style) after PATCH; got:\n%s", got)
	}
	if !strings.Contains(got, "image: api-server:latest") {
		t.Errorf("host values.yaml lost a sibling top-level key after PATCH; got:\n%s", got)
	}

	// Content selection: Chart.yaml is NOT the values.yaml, so the guest never
	// patched it — it carries no injected env var.
	chart := findArtifactBySuffix(t, result.Artifacts, "charts/api/Chart.yaml")
	if strings.Contains(string(chart), "INJECTED_BY_FEATURE") {
		t.Errorf("Chart.yaml should not carry the injected env var; got:\n%s", chart)
	}

	// Content selection: the chart template (an opaque unit carrying Go-template
	// actions) is never a PATCH target.
	deploy := findArtifactBySuffix(t, result.Artifacts, "charts/api/templates/deployment.yaml")
	if strings.Contains(string(deploy), "INJECTED_BY_FEATURE") {
		t.Errorf("chart template should not carry the injected env var; got:\n%s", deploy)
	}

	// The FeatureRecipe's own resource-generating output rendered alongside the
	// host (proving the pipeline ran from within a resource-generating FR).
	_ = findArtifactBySuffix(t, result.Artifacts, "features/api/guardrail/guardrail-config.yaml")
}

// TestHostPatchHelmchart_Deterministic compiles the host-patch-helmchart
// fixture repeatedly and asserts byte-identical output across runs — the
// per-node /host PATCH path (host-codec selection, segmentation, apply-back) is
// order-preserving end-to-end. This is helm-chart's always-on double-run guard
// for the cross-entity case.
func TestHostPatchHelmchart_Deterministic(t *testing.T) {
	in := hostPatchHelmchartFixtureInput(t)
	t.Parallel()
	pipelinetest.AssertDeterministic(t, in)
}
