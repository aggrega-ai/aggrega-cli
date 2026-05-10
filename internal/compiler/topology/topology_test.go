// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Integration-shaped tests for the Build orchestrator. Each test
// constructs a *types.Aggregate fixture in-memory plus a CatalogLookup
// stub, calls Build directly, and asserts on the returned
// TopologyResult. Coverage groups one cluster per Build behaviour:
//
//   - TestBuild_SimpleTopology — happy-path pin: two-component graph,
//     one edge, Labels populated from the catalog stub, edge
//     Source/Target/Socket present and correct.
//   - TestBuild_CyclicEdges — pins the cycles-supported contract:
//     two components mutually `uses` each other; Build returns no
//     error and emits both back-edges (the runtime topology supports
//     cyclic edges; only the sub-aggregate inclusion graph rejects
//     cycles).
//   - TestBuild_FeatureUsesCreatesEdge — pins the per-edge origin
//     marker: a Feature's own `uses` block produces an edge whose
//     OriginFeature equals the Feature type URI, keeping
//     Feature-authored edges separable from Component-authored edges.
//   - TestBuild_BoundaryPath — pins phase 3: BoundaryInfra-supplied
//     boundary path lands on the per-node TopologyNode.
//   - TestBuild_DeterministicEdgeOrder — pins the compositional-author-
//     order discipline at the edges slice: ten consecutive Builds against
//     the same input produce byte-identical edge ordering, and the order
//     follows the Aggregate's component declaration sequence rather than
//     alphabetical-by-source-node.

package topology

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestBuild_SimpleTopology(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"main-db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server", Uses: uses})
	components.Set("main-db", types.ComponentInstance{Type: "core/database/postgres"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	catalog := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server/api-server":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/server/api-server",
						Labels: labels,
					},
				}, true
			case "core/database/postgres":
				labels2 := types.OrderedMap[string]{}
				labels2.Set(types.ComponentTypeLabel, "stateful-server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/database/postgres",
						Labels: labels2,
					},
				}, true
			default:
				return types.Component{}, false
			}
		},
	}

	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		CatalogLookup: catalog,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Nodes.Len() != 2 {
		t.Fatalf("expected 2 nodes, got %d", result.Nodes.Len())
	}
	apiNode, _ := result.Nodes.Get("api")
	apiLabel, _ := apiNode.Labels.Get(types.ComponentTypeLabel)
	if apiLabel != "server" {
		t.Errorf("api labels: %v", apiNode.Labels.Keys())
	}
	dbNode, _ := result.Nodes.Get("main-db")
	dbLabel, _ := dbNode.Labels.Get(types.ComponentTypeLabel)
	if dbLabel != "stateful-server" {
		t.Errorf("main-db labels: %v", dbNode.Labels.Keys())
	}

	if len(result.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(result.Edges))
	}
	e := result.Edges[0]
	if e.Source != "api" || e.Target != "main-db" || e.Socket != "databases" {
		t.Errorf("edge: %+v", e)
	}
}

