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

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
					Type: "core/server/api-server",
					Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", []interface{}{"main-db"}); return _m }(),
				}); _m.Set("main-db", types.ComponentInstance{
					Type: "core/database/postgres",
				}); return _m }(),
		},
	}

	catalog := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server/api-server":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/server/api-server",
						Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(),
					},
				}, true
			case "core/database/postgres":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/database/postgres",
						Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "stateful-server"); return _m }(),
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

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "cyclic-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("agent-a", types.ComponentInstance{
					Type: "core/agent",
					Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("delegates", []interface{}{"agent-b"}); return _m }(),
				}); _m.Set("agent-b", types.ComponentInstance{
					Type: "core/agent",
					Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("delegates", []interface{}{"agent-a"}); return _m }(),
				}); return _m }(),
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

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
					Type: "core/server/api-server",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("monitoring", types.FeatureInstance{
							Type: "core/monitoring",
							Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("targets", []interface{}{"metrics-db"}); return _m }(),
						}); return _m }(),
				}); _m.Set("metrics-db", types.ComponentInstance{
					Type: "core/database/postgres",
				}); return _m }(),
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

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/server/api-server"}); return _m }(),
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

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("z-svc", types.ComponentInstance{Type: "t", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("s", []interface{}{"target"}); return _m }()}); _m.Set("a-svc", types.ComponentInstance{Type: "t", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("s", []interface{}{"target"}); return _m }()}); _m.Set("m-svc", types.ComponentInstance{Type: "t", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("s", []interface{}{"target"}); return _m }()}); return _m }(),
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
