// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// routingOrderDeclared is the order the Release below declares its boundaries,
// and with them the boundary-scope attachments each carries. It is neither sorted
// nor reverse-sorted, so a walk that orders the attachments by sorting their keys
// in either direction does not reproduce it.
var routingOrderDeclared = []string{
	"b07", "b02", "b11", "b05", "b09", "b01", "b12", "b04", "b08", "b03", "b10", "b06",
}

// TestBoundaryScopeContributionsAreRoutedInTheReleasesOrder drives RenderAll
// through the shipped helm guest over a Release declaring twelve boundaries, each
// attaching a boundary-scope Feature of its own and holding its one component in a
// nested boundary that writes elsewhere. Every boundary's own directory is the same
// one, so the twelve attachments' output — and nothing else — meets in one dispatch
// group, which records the boundaries that fed it in the order its contributions
// were routed.
//
// That order must be the one the Release declares. The record of what each
// attachment rendered is keyed by the attachment and holds no order of its own, so
// a routing that walked the record directly would record the boundaries in a
// different order on different runs of identical input. The components are placed
// in sorted order, unlike the declarations, so a routing that followed the node walk
// rather than the declarations fails this as well.
//
// The failure the test exists for is probabilistic by nature: an unordered walk
// reproduces one fixed order of twelve keys only by chance. With more than eight
// entries a Go map spreads its keys by a hash seeded per map, so a walk of twelve
// keys meets one given order with a chance on the order of twelve rotations over
// twelve factorial orderings — roughly one in forty million, an estimate rather
// than a measurement — which makes a single run a reliable detector.
func TestBoundaryScopeContributionsAreRoutedInTheReleasesOrder(t *testing.T) {
	t.Parallel()

	const featType = "core/aux:1.0.0"

	nodes := types.OrderedMap[types.IRNode]{}
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	featureOrder := ir.FeatureOrder{}
	boundaryInfra := map[string]*resolver.BoundaryInfra{}
	sortedBoundaries := slices.Sorted(slices.Values(routingOrderDeclared))
	for _, b := range sortedBoundaries {
		component, instance := "c"+b[1:], "aux"+b[1:]
		nodes.Set(component, types.IRNode{Component: "core/api", BoundaryPath: b + "/inner"})
		attached := types.OrderedMap[types.IRFeatureEntry]{}
		attached.Set(instance, types.IRFeatureEntry{Type: featType})
		fvm.Set(component, attached)
		featureOrder[component] = []string{instance}
		boundaryInfra[component] = &resolver.BoundaryInfra{BoundaryPath: b + "/inner", Directory: "elsewhere-" + b}
	}

	boundaries := types.OrderedMap[types.Boundary]{}
	declaredFeatures := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	for _, b := range routingOrderDeclared {
		component, instance := "c"+b[1:], "aux"+b[1:]
		inner := types.Boundary{
			Directory:  "elsewhere-" + b,
			Features:   types.OrderedMap[types.BoundaryFeature]{},
			Components: types.OrderedMap[types.BoundaryComponent]{},
			Boundaries: types.OrderedMap[types.Boundary]{},
		}
		inner.Components.Set(component, types.BoundaryComponent{})
		outer := types.Boundary{
			Directory:  "shared",
			Features:   types.OrderedMap[types.BoundaryFeature]{},
			Components: types.OrderedMap[types.BoundaryComponent]{},
			Boundaries: types.OrderedMap[types.Boundary]{},
		}
		outer.Features.Set(instance, types.BoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
		outer.Boundaries.Set("inner", inner)
		boundaries.Set(b, outer)

		projected := types.OrderedMap[types.IRBoundaryFeature]{}
		projected.Set(instance, types.IRBoundaryFeature{Type: featType, Infra: types.OrderedMap[any]{}})
		declaredFeatures.Set(b, projected)
	}

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "routing-order-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     declaredFeatures,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: featureOrder,
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "routing-order-app"}},
			Release:       &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: boundaryInfra,
		},
	}

	componentRecipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "helm",
			Implements: "core/api",
			Artifacts: oneInlineArtifact("api-config.yaml",
				"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: api\ndata:\n  marker: \"x\"\n"),
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/api" {
				return componentRecipe, true
			}
			return nil, false
		},
		// One FeatureRecipe per attachment, so each one's ConfigMap carries a name
		// of its own in the one group they share.
		FeatureRecipe: func(instance, featureType string) (*types.FeatureRecipe, bool) {
			if !strings.HasPrefix(featureType, "core/aux") {
				return nil, false
			}
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
				Spec: types.FeatureRecipeSpec{
					Capability:     "helm",
					Implements:     featType,
					ExecutionScope: "boundary",
					ValuesMapping:  types.ValuesMapping{Inline: `{}`},
					Artifacts: oneInlineArtifact("aux-config.yaml", fmt.Sprintf(
						"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\ndata:\n  marker: \"x\"\n", instance)),
				},
			}, true
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	var shared *renderer.AssemblyContribution
	for i := range result.Contributions {
		c := &result.Contributions[i]
		if c.AssemblyPath == "shared/helm" && c.Provider == "helm" {
			shared = c
		}
	}
	if shared == nil {
		t.Fatalf("no dispatch group at shared/helm; groups: %v", result.Contributions)
	}
	if !reflect.DeepEqual(shared.Boundaries, routingOrderDeclared) {
		t.Fatalf("the shared group recorded its boundaries as %v; want the Release's order %v",
			shared.Boundaries, routingOrderDeclared)
	}
	// The control that the twelve contributions were all routed: each attachment's
	// file is written in the shared group, under its instance name.
	for _, b := range routingOrderDeclared {
		want := "shared/helm/aux" + b[1:] + "--aux-config.yaml"
		if _, ok := result.Files[want]; !ok {
			t.Errorf("no file at %s; wrote %v", want, sortedEmittedPaths(result.Files))
		}
	}
}