func TestBuild_CyclicEdges(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("delegates", types.UsesSocketValue{Compact: []string{"agent-b"}})
	uses2 := types.OrderedMap[types.UsesSocketValue]{}
	uses2.Set("delegates", types.UsesSocketValue{Compact: []string{"agent-a"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("agent-a", types.ComponentInstance{Type: "core/agent", Uses: uses})
	components.Set("agent-b", types.ComponentInstance{Type: "core/agent", Uses: uses2})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "cyclic-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error (cyclic edges should be allowed): %v", err)
	}
	if len(result.Edges) != 2 {
		t.Fatalf("expected 2 edges (cyclic), got %d", len(result.Edges))
	}
}

func TestBuild_FeatureUsesCreatesEdge(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("targets", types.UsesSocketValue{Compact: []string{"metrics-db"}})
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("monitoring", types.FeatureInstance{Type: "core/monitoring", Uses: uses})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server", Features: features})
	components.Set("metrics-db", types.ComponentInstance{Type: "core/database/postgres"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Edges) != 1 {
		t.Fatalf("expected 1 edge from feature uses, got %d", len(result.Edges))
	}
	if result.Edges[0].OriginFeature != "core/monitoring" {
		t.Errorf("OriginFeature = %q, want %q", result.Edges[0].OriginFeature, "core/monitoring")
	}
}

func TestBuild_BoundaryPath(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server"})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate: agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				"api": {BoundaryPath: "aws-prod/eks-main"},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	apiNode2, _ := result.Nodes.Get("api")
	if apiNode2.BoundaryPath != "aws-prod/eks-main" {
		t.Errorf("boundary path: %q", apiNode2.BoundaryPath)
	}
}

// TestErrSubAggregateNameCollision_HaltsOnOverwrite pins the defensive
// guard that fires when sub-aggregate flattening produces a node ID
// already present in the flat node map. The collision is impossible by
// current schema (root-level instance names cannot contain dots) but
// the guard defends against future schema loosening or validation
// bypass. The test constructs the colliding scenario by directly
// authoring a root-level instance name containing a dot — schema-
// invalid but useful to exercise the guard.
func TestErrSubAggregateNameCollision_HaltsOnOverwrite(t *testing.T) {
	t.Parallel()
	childComps := types.OrderedMap[types.ComponentInstance]{}
	childComps.Set("child", types.ComponentInstance{Type: "core/db"})
	childAgg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "data-mod"},
		Spec:     types.AggregateSpec{Components: childComps},
	}

	rootComps := types.OrderedMap[types.ComponentInstance]{}
	// "data-mod.child" is invalid as a root name (schema bans dots), but
	// we construct it directly to exercise the defensive guard against
	// schema loosening.
	rootComps.Set("data-mod.child", types.ComponentInstance{Type: "core/db"})
	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("data-mod", types.SubAggregate{Type: "modules/data-layer"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Components: rootComps,
			Aggregates: aggregates,
		},
	}

	loader := func(typeName string) (*types.Aggregate, error) {
		if typeName == "modules/data-layer" {
			return childAgg, nil
		}
		return nil, fmt.Errorf("unknown: %s", typeName)
	}

	_, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		LoadSubAggregate: loader,
	})
	if err == nil {
		t.Fatal("expected ErrSubAggregateNameCollision, got nil")
	}
	if !errors.Is(err, ErrSubAggregateNameCollision) {
		t.Errorf("expected ErrSubAggregateNameCollision, got %v", err)
	}
}

func TestBuild_DeterministicEdgeOrder(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("s", types.UsesSocketValue{Compact: []string{"target"}})
	uses2 := types.OrderedMap[types.UsesSocketValue]{}
	uses2.Set("s", types.UsesSocketValue{Compact: []string{"target"}})
	uses3 := types.OrderedMap[types.UsesSocketValue]{}
	uses3.Set("s", types.UsesSocketValue{Compact: []string{"target"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("z-svc", types.ComponentInstance{Type: "t", Uses: uses})
	components.Set("a-svc", types.ComponentInstance{Type: "t", Uses: uses2})
	components.Set("m-svc", types.ComponentInstance{Type: "t", Uses: uses3})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	var firstSources []string
	for i := 0; i < 10; i++ {
		result, err := Build(BuildConfig{
			ResolveResult: &resolver.ResolveResult{
				Aggregate:     agg,
				BoundaryInfra: map[string]*resolver.BoundaryInfra{},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sources := make([]string, len(result.Edges))
		for j, e := range result.Edges {
			sources[j] = e.Source
		}
		if i == 0 {
			firstSources = sources
		} else {
			for j, s := range sources {
				if s != firstSources[j] {
					t.Errorf("run %d: edge order changed at %d: %q vs %q", i, j, s, firstSources[j])
				}
			}
		}
	}
	// Verify compositional author order: components declared in sequence
	// z-svc, a-svc, m-svc — edge slice mirrors the declaration order.
	if firstSources[0] != "z-svc" || firstSources[1] != "a-svc" || firstSources[2] != "m-svc" {
		t.Errorf("edges not in author order: %v (want [z-svc a-svc m-svc])", firstSources)
	}
}
