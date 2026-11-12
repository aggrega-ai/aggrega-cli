// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Tests for the two facts flattening computes that the topology result carries
// onward: the set of nodes a false `active` toggle removed, and the export-alias
// table as the Aggregate tree declared it. Each is read from the built result —
// the later stage — rather than from the flatten output that produced it, so the
// test would fail if the fact were computed and then dropped at the boundary.

package topology

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBuild_CarriesPrunedSetForward reads the prune set from the built result and
// holds it to naming exactly the removed nodes, in the order flattening
// encountered them.
//
// The order matters as much as the membership. The result is an input to the
// stage that emits artifacts, and byte-identical output across runs of the same
// input is a hard contract — so a set crossing this boundary has to carry an
// order of its own rather than borrow Go's map iteration, which varies per run.
// Reading the same build twice and comparing the sequences is what makes a
// regression to an unordered carrier visible here rather than as an
// intermittently reordered artifact much later.
func TestBuild_CarriesPrunedSetForward(t *testing.T) {
	t.Parallel()

	build := func() *TopologyResult {
		components := types.OrderedMap[types.ComponentInstance]{}
		components.Set("api", types.ComponentInstance{Type: "core/compute/api-server"})
		components.Set("cache", types.ComponentInstance{Type: "core/datastore/postgres", Active: activeFlag("false")})
		components.Set("worker", types.ComponentInstance{Type: "core/compute/api-server"})
		components.Set("legacy", types.ComponentInstance{Type: "core/datastore/postgres", Active: activeFlag("false")})
		agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

		res, err := buildAgg(t, agg, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return res
	}

	res := build()

	if res.Pruned.Len() != 2 {
		t.Fatalf("expected 2 pruned nodes carried onto the result, got %d: %v", res.Pruned.Len(), res.Pruned.Keys())
	}
	for _, id := range []string{"cache", "legacy"} {
		if _, ok := res.Pruned.Get(id); !ok {
			t.Errorf("pruned node %q missing from the carried set: %v", id, res.Pruned.Keys())
		}
	}
	// A survivor must never appear in the set — the set is what lets a consumer
	// tell a deliberately removed component apart from one that never existed.
	if _, ok := res.Pruned.Get("api"); ok {
		t.Errorf("surviving node reported as pruned: %v", res.Pruned.Keys())
	}
	// Present in the set means absent from the node map.
	for _, id := range res.Pruned.Keys() {
		if _, ok := res.Nodes.Get(id); ok {
			t.Errorf("node %q is both pruned and present in Nodes", id)
		}
	}

	// Encounter order is author order, and it is stable across builds.
	want := []string{"cache", "legacy"}
	got := res.Pruned.Keys()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("prune order is not author order: got %v, want %v", got, want)
		}
	}
	second := build().Pruned.Keys()
	for i := range got {
		if got[i] != second[i] {
			t.Fatalf("prune order is not stable across builds: %v vs %v", got, second)
		}
	}
}

// TestBuild_CarriesDeclaredExportAliasesForward reads both export-alias tables
// from the built result and holds them to differing exactly by the alias whose
// target was pruned.
//
// The declared table is the fact that would otherwise be unrecoverable: the
// drop removes the alias in place, so after it has run there is no way to learn
// that the alias was ever published. Snapshotting before the drop is what makes
// the answer independent of when a consumer looks, rather than dependent on
// reading the live table before something mutates it.
func TestBuild_CarriesDeclaredExportAliasesForward(t *testing.T) {
	t.Parallel()

	childComps := types.OrderedMap[types.ComponentInstance]{}
	childComps.Set("kept", types.ComponentInstance{Type: "core/compute/api-server"})
	childComps.Set("gone", types.ComponentInstance{Type: "core/datastore/postgres", Active: activeFlag("false")})
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

	// The declared table carries both aliases, including the dropped one.
	if got, ok := res.ExportedComponentsDeclared.Get("mod.public-gone"); !ok || got != "mod.gone" {
		t.Errorf("declared table lost the alias whose target was pruned: got %q ok=%v, table %v",
			got, ok, res.ExportedComponentsDeclared.Keys())
	}
	if got, ok := res.ExportedComponentsDeclared.Get("mod.public-kept"); !ok || got != "mod.kept" {
		t.Errorf("declared table lost a surviving alias: got %q ok=%v, table %v",
			got, ok, res.ExportedComponentsDeclared.Keys())
	}

	// The live table carries only the survivor, so the two tables together answer
	// "declared and dropped" versus "never declared".
	if _, ok := res.ExportedComponents.Get("mod.public-gone"); ok {
		t.Errorf("live table still publishes an alias whose target was pruned: %v", res.ExportedComponents.Keys())
	}
	if _, ok := res.ExportedComponents.Get("mod.public-kept"); !ok {
		t.Errorf("live table lost a surviving alias: %v", res.ExportedComponents.Keys())
	}
	if res.ExportedComponentsDeclared.Len() != res.ExportedComponents.Len()+1 {
		t.Errorf("expected the declared table to carry exactly one more alias than the live one; declared=%v live=%v",
			res.ExportedComponentsDeclared.Keys(), res.ExportedComponents.Keys())
	}

	// Declaration order is the authoring order of the child's export block.
	want := []string{"mod.public-kept", "mod.public-gone"}
	got := res.ExportedComponentsDeclared.Keys()
	if len(got) != len(want) {
		t.Fatalf("declared aliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("declared alias order is not authoring order: got %v, want %v", got, want)
		}
	}
}
