// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// edgeSchemaCatalog returns a CatalogLookup with one consuming Component
// "app/svc:1.0.0" whose usesSchema declares two sockets: "databases" carries a
// per-edge valueSchema (pool_size 1..100) and infraSchema (storage_class
// string); "plain" declares no per-edge contract at all. Built from full
// Component YAML so the two contracts are field-node mappings, exactly as the
// catalog loader produces them.
func edgeSchemaCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	const compYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: app/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    databases:
      selector: "component.aggrega.ai/type=db"
      valueSchema:
        pool_size: { type: integer, minimum: 1, maximum: 100 }
      infraSchema:
        storage_class: { type: string }
    plain:
      selector: "component.aggrega.ai/type=db"
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(compYAML), &comp); err != nil {
		t.Fatalf("component fixture parse: %v", err)
	}
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "app/svc:1.0.0" {
				return comp, true
			}
			return types.Component{}, false
		},
	}
}

// aggWithEdge builds a one-component Aggregate ("svc") whose `uses` declares a
// verbose entry under the given socket/alias carrying the supplied per-edge
// values.
func aggWithEdge(socket, alias string, values types.OrderedMap[any]) *types.Aggregate {
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set(alias, types.VerboseUsesEntry{Target: "db", Values: values})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set(socket, types.UsesSocketValue{Verbose: verbose})
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("svc", types.ComponentInstance{Type: "app/svc:1.0.0", Uses: uses})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "demo"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

func om(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// normalizedReleaseTopLevelEdge builds a Release that authors node "svc"'s
// (socket, alias) edge at the top-level `.spec.components` surface, then folds
// it through NormalizeRelease — producing the synthesized `default` boundary the
// pre-flatten rules see.
func normalizedReleaseTopLevelEdge(socket, alias string, infra types.OrderedMap[any]) *types.Release {
	aliases := types.OrderedMap[types.ReleaseUsesEntry]{}
	aliases.Set(alias, types.ReleaseUsesEntry{Infra: infra})
	ru := types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}
	ru.Set(socket, aliases)
	comps := types.OrderedMap[types.BoundaryComponent]{}
	comps.Set("svc", types.BoundaryComponent{Uses: ru})
	rel := &types.Release{Metadata: types.ReleaseMetadata{Name: "demo-prod"}}
	rel.Spec.Components = comps
	return types.NormalizeRelease(rel).Release()
}

// TestReleaseComponentFieldPath pins the Release-side path renderer: a
// synthesized-default component renders at the root shape, a declared boundary
// renders qualified, and a boundary a user *declares* as "default" still renders
// qualified — the key is the flag, not the path string.
//
// The routed rows pin the locator a component placed through a sub-module routing
// entry renders under: the key path the author wrote, which names the module and
// the component's LOCAL name inside it, never the qualified id — that id is not a
// key anywhere in the document, and the Release schema forbids a dot in a routed
// component key.
func TestReleaseComponentFieldPath(t *testing.T) {
	t.Parallel()
	direct := []string{"components", "svc"}
	if got := releaseComponentFieldPath("default", direct, true); got != `.spec.components["svc"]` {
		t.Errorf("synthesized default must render root-shaped; got %q", got)
	}
	if got := releaseComponentFieldPath("prod", direct, false); got != `.spec.boundaries["prod"].components["svc"]` {
		t.Errorf("declared boundary must render qualified; got %q", got)
	}
	if got := releaseComponentFieldPath("default", direct, false); got != `.spec.boundaries["default"].components["svc"]` {
		t.Errorf("a declared boundary named default must stay qualified; got %q", got)
	}
	routed := []string{"aggregates", "data", "components", "consumer"}
	if got := releaseComponentFieldPath("prod", routed, false); got != `.spec.boundaries["prod"].aggregates["data"].components["consumer"]` {
		t.Errorf("a routed component must render through its routing entry; got %q", got)
	}
	nested := []string{"aggregates", "data", "aggregates", "inner", "components", "consumer"}
	if got := releaseComponentFieldPath("prod", nested, false); got != `.spec.boundaries["prod"].aggregates["data"].aggregates["inner"].components["consumer"]` {
		t.Errorf("a nested routed component must render through every routing entry; got %q", got)
	}
}

// edgeSocketFindings asks the rule about a socket the Release's edge surface
// wires, and returns the sockets it reported.
func edgeSocketFindings(
	t *testing.T, agg *types.Aggregate, release *types.Release, catalog CatalogLookup,
) []string {
	t.Helper()
	na := normalized(t, agg)
	view := newNormalizedView(na, release, catalog, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, releaseEdgeSocketDeclared)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	return declaredSocketsOf(objections)
}

// TestTheReleaseEdgeSocketRuleSeesAnEdgeWrittenAtTheRootSurface pins that a
// Release authoring its edges at the top-level component surface — which the
// loader folds into a synthesized boundary — is still read by the rule.
//
// The fold is what makes this worth a test: the rule reads the Release through
// the presentation, and a reading that only walked DECLARED boundaries would find
// nothing at all for such a document while every test written against an explicit
// boundary carried on passing.
func TestTheReleaseEdgeSocketRuleSeesAnEdgeWrittenAtTheRootSurface(t *testing.T) {
	t.Parallel()

	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	catalog := edgeSchemaCatalog(t)

	undeclared := normalizedReleaseTopLevelEdge("caches", "primary", om("x", "y"))
	if got := edgeSocketFindings(t, agg, undeclared, catalog); !equalStrings(got, []string{"caches"}) {
		t.Errorf("a socket the consuming type does not declare, wired at the root surface, "+
			"reported %v; want [caches]", got)
	}

	declared := normalizedReleaseTopLevelEdge("databases", "main", om("storage_class", "fast"))
	if got := edgeSocketFindings(t, agg, declared, catalog); len(got) != 0 {
		t.Errorf("a declared socket wired at the root surface must pass; got %v", got)
	}
}

// TestTheConnectionRulesAreOptInPerPillar pins the boundary of the per-edge
// contracts: what is opt-in is the SOCKET's contract on that pillar, not the
// edge's content.
//
// A socket declaring no contract on a pillar is skipped whatever its edges write
// there, and a socket declaring one measures every edge under it — including an
// edge that wrote a key the contract does not declare, which is reported AT that
// key rather than at the entry, because the key is the thing the author has to
// remove.
//
// The two are asserted over one catalog carrying both socket shapes, so a rule
// that had stopped reading contracts at all would fail the second half rather
// than pass the first.
func TestTheConnectionRulesAreOptInPerPillar(t *testing.T) {
	t.Parallel()

	catalog := edgeSchemaCatalog(t)

	configuration := func(t *testing.T, agg *types.Aggregate) []string {
		t.Helper()
		na := normalized(t, agg)
		view := newNormalizedView(na, nil, catalog, nil)
		objections, err := constraint.RunOnEdges[constraint.NormalizedView](
			nil, newCoordinateSet(view), view, connectionValuesConformToTheSocket)
		if err != nil {
			t.Fatalf("the connection walk returned an error: %v", err)
		}
		return locatorsOf(objections)
	}

	if got := configuration(t, aggWithEdge("plain", "main", om("anything", "goes"))); len(got) != 0 {
		t.Errorf("a socket declaring no contract on this pillar must skip its edges whatever "+
			"they write; got %v", got)
	}
	if got := configuration(t, aggWithEdge("databases", "main", om("pool_size", 20))); len(got) != 0 {
		t.Errorf("a conforming edge must pass; got %v", got)
	}

	got := configuration(t, aggWithEdge("databases", "main", om("pool_sze", 20)))
	want := []string{`.spec.components.svc.uses.databases.main.values.pool_sze`}
	if !equalStrings(got, want) {
		t.Errorf("a key the contract does not declare raised %v; want %v — at the key the "+
			"author wrote, which is the thing they have to remove", got, want)
	}

	got = configuration(t, aggWithEdge("databases", "main", om("pool_size", 9999)))
	want = []string{`.spec.components.svc.uses.databases.main.values.pool_size`}
	if !equalStrings(got, want) {
		t.Errorf("a declared key outside its constraint raised %v; want %v — at the key", got, want)
	}
}

// TestTheConnectionSizingRuleIsOptInPerPillarToo is the operator-side mirror of
// the pillar opt-in above, asserted separately because the two pillars are
// compiled by separate calls and a contract read on one says nothing about the
// other.
func TestTheConnectionSizingRuleIsOptInPerPillarToo(t *testing.T) {
	t.Parallel()

	catalog := edgeSchemaCatalog(t)
	sizing := func(t *testing.T, socket, alias string, infra types.OrderedMap[any]) []string {
		t.Helper()
		agg := aggWithEdge(socket, alias, types.OrderedMap[any]{})
		release := normalizedReleaseTopLevelEdge(socket, alias, infra)
		na := normalized(t, agg)
		view := newNormalizedView(na, release, catalog, nil)
		objections, err := constraint.RunOnEdges[constraint.NormalizedView](
			nil, newCoordinateSet(view), view, connectionInfraConformsToTheSocket)
		if err != nil {
			t.Fatalf("the connection walk returned an error: %v", err)
		}
		return locatorsOf(objections)
	}

	if got := sizing(t, "plain", "main", om("anything", "goes")); len(got) != 0 {
		t.Errorf("a socket declaring no sizing contract must skip its edges; got %v", got)
	}
	if got := sizing(t, "databases", "main", om("storage_class", "fast")); len(got) != 0 {
		t.Errorf("conforming per-edge sizing must pass; got %v", got)
	}
	got := sizing(t, "databases", "main", om("storage_clas", "fast"))
	want := []string{`.spec.components.svc.uses.databases.main.infra.storage_clas`}
	if !equalStrings(got, want) {
		t.Errorf("a key the sizing contract does not declare raised %v; want %v", got, want)
	}
}
