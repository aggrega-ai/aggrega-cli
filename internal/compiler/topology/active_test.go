// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Unit tests for the structural `active` presence toggle. The toggle reaches
// flatten already resolved to a literal (the resolver renders any parameter
// template before unmarshal), so these tests hand flatten and Build the
// resolved literals directly and pin the prune, cascade, classification, and
// determinism behaviours. The end-to-end release-precedence and
// .values-rejection paths (which live in the resolver) are exercised against
// the real binary; here the analog non-boolean literal is pinned at flatten.

package topology

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func comps(pairs ...[2]string) types.OrderedMap[types.ComponentInstance] {
	m := types.OrderedMap[types.ComponentInstance]{}
	for _, p := range pairs {
		m.Set(p[0], types.ComponentInstance{Type: "core/compute/api-server", Active: types.ActiveFlag(p[1])})
	}
	return m
}

func TestFlatten_ActivePresentVsPruned(t *testing.T) {
	t.Parallel()

	// active resolved to true → present.
	on := &types.Aggregate{Spec: types.AggregateSpec{Components: comps([2]string{"api", "true"}, [2]string{"cache", ""})}}
	res, err := flattenAggregate(on, "", "", "", nil, map[string]bool{}, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Nodes.Len() != 2 {
		t.Fatalf("expected 2 nodes present, got %d", res.Nodes.Len())
	}
	if res.Pruned.Len() != 0 {
		t.Fatalf("expected no pruned nodes, got %v", res.Pruned.Keys())
	}

	// active resolved to false → pruned, recorded in Pruned, absent from Nodes.
	off := &types.Aggregate{Spec: types.AggregateSpec{Components: comps([2]string{"api", "true"}, [2]string{"cache", "false"})}}
	res2, err := flattenAggregate(off, "", "", "", nil, map[string]bool{}, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res2.Nodes.Len() != 1 {
		t.Fatalf("expected 1 node present, got %d", res2.Nodes.Len())
	}
	if _, ok := res2.Nodes.Get("cache"); ok {
		t.Error("pruned cache must be absent from Nodes")
	}
	if _, ok := res2.Pruned.Get("cache"); !ok {
		t.Errorf("expected cache in Pruned, got %v", res2.Pruned.Keys())
	}
}

func TestFlatten_ActiveDeterministic(t *testing.T) {
	t.Parallel()
	build := func() []string {
		agg := &types.Aggregate{Spec: types.AggregateSpec{Components: comps(
			[2]string{"a", "true"}, [2]string{"b", "false"}, [2]string{"c", ""}, [2]string{"d", "false"})}}
		res, err := flattenAggregate(agg, "", "", "", nil, map[string]bool{}, 0, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return res.Nodes.Keys()
	}
	first, second := build(), build()
	if len(first) != 2 {
		t.Fatalf("expected 2 surviving nodes, got %v", first)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("non-deterministic node order: %v vs %v", first, second)
		}
	}
	// Author order preserved among survivors.
	if first[0] != "a" || first[1] != "c" {
		t.Errorf("survivor author order lost: %v", first)
	}
}

func TestFlatten_ActiveOmittedIsPresent(t *testing.T) {
	t.Parallel()
	// Omitted (zero ActiveFlag) and explicit "true" behave identically: present,
	// nothing pruned — the additive no-op default.
	for _, v := range []string{"", "true"} {
		agg := &types.Aggregate{Spec: types.AggregateSpec{Components: comps([2]string{"api", v})}}
		res, err := flattenAggregate(agg, "", "", "", nil, map[string]bool{}, 0, nil)
		if err != nil {
			t.Fatalf("active=%q: unexpected error: %v", v, err)
		}
		if res.Nodes.Len() != 1 || res.Pruned.Len() != 0 {
			t.Errorf("active=%q: expected 1 node and no prune, got %d nodes / pruned %v", v, res.Nodes.Len(), res.Pruned.Keys())
		}
	}
}

func TestFlatten_ActiveNonBooleanHalts(t *testing.T) {
	t.Parallel()
	// A resolved literal that is neither empty nor a boolean — e.g. the
	// "<no value>" a template over a missing key renders — is a Loud Guardrail.
	for _, bad := range []string{"<no value>", "yes", "maybe"} {
		agg := &types.Aggregate{Spec: types.AggregateSpec{Components: comps([2]string{"api", bad})}}
		_, err := flattenAggregate(agg, "", "", "", nil, map[string]bool{}, 0, nil)
		if !errors.Is(err, ErrInvalidActive) {
			t.Errorf("active=%q: expected ErrInvalidActive, got %v", bad, err)
		}
	}
}

func TestFlatten_SubAggregateActiveFalsePrunesModule(t *testing.T) {
	t.Parallel()
	childComps := types.OrderedMap[types.ComponentInstance]{}
	childComps.Set("child-db", types.ComponentInstance{Type: "core/datastore/postgres"})
	childAgg := &types.Aggregate{Spec: types.AggregateSpec{Components: childComps}}

	loaderCalled := false
	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		loaderCalled = true
		return childAgg, "", nil, nil
	}

	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("data", types.SubAggregate{Type: "modules/data-layer", Active: "false"})
	root := &types.Aggregate{Spec: types.AggregateSpec{
		Components: comps([2]string{"api", "true"}),
		Aggregates: aggregates,
	}}

	res, err := flattenAggregate(root, "", "", "", loader, map[string]bool{}, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaderCalled {
		t.Error("loader must not be called for a pruned module")
	}
	if res.Nodes.Len() != 1 {
		t.Fatalf("expected only the root api node, got %d", res.Nodes.Len())
	}
	if _, ok := res.Nodes.Get("data.child-db"); ok {
		t.Error("pruned module's components must not materialize")
	}
}

func buildAgg(t *testing.T, agg *types.Aggregate, loader SubAggregateLoader) (*TopologyResult, error) {
	t.Helper()
	return Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		LoadSubAggregate: loader,
	})
}

