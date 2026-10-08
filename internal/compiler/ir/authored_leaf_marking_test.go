// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the per-authored-leaf sensitivity marking at the two units a real
// build reaches only through a whole workspace: the node's own pillars and a
// boundary attachment's infra. The end-to-end fixture proves the marking is
// derived and carried; what is proved here is the shape of its ABSENCE, which
// an end-to-end fixture cannot reach — a node whose Component resolves to no
// catalog entity, and a build with no catalog at all.
//
// Absence is what these cases exist for because absence is the answer a
// consumer most easily misreads. The marking is omitted from the wire when it
// names nothing, so "no declaration matched" and "there was no schema to read"
// arrive identically, and both mean the marking found nothing at this unit —
// never that the payload carries nothing sensitive.

package ir

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// markingNodeGraph builds a one-node graph whose node authors the given values
// against the named Component type, under the supplied catalog.
func markingNodeGraph(t *testing.T, componentType string, values types.OrderedMap[any], lookup resolver.CatalogLookup) *types.IRGraph {
	t.Helper()
	node := &topology.TopologyNode{InstanceID: "api", ComponentType: componentType, Values: values}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", node)
	graph, _, _, err := buildIR(BuildIRConfig{
		Topology: &topology.TopologyResult{Nodes: nodes},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		CatalogLookup: lookup,
	})
	if err != nil {
		t.Fatalf("BuildIRContext: %v", err)
	}
	return graph
}

func markingNodeValues() types.OrderedMap[any] {
	v := types.OrderedMap[any]{}
	v.Set("password", "plaintext")
	return v
}

// TestNodeMarking_AbsentWhenTheComponentResolvesToNoEntity pins the answer for
// a node whose Component type names nothing in the catalog. Nothing declares
// anything in that world, so the marking names nothing — and it is nil rather
// than an allocated empty slice, which is what keeps the field off the wire
// entirely instead of carrying an empty array that reads as a positive finding.
func TestNodeMarking_AbsentWhenTheComponentResolvesToNoEntity(t *testing.T) {
	t.Parallel()
	lookup := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return types.Component{}, false },
	}
	graph := markingNodeGraph(t, "core/unknown", markingNodeValues(), lookup)
	node, _ := graph.Spec.Nodes.Get("api")
	if node.SensitivePayloadLeaves != nil {
		t.Errorf("node marking = %#v, want nil: an unresolvable Component declares nothing, and an "+
			"allocated empty slice would put an empty array on the wire where the field should be absent",
			node.SensitivePayloadLeaves)
	}
}

// TestNodeMarking_AbsentWithNoCatalogAtAll covers the build shape several
// callers use, where the lookup's accessors are nil. The question is answered
// without reaching a schema reader that has no catalog to read, so this is a
// crash assertion as much as an emptiness one.
func TestNodeMarking_AbsentWithNoCatalogAtAll(t *testing.T) {
	t.Parallel()
	graph := markingNodeGraph(t, "core/whatever", markingNodeValues(), resolver.CatalogLookup{})
	node, _ := graph.Spec.Nodes.Get("api")
	if node.SensitivePayloadLeaves != nil {
		t.Errorf("node marking = %#v, want nil with no catalog wired", node.SensitivePayloadLeaves)
	}
}

// TestBoundaryMarking_AbsentWhenTheFeatureResolvesToNoEntity is the boundary
// surface's counterpart. The projection still carries the attachment and its
// infra — the marking's absence must not remove the attachment from the graph,
// which is what a reader of the boundary projection depends on.
func TestBoundaryMarking_AbsentWhenTheFeatureResolvesToNoEntity(t *testing.T) {
	t.Parallel()
	infra := types.OrderedMap[any]{}
	infra.Set("token", "plaintext")
	feats := types.OrderedMap[types.BoundaryFeature]{}
	feats.Set("guard", types.BoundaryFeature{Type: "feature.core/unknown", Infra: infra})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Features: feats})

	lookup := resolver.CatalogLookup{
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
	out := buildBoundaryFeaturesProjection(boundaries, lookup, nil)

	inner, ok := out.Get("prod")
	if !ok {
		t.Fatalf("projection carries no boundary %q; have %v", "prod", out.Keys())
	}
	bf, ok := inner.Get("guard")
	if !ok {
		t.Fatalf("boundary carries no attachment %q; have %v", "guard", inner.Keys())
	}
	if bf.Type != "feature.core/unknown" {
		t.Errorf("attachment type = %q, want it carried unchanged", bf.Type)
	}
	if bf.Infra.Len() != 1 {
		t.Errorf("attachment infra = %v, want the authored infra carried unchanged", bf.Infra.Keys())
	}
	if bf.SensitivePayloadLeaves != nil {
		t.Errorf("boundary marking = %#v, want nil: an unresolvable Feature declares nothing",
			bf.SensitivePayloadLeaves)
	}
}

// TestBoundaryMarking_ZeroCatalogDoesNotPanic covers the projection built with
// a lookup whose accessors are nil, which is the shape a caller with no catalog
// supplies.
func TestBoundaryMarking_ZeroCatalogDoesNotPanic(t *testing.T) {
	t.Parallel()
	infra := types.OrderedMap[any]{}
	infra.Set("token", "plaintext")
	feats := types.OrderedMap[types.BoundaryFeature]{}
	feats.Set("guard", types.BoundaryFeature{Type: "feature.core/unknown", Infra: infra})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Features: feats})

	out := buildBoundaryFeaturesProjection(boundaries, resolver.CatalogLookup{}, nil)
	inner, _ := out.Get("prod")
	bf, ok := inner.Get("guard")
	if !ok {
		t.Fatalf("boundary carries no attachment %q; have %v", "guard", inner.Keys())
	}
	if bf.SensitivePayloadLeaves != nil {
		t.Errorf("boundary marking = %#v, want nil with no catalog wired", bf.SensitivePayloadLeaves)
	}
}

// TestDeriveAuthoredSensitiveLeaves_NilNotEmpty pins the one home's own return
// contract directly, at the two shapes the callers above cannot separate: a
// declaration set that is empty, and a payload that authors nothing at the
// leaves a non-empty set does declare. Both answer nil.
func TestDeriveAuthoredSensitiveLeaves_NilNotEmpty(t *testing.T) {
	t.Parallel()

	authored := types.OrderedMap[any]{}
	authored.Set("public", "readable")

	if got := derivePayloadSensitiveLeaves(authored, types.OrderedMap[any]{}, resolver.SensitiveLeaves{}); got != nil {
		t.Errorf("with no declarations: got %#v, want nil", got)
	}

	declared := resolver.SensitiveLeaves{Values: map[string]bool{"secret": true}}
	if got := derivePayloadSensitiveLeaves(authored, types.OrderedMap[any]{}, declared); got != nil {
		t.Errorf("with a declaration nothing was authored at: got %#v, want nil", got)
	}

	// The positive control, so the two nils above are the rule answering rather
	// than the helper being inert on this input shape.
	authored.Set("secret", "material")
	got := derivePayloadSensitiveLeaves(authored, types.OrderedMap[any]{}, declared)
	if len(got) != 1 || got[0] != "values.secret" {
		t.Errorf("with the declared leaf authored: got %#v, want [values.secret]", got)
	}
}
