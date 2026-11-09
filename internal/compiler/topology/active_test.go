// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the structural `active` presence toggle, at two tiers.
//
// The prune, cascade, classification and determinism behaviours are pinned at
// flatten from resolved literals handed in directly, which is the right tier for
// them: each is a property of what flatten does with a literal it was given.
//
// Whether a toggle is present at all is NOT such a property, and it cannot be
// tested that way. The distinction between a toggle the author omitted and one
// that resolved to nothing is made by the producer — the resolver renders the
// parameter template, and the decoder allocates the toggle only for a key the
// document declares — so a fixture that assigns the toggle by hand has already
// decided the question under test and cannot fail for getting it wrong. Those
// rows go through the real producer chain instead: authored YAML, interpolated
// against a parameter map, decoded, then flattened.

package topology

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// activeFlag is the authored-toggle constructor for a fixture. A test spells an
// omitted toggle as a nil pointer and never as this with an empty string: the
// two are the distinct inputs whose separation is under test.
func activeFlag(s string) *types.ActiveFlag {
	f := types.ActiveFlag(s)
	return &f
}

// comps builds a component map from name/toggle pairs, where an EMPTY toggle
// string means the author omitted `active:` entirely — the nil pointer — because
// that is the case these fixtures use it for. A present-but-empty toggle is
// authored in YAML by the producer-chain tests rather than assembled here.
func comps(pairs ...[2]string) types.OrderedMap[types.ComponentInstance] {
	m := types.OrderedMap[types.ComponentInstance]{}
	for _, p := range pairs {
		inst := types.ComponentInstance{Type: "core/compute/api-server"}
		if p[1] != "" {
			inst.Active = activeFlag(p[1])
		}
		m.Set(p[0], inst)
	}
	return m
}