func TestBuild_SurvivorUsesPrunedHalts(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"main-db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	components.Set("main-db", types.ComponentInstance{Type: "core/datastore/postgres", Active: "false"})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	_, err := buildAgg(t, agg, nil)
	if !errors.Is(err, ErrUsesPrunedComponent) {
		t.Fatalf("expected ErrUsesPrunedComponent, got %v", err)
	}
	if errors.Is(err, ErrDanglingUsesTarget) {
		t.Error("a pruned target must not be classified as a generic dangling target")
	}
}

func TestBuild_DanglingTargetStillClassifiedDangling(t *testing.T) {
	t.Parallel()
	// A survivor edge to a target that never existed keeps ErrDanglingUsesTarget
	// — the pruned classification must not swallow the pre-existing guardrail.
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"ghost"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	_, err := buildAgg(t, agg, nil)
	if !errors.Is(err, ErrDanglingUsesTarget) {
		t.Fatalf("expected ErrDanglingUsesTarget, got %v", err)
	}
	if errors.Is(err, ErrUsesPrunedComponent) {
		t.Error("a never-existent target must not be classified as pruned")
	}
}

func TestBuild_PrunedExportedComponentDropped(t *testing.T) {
	t.Parallel()
	childComps := types.OrderedMap[types.ComponentInstance]{}
	childComps.Set("kept", types.ComponentInstance{Type: "core/compute/api-server"})
	childComps.Set("gone", types.ComponentInstance{Type: "core/datastore/postgres", Active: "false"})
	exported := types.OrderedMap[string]{}
	exported.Set("public-kept", "kept")
	exported.Set("public-gone", "gone")
	childAgg := &types.Aggregate{Spec: types.AggregateSpec{Components: childComps, ExportedComponents: exported}}

	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("mod", types.SubAggregate{Type: "modules/x"})
	root := &types.Aggregate{Spec: types.AggregateSpec{Aggregates: aggregates}}

	loader := func(string, map[string]any) (*types.Aggregate, string, map[string]any, error) {
		return childAgg, "", nil, nil
	}

	res, err := buildAgg(t, root, loader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := res.ExportedComponents.Get("mod.public-gone"); ok {
		t.Errorf("export alias of a pruned component must be dropped: %v", res.ExportedComponents.Keys())
	}
	if got, _ := res.ExportedComponents.Get("mod.public-kept"); got != "mod.kept" {
		t.Errorf("surviving export alias lost: %v", res.ExportedComponents.Keys())
	}
}
