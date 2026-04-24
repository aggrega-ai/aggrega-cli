// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestBuild_SimpleTopology(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[interface{}]{}
	uses.Set("databases", []interface{}{"main-db"})
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

	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}
	apiLabel, _ := result.Nodes["api"].Labels.Get(types.ComponentTypeLabel)
	if apiLabel != "server" {
		t.Errorf("api labels: %v", result.Nodes["api"].Labels.Keys())
	}
	dbLabel, _ := result.Nodes["main-db"].Labels.Get(types.ComponentTypeLabel)
	if dbLabel != "stateful-server" {
		t.Errorf("main-db labels: %v", result.Nodes["main-db"].Labels.Keys())
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
	uses := types.OrderedMap[interface{}]{}
	uses.Set("delegates", []interface{}{"agent-b"})
	uses2 := types.OrderedMap[interface{}]{}
	uses2.Set("delegates", []interface{}{"agent-a"})
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
	uses := types.OrderedMap[interface{}]{}
	uses.Set("targets", []interface{}{"metrics-db"})
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
	if result.Nodes["api"].BoundaryPath != "aws-prod/eks-main" {
		t.Errorf("boundary path: %q", result.Nodes["api"].BoundaryPath)
	}
}

func TestBuild_DeterministicEdgeOrder(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[interface{}]{}
	uses.Set("s", []interface{}{"target"})
	uses2 := types.OrderedMap[interface{}]{}
	uses2.Set("s", []interface{}{"target"})
	uses3 := types.OrderedMap[interface{}]{}
	uses3.Set("s", []interface{}{"target"})
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
	// Verify sorted: a-svc < m-svc < z-svc
	if firstSources[0] != "a-svc" || firstSources[1] != "m-svc" || firstSources[2] != "z-svc" {
		t.Errorf("edges not sorted by source: %v", firstSources)
	}
}
