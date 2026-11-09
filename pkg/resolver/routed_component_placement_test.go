// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestResolve_PlacesRoutedComponentsBeforeWiringTheBoundaries proves the resolve
// invokes the placement hook with the Aggregate it parsed, and does so before it
// wires the per-component boundary contexts: a component the hook names under a
// bare routing entry comes out of the resolve with a boundary context of its
// own, under its qualified id, exactly as an authored entry does.
func TestResolve_PlacesRoutedComponentsBeforeWiringTheBoundaries(t *testing.T) {
	t.Parallel()

	raw := []byte("apiVersion: aggrega.ai/v1beta1\n" +
		"kind: Aggregate\n" +
		"metadata:\n" +
		"  name: a\n" +
		"  version: \"1.0.0\"\n" +
		"spec:\n" +
		"  aggregates:\n" +
		"    data:\n" +
		"      type: ./data.yaml\n")

	rel := &types.Release{}
	var boundaries types.OrderedMap[types.Boundary]
	east := types.Boundary{Directory: "./east"}
	east.Aggregates.Set("data", types.AggregateRouting{})
	boundaries.Set("east", east)
	rel.Spec.Boundaries = boundaries

	invoked := 0
	sawImport := false
	place := func(aggregate types.NormalizedAggregate, release *types.Release) {
		invoked++
		_, sawImport = aggregate.Aggregate().Spec.Aggregates.Get("data")
		b, _ := release.Spec.Boundaries.Get("east")
		route, _ := b.Aggregates.Get("data")
		route.Components.Set("backend", types.BoundaryComponent{})
		b.Aggregates.Set("data", route)
		release.Spec.Boundaries.Set("east", b)
	}

	var placed *resolver.BoundaryInfra
	err := resolver.ResolveAndUse(context.Background(),
		resolver.ResolveInputs{
			Release: rel, Manifest: &types.Manifest{}, AggregateRawBytes: raw, AggregatePath: "a.yaml",
			PlaceRoutedComponents: place,
		},
		func(rr *resolver.ResolveResult) error {
			placed = rr.BoundaryInfra["data.backend"]
			return nil
		})
	if err != nil {
		t.Fatalf("ResolveAndUse: %v", err)
	}
	if invoked != 1 {
		t.Fatalf("the placement hook was invoked %d times, want once", invoked)
	}
	if !sawImport {
		t.Errorf("the placement hook was not handed the parsed Aggregate (its import of data is missing)")
	}
	if placed == nil || placed.BoundaryPath != "east" {
		t.Errorf("the placed component has no boundary context of its own after the resolve: %+v", placed)
	}
}
