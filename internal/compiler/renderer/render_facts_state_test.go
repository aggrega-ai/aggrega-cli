// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// factReportingEmitter stands in for an attachment's provider guest that reports
// facts about what it placed, through the render-fact sink of the context it is
// handed — the same thing a real guest's crossing does when the engine forwards
// its report. It places the artifact set exactly as the plain stub does.
type factReportingEmitter struct {
	stubArtifactsEmitter
	facts []RenderFact
}

func (e *factReportingEmitter) EmitArtifacts(ctx context.Context, proj ArtifactsProjection, nodeCtx NodeRenderContext) ([]EmittedFile, error) {
	if len(e.facts) > 0 && nodeCtx.RenderFactSink != nil {
		nodeCtx.RenderFactSink(e.facts)
	}
	return e.stubArtifactsEmitter.EmitArtifacts(ctx, proj, nodeCtx)
}

// TestRenderFacts_TheNodesOwnAreCarriedOnItsStateAndAnAttachmentsBesideThem
// holds where the facts a guest reports land on the host side. The node's own
// render reports across two crossings, through the sink wired exactly as the
// orchestrator wires it, and every fact arrives on the node's state in order and
// byte for byte. The attachment pass is then handed that state, and an attachment
// whose own guest reports facts has them recorded under its instance name — never
// added to the host's, which would make something the attachment emitted read as
// something the host rendered. The second row's attachment reports nothing and
// leaves no entry.
func TestRenderFacts_TheNodesOwnAreCarriedOnItsStateAndAnAttachmentsBesideThem(t *testing.T) {
	t.Parallel()

	// Out of name order, every value a list, with an object entry whose keys are
	// out of key order and carry a space: anything that sorted, re-serialised or
	// tidied would show.
	hostCrossings := [][]RenderFact{
		{{Name: "zeta_fact", Value: json.RawMessage(`[{"tier":"web", "app":"api"}]`)}},
		{{Name: "alpha_fact", Value: json.RawMessage(`["b","a"]`)}},
	}
	hostWant := []RenderFact{hostCrossings[0][0], hostCrossings[1][0]}
	guardFacts := []RenderFact{{Name: "guard_fact", Value: json.RawMessage(`[{"z":1,"a":2}]`)}}

	for _, tc := range []struct {
		name            string
		attachmentFacts []RenderFact
	}{
		{name: "control: the attachment's guest reports facts of its own", attachmentFacts: guardFacts},
		{name: "the attachment's guest reports nothing", attachmentFacts: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hostCtx := &types.TemplateContext{
				Context: types.ContextBlock{InstanceID: "api"},
				Values:  types.OrderedMap[any]{},
				Infra:   types.OrderedMap[any]{},
			}
			hostRecipe := &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "helm", Implements: "core/api"},
			}
			state := NewPhaseBState(hostRecipe, "api", "local", "")
			nodeCtx := NodeRenderContext{NodeID: "api", BoundaryPath: "local"}
			nodeCtx.RenderFactSink = state.recordRenderFacts

			for _, reported := range hostCrossings {
				nodeCtx.RenderFactSink(reported)
			}
			if !reflect.DeepEqual(state.RenderFacts, hostWant) {
				t.Fatalf("the node's own facts on its state = %s, want %s", factsText(state.RenderFacts), factsText(hostWant))
			}

			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set("guard.yaml", types.ArtifactEntry{Inline: "kind: Guard\n"})
			fr := &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "guard", Version: "1.0.0"},
				Spec: types.FeatureRecipeSpec{
					Capability: "helm",
					Implements: "core/guard",
					Artifacts:  art,
				},
			}
			emitter := &factReportingEmitter{facts: tc.attachmentFacts}
			factory := func(string) (Provider, error) { return emitter, nil }

			b6, _, err := phaseB6(context.Background(), hostCtx, state,
				[]FeatureRecipeRef{{InstanceName: "guard", FeatureRecipe: fr}}, factory,
				NodeProvides{}, RecipeLookup{}, nodeCtx,
				nil, nil, nil, nil, RendererInput{}, nil, false, "helm", false, renderctx.RenderContext{})
			if err != nil {
				t.Fatalf("phaseB6: %v", err)
			}
			if len(b6.files) == 0 {
				t.Fatal("the attachment placed nothing, so its render never crossed the sink this test is about")
			}

			if !reflect.DeepEqual(state.RenderFacts, hostWant) {
				t.Errorf("the attachment pass changed the node's own facts: got %s, want %s",
					factsText(state.RenderFacts), factsText(hostWant))
			}
			got, recorded := state.AttachmentRenderFacts.Get("guard")
			if tc.attachmentFacts == nil {
				if state.AttachmentRenderFacts.Len() != 0 {
					t.Errorf("an attachment reporting nothing left an entry: %v", state.AttachmentRenderFacts.Keys())
				}
				return
			}
			if !recorded {
				t.Fatalf("the attachment's facts were not recorded under its instance name; recorded under %v",
					state.AttachmentRenderFacts.Keys())
			}
			if !reflect.DeepEqual(got, tc.attachmentFacts) {
				t.Errorf("the attachment's facts = %s, want %s", factsText(got), factsText(tc.attachmentFacts))
			}
		})
	}
}

