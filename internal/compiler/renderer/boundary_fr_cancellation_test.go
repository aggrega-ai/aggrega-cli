// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The boundary-to-consumer env channel asks the provider engine to compute a
// boundary's overrides, and that computation happens in the provider GUEST. The
// call declared a context and the prepopulation pass minted a background one, so
// a build's withdrawal reached the crossing on every other render path and not
// this one — even though the single production caller is the render orchestrator,
// which has held the build's context throughout.
//
// The stub below records the context it was handed. That is the discriminator:
// the question is not whether the call returns, but WHOSE context the guest
// crossing runs under, because a crossing on a context nobody can end is
// uninterruptible however promptly the caller gives up.

// contextRecordingOverrideEngine answers the channel and records what it was
// handed, so the proof can tell the build's context from one minted in between.
type contextRecordingOverrideEngine struct {
	seen  context.Context
	calls int
}

func (e *contextRecordingOverrideEngine) Render(context.Context, map[string]any, NodeRenderContext) ([]RenderedFile, error) {
	return nil, nil
}

func (e *contextRecordingOverrideEngine) Contract() ProviderContract {
	return ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (e *contextRecordingOverrideEngine) BoundaryEnvOverrides(ctx context.Context, _ []types.EnrichedEdge, _ types.OrderedMap[any]) (map[string]map[string]string, error) {
	e.calls++
	e.seen = ctx
	return map[string]map[string]string{"api": {"FOO_ADDR": "svc-x:1234"}}, nil
}

// TestTheBuildsContextReachesTheBoundaryOverrideCrossing is the load-bearing
// proof.
//
// A value is planted on the caller's context and the engine is asked whether it
// arrived. A background context minted in between carries nothing, so the marker
// is what separates "the build's own" from "one this pass invented".
//
// A planted defect makes this fail: mint a background context at the override
// call instead of taking the pass's, and the marker is gone.
func TestTheBuildsContextReachesTheBoundaryOverrideCrossing(t *testing.T) {
	type marker struct{}
	const want = "the build's own"

	engine := &contextRecordingOverrideEngine{}
	graph, release, lookup := boundaryOverrideFixture()

	ctx := context.WithValue(context.Background(), marker{}, want)
	state := NewBoundaryFRState()
	factory := func(string) (Provider, error) { return engine, nil }

	if err := PrepopulateBoundaryScopeProvides(ctx, graph, release, nil, lookup, nil, factory, state, nil); err != nil {
		t.Fatalf("PrepopulateBoundaryScopeProvides: %v", err)
	}
	if engine.calls != 1 {
		t.Fatalf("the channel was asked %d times rather than once, so there is nothing to inspect",
			engine.calls)
	}
	if got, _ := engine.seen.Value(marker{}).(string); got != want {
		t.Errorf("the guest crossing was handed a context carrying %q; the build's own carries "+
			"%q, so the crossing runs under a context the build cannot end", got, want)
	}
}

// TestAWithdrawnBuildsOverrideCrossingSeesTheWithdrawal pins the consequence: a
// build already withdrawn hands the crossing a context that reports it.
//
// The control is what discriminates — a live build's crossing must see no
// withdrawal — because an engine handed an always-ended context would satisfy the
// withdrawn arm on its own.
func TestAWithdrawnBuildsOverrideCrossingSeesTheWithdrawal(t *testing.T) {
	graph, release, lookup := boundaryOverrideFixture()
	factory := func(e *contextRecordingOverrideEngine) EngineFactory {
		return func(string) (Provider, error) { return e, nil }
	}

	live := &contextRecordingOverrideEngine{}
	if err := PrepopulateBoundaryScopeProvides(context.Background(), graph, release, nil, lookup, nil, factory(live), NewBoundaryFRState(), nil); err != nil {
		t.Fatalf("the live control failed: %v", err)
	}
	if live.seen == nil || live.seen.Err() != nil {
		t.Fatalf("the live control's crossing already saw a withdrawal (%v), so the arm below "+
			"proves nothing", live.seen)
	}

	withdrawn, stop := context.WithCancel(context.Background())
	stop()

	stopped := &contextRecordingOverrideEngine{}
	if err := PrepopulateBoundaryScopeProvides(withdrawn, graph, release, nil, lookup, nil, factory(stopped), NewBoundaryFRState(), nil); err != nil {
		t.Fatalf("the withdrawn build failed for an unrelated reason: %v", err)
	}
	if stopped.seen == nil || stopped.seen.Err() == nil {
		t.Errorf("a withdrawn build's override crossing saw no withdrawal (%v); the stop did not "+
			"reach the guest", stopped.seen)
	}
}

// boundaryOverrideFixture builds the smallest graph that routes one
// boundary-scope FeatureRecipe through the override channel, reusing the
// package's own boundary-scope fixture so this proof and the channel's own test
// describe the same shape rather than two spellings of it.
func boundaryOverrideFixture() (*types.IRGraph, *types.Release, RecipeLookup) {
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
	return graph, release, lookup
}
