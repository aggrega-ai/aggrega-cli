// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs here cover the fact the subsystem-identity rule reads: which
// subsystems declared a component under each identity the composition holds.
//
// They are written against a composition the build really produced, because the
// point of the fact is that it survives the flattening. The node map keeps one
// component per identity, so a proof reading the fact off a hand-built structure
// would be asserting the structure rather than what the walk recorded.

// componentsIn builds a subsystem holding one component of each named instance,
// all of one type — enough to be flattened, and nothing else.
func componentsIn(names ...string) types.Subsystem {
	comps := types.OrderedMap[types.ComponentInstance]{}
	for _, name := range names {
		comps.Set(name, types.ComponentInstance{Type: "core/db"})
	}
	return types.Subsystem{Components: comps}
}

// placedEverywhere is a boundary map placing whatever is asked of it, so the
// placement rules ahead of the facts under proof have nothing to report and the
// build reaches its end.
func placedEverywhere(nodes ...string) map[string]*resolver.BoundaryInfra {
	out := map[string]*resolver.BoundaryInfra{}
	for _, node := range nodes {
		out[node] = &resolver.BoundaryInfra{BoundaryPath: "env/cluster"}
	}
	return out
}

// buildComposition flattens an Aggregate tree and hands back the presentation
// the build carried on its result.
func buildComposition(
	t *testing.T,
	agg *types.Aggregate,
	loader SubAggregateLoader,
	placed map[string]*resolver.BoundaryInfra,
) *TopologyResult {
	t.Helper()
	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: placed,
		},
		LoadSubAggregate: loader,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if result.StageView == nil {
		t.Fatal("the build produced no view of the composition")
	}
	return result
}

// TestDeclaringSubsystemsSurvivesTheFlattening is the fact's own proof, and the
// claim it establishes is the one the rule rests on: both declarations are
// readable from the COMPLETED composition, not only while it is being built.
//
// The node map is checked alongside, because that is what makes the fact worth
// declaring — it holds one entry, so the pair is unreachable from it.
func TestDeclaringSubsystemsSurvivesTheFlattening(t *testing.T) {
	t.Parallel()

	subs := types.OrderedMap[types.Subsystem]{}
	subs.Set("serving", componentsIn("api"))
	subs.Set("legacy", componentsIn("api"))
	subs.Set("web", componentsIn("ui"))
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec:     types.AggregateSpec{Subsystems: subs},
	}

	result := buildComposition(t, agg, nil, placedEverywhere("api", "ui"))

	if got := result.Nodes.Len(); got != 2 {
		t.Fatalf("flattened nodes = %d, want 2 — the two declarations of \"api\" take one identity", got)
	}
	if want := []string{"serving", "legacy"}; !slices.Equal(result.StageView.NodeDeclaringSubsystems("api"), want) {
		t.Errorf("NodeDeclaringSubsystems(%q) = %v, want %v",
			"api", result.StageView.NodeDeclaringSubsystems("api"), want)
	}
	// The ordinary answer, so the two-entry case above cannot be read as what the
	// fact returns for everything.
	if want := []string{"web"}; !slices.Equal(result.StageView.NodeDeclaringSubsystems("ui"), want) {
		t.Errorf("NodeDeclaringSubsystems(%q) = %v, want %v",
			"ui", result.StageView.NodeDeclaringSubsystems("ui"), want)
	}
	// A name the composition never held, which the fact's declaration says is
	// empty rather than absent-and-unanswerable.
	if got := result.StageView.NodeDeclaringSubsystems("no-such-component"); len(got) != 0 {
		t.Errorf("NodeDeclaringSubsystems over a name nothing declared = %v, want empty", got)
	}
}

// TestDeclaringSubsystemsExcludesADeactivatedComponent pins the other half of
// the fact's declaration: a component an activation toggle removed was never
// composed, so nothing can collide with it.
//
// Without this, switching a component off while another subsystem declared the
// same name would report a collision between one live component and one the
// author had deliberately removed.
func TestDeclaringSubsystemsExcludesADeactivatedComponent(t *testing.T) {
	t.Parallel()

	off := types.ActiveFlag("false")
	removed := types.OrderedMap[types.ComponentInstance]{}
	removed.Set("api", types.ComponentInstance{Type: "core/db", Active: &off})

	subs := types.OrderedMap[types.Subsystem]{}
	subs.Set("serving", componentsIn("api"))
	subs.Set("legacy", types.Subsystem{Components: removed})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec:     types.AggregateSpec{Subsystems: subs},
	}

	result := buildComposition(t, agg, nil, placedEverywhere("api"))

	if want := []string{"serving"}; !slices.Equal(result.StageView.NodeDeclaringSubsystems("api"), want) {
		t.Errorf("NodeDeclaringSubsystems(%q) = %v, want %v — a removed component is not a declaration",
			"api", result.StageView.NodeDeclaringSubsystems("api"), want)
	}
}

// TestDeclaringSubsystemsReachesAModulesOwnSubsystems is the coverage the walk's
// own raise used to provide and would otherwise have been lost with it.
//
// A module's subsystems are read only when the composition is flattened, so a
// rule asked over one authored document never sees them. The record travels with
// the module's nodes into the importing composition, which is what keeps the pair
// answerable for a component no authoring document names.
func TestDeclaringSubsystemsReachesAModulesOwnSubsystems(t *testing.T) {
	t.Parallel()

	childSubs := types.OrderedMap[types.Subsystem]{}
	childSubs.Set("primary", componentsIn("store"))
	childSubs.Set("mirror", componentsIn("store"))
	childAgg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "data-mod"},
		Spec:     types.AggregateSpec{Subsystems: childSubs},
	}

	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("data", types.SubAggregate{Type: "modules/data-layer"})
	rootSubs := types.OrderedMap[types.Subsystem]{}
	rootSubs.Set("web", componentsIn("ui"))
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Subsystems: rootSubs,
			Aggregates: aggregates,
		},
	}

	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		if typeName == "modules/data-layer" {
			return childAgg, "", nil, nil
		}
		return nil, "", nil, fmt.Errorf("unknown: %s", typeName)
	}

	result := buildComposition(t, agg, loader, placedEverywhere("ui", "data.store"))

	if want := []string{"primary", "mirror"}; !slices.Equal(result.StageView.NodeDeclaringSubsystems("data.store"), want) {
		t.Errorf("NodeDeclaringSubsystems(%q) = %v, want %v",
			"data.store", result.StageView.NodeDeclaringSubsystems("data.store"), want)
	}
	// The module merge keeps its own condition, and a pair inside one module is
	// not it: nothing that identity collided with was already in the set.
	if _, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: placedEverywhere("ui", "data.store"),
		},
		LoadSubAggregate: loader,
	}); errors.Is(err, ErrSubAggregateNameCollision()) {
		t.Errorf("a pair inside one module raised the merge condition: %v", err)
	}
}
