// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rule refusing a duplicated attachment delivery key is declared over what
// the render records about each node's injection pipe, so the render entry point
// is where it is invoked and this file drives it from there.
//
// # Why the render entry point rather than the delivery function
//
// The unit rows beside this file drive the rule through the framework's traversal
// over a fixture, which proves the predicate and the attribution. What they cannot
// prove is that a build reaches it: the recording, the presentation and the axes
// are three separate things the render has to have in hand at the seam, and a
// build missing any of them would render the duplicate silently. So these rows
// call the render entry point and assert on what comes back.

// injectRenderGraphFacts stands in for the presentation the graph finalisation
// produces, which the render embeds beneath the presentations built over it.
//
// It is supplied because the presentation chain is cumulative and refuses to
// assemble with a hole in it: the render publishes no mid-render presentation
// unless the one it extends exists, and this rule reads a mid-render fact. Every
// fixture in this package that predates a declared render rule omits it, which is
// exactly how a migrated halt becomes a silent pass — so it is supplied here
// rather than left to be discovered.
//
// It carries no value behind the interface deliberately: the rule reads no
// graph-stage fact, so a read panics here rather than answering a zero value.
type injectRenderGraphFacts struct{ constraint.GraphView }

// injectRenderCoordinates stands in for the enumeration the finalised graph
// carries — the node axis and the per-node attachment axis.
//
// The node instance carries its authored provenance, because that is what a
// finding's position is assembled from: without it the rule would run and report
// a finding positioned nowhere. Production supplies the compilation's own
// enumeration, which carries the same three coordinates.
type injectRenderCoordinates struct {
	nodes       []constraint.NodeInstance
	attachments map[string][]string
}

func (c injectRenderCoordinates) Nodes() []constraint.NodeInstance { return c.nodes }

func (c injectRenderCoordinates) Attachments(node string) []string {
	return c.attachments[node]
}

// Edges answers empty: this fixture presents no connection, so the rule over a
// connection's two providers is invoked for nothing and these rows measure the
// delivery reading alone. The method is present because the coordinate set names
// every axis the stage's rules are keyed by, which is what stops a fixture from
// silently omitting one.
func (c injectRenderCoordinates) Edges() []constraint.EdgeInstance { return nil }

// injectCollisionInput builds a render whose "api" node delivers one key from its
// own injection block and carries one attachment whose block asks for a delivery
// key of its own.
//
// attachmentKey is the key the attachment asks for: the same one the component's
// own block delivers makes the duplicate, a different one is the control that has
// to keep rendering.
func injectCollisionInput(attachmentKey string) (renderer.RendererInput, renderer.RecipeLookup) {
	input := testInput()

	node, _ := input.Graph.Spec.Nodes.Get("api")
	node.SelfInject = &types.InjectBlock{Env: []any{map[string]any{"in": "DB_PASSWORD", "value": "from-self-inject"}}}
	input.Graph.Spec.Nodes.Set("api", node)

	envInj := types.OrderedMap[string]{}
	envInj.Set(attachmentKey, "root_password")
	inj := types.OrderedMap[types.OrderedMap[string]]{}
	inj.Set("env", envInj)
	vals := types.OrderedMap[any]{}
	vals.Set("generated", "s3cr3t")
	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("db-creds", types.IRFeatureEntry{
		Type:   "core/db-creds",
		Values: vals,
		Inject: inj,
	})
	outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	outer.Set("api", inner)
	input.Graph.Spec.FeatureValuesMapping = outer
	input.FeatureOrder = map[string][]string{"api": {"db-creds"}}

	input.GraphView = injectRenderGraphFacts{}
	input.GraphCoordinates = injectRenderCoordinates{
		nodes: []constraint.NodeInstance{{
			ID: "api", AuthoredName: "api", SourceFile: "aggregate.yaml",
		}},
		attachments: map[string][]string{"api": {"db-creds"}},
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
			if featureType != "core/db-creds" {
				return nil, false
			}
			provides := types.OrderedMap[string]{}
			provides.Set("root_password", "{{ .values.generated }}")
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "db-creds"},
				Spec: types.FeatureRecipeSpec{
					Capability: "compose",
					Implements: "core/db-creds",
					Provides:   provides,
				},
			}, true
		},
	}
	return input, lookup
}

// TestRenderAll_AttachmentTakingASelfInjectKeyHalts is the REFUSAL row through
// the render entry point: the component's own injection block delivers
// DB_PASSWORD and the attachment's block asks for the same key, which is a
// duplicate across two developer-authored surfaces.
func TestRenderAll_AttachmentTakingASelfInjectKeyHalts(t *testing.T) {
	t.Parallel()
	input, lookup := injectCollisionInput("DB_PASSWORD")
	factory := func(string) (renderer.Provider, error) { return &captureComposerEngine{}, nil }

	_, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err == nil {
		t.Fatal("RenderAll succeeded; expected a halt for an attachment taking a key the component's own block delivers")
	}
	if !errors.Is(err, renderer.ErrAttachmentInjectKeyCollision()) {
		t.Fatalf("RenderAll error = %v; want ErrAttachmentInjectKeyCollision", err)
	}
}

// TestRenderAll_AttachmentDeliveringItsOwnKeyRenders is the SILENT row on the
// same fixture: the attachment asks for a key nobody else delivers, so the build
// renders and the delivered value reaches the pipe.
//
// It is the row a refusal-only corpus cannot see. A rule that refused every
// attachment delivery would satisfy the row above and fail here.
func TestRenderAll_AttachmentDeliveringItsOwnKeyRenders(t *testing.T) {
	t.Parallel()
	input, lookup := injectCollisionInput("DB_SECRET")
	factory := func(string) (renderer.Provider, error) { return &captureComposerEngine{}, nil }

	if _, err := renderer.RenderAll(context.Background(), input, lookup, factory); err != nil {
		t.Fatalf("RenderAll must render an attachment delivering a key of its own; got %v", err)
	}
}
