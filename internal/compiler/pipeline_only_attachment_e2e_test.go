// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// pipelineOnlyAttachmentInput names the pipeline-only-attachment fixture: a
// single helm-chart-sourced component (the host) with a component-scope
// FeatureRecipe attached that declares a pipeline and NO render input — no
// source, no valuesMapping, no workload, no artifacts. The attachment therefore
// generates no resource of its own, and the pipeline's /host PATCH into the
// host chart's values.yaml is the only trace it leaves in the rendered output.
//
// releasePath selects which Release the build compiles: the attached one, the
// one whose Aggregate declares no attachment, or the one whose Manifest binds a
// FeatureRecipe declaring neither a render input nor a pipeline.
//
// The step references a Plugin ENTITY URI; the entity's spec.plugin .wasm
// artifact is built on demand from the Rust PDK example and staged co-located
// beside its Plugin entity in the catalog so the build's entity-front resolves
// it. When the wasm toolchain is absent the staging build skips the calling test.
func pipelineOnlyAttachmentInput(t *testing.T, releasePath string) pipelinetest.Input {
	t.Helper()
	catalogPaths := stageColocatedCatalog(t, []string{
		"testdata/pipeline-only-attachment/catalog/base",
		"testdata/pipeline-only-attachment/catalog/recipes/helm-chart",
	}, map[string]string{
		"pipeline-step/host-patch": "frp-step-host-patch-rust.wasm",
	})
	return pipelinetest.Input{
		ReleasePath:  releasePath,
		CatalogPaths: catalogPaths,
	}
}

// injectedByPipeline is the key the attached FeatureRecipe's pipeline step sets
// on the host component's own rendered document. Nothing in the fixture's
// authored chart carries it, so its presence is attributable to the pipeline
// alone.
const injectedByPipeline = "INJECTED_BY_FEATURE: frp-step-host-patch"

// TestPipelineOnlyAttachment_PipelineRuns compiles the fixture and asserts the
// pipeline of an attachment that emits no resource of its own actually ran: the
// host component's own values.yaml carries the key the step set, and the
// attachment contributed no file of its own (it declares no render input, so
// there is nothing for it to emit).
//
// The exit code proves nothing on its own here — a build that skipped the
// attachment entirely also succeeds — which is why the assertion is on the
// injected key in the host's rendered document rather than on the build result.
func TestPipelineOnlyAttachment_PipelineRuns(t *testing.T) {
	in := pipelineOnlyAttachmentInput(t, "testdata/pipeline-only-attachment/release.yaml")
	t.Parallel()

	result := pipelinetest.Build(t, in)

	values := string(findArtifactBySuffix(t, result.Artifacts, "charts/api/values.yaml"))
	if !strings.Contains(values, injectedByPipeline) {
		t.Errorf("host values.yaml missing the key the attachment's pipeline sets (%q); got:\n%s", injectedByPipeline, values)
	}
	// Byte-faithful node-span surgery: the host's authored comment and its
	// pre-existing sibling keys survive the patch verbatim.
	if !strings.Contains(values, "EXISTING_VAR: 'keep-me'") {
		t.Errorf("host values.yaml lost a pre-existing env var (or its scalar style); got:\n%s", values)
	}
	if !strings.Contains(values, "image: api-server:latest") {
		t.Errorf("host values.yaml lost a sibling top-level key; got:\n%s", values)
	}

	// The attachment emits no file of its own: it declares no render input, so
	// its participation is its pipeline and nothing else.
	for k := range result.Artifacts {
		// Host-agnostic on purpose: an attachment's output is placed under its
		// host as well as its instance, and naming one host here would let this
		// assertion pass by matching nothing the day the corpus renames it.
		if strings.Contains(k, "features/") && strings.Contains(k, "/guardrail/") {
			t.Errorf("attachment emitted a file of its own at %q; it declares no render input", k)
		}
	}
}

// TestPipelineOnlyAttachment_KeyAbsentWithoutTheAttachment compiles the same
// catalog against the Aggregate that declares no attachment and asserts the
// injected key is absent. It is the negative half of the assertion above: it is
// what makes the key's presence attributable to the attachment rather than to
// anything the host component authored.
func TestPipelineOnlyAttachment_KeyAbsentWithoutTheAttachment(t *testing.T) {
	in := pipelineOnlyAttachmentInput(t, "testdata/pipeline-only-attachment/release-unattached.yaml")
	t.Parallel()

	result := pipelinetest.Build(t, in)

	values := string(findArtifactBySuffix(t, result.Artifacts, "charts/api/values.yaml"))
	if strings.Contains(values, injectedByPipeline) {
		t.Errorf("host values.yaml carries %q with no attachment declared; got:\n%s", injectedByPipeline, values)
	}
	if !strings.Contains(values, "EXISTING_VAR: 'keep-me'") {
		t.Errorf("host values.yaml is not the authored chart; got:\n%s", values)
	}
}

// TestPipelineOnlyAttachment_InertAttachmentContributesNothing binds the same
// attachment to a FeatureRecipe declaring neither a render input nor a pipeline
// — a pure-wiring binding, which is a valid shape. Such an attachment has no
// work in feature rendering, so it must stay out of it: the build succeeds, the
// host document is the authored chart, and no file is emitted under the
// attachment's prefix.
//
// It guards the admission test from being loosened into "every attachment
// participates": that loosening passes every assertion above and fails here.
func TestPipelineOnlyAttachment_InertAttachmentContributesNothing(t *testing.T) {
	in := pipelineOnlyAttachmentInput(t, "testdata/pipeline-only-attachment/release-inert.yaml")
	t.Parallel()

	result := pipelinetest.Build(t, in)

	values := string(findArtifactBySuffix(t, result.Artifacts, "charts/api/values.yaml"))
	if strings.Contains(values, injectedByPipeline) {
		t.Errorf("host values.yaml was patched by an attachment declaring no pipeline; got:\n%s", values)
	}
	if !strings.Contains(values, "EXISTING_VAR: 'keep-me'") {
		t.Errorf("host values.yaml is not the authored chart; got:\n%s", values)
	}
	for k := range result.Artifacts {
		// Host-agnostic on purpose: an attachment's output is placed under its
		// host as well as its instance, and naming one host here would let this
		// assertion pass by matching nothing the day the corpus renames it.
		if strings.Contains(k, "features/") && strings.Contains(k, "/guardrail/") {
			t.Errorf("an attachment declaring nothing emitted a file at %q", k)
		}
	}
}

// TestPipelineOnlyAttachment_Deterministic compiles the fixture repeatedly and
// asserts byte-identical output across runs — an attachment admitted for its
// pipeline alone runs through the same order-preserving /host path a
// resource-generating one does.
func TestPipelineOnlyAttachment_Deterministic(t *testing.T) {
	in := pipelineOnlyAttachmentInput(t, "testdata/pipeline-only-attachment/release.yaml")
	t.Parallel()
	pipelinetest.AssertDeterministic(t, in)
}
