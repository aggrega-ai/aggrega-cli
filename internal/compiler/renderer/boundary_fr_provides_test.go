// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// boundaryScopeProvidesFR builds a sourceless boundary-scope FeatureRecipe whose
// provides reference the boundary attachment's .infra, so the resolved value
// discriminates which instance's infra drove it.
func boundaryScopeProvidesFR() *types.FeatureRecipe {
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "db-fr", Version: "1.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "stub",
			Implements:     "core/db:1.0",
			ExecutionScope: "boundary",
		},
	}
	fr.Spec.Provides.Set("dsn", "{{ .infra.host }}:5432")
	return fr
}

func dbProvidesLookup(fr *types.FeatureRecipe) RecipeLookup {
	return RecipeLookup{
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/db:1.0" {
				return fr, true
			}
			return nil, false
		},
	}
}

func boundaryUsesEdge(source, target, originFeature, originInstance, localKey, providesKey string) types.IREdge {
	env := types.OrderedMap[string]{}
	env.Set(localKey, providesKey)
	inj := types.OrderedMap[types.OrderedMap[string]]{}
	inj.Set("env", env)
	return types.IREdge{
		Source:                source,
		Target:                target,
		OriginFeature:         originFeature,
		OriginFeatureInstance: originInstance,
		Inject:                inj,
	}
}

// TestBoundaryScopeFRProvides_ResolvesThroughUsesEdge is the headline. A
// per-component consumer's uses edge, originating from a boundary-scope
// FeatureRecipe attachment, resolves that FR's provided value (non-empty) after
// the provides pre-pass populates BoundaryFRState. The negative control — the
// same edge WITHOUT the OriginFeatureInstance enrichment — resolves nothing
// (the prior silent-empty behavior), proving the wiring is what makes it work
// and the green is not vacuous.
func TestBoundaryScopeFRProvides_ResolvesThroughUsesEdge(t *testing.T) {
	t.Parallel()
	fr := boundaryScopeProvidesFR()
	lookup := dbProvidesLookup(fr)

	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	infra := types.OrderedMap[any]{}
	infra.Set("host", "db.internal")
	inner := types.OrderedMap[types.IRBoundaryFeature]{}
	inner.Set("db", types.IRBoundaryFeature{Type: "core/db:1.0", Infra: infra})
	graph.Spec.BoundaryFeatures.Set("cluster", inner)

	release := &types.Release{}
	release.Spec.Boundaries.Set("cluster", types.Boundary{Description: "cluster"})

	state := NewBoundaryFRState()
	if err := PrepopulateBoundaryScopeProvides(graph, release, nil, lookup, nil, nil, state); err != nil {
		t.Fatalf("PrepopulateBoundaryScopeProvides: %v", err)
	}
	// The pre-pass populated the boundary-scope FR's provides before any
	// node render.
	prov, ok := state.Provides[boundaryFRKey{BoundaryPath: "cluster", FRInstance: "db"}]
	if !ok || prov["dsn"] != "db.internal:5432" {
		t.Fatalf("pre-pass provides = %v (ok=%v); want dsn=db.internal:5432", prov, ok)
	}

	// Headline: the consumer's uses edge resolves the boundary-scope provided value.
	edges := []types.IREdge{boundaryUsesEdge("api", "db", "core/db:1.0", "db", "DB_DSN", "dsn")}
	inject := &types.InjectPipe{}
	RehydrateInjectFromProvides(inject, edges, "api", NodeProvides{}, graph, lookup, state)
	if v, ok := inject.Env.Get("DB_DSN"); !ok || v != "db.internal:5432" {
		t.Errorf("DB_DSN = %q (ok=%v); want db.internal:5432 — boundary-scope FR provides not delivered", v, ok)
	}

	// Negative control: drop the enrichment (the prior edge shape) → silent-empty.
	edgesNoEnrich := []types.IREdge{boundaryUsesEdge("api", "db", "core/db:1.0", "", "DB_DSN", "dsn")}
	injectNo := &types.InjectPipe{}
	RehydrateInjectFromProvides(injectNo, edgesNoEnrich, "api", NodeProvides{}, graph, lookup, state)
	if _, ok := injectNo.Env.Get("DB_DSN"); ok {
		t.Error("without OriginFeatureInstance the boundary-scope provides must NOT resolve (the test is not vacuously green)")
	}
}

