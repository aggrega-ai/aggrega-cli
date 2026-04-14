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
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/api-server",
					Uses: map[string]interface{}{
						"databases": []interface{}{"main-db"},
					},
				},
				"main-db": {
					Type: "core/postgres",
				},
			},
		},
	}

	catalog := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/api-server":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/api-server",
						Labels: map[string]string{types.ComponentTypeLabel: "server"},
					},
				}, true
			case "core/postgres":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/postgres",
						Labels: map[string]string{types.ComponentTypeLabel: "stateful-server"},
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
	if result.Nodes["api"].Labels[types.ComponentTypeLabel] != "server" {
		t.Errorf("api labels: %v", result.Nodes["api"].Labels)
	}
	if result.Nodes["main-db"].Labels[types.ComponentTypeLabel] != "stateful-server" {
		t.Errorf("main-db labels: %v", result.Nodes["main-db"].Labels)
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
			Components: map[string]types.ComponentInstance{
				"agent-a": {
					Type: "core/agent",
					Uses: map[string]interface{}{
						"delegates": []interface{}{"agent-b"},
					},
				},
				"agent-b": {
					Type: "core/agent",
					Uses: map[string]interface{}{
						"delegates": []interface{}{"agent-a"},
					},
				},
			},
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
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/api-server",
					Features: map[string]types.FeatureInstance{
						"monitoring": {
							Type: "core/monitoring",
							Uses: map[string]interface{}{
								"targets": []interface{}{"metrics-db"},
							},
						},
					},
				},
				"metrics-db": {
					Type: "core/postgres",
				},
			},
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
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/api-server"},
			},
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
			Components: map[string]types.ComponentInstance{
				"z-svc": {Type: "t", Uses: map[string]interface{}{"s": []interface{}{"target"}}},
				"a-svc": {Type: "t", Uses: map[string]interface{}{"s": []interface{}{"target"}}},
				"m-svc": {Type: "t", Uses: map[string]interface{}{"s": []interface{}{"target"}}},
			},
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
