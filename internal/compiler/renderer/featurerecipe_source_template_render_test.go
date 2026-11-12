// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// attachmentSourceCapture records the source the render path handed this
// attachment's provider, so a test can assert what a component-scope attachment
// actually rendered against rather than what its entity declared.
type attachmentSourceCapture struct{ captured *any }

func (e attachmentSourceCapture) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	*e.captured = nodeCtx.Source
	return nil, nil
}

func (e attachmentSourceCapture) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// attachmentCaptureFactory routes the attachment's own capability to the capture
// provider and everything else — the host component's render — to the real
// engines, so the host still renders through the production path while the
// attachment's source is observable.
func attachmentCaptureFactory(t *testing.T, captured *any) renderer.EngineFactory {
	t.Helper()
	base := wasmEngineFactory(t)
	return func(capability string) (renderer.Provider, error) {
		if capability == "capture" {
			return attachmentSourceCapture{captured: captured}, nil
		}
		return base(capability)
	}
}

// monitoringSourceFR builds the component-scope attachment under test: a
// FeatureRecipe whose source: is whatever the caller passes, with a locals block a
// source template can read.
func monitoringSourceFR(source any) *types.FeatureRecipe {
	locals := types.OrderedMap[string]{}
	locals.Set("chart_dir", "./charts/mon")
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "mon"},
		Spec: types.FeatureRecipeSpec{
			Capability: "capture",
			Implements: "core/monitoring",
			Source:     source,
			Locals:     locals,
		},
	}
}

// monitoringSourceLookup is hostWorkloadRecipeLookup with the document lookup
// wired, which is what a build gets from the catalog and what an attachment's
// render needs in order to anchor what its source template produced.
func monitoringSourceLookup(fr *types.FeatureRecipe, document string) renderer.RecipeLookup {
	lookup := hostWorkloadRecipeLookup(fr)
	lookup.FeatureRecipeDocument = func(*types.FeatureRecipe) (string, bool) { return document, true }
	return lookup
}

// TestRenderAll_ComponentScopeAttachmentTemplatedSourceIsEvaluatedAndAnchored is
// the component-scope half of the capability, driven through the real render
// entry point: a Feature is attached to a component, its FeatureRecipe writes its
// source: as a template producing a relative path, and the provider must be handed
// the path that template produced, resolved against the document the
// FeatureRecipe was declared in.
//
// It is asserted at the provider rather than at the entity because the entity is
// where the template is authored and the provider is where the resolved source is
// consumed: an assertion on the entity would pass on a value nothing rendered
// against.
func TestRenderAll_ComponentScopeAttachmentTemplatedSourceIsEvaluatedAndAnchored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	document := filepath.Join(root, "catalog", "monitoring", "featurerecipe.yaml")

	var captured any
	fr := monitoringSourceFR("{{ .locals.chart_dir }}")
	_, err := renderer.RenderAll(context.Background(), monitoringFeatureInput(t),
		monitoringSourceLookup(fr, document), attachmentCaptureFactory(t, &captured))
	if err != nil {
		t.Fatalf("RenderAll (templated attachment source): %v", err)
	}

	want := filepath.Join(root, "catalog", "monitoring", "charts", "mon")
	if captured != want {
		t.Errorf("attachment templated source resolved to %#v; want %q — an unevaluated template reaches the provider as literal text and stats nothing", captured, want)
	}
}

// TestRenderAll_ComponentScopeAttachmentLiteralSourceIsNotAnchored is the
// negative control for the test above: a plain relative literal reaches the
// provider exactly as written, even though a document is wired. The load already
// anchored such a source against that same document, so anchoring it again here
// would join the directory twice.
func TestRenderAll_ComponentScopeAttachmentLiteralSourceIsNotAnchored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	document := filepath.Join(root, "catalog", "monitoring", "featurerecipe.yaml")

	var captured any
	fr := monitoringSourceFR("./charts/mon")
	_, err := renderer.RenderAll(context.Background(), monitoringFeatureInput(t),
		monitoringSourceLookup(fr, document), attachmentCaptureFactory(t, &captured))
	if err != nil {
		t.Fatalf("RenderAll (literal attachment source): %v", err)
	}

	if captured != "./charts/mon" {
		t.Errorf("literal relative attachment source reached the provider as %#v; want it untouched", captured)
	}
}
