// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// ownPatchComposeFixtureInput names the own-patch-compose fixture: a single
// compose-synthesized component whose Recipe declares an own-scope pipeline
// step. Compose is a BoundaryAssembler that merges per-node fragments into one
// docker-compose.yaml only at assembly, so the component's own documents are
// pre-assembly fragments per-node — the /own PATCH defers to the post-assembly
// phase, where the merged document is re-segmented and the deferred step
// replayed own-scope against it. This is the scope-symmetric mirror of the
// host-patch-compose fixture (which defers a component-scope FeatureRecipe's
// /host pipeline). The step references a Plugin ENTITY URI; the entity's
// spec.plugin .wasm artifact is built on demand from the Rust PDK example and
// staged co-located beside its Plugin entity in the catalog so the build's
// entity-front resolves it. When the wasm toolchain is absent the staging build
// skips the calling test.
func ownPatchComposeFixtureInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	catalogPaths := stageColocatedCatalog(t, []string{
		"testdata/own-patch-compose/catalog/base",
		"testdata/own-patch-compose/catalog/recipes/compose",
	}, map[string]string{
		"pipeline-step/own-patch-compose": "frp-step-own-patch-compose-rust.wasm",
	})
	return pipelinetest.Input{
		ReleasePath:  "testdata/own-patch-compose/release.yaml",
		CatalogPaths: catalogPaths,
	}
}

// TestOwnPatchCompose_PostAssemblyOwnPatch compiles the own-patch-compose
// fixture and asserts the own-scope PATCH ran in the post-assembly phase
// against the merged docker-compose.yaml: the component's own service carries
// the injected env var, the surgical node-span edit preserved the pre-existing
// env var and a sibling top-level key byte-for-byte. The component's provider is
// an assembler, so this exercises the own-scope deferral + post-assembly replay
// path — the per-node /own PATCH could not run because the merged document is
// not discrete until assembly.
func TestOwnPatchCompose_PostAssemblyOwnPatch(t *testing.T) {
	in := ownPatchComposeFixtureInput(t)
	t.Parallel()

	result := pipelinetest.Build(t, in)

	compose := findArtifactBySuffix(t, result.Artifacts, "compose/docker-compose.yaml")
	got := string(compose)

	// PATCH landed post-assembly: the guest content-selected the merged compose
	// document and added the env var under the component service's environment
	// block — at OWN scope (the Recipe patching its own output).
	if !strings.Contains(got, "INJECTED_BY_OWN_PIPELINE: frp-step-own-patch-compose") {
		t.Errorf("merged docker-compose.yaml missing the own-scope injected env var; got:\n%s", got)
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
		t.Errorf("merged docker-compose.yaml lost the service image after PATCH; got:\n%s", got)
	}
}

// TestOwnPatchCompose_Deterministic compiles the own-patch-compose fixture
// repeatedly and asserts byte-identical output across runs — the deferral
// record ordering and the post-assembly own-scope replay (re-segmentation,
// content-selection, apply-back) are order-preserving end-to-end. This is
// compose's always-on double-run guard for the post-assembly own-scope case.
func TestOwnPatchCompose_Deterministic(t *testing.T) {
	in := ownPatchComposeFixtureInput(t)
	t.Parallel()
	pipelinetest.AssertDeterministic(t, in)
}
