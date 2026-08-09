// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// pipelineRenderFixtureInput names the pipeline-render fixture for the
// build-faithful harness: a single helm-synthesised component whose Recipe
// carries one pipeline step referencing a Plugin ENTITY URI. The step
// content-selects the rendered Deployment, PATCHes it (own-scope annotation), and
// EMITs an own-scope ConfigMap. The fixture exercises the production entity-front:
// the entity's spec.plugin .wasm artifact is built on demand from the Rust PDK
// example and staged co-located beside its Plugin entity in the catalog, so the
// build resolves entity URI → spec.plugin → co-located tier end-to-end. When the
// wasm toolchain is absent EnsureFixtureBytes skips the calling test.
func pipelineRenderFixtureInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	catalogPaths := stageColocatedCatalog(t, []string{
		"testdata/pipeline-render/catalog/base",
		"testdata/pipeline-render/catalog/recipes/helm",
	}, map[string]string{
		"pipeline-step/patch": "frp-step-patch-rust.wasm",
	})
	return pipelinetest.Input{
		ReleasePath:  "testdata/pipeline-render/release.yaml",
		CatalogPaths: catalogPaths,
	}
}

// TestPipelineRender_PatchAndEmit compiles the pipeline-render fixture and
// asserts the pipeline ran end-to-end: the rendered Deployment carries the
// own-scope PATCH annotation, the own-scope EMIT produced a new ConfigMap
// artifact, and content-selection left the Service untouched.
func TestPipelineRender_PatchAndEmit(t *testing.T) {
	in := pipelineRenderFixtureInput(t)
	t.Parallel()

	result := pipelinetest.Build(t, in)

	deploy, ok := result.Artifacts["dist/main/helm/api-deployment.yaml"]
	if !ok {
		t.Fatalf("expected rendered Deployment artifact; got keys %v", artifactKeys(result.Artifacts))
	}
	// PATCH landed: the guest content-selected the Deployment and added the
	// annotation, spliced into the existing annotations block.
	if !strings.Contains(string(deploy), `aggrega.ai/pipeline-patched: "true"`) {
		t.Errorf("Deployment missing pipeline PATCH annotation; got:\n%s", deploy)
	}
	// The PATCH is byte-faithful: the surrounding rendered annotations stay
	// intact (only the targeted document body re-serialised).
	if !strings.Contains(string(deploy), "aggrega.ai/recipe-uri: server-recipe:1.0.0") {
		t.Errorf("Deployment lost a pre-existing annotation after PATCH; got:\n%s", deploy)
	}

	// EMIT landed: an own-scope ConfigMap surfaced as a new artifact.
	cm, ok := result.Artifacts["dist/main/helm/pipeline-configmap.yaml"]
	if !ok {
		t.Fatalf("expected emitted ConfigMap artifact; got keys %v", artifactKeys(result.Artifacts))
	}
	if !strings.Contains(string(cm), "name: pipeline-emitted") {
		t.Errorf("emitted ConfigMap missing expected content; got:\n%s", cm)
	}

	// Content selection: the Service was NOT a Deployment, so it carries no
	// pipeline annotation.
	svc, ok := result.Artifacts["dist/main/helm/api-svc-service.yaml"]
	if !ok {
		t.Fatalf("expected rendered Service artifact; got keys %v", artifactKeys(result.Artifacts))
	}
	if strings.Contains(string(svc), "aggrega.ai/pipeline-patched") {
		t.Errorf("Service should not carry the pipeline PATCH annotation; got:\n%s", svc)
	}
}

// TestPipelineRender_Deterministic compiles the pipeline-render fixture
// repeatedly and asserts byte-identical output across the runs — the pipeline
// executor (segmenter split, keyed-set construction, apply-back, EMIT ordering)
// is order-preserving end-to-end (the deterministic-output contract). This is
// the helm provider's always-on double-run guard.
func TestPipelineRender_Deterministic(t *testing.T) {
	in := pipelineRenderFixtureInput(t)
	t.Parallel()
	pipelinetest.AssertDeterministic(t, in)
}

// artifactKeys returns the sorted artifact keys for diagnostic messages.
func artifactKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
