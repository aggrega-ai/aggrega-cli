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
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rule refusing a declared inject mechanism no attachment mapping names is
// declared over what the render records at Phase A, so the render entry point is
// where it is invoked and this file drives it from there.
//
// # Why the render entry point rather than the phase
//
// The unit rows beside this file drive the rule through the framework's traversal
// over a fixture, which proves the predicate and the attribution. What they
// cannot prove is that a build reaches it: the recording, the presentation and
// the axes are three separate things the render has to have in hand at the seam,
// and a build missing any of them would render the unmapped mechanism silently.

// injectMappingInput builds a render whose "api" node declares one custom inject
// mechanism and carries attachments whose FeatureRecipes each produce the given
// injectMapping output.
//
// mappings is keyed by attachment instance name, in the cascade order the slice
// gives. Every attachment binds a Feature type of its own so its FeatureRecipe
// can carry a mapping of its own, and each FeatureRecipe is named apart from the
// instance that binds it — the two are independently authored, and a fixture
// letting them coincide cannot see a finding that names the wrong one.
func injectMappingInput(
	order []string, mappings map[string]string,
) (renderer.RendererInput, renderer.RecipeLookup) {
	input := testInput()

	node, _ := input.Graph.Spec.Nodes.Get("api")
	raw := &types.InjectBlock{}
	raw.Additional.Set("ssm_param", map[string]any{"DB_PASSWORD": "root_password"})
	node.SelfInject = raw
	input.Graph.Spec.Nodes.Set("api", node)

	inner := types.OrderedMap[types.IRFeatureEntry]{}
	for _, instance := range order {
		vals := types.OrderedMap[any]{}
		vals.Set("generated", "s3cr3t")
		inner.Set(instance, types.IRFeatureEntry{Type: "core/" + instance, Values: vals})
	}
	outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	outer.Set("api", inner)
	input.Graph.Spec.FeatureValuesMapping = outer
	input.FeatureOrder = map[string][]string{"api": order}

	input.GraphView = injectRenderGraphFacts{}
	input.GraphCoordinates = injectRenderCoordinates{
		nodes: []constraint.NodeInstance{{
			ID: "api", AuthoredName: "api", SourceFile: "aggregate.yaml",
		}},
		attachments: map[string][]string{"api": order},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			artifacts := types.OrderedMap[types.ArtifactEntry]{}
			artifacts.Set("extra.yaml", types.ArtifactEntry{Inline: "services: {}\n"})
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability: "compose",
					Implements: "core/compute/api-server",
					Artifacts:  artifacts,
				},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			instance, ok := strings.CutPrefix(featureType, "core/")
			if !ok {
				return nil, false
			}
			mapping, known := mappings[instance]
			if !known {
				return nil, false
			}
			provides := types.OrderedMap[string]{}
			provides.Set("root_password", "{{ .values.generated }}")
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: instance + "-recipe"},
				Spec: types.FeatureRecipeSpec{
					Capability:    "compose",
					Implements:    featureType,
					Provides:      provides,
					InjectMapping: mapping,
				},
			}, true
		},
	}
	return input, lookup
}

// TestRenderAll_UnmappedInjectMechanismHalts is the REFUSAL row through the
// render entry point: the component declares ssm_param and the attachment's
// mapping produces only an env key, so a declared delivery reaches nothing.
func TestRenderAll_UnmappedInjectMechanismHalts(t *testing.T) {
	t.Parallel()
	input, lookup := injectMappingInput(
		[]string{"db-creds"},
		map[string]string{"db-creds": `{"env": {"OTHER_VAR": "value"}}`},
	)
	factory := func(string) (renderer.Provider, error) { return &captureComposerEngine{}, nil }

	_, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err == nil {
		t.Fatal("RenderAll succeeded; expected a halt for a declared mechanism the mapping does not name")
	}
	if !errors.Is(err, renderer.ErrUnmappedCustomMechanism()) {
		t.Fatalf("RenderAll error = %v; want ErrUnmappedCustomMechanism", err)
	}
	if !strings.Contains(err.Error(), `recipe "db-creds-recipe"`) {
		t.Errorf("the halt must name the FeatureRecipe the key is added to; got %v", err)
	}
	if !strings.Contains(err.Error(), ".spec.components.api.features.db-creds") {
		t.Errorf("the halt must be positioned at the attachment's own entry; got %v", err)
	}
}

