// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// hostPatchComposeFixtureInput names the host-patch-compose fixture: a single
// compose-synthesized component (the host) with a component-scope FeatureRecipe
// attached whose pipeline runs a /host PATCH step. Compose is a
// BoundaryAssembler that merges per-node fragments into one docker-compose.yaml
// only at assembly, so the host's documents are pre-assembly fragments per-node
// — the /host PATCH defers to the post-assembly phase, where the merged
// document is re-segmented and the deferred step replayed host-scope-only. The
// step references a Plugin ENTITY URI; the entity's spec.plugin .wasm
// artifact is built on demand from the Rust PDK example and staged co-located
// beside its Plugin entity in the catalog so the build's entity-front resolves it.
// When the wasm toolchain is absent the staging build skips the calling test.
func hostPatchComposeFixtureInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	catalogPaths := stageColocatedCatalog(t, []string{
		"testdata/host-patch-compose/catalog/base",
		"testdata/host-patch-compose/catalog/recipes/compose",
	}, map[string]string{
		"pipeline-step/host-patch-compose": "frp-step-host-patch-compose-rust.wasm",
	})
	return pipelinetest.Input{
		ReleasePath:  "testdata/host-patch-compose/release.yaml",
		CatalogPaths: catalogPaths,
	}
}

// TestHostPatchCompose_PostAssemblyHostPatch compiles the host-patch-compose
// fixture and asserts the cross-entity PATCH ran in the post-assembly phase
// against the merged docker-compose.yaml: the host service's environment block
// carries the injected env var, the surgical node-span edit preserved the
// pre-existing env var and a sibling top-level key byte-for-byte, and the
// FeatureRecipe's own output rendered alongside. The host's provider is an
// assembler, so this exercises the deferral + post-assembly replay path — the
// per-node /host PATCH could not run because the merged document is not
// discrete until assembly.
func TestHostPatchCompose_PostAssemblyHostPatch(t *testing.T) {
	in := hostPatchComposeFixtureInput(t)
	t.Parallel()

	result := pipelinetest.Build(t, in)

	compose := findArtifactBySuffix(t, result.Artifacts, "compose/docker-compose.yaml")
	got := string(compose)

	// PATCH landed post-assembly: the guest content-selected the merged compose
	// document and added the env var under the host service's environment block.
	if !strings.Contains(got, "INJECTED_BY_FEATURE: frp-step-host-patch-compose") {
		t.Errorf("merged docker-compose.yaml missing the injected env var; got:\n%s", got)
	}
	// Byte-faithful node-span surgery: the pre-existing env var the recipe's
	// workload declared survives verbatim alongside the injected one.
	if !strings.Contains(got, "PORT:") {
		t.Errorf("merged docker-compose.yaml lost the pre-existing PORT env var after PATCH; got:\n%s", got)
	}
	// A sibling top-level key (the assembler's networks block) survives the
	// surgical edit confined to the service's environment span.
	if !strings.Contains(got, "networks:") {
		t.Errorf("merged docker-compose.yaml lost the top-level networks block after PATCH; got:\n%s", got)
	}
	// The service definition itself survives — the env var landed inside it, not
	// in place of it.
	if !strings.Contains(got, "image: api-server:latest") {
		t.Errorf("merged docker-compose.yaml lost the host service image after PATCH; got:\n%s", got)
	}

	// The FeatureRecipe's own resource-generating output rendered alongside the
	// host (proving the pipeline ran from within a resource-generating FR and
	// the FR's additive own output is unaffected by the deferred host PATCH).
	_ = findArtifactBySuffix(t, result.Artifacts, "features/guardrail/guardrail-config.yaml")
}

// TestHostPatchCompose_Deterministic compiles the host-patch-compose fixture
// repeatedly and asserts byte-identical output across runs — the deferral
// record ordering (host-node author order, then FeatureOrder index) and the
// post-assembly replay (re-segmentation, content-selection, apply-back) are
// order-preserving end-to-end. This is compose's always-on double-run guard for
// the post-assembly cross-entity case.
func TestHostPatchCompose_Deterministic(t *testing.T) {
	in := hostPatchComposeFixtureInput(t)
	t.Parallel()
	pipelinetest.AssertDeterministic(t, in)
}
