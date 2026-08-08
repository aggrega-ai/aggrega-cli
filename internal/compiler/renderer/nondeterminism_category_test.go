// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nondeterministicEngine fails every render crossing with the provider
// non-determinism halt, in the shape a provider engine raises it: the sentinel
// wrapped in a message naming the node. One engine serves all three crossings —
// source render, artifacts emission and workload synthesis — so each path is
// exercised against an identical inner error and the only thing that differs
// between the cases is the crossing the error surfaces through.
type nondeterministicEngine struct{}

// Contract is permissive so the central input-kind gate admits this fake for
// whichever input kind the case under test declares. The gate is not what these
// cases exercise; the category a non-determinism halt carries out of the render
// is.
func (nondeterministicEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (nondeterministicEngine) halt(nodeID string) error {
	return fmt.Errorf("node %q: two renders of identical input produced different output: %w",
		nodeID, renderer.ErrRenderNondeterministic())
}

func (e nondeterministicEngine) Render(_ context.Context, _ map[string]any, nodeCtx renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return nil, e.halt(nodeCtx.NodeID)
}

func (e nondeterministicEngine) SynthesizeWorkload(_ context.Context, _ *types.TemplateContext, _ *types.Workload, nodeCtx renderer.NodeRenderContext) ([]renderer.EmittedFile, error) {
	return nil, e.halt(nodeCtx.NodeID)
}

func (nondeterministicEngine) ArtifactTemplateDelimiters() (string, string) { return "{{", "}}" }

func (e nondeterministicEngine) EmitArtifacts(_ context.Context, _ renderer.ArtifactsProjection, nodeCtx renderer.NodeRenderContext) ([]renderer.EmittedFile, error) {
	return nil, e.halt(nodeCtx.NodeID)
}

// renderWithNondeterministicProvider drives a one-node render whose Recipe
// declares the given input kind and whose provider fails the crossing with the
// non-determinism halt. It returns the error as a caller of the renderer
// receives it.
func renderWithNondeterministicProvider(t *testing.T, spec types.RecipeSpec) error {
	t.Helper()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", BoundaryPath: "default"})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "nondeterminism-app"},
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
				Spec:     spec,
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	factory := func(string) (renderer.Engine, error) { return nondeterministicEngine{}, nil }

	_, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err == nil {
		t.Fatal("render of a non-deterministic provider returned nil; expected a halt")
	}
	return err
}

// TestRenderNondeterministic_OneCategoryAcrossEveryCrossing pins that a provider
// which renders non-deterministically reports the same category whichever render
// crossing surfaces it.
//
// A guest that produced different output from identical input is a provider-side
// fault on every path — the fault is in the provider, not in the catalog entry
// that happened to route through it — so the category is a property of the
// condition and not of the crossing it escaped through. Without this the same
// fault surfaced to the user under two different tags depending only on whether
// the Recipe declared source:, artifacts: or workload:, which makes the tag
// useless for deciding who has to fix it.
//
// Each case drives the crossing for real through the full render entry point
// rather than reading the wrapping sites: the three input kinds are what select
// the crossing, so declaring the kind is what proves the path was taken.
func TestRenderNondeterministic_OneCategoryAcrossEveryCrossing(t *testing.T) {
	t.Parallel()

	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("extra.yaml", types.ArtifactEntry{Inline: "key: value\n"})

	cases := []struct {
		name string
		spec types.RecipeSpec
	}{
		{
			name: "source render",
			spec: types.RecipeSpec{Capability: "compose", Source: "./app"},
		},
		{
			name: "artifacts emission",
			spec: types.RecipeSpec{Capability: "compose", Artifacts: artifacts},
		},
		{
			name: "workload synthesis",
			spec: types.RecipeSpec{
				Capability: "compose",
				Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: api:latest
`},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := renderWithNondeterministicProvider(t, tc.spec)

			if !errors.Is(err, renderer.ErrRenderNondeterministic()) {
				t.Fatalf("expected the non-determinism halt in the chain; got %v", err)
			}

			var ce *cerrors.CompilerError
			if !errors.As(err, &ce) {
				t.Fatalf("expected a categorised error; got %T: %v", err, err)
			}
			if ce.Category != cerrors.TranslationError {
				t.Errorf("Category = %q; want %q — a provider that renders non-deterministically is a provider-side fault on every crossing",
					ce.Category, cerrors.TranslationError)
			}
		})
	}
}
