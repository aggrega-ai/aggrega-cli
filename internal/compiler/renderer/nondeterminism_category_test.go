// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

	factory := func(string) (renderer.Provider, error) { return nondeterministicEngine{}, nil }

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
			if ce.Category() != cerrors.TranslationError {
				t.Errorf("Category = %q; want %q — a provider that renders non-deterministically is a provider-side fault on every crossing",
					ce.Category(), cerrors.TranslationError)
			}
		})
	}
}

// artifactsUnawareEngine admits the artifacts input kind at the capability gate
// and then implements no artifacts emission. The refusal that produces is raised
// before any provider crossing runs, and it is a catalog fault: the entity
// declares artifacts: against a provider that cannot place them.
//
// It exists so the artifacts pass is exercised by more than one fault. A
// provider-side fault and a catalog-side fault reach the user through that one
// pass, and they must not report the same category — so a change that moved both
// would otherwise look identical to a change that moved the intended one.
type artifactsUnawareEngine struct{}

func (artifactsUnawareEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (artifactsUnawareEngine) Render(context.Context, map[string]any, renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return nil, nil
}

// renderWithFeatureRecipe drives a one-node render whose host Recipe declares no
// input kind and whose single attachment carries the given FeatureRecipe spec. It
// returns the error as a caller of the renderer receives it.
//
// The host declaring nothing is what makes each case attributable: with no
// source:, workload: or artifacts: on the host Recipe, no host crossing reaches
// the provider at all, so every provider fault below belongs to the attachment's
// FeatureRecipe render.
func renderWithFeatureRecipe(t *testing.T, frSpec types.FeatureRecipeSpec, engine renderer.Provider) (*renderer.RenderResult, error) {
	t.Helper()

	const nodeID = "api"
	const instance = "probe"

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(nodeID, types.IRNode{Component: "core/compute/api-server", BoundaryPath: "default"})

	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set(instance, types.IRFeatureEntry{Type: "example/probe"})
	perNode := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	perNode.Set(nodeID, attachments)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "nondeterminism-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				FeatureValuesMapping: perNode,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  ir.FeatureOrder{nodeID: {instance}},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "compose"},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) {
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "probe-fr", Version: "1.0.0"},
				Spec:     frSpec,
			}, true
		},
	}

	factory := func(string) (renderer.Provider, error) { return engine, nil }

	return renderer.RenderAll(context.Background(), input, lookup, factory)
}

// TestFeatureRecipeRender_CategoryFollowsTheCondition pins that an error escaping
// the FeatureRecipe render path carries a category, and that the category is the
// one the failing condition calls for rather than the one the crossing suggests.
//
// The FeatureRecipe render path reuses the component render path's passes, so the
// same three provider crossings exist on it — source render, artifacts emission
// and workload synthesis — plus the catalog-side faults the artifacts pass raises
// before it reaches the provider. A provider that produced different output from
// identical input is a provider-side fault wherever it surfaces; an entity that
// declares artifacts against a provider that cannot place them, or whose artifact
// body is not a valid template, is a catalog fault. Both reach the user through
// the artifacts pass, which is why they are exercised together: it is the one pass
// where the two answers meet, so a wrap applied there has to keep them apart.
//
// Every case drives the real render entry point and reads the category off the
// escaping error. Reading the wrapping sites instead would assert what the code
// says rather than what the path produces, which is the same class of claim this
// pins against.
func TestFeatureRecipeRender_CategoryFollowsTheCondition(t *testing.T) {
	t.Parallel()

	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("extra.yaml", types.ArtifactEntry{Inline: "key: value\n"})

	unparseable := types.OrderedMap[types.ArtifactEntry]{}
	unparseable.Set("extra.yaml", types.ArtifactEntry{Inline: "{{ .never_closed"})

	cases := []struct {
		name string
		spec types.FeatureRecipeSpec
		// engine selects the fault: the non-determinism halt on every crossing,
		// or a provider that cannot place artifacts at all.
		engine renderer.Provider
		// nondeterministic reports whether the case's fault is the provider
		// halt, which is what proves the provider crossing was actually reached.
		nondeterministic bool
		// names is a fragment of the fault's own diagnostic, read off the cause
		// chain rather than the outermost frame. For a case whose fault carries
		// no sentinel it is the control that the intended condition — and not
		// some earlier one — is what halted; for every case it pins that
		// assigning a category did not cost the specific diagnostic underneath
		// it, which is the frame that tells the author what to fix.
		names string
		want  cerrors.Category
	}{
		{
			name:             "source render, provider fault",
			spec:             types.FeatureRecipeSpec{Capability: "compose", Source: "./app"},
			engine:           nondeterministicEngine{},
			nondeterministic: true,
			names:            "provider render is not deterministic",
			want:             cerrors.TranslationError,
		},
		{
			name:             "artifacts emission, provider fault",
			spec:             types.FeatureRecipeSpec{Capability: "compose", Artifacts: artifacts},
			engine:           nondeterministicEngine{},
			nondeterministic: true,
			names:            "provider render is not deterministic",
			want:             cerrors.TranslationError,
		},
		{
			name: "workload synthesis, provider fault",
			spec: types.FeatureRecipeSpec{
				Capability: "compose",
				Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: api:latest
`},
			},
			engine:           nondeterministicEngine{},
			nondeterministic: true,
			names:            "provider render is not deterministic",
			want:             cerrors.TranslationError,
		},
		{
			name:   "artifacts declared against a provider that places none",
			spec:   types.FeatureRecipeSpec{Capability: "compose", Artifacts: artifacts},
			engine: artifactsUnawareEngine{},
			names:  "does not implement artifacts emission",
			want:   cerrors.CatalogError,
		},
		{
			name:   "artifact body is not a valid template",
			spec:   types.FeatureRecipeSpec{Capability: "compose", Artifacts: unparseable},
			engine: nondeterministicEngine{},
			names:  "extra.yaml",
			want:   cerrors.CatalogError,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := renderWithFeatureRecipe(t, tc.spec, tc.engine)
			if err == nil {
				t.Fatal("render returned nil; expected a halt from the feature recipe render")
			}

			if tc.nondeterministic {
				// Only the fake provider raises this sentinel, and it can only
				// raise it from a crossing, so its presence is what establishes
				// that the crossing under test ran.
				if !errors.Is(err, renderer.ErrRenderNondeterministic()) {
					t.Fatalf("expected the non-determinism halt in the chain; got %v", err)
				}
			}
			// Read the whole cause chain, not the outermost frame: a categorised
			// error renders one frame at a time, so a chain-blind assertion here
			// would stop noticing the diagnostic it exists to protect.
			if chain := cerrors.CauseChain(err); !strings.Contains(chain, tc.names) {
				t.Fatalf("cause chain does not name %q, so the specific diagnostic did not survive to the user; got %q", tc.names, chain)
			}

			var ce *cerrors.CompilerError
			if !errors.As(err, &ce) {
				t.Fatalf("an error escaping the feature recipe render carries no category; got %T: %v", err, err)
			}
			if ce.Category() != tc.want {
				t.Errorf("Category = %q; want %q", ce.Category(), tc.want)
			}
		})
	}
}