func TestFlatten_ActivePresentVsPruned(t *testing.T) {
	t.Parallel()

	// active resolved to true → present.
	on := &types.Aggregate{Spec: types.AggregateSpec{Components: comps([2]string{"api", "true"}, [2]string{"cache", ""})}}
	res, err := flattenAggregate(types.Normalize(on), "", "", "", nil, map[string]bool{}, 0, nil)
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
	res2, err := flattenAggregate(types.Normalize(off), "", "", "", nil, map[string]bool{}, 0, nil)
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
		res, err := flattenAggregate(types.Normalize(agg), "", "", "", nil, map[string]bool{}, 0, nil)
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
	// An omitted toggle (the nil pointer) and an explicit "true" behave
	// identically: present, nothing pruned — the additive no-op default.
	for _, v := range []string{"", "true"} {
		agg := &types.Aggregate{Spec: types.AggregateSpec{Components: comps([2]string{"api", v})}}
		res, err := flattenAggregate(types.Normalize(agg), "", "", "", nil, map[string]bool{}, 0, nil)
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
	// An authored toggle that does not parse as a boolean is a Loud Guardrail.
	// The empty literal is in the table because it is a value like any other
	// once the toggle is present: only an ABSENT toggle defaults to present, and
	// whitespace is here because trimming it leaves the same empty literal.
	for _, bad := range []string{"yes", "maybe", "", "   "} {
		components := types.OrderedMap[types.ComponentInstance]{}
		components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Active: activeFlag(bad)})
		agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}
		_, err := flattenAggregate(types.Normalize(agg), "", "", "", nil, map[string]bool{}, 0, nil)
		if !errors.Is(err, ErrInvalidActive()) {
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
	aggregates.Set("data", types.SubAggregate{Type: "modules/data-layer", Active: activeFlag("false")})
	root := &types.Aggregate{Spec: types.AggregateSpec{
		Components: comps([2]string{"api", "true"}),
		Aggregates: aggregates,
	}}

	res, err := flattenAggregate(types.Normalize(root), "", "", "", loader, map[string]bool{}, 0, nil)
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

// TestBuild_SurvivorUsesPrunedKeepsTheRemovalRecorded pins the half of the
// classification this stage owns: a survivor's edge into a switched-off
// component survives, and the removed name stays on the pruned set.
//
// The two together are what let the rules over the finalised graph tell a target
// the author switched off from one that never existed and give each its own
// sentence. Dropping either would collapse the two into one finding, silently,
// because a rule reading a name the stage no longer carries sees exactly what it
// sees for a name that was never written. The refusal itself is proved where the
// graph exists.
func TestBuild_SurvivorUsesPrunedKeepsTheRemovalRecorded(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"main-db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	components.Set("main-db", types.ComponentInstance{Type: "core/datastore/postgres", Active: activeFlag("false")})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	result, err := buildAgg(t, agg, nil)
	if err != nil {
		t.Fatalf("the composition must reach the graph for the rules over it to be asked; got %v", err)
	}
	if len(result.Edges) != 1 || result.Edges[0].Target != "main-db" {
		t.Fatalf("edges = %+v; want exactly the survivor's edge into the switched-off target", result.Edges)
	}
	if _, held := result.Nodes.Get("main-db"); held {
		t.Error("a switched-off component must not be in the node set")
	}
	if _, removed := result.Pruned.Get("main-db"); !removed {
		t.Error("the removed name must stay on the pruned set; without it the removal reads as a target that never existed")
	}
}

// TestBuild_DanglingTargetIsNotRecordedAsRemoved is the other side of the same
// distinction: a target that never existed must not appear on the pruned set, or
// the rule naming the activation toggle would claim an author switched off
// something they never wrote.
func TestBuild_DanglingTargetIsNotRecordedAsRemoved(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"ghost"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	result, err := buildAgg(t, agg, nil)
	if err != nil {
		t.Fatalf("the composition must reach the graph for the rules over it to be asked; got %v", err)
	}
	if len(result.Edges) != 1 || result.Edges[0].Target != "ghost" {
		t.Fatalf("edges = %+v; want exactly the edge into the target that never existed", result.Edges)
	}
	if _, held := result.Nodes.Get("ghost"); held {
		t.Error("the target must answer to no node, or the fixture no longer poses the question")
	}
	if _, removed := result.Pruned.Get("ghost"); removed {
		t.Error("a name nothing ever declared must not be recorded as removed")
	}
}

func TestBuild_PrunedExportedComponentDropped(t *testing.T) {
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
	if _, ok := res.ExportedComponents.Get("mod.public-gone"); ok {
		t.Errorf("export alias of a pruned component must be dropped: %v", res.ExportedComponents.Keys())
	}
	if got, _ := res.ExportedComponents.Get("mod.public-kept"); got != "mod.kept" {
		t.Errorf("surviving export alias lost: %v", res.ExportedComponents.Keys())
	}
}

// flattenAuthored drives the real producer chain for an authored Aggregate: the
// resolver interpolates the document against a parameter map exactly as a build
// does, the decoder turns the interpolated bytes into an Aggregate, and flatten
// reads whatever that produced.
//
// Nothing here assigns a toggle. That is the point: an unfulfilled parameter
// renders the toggle to nothing and a decoder leaves an undeclared toggle nil,
// and both of those happen upstream of flatten. A fixture that set the toggle
// itself would be asserting its own assignment.
func flattenAuthored(t *testing.T, aggYAML string, params map[string]any) (*flattenResult, error) {
	t.Helper()
	interpolated, err := resolver.InterpolateParameters([]byte(aggYAML), params)
	if err != nil {
		t.Fatalf("interpolation must not fail for this fixture: %v", err)
	}
	var agg types.Aggregate
	if err := yaml.Unmarshal(interpolated, &agg); err != nil {
		t.Fatalf("decoding the interpolated document: %v\n%s", err, interpolated)
	}
	return flattenAggregate(types.Normalize(&agg), "", "", "", nil, map[string]bool{}, 0, nil)
}

// TestActiveToggleThroughTheProducerChain separates a toggle the author omitted
// from one that resolved to nothing, over the chain that actually makes the
// distinction.
//
// The rows are the reachable ways a toggle arrives at flatten, and each is a
// separate authored document rather than a variation on one assigned value. A
// green over the halting row alone would prove only that something now halts,
// which is why the neighbouring cases are rows here and not separate tests: an
// omitted toggle keeps the node, a literal false drops it, and a fulfilled
// parameter is honoured in both directions.
//
// The empty-literal rows are two authorings of one input. A toggle whose
// parameter nobody fulfilled and a toggle authored empty outright resolve to the
// same literal, so the guard cannot tell them apart and both halt. That is why
// the diagnostic speaks of what the toggle resolved to rather than asserting a
// parameter was left unfulfilled.
func TestActiveToggleThroughTheProducerChain(t *testing.T) {
	t.Parallel()

	const doc = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: core/compute/api-server
    web:
      type: core/devtool/adminer
%s`

	authored := func(toggle string) string {
		if toggle == "" {
			return "      " // no `active:` key at all
		}
		return "      active: " + toggle + "\n"
	}

	cases := []struct {
		name      string
		toggle    string
		params    map[string]any
		wantHalt  bool
		wantNodes []string
	}{
		{
			name:      "no toggle authored keeps the node",
			toggle:    "",
			params:    map[string]any{},
			wantNodes: []string{"api", "web"},
		},
		{
			name:      "a literal false drops the node",
			toggle:    "false",
			params:    map[string]any{},
			wantNodes: []string{"api"},
		},
		{
			name:      "a literal true keeps the node",
			toggle:    "true",
			params:    map[string]any{},
			wantNodes: []string{"api", "web"},
		},
		{
			name:      "a fulfilled parameter is honoured as true",
			toggle:    `"{{ .parameters.enable_web }}"`,
			params:    map[string]any{"enable_web": "true"},
			wantNodes: []string{"api", "web"},
		},
		{
			name:      "a fulfilled parameter is honoured as false",
			toggle:    `"{{ .parameters.enable_web }}"`,
			params:    map[string]any{"enable_web": "false"},
			wantNodes: []string{"api"},
		},
		{
			name:     "an unfulfilled parameter halts rather than deploying the node",
			toggle:   `"{{ .parameters.enable_web }}"`,
			params:   map[string]any{},
			wantHalt: true,
		},
		{
			name:     "a parameter fulfilled with an empty string halts the same way",
			toggle:   `"{{ .parameters.enable_web }}"`,
			params:   map[string]any{"enable_web": ""},
			wantHalt: true,
		},
		{
			name:     "a toggle authored empty outright halts the same way",
			toggle:   `""`,
			params:   map[string]any{},
			wantHalt: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := flattenAuthored(t, fmt.Sprintf(doc, authored(tc.toggle)), tc.params)
			if tc.wantHalt {
				if !errors.Is(err, ErrInvalidActive()) {
					t.Fatalf("expected ErrInvalidActive, got %v", err)
				}
				// The node the author gated is named, so the diagnostic points at
				// the document the author has to edit.
				if !strings.Contains(err.Error(), `node "web"`) {
					t.Errorf("the halt must name the gated node, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected the build to proceed, got %v", err)
			}
			if got := res.Nodes.Keys(); !slices.Equal(got, tc.wantNodes) {
				t.Errorf("surviving nodes %v, want %v", got, tc.wantNodes)
			}
		})
	}
}

// TestSubAggregateActiveToggleThroughTheProducerChain is the same separation on
// the module axis. It is a distinct test because the toggle is read at a
// different site — a module's toggle is evaluated before its document is loaded
// at all — so a fix applied to one read site would leave the other silent.
func TestSubAggregateActiveToggleThroughTheProducerChain(t *testing.T) {
	t.Parallel()

	const doc = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: core/compute/api-server
  aggregates:
    data:
      type: modules/data-layer
%s`

	childComps := types.OrderedMap[types.ComponentInstance]{}
	childComps.Set("child-db", types.ComponentInstance{Type: "core/datastore/postgres"})
	childAgg := &types.Aggregate{Spec: types.AggregateSpec{Components: childComps}}
	loader := func(string, map[string]any) (*types.Aggregate, string, map[string]any, error) {
		return childAgg, "", nil, nil
	}

	run := func(t *testing.T, toggle string, params map[string]any) (*flattenResult, error) {
		t.Helper()
		body := "      "
		if toggle != "" {
			body = "      active: " + toggle + "\n"
		}
		interpolated, err := resolver.InterpolateParameters([]byte(fmt.Sprintf(doc, body)), params)
		if err != nil {
			t.Fatalf("interpolation must not fail for this fixture: %v", err)
		}
		var agg types.Aggregate
		if err := yaml.Unmarshal(interpolated, &agg); err != nil {
			t.Fatalf("decoding the interpolated document: %v\n%s", err, interpolated)
		}
		return flattenAggregate(types.Normalize(&agg), "", "", "", loader, map[string]bool{}, 0, nil)
	}

	t.Run("an unfulfilled parameter halts rather than composing the module", func(t *testing.T) {
		t.Parallel()
		_, err := run(t, `"{{ .parameters.enable_data }}"`, map[string]any{})
		if !errors.Is(err, ErrInvalidActive()) {
			t.Fatalf("expected ErrInvalidActive, got %v", err)
		}
		if !strings.Contains(err.Error(), `node "data"`) {
			t.Errorf("the halt must name the gated module, got %v", err)
		}
	})

	t.Run("no toggle authored composes the module", func(t *testing.T) {
		t.Parallel()
		res, err := run(t, "", map[string]any{})
		if err != nil {
			t.Fatalf("expected the build to proceed, got %v", err)
		}
		if got := res.Nodes.Keys(); !slices.Equal(got, []string{"api", "data.child-db"}) {
			t.Errorf("surviving nodes %v, want [api data.child-db]", got)
		}
	})

	t.Run("a fulfilled parameter prunes the module", func(t *testing.T) {
		t.Parallel()
		res, err := run(t, `"{{ .parameters.enable_data }}"`, map[string]any{"enable_data": "false"})
		if err != nil {
			t.Fatalf("expected the build to proceed, got %v", err)
		}
		if got := res.Nodes.Keys(); !slices.Equal(got, []string{"api"}) {
			t.Errorf("surviving nodes %v, want [api]", got)
		}
	})
}