// factReportingRenderEngine stands in for a boundary-scope FeatureRecipe's
// provider guest: it reports facts through the sink of the context it is handed
// at a source render, and counts its renders so a test cannot pass without one.
type factReportingRenderEngine struct {
	stubEngine
	facts   []RenderFact
	renders *int
}

func (e factReportingRenderEngine) Render(_ context.Context, _ map[string]any, nodeCtx NodeRenderContext) ([]RenderedFile, error) {
	*e.renders++
	if nodeCtx.RenderFactSink != nil {
		nodeCtx.RenderFactSink(e.facts)
	}
	return nil, nil
}

// TestRenderFacts_ABoundaryScopeRenderIsNoNodesOwn holds the third place a
// render's report can land. A boundary-scope FeatureRecipe renders with a copy of
// a consumer node's context, so the sink it inherits records that node's own
// render — but the render belongs to the boundary, and no node rendered it. The
// consumer's record must be left as it was.
func TestRenderFacts_ABoundaryScopeRenderIsNoNodesOwn(t *testing.T) {
	t.Parallel()

	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "compose-bridge-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "stub",
			Implements:     "core/compose-bridge:1.0.0",
			ExecutionScope: "boundary",
			Source:         "./charts/local-bridge",
			ValuesMapping:  types.ValuesMapping{Inline: "{}"},
		},
	}
	clusterBF := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBF.Set("bridge", types.IRBoundaryFeature{Type: "core/compose-bridge:1.0.0", Infra: types.OrderedMap[any]{}})
	graph := &types.IRGraph{Metadata: types.IRMetadata{AggregateName: "bridge-app"}}
	graph.Spec.BoundaryFeatures.Set("cluster", clusterBF)
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	release := &types.Release{}
	release.Spec.Boundaries.Set("cluster", types.Boundary{Description: "cluster"})

	consumer := NewPhaseBState(nil, "api", "cluster", "")
	consumerCtx := NodeRenderContext{NodeID: "api", BoundaryPath: "cluster"}
	consumerCtx.RenderFactSink = consumer.recordRenderFacts

	renders := 0
	engine := factReportingRenderEngine{
		facts:   []RenderFact{{Name: "bridge_fact", Value: json.RawMessage(`["bridge"]`)}},
		renders: &renders,
	}
	factory := func(string) (Provider, error) { return engine, nil }
	key := boundaryFRKey{BoundaryPath: "cluster", FRInstance: "bridge"}

	input := RendererInput{
		Lockfile:      &lockfile.Lockfile{Version: 1},
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	}
	if err := dispatchBoundaryScopeFR(
		context.Background(), fr, "bridge", key, consumer, NewBoundaryFRState(),
		consumerCtx, factory, RecipeLookup{}, graph, release, nil, nil, input,
	); err != nil {
		t.Fatalf("dispatchBoundaryScopeFR: %v", err)
	}
	if renders == 0 {
		t.Fatal("the boundary-scope render never ran, so nothing here was tested")
	}
	if len(consumer.RenderFacts) != 0 || consumer.AttachmentRenderFacts.Len() != 0 {
		t.Errorf("a boundary-scope render's facts landed on the consumer node's record: own %s, attachments %v",
			factsText(consumer.RenderFacts), consumer.AttachmentRenderFacts.Keys())
	}
}

// factsText renders a fact list as name=value pairs, so a mismatch reports the
// bytes rather than a slice of byte values.
func factsText(facts []RenderFact) string {
	s := "["
	for i, f := range facts {
		if i > 0 {
			s += " "
		}
		s += f.Name + "=" + string(f.Value)
	}
	return s + "]"
}
