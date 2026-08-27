// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderOnlyAssemblerWrapper mimics the Wasm-provider render adapter for an
// assembler provider whose plugin advertises only per-node rendering: it drives
// render itself, does NOT implement BoundaryAssembler, and forwards the wrapped
// provider's boundary-assembly self-report. Boundary assembly is functional
// dispatch asserted on the dispatched engine — so this wrapper reports it
// assembles yet genuinely cannot, which is exactly the mismatch the guardrail
// catches.
type renderOnlyAssemblerWrapper struct {
	wrapped renderer.Provider
}

func (renderOnlyAssemblerWrapper) Render(_ context.Context, _ map[string]any, _ renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return nil, nil
}

func (renderOnlyAssemblerWrapper) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (w renderOnlyAssemblerWrapper) BoundaryAssembly() string {
	if r, ok := w.wrapped.(renderer.BoundaryAssemblyReporter); ok {
		return r.BoundaryAssembly()
	}
	return ""
}

// ArtifactTemplateDelimiters and EmitArtifacts satisfy renderer.ArtifactsEmitter,
// mirroring the Wasm adapter: the wrapper places each pre-evaluated entry, marking
// Intermediate from its forwarded boundary-assembly self-report. The merge
// self-report makes the placed artifacts intermediate, so the dispatch group needs
// an assembler the wrapper genuinely lacks — the mismatch the guardrail catches.
func (renderOnlyAssemblerWrapper) ArtifactTemplateDelimiters() (string, string) { return "{{", "}}" }

func (w renderOnlyAssemblerWrapper) EmitArtifacts(_ context.Context, proj renderer.ArtifactsProjection, _ renderer.NodeRenderContext) ([]renderer.EmittedFile, error) {
	intermediate := renderer.RenderIsIntermediate(w)
	files := make([]renderer.EmittedFile, 0, len(proj.Entries))
	for _, ent := range proj.Entries {
		origin := renderer.EmitOriginArtifactsInline
		if ent.Origin == "import" {
			origin = renderer.EmitOriginArtifactsImport
		}
		files = append(files, renderer.EmittedFile{Name: ent.Name, Origin: origin, Body: ent.Content, Intermediate: intermediate})
	}
	return files, nil
}

// guardrailGraphFacts stands in for the presentation the graph finalisation
// produces, which the render embeds beneath the presentations built over it.
//
// The rule under test reads two facts and neither is a graph-stage one — both are
// answered from what the grouping recorded about each group's elected engine — so
// this is never read. It carries no value behind the interface deliberately: a read
// panics here rather than answering a zero value, so a rule that later starts
// reading a graph-stage fact fails loudly instead of being quietly handed nothing.
//
// It is supplied because the presentation chain is cumulative and refuses to
// assemble with a hole in it: the render publishes no mid-render presentation
// unless the one it extends exists. Production always supplies this, from the one
// call path that renders; a fixture omitting it would leave the rule invoked zero
// times for a reason that has nothing to do with what is being proved — and a rule
// invoked zero times reports exactly what a rule that ran and approved reports.
type guardrailGraphFacts struct{ constraint.GraphView }

// TestRenderAll_AssemblerProviderMissingAssembleBoundary_Halts proves the Loud
// Guardrail: when a provider that self-reports assembling a dispatch group is
// served by an engine that can render per node but cannot assemble (a rendering
// plugin advertising per-node rendering without the matching boundary-assembly
// export), the build halts with ErrBoundaryAssemblerMissing rather than silently
// skipping assembly and emitting unmerged per-node fragments.
func TestRenderAll_AssemblerProviderMissingAssembleBoundary_Halts(t *testing.T) {
	t.Parallel()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "default"})

	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("extra-services.yaml", types.ArtifactEntry{Inline: "services:\n  sidecar:\n    image: busybox:1.36\n"})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "compose-app"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
		GraphView:     guardrailGraphFacts{},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "compose", Artifacts: artifacts},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	wrapper := renderOnlyAssemblerWrapper{wrapped: &captureComposerEngine{}}

	// The guardrail is only meaningful if the dispatched engine genuinely
	// cannot assemble while reporting — via its forwarded host-capability holder —
	// that the provider it stands in for does.
	if _, ok := renderer.Provider(wrapper).(renderer.BoundaryAssembler); ok {
		t.Fatal("wrapper unexpectedly implements BoundaryAssembler directly; guardrail scenario not exercised")
	}
	if !renderer.AssemblesBoundary(wrapper) {
		t.Fatal("wrapper must report it assembles via its forwarded self-report for this scenario")
	}

	factory := func(string) (renderer.Provider, error) { return wrapper, nil }

	_, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err == nil {
		t.Fatal("RenderAll succeeded; expected a loud halt for a provider that cannot assemble its dispatch group")
	}
	if !errors.Is(err, renderer.ErrBoundaryAssemblerMissing()) {
		t.Fatalf("RenderAll error = %v; want ErrBoundaryAssemblerMissing", err)
	}
}