// TestBoundaryScopeFRProvides_SameTypeTwiceDisambiguates pins that the enriched
// OriginFeatureInstance selects the RIGHT instance when one boundary carries two
// attachments of the same FeatureRecipe type with different infra.
func TestBoundaryScopeFRProvides_SameTypeTwiceDisambiguates(t *testing.T) {
	t.Parallel()
	fr := boundaryScopeProvidesFR()
	lookup := dbProvidesLookup(fr)

	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	infraA := types.OrderedMap[any]{}
	infraA.Set("host", "a.internal")
	infraB := types.OrderedMap[any]{}
	infraB.Set("host", "b.internal")
	inner := types.OrderedMap[types.IRBoundaryFeature]{}
	inner.Set("db-a", types.IRBoundaryFeature{Type: "core/db:1.0", Infra: infraA})
	inner.Set("db-b", types.IRBoundaryFeature{Type: "core/db:1.0", Infra: infraB})
	graph.Spec.BoundaryFeatures.Set("cluster", inner)

	release := &types.Release{}
	release.Spec.Boundaries.Set("cluster", types.Boundary{Description: "cluster"})

	state := NewBoundaryFRState()
	if err := PrepopulateBoundaryScopeProvides(graph, release, nil, lookup, nil, nil, state); err != nil {
		t.Fatalf("PrepopulateBoundaryScopeProvides: %v", err)
	}

	// Edge pointing at instance db-b must resolve b.internal, not a.internal.
	edges := []types.IREdge{boundaryUsesEdge("api", "db-b", "core/db:1.0", "db-b", "DB_DSN", "dsn")}
	inject := &types.InjectPipe{}
	RehydrateInjectFromProvides(inject, edges, "api", NodeProvides{}, graph, lookup, state)
	if v, _ := inject.Env.Get("DB_DSN"); v != "b.internal:5432" {
		t.Errorf("DB_DSN = %q; want b.internal:5432 — the enriched instance must pick db-b, not the other same-type attachment", v)
	}
}

// TestBoundaryScopeFRProvides_PrepassIdempotentWithDispatch proves the pre-pass
// and the Phase B.6 dispatch evaluate a boundary-scope FR's provides identically
// (they share buildBoundaryFRTemplateContext + evaluateBoundaryFRTemplates), so
// the double-eval (pre-pass plus dispatch) cannot diverge.
func TestBoundaryScopeFRProvides_PrepassIdempotentWithDispatch(t *testing.T) {
	t.Parallel()
	fr := boundaryScopeProvidesFR()
	lookup := dbProvidesLookup(fr)

	graph := &types.IRGraph{Metadata: types.IRMetadata{AggregateName: "app"}}
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	infra := types.OrderedMap[any]{}
	infra.Set("host", "db.internal")
	inner := types.OrderedMap[types.IRBoundaryFeature]{}
	inner.Set("db", types.IRBoundaryFeature{Type: "core/db:1.0", Infra: infra})
	graph.Spec.BoundaryFeatures.Set("cluster", inner)

	release := &types.Release{}
	release.Spec.Boundaries.Set("cluster", types.Boundary{Description: "cluster"})

	state := NewBoundaryFRState()
	if err := PrepopulateBoundaryScopeProvides(graph, release, nil, lookup, nil, nil, state); err != nil {
		t.Fatalf("pre-pass: %v", err)
	}
	key := boundaryFRKey{BoundaryPath: "cluster", FRInstance: "db"}
	prePass := state.Provides[key]["dsn"]

	// Run the Phase B.6 dispatch for the same FR; it re-evaluates the provides.
	var captured any
	factory := func(string) (Provider, error) { return sourceCaptureEngine{captured: &captured}, nil }
	if err := dispatchBoundaryScopeFR(
		context.Background(), fr, "db", key, &PhaseBState{}, state,
		NodeRenderContext{}, factory, graph, release, nil, nil, RendererInput{},
	); err != nil {
		t.Fatalf("dispatchBoundaryScopeFR: %v", err)
	}
	if dispatched := state.Provides[key]["dsn"]; dispatched != prePass {
		t.Errorf("provides diverged: pre-pass=%q dispatch=%q", prePass, dispatched)
	}
	if prePass != "db.internal:5432" {
		t.Errorf("provides = %q; want db.internal:5432", prePass)
	}
}

// TestBoundaryScopeFRProvides_GracefulNotFound pins that an enriched edge whose
// instance has no entry in BoundaryFRState falls through to the per-component
// path without panicking and resolves nothing.
func TestBoundaryScopeFRProvides_GracefulNotFound(t *testing.T) {
	t.Parallel()
	fr := boundaryScopeProvidesFR()
	lookup := dbProvidesLookup(fr)

	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})

	state := NewBoundaryFRState() // empty: no provides populated

	edges := []types.IREdge{boundaryUsesEdge("api", "db", "core/db:1.0", "db-missing", "DB_DSN", "dsn")}
	inject := &types.InjectPipe{}
	RehydrateInjectFromProvides(inject, edges, "api", NodeProvides{}, graph, lookup, state)
	if _, ok := inject.Env.Get("DB_DSN"); ok {
		t.Error("a not-found boundary-scope instance must resolve nothing, not panic or mis-resolve")
	}
}
