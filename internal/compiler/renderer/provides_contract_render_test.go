// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// The rule refusing a Recipe short of its Component's output contract is declared
// over what the render resolved for each provides key, so the render entry point
// is where it is invoked and this file drives it from there.
//
// # Why the render entry point rather than the predicate
//
// The unit rows beside this file drive the rule through the framework's traversal
// over a fixture, which proves the predicate and the axis. What they cannot prove
// is that a build reaches it: the retained contract, the presentation chain and
// the resolved bag are three separate things the render has to hold at the same
// seam, and a build missing any of them would render the shortfall silently. So
// these rows call the render entry point and assert on what comes back.

// providesRenderGraphFacts stands in for the presentation the graph finalisation
// produces, which the render embeds beneath the presentations built over it.
//
// It is supplied because the presentation chain is cumulative and refuses to
// assemble with a hole in it: the render publishes no mid-render presentation
// unless the one it extends exists, and this rule reads a mid-render fact. Most
// fixtures in this package predate the declared render rules and omit it, which is
// exactly how a migrated halt becomes a silent pass — so it is supplied here
// rather than left to be discovered.
//
// Unlike the sibling stub beside it this one answers one accessor, because this
// rule reads one fact from below its own stage: the component type a flattened
// node carries, which its diagnostic names. Every other accessor panics on the nil
// embedding rather than answering a zero value.
type providesRenderGraphFacts struct {
	constraint.GraphView

	componentTypes map[string]string
}

func (g providesRenderGraphFacts) FlattenedNodeComponentType(node string) (string, bool) {
	componentType, held := g.componentTypes[node]
	return componentType, held
}

// providesContractInput builds a render whose "api" node binds a Component
// declaring a two-key output contract, with the Recipe's own provides block
// supplied by the caller so a row can under-deliver or fulfil it.
//
// recipeProvides names the keys the Recipe publishes. The Component declares host
// and service_port either way, so omitting one of them is the shortfall and naming
// both is the control that has to keep rendering.
func providesContractInput(recipeProvides ...string) (renderer.RendererInput, renderer.RecipeLookup) {
	input := testInput()

	// The catalog reading the retained contract is built from: a Component whose
	// schema declares the two-key output contract. It is authored as the document
	// text a Component carries rather than as a struct, so the contract the render
	// reads is resolved by the same reader the build uses.
	var schema yaml.Node
	if err := yaml.Unmarshal([]byte(`values:
  port: { type: integer }
provides:
  host: { type: string }
  service_port: { type: string }
`), &schema); err != nil {
		panic("provides-contract fixture schema: " + err.Error())
	}
	input.CatalogLookup.Component = func(ref string) (types.Component, bool) {
		if ref != "core/compute/api-server" {
			return types.Component{}, false
		}
		return types.Component{
			Metadata: types.ComponentMetadata{Name: "core/compute/api-server"},
			Spec:     types.ComponentSpec{Schema: schema},
		}, true
	}

	input.GraphView = providesRenderGraphFacts{
		componentTypes: map[string]string{"api": "core/compute/api-server"},
	}

	base := testRecipeLookup()
	lookup := renderer.RecipeLookup{
		Recipe: func(node, componentType string) (*types.Recipe, bool) {
			recipe, found := base.Recipe(node, componentType)
			if !found || recipe == nil {
				return recipe, found
			}
			clone := *recipe
			published := types.OrderedMap[string]{}
			for _, key := range recipeProvides {
				published.Set(key, `{{ .context.instance_id }}-`+key)
			}
			clone.Spec.Provides = published
			return &clone, true
		},
		FeatureRecipe: base.FeatureRecipe,
	}
	return input, lookup
}

// TestRenderAll_RecipeShortOfItsComponentsContractHalts is the REFUSAL row
// through the render entry point: the Component declares host and service_port
// and the bound Recipe publishes only host.
//
// This is the row that proves a BUILD reaches the rule, which no unit row can.
func TestRenderAll_RecipeShortOfItsComponentsContractHalts(t *testing.T) {
	t.Parallel()
	input, lookup := providesContractInput("host")

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err == nil {
		t.Fatal("RenderAll succeeded; expected a halt for a Recipe short of its Component's output contract")
	}
	if !errors.Is(err, renderer.ErrProvidesContractUnfulfilled()) {
		t.Fatalf("RenderAll error = %v; want ErrProvidesContractUnfulfilled", err)
	}

	// The finding is positioned at the Recipe's own provides entry, which is the
	// document and the key an author edits. Read structurally rather than by
	// substring, so the assertion cannot pass on an envelope.
	var ce *cerrors.CompilerError
	if !errors.As(err, &ce) {
		t.Fatalf("the halt is not a compiler diagnostic: %v", err)
	}
	if ce.Source.File != "api-recipe" {
		t.Errorf("source file = %q, want the Recipe the shortfall is in", ce.Source.File)
	}
	if want := ".spec.provides.service_port"; ce.Source.Locator != want {
		t.Errorf("locator = %q, want %q", ce.Source.Locator, want)
	}
	if !strings.Contains(ce.Message, `"service_port"`) {
		t.Errorf("the diagnostic must name the missing key; got %q", ce.Message)
	}
	if !strings.Contains(ce.Message, "core/compute/api-server") {
		t.Errorf("the diagnostic must name the component type whose contract declares it; got %q", ce.Message)
	}
}

// TestRenderAll_RecipeFulfillingItsComponentsContractRenders is the SILENT row on
// the same fixture: the Recipe publishes every key the Component declares.
//
// It is the row a refusal-only corpus cannot see. A rule that refused every
// declared key would satisfy the row above and fail here.
func TestRenderAll_RecipeFulfillingItsComponentsContractRenders(t *testing.T) {
	t.Parallel()
	input, lookup := providesContractInput("host", "service_port")

	if _, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t)); err != nil {
		t.Fatalf("RenderAll must render a Recipe that fulfils its Component's contract; got %v", err)
	}
}

// TestRenderAll_RecipePublishingBeyondTheContractRenders is the second SILENT row,
// and it pins the direction of the contract rather than only its satisfaction.
//
// The contract is subset-required and superset-permitted, so a Recipe publishing
// a key its Component never declared has still fulfilled it. A predicate that
// compared the two sets for equality would refuse this and would pass every other
// row in this file.
func TestRenderAll_RecipePublishingBeyondTheContractRenders(t *testing.T) {
	t.Parallel()
	input, lookup := providesContractInput("host", "service_port", "internal_handle")

	if _, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t)); err != nil {
		t.Fatalf("RenderAll must tolerate a Recipe publishing beyond its Component's contract; got %v", err)
	}
}
