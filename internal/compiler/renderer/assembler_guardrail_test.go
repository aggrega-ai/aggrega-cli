// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderOnlyAssemblerWrapper mimics the Wasm-provider render adapter for an
// assembler provider whose plugin advertises only per-node rendering: it drives
// render itself, does NOT implement BoundaryAssembler, and exposes the wrapped
// in-tree assembler engine only through Unwrap. Boundary assembly is functional
// dispatch asserted on the dispatched engine, not a host-side capability that
// unwraps — so this wrapper genuinely cannot assemble.
type renderOnlyAssemblerWrapper struct {
	wrapped renderer.Engine
}

func (renderOnlyAssemblerWrapper) Render(_ context.Context, _ map[string]any, _ renderer.NodeRenderContext) (map[string][]byte, error) {
	return nil, nil
}

func (renderOnlyAssemblerWrapper) Capabilities() renderer.ProviderCapabilities {
	return renderer.ProviderCapabilities{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (w renderOnlyAssemblerWrapper) Unwrap() renderer.Engine { return w.wrapped }

// TestRenderAll_AssemblerProviderMissingAssembleBoundary_Halts proves the Loud
// Guardrail: when a provider whose in-tree engine assembles a dispatch group is
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
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Provider: "compose", Artifacts: artifacts},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	wrapper := renderOnlyAssemblerWrapper{wrapped: &captureComposerEngine{}}

	// The guardrail is only meaningful if the dispatched engine genuinely
	// cannot assemble while its wrapped engine can.
	if _, ok := renderer.Engine(wrapper).(renderer.BoundaryAssembler); ok {
		t.Fatal("wrapper unexpectedly implements BoundaryAssembler directly; guardrail scenario not exercised")
	}
	if _, ok := wrapper.Unwrap().(renderer.BoundaryAssembler); !ok {
		t.Fatal("wrapped engine must implement BoundaryAssembler for this scenario")
	}

	factory := func(string) (renderer.Engine, error) { return wrapper, nil }

	_, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err == nil {
		t.Fatal("RenderAll succeeded; expected a loud halt for a provider that cannot assemble its dispatch group")
	}
	if !errors.Is(err, renderer.ErrBoundaryAssemblerMissing) {
		t.Fatalf("RenderAll error = %v; want ErrBoundaryAssemblerMissing", err)
	}
}