// TestRenderAll_MappedInjectMechanismRenders is the SILENT row on the same
// fixture: the mapping names the declared mechanism, so the build renders.
//
// It is the row a refusal-only corpus cannot see. A rule that refused every
// attachment would satisfy the row above and fail here.
func TestRenderAll_MappedInjectMechanismRenders(t *testing.T) {
	t.Parallel()
	input, lookup := injectMappingInput(
		[]string{"db-creds"},
		map[string]string{"db-creds": `{"ssm_param": {"DB_PASSWORD": "/db/password"}}`},
	)
	factory := func(string) (renderer.Provider, error) { return &captureComposerEngine{}, nil }

	if _, err := renderer.RenderAll(context.Background(), input, lookup, factory); err != nil {
		t.Fatalf("RenderAll must render a mapping naming the declared mechanism; got %v", err)
	}
}

// TestRenderAll_FlatInjectMappingRenders is the second SILENT row, and it is the
// one that keeps a legitimate idiom legal: a FeatureRecipe writing a flat
// delivery dictionary names no mechanism at all, so it makes no claim about the
// component's declarations and the build renders.
//
// Without it, a reading that treated "named nothing" as "named none" would refuse
// every env-only FeatureRecipe on a component that declares any injection block,
// and both rows above would stay green.
func TestRenderAll_FlatInjectMappingRenders(t *testing.T) {
	t.Parallel()
	input, lookup := injectMappingInput(
		[]string{"db-creds"},
		map[string]string{"db-creds": `{"OTHER_VAR": "value"}`},
	)
	factory := func(string) (renderer.Provider, error) { return &captureComposerEngine{}, nil }

	if _, err := renderer.RenderAll(context.Background(), input, lookup, factory); err != nil {
		t.Fatalf("RenderAll must render a flat injectMapping output; got %v", err)
	}
}

// TestRenderAll_EveryAttachmentLeavingItUnmappedIsReported is the row that pins
// what gathering the reading after the phase buys: two attachments each leave the
// declared mechanism unmapped and BOTH are reported, where a reading inside the
// parse stopped at the first.
func TestRenderAll_EveryAttachmentLeavingItUnmappedIsReported(t *testing.T) {
	t.Parallel()
	input, lookup := injectMappingInput(
		[]string{"first-creds", "second-creds"},
		map[string]string{
			"first-creds":  `{"env": {"A": "1"}}`,
			"second-creds": `{"env": {"B": "2"}}`,
		},
	)
	factory := func(string) (renderer.Provider, error) { return &captureComposerEngine{}, nil }

	_, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err == nil {
		t.Fatal("RenderAll succeeded; expected a halt naming both attachments")
	}
	if !errors.Is(err, renderer.ErrUnmappedCustomMechanism()) {
		t.Fatalf("RenderAll error = %v; want ErrUnmappedCustomMechanism", err)
	}
	text := renderedHaltText(err)
	for _, want := range []string{"first-creds-recipe", "second-creds-recipe"} {
		if !strings.Contains(text, want) {
			t.Errorf("the halt must name %s; got %s", want, text)
		}
	}
}

// TestRenderAll_ALaterBrokenMappingIsReportedFirst pins the ONE ordering
// difference gathering the reading after the phase makes, so the difference is
// measured rather than latent.
//
// The input carries two faults: the first attachment's mapping leaves the
// declared mechanism unmapped, and the second attachment's mapping template does
// not evaluate. Every attachment's mapping is now evaluated before the unmapped
// question is asked, so the template fault halts the build first. Both are
// catalog faults in the same node's attachment set and the build stops either
// way; what changes is which one an author is shown first.
func TestRenderAll_ALaterBrokenMappingIsReportedFirst(t *testing.T) {
	t.Parallel()
	input, lookup := injectMappingInput(
		[]string{"first-creds", "broken-creds"},
		map[string]string{
			"first-creds":  `{"env": {"A": "1"}}`,
			"broken-creds": `{{ .nope.missing }}`,
		},
	)
	factory := func(string) (renderer.Provider, error) { return &captureComposerEngine{}, nil }

	_, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err == nil {
		t.Fatal("RenderAll succeeded; expected the broken mapping template to halt")
	}
	if errors.Is(err, renderer.ErrUnmappedCustomMechanism()) {
		t.Fatalf("the template fault halts before the unmapped question is asked; got %v", err)
	}
}

// renderedHaltText flattens a halt's whole cause chain into one string, so an
// assertion about several joined findings does not read only the envelope.
func renderedHaltText(err error) string {
	var b strings.Builder
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		b.WriteString(e.Error())
		b.WriteString("\n")
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, sub := range joined.Unwrap() {
				walk(sub)
			}
			return
		}
		walk(errors.Unwrap(e))
	}
	walk(err)
	return b.String()
}
