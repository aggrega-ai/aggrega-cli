// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"fmt"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/analyzer"
	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// Shared catalog for integration tests.
func integrationCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server/api-server":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/server/api-server",
						Version: "1.0.0",
						Labels:  map[string]string{types.ComponentTypeLabel: "server"},
					},
					Spec: types.ComponentSpec{
						UsesSchema: map[string]types.UsesSchemaEntry{
							"databases": {Selector: "component.aggrega.ai/type=stateful-server"},
							"tools":     {Selector: "tool.aggrega.ai/impact=read-only"},
						},
					},
				}, true
			case "core/database/postgres":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/database/postgres",
						Version: "2.0.0",
						Labels:  map[string]string{types.ComponentTypeLabel: "stateful-server"},
					},
				}, true
			case "core/redis":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/redis",
						Version: "1.0.0",
						Labels:  map[string]string{types.ComponentTypeLabel: "resource"},
					},
				}, true
			case "core/agent":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/agent",
						Version: "1.0.0",
						Labels:  map[string]string{types.ComponentTypeLabel: "worker"},
					},
					Spec: types.ComponentSpec{
						UsesSchema: map[string]types.UsesSchemaEntry{
							"delegates": {Selector: "component.aggrega.ai/type=worker"},
						},
					},
				}, true
			case "core/worker":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/worker",
						Version: "1.0.0",
						Labels:  map[string]string{types.ComponentTypeLabel: "worker"},
					},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/monitoring":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/monitoring", Version: "1.0.0"},
					Spec: types.FeatureSpec{
						UsesSchema: map[string]types.UsesSchemaEntry{
							"targets": {Selector: "component.aggrega.ai/type=server"},
						},
					},
				}, true
			case "core/db-creds":
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:   "core/db-creds",
						Labels: map[string]string{types.FeatureKindLabel: types.FeatureKindSecret},
					},
				}, true
			default:
				return types.Feature{}, false
			}
		},
	}
}

func buildResolveResult(agg *types.Aggregate) *resolver.ResolveResult {
	return &resolver.ResolveResult{
		Aggregate: agg,
		Release: &types.Release{
			Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			Spec: types.ReleaseSpec{
				Boundaries: map[string]types.Boundary{
					"local": {Directory: "./output"},
				},
			},
		},
		Manifest:      &types.Manifest{Metadata: types.ManifestMetadata{Version: "1.0.0"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}
}

func runPipeline(t *testing.T, agg *types.Aggregate) (*types.IRGraph, *analyzer.AnalysisResult) {
	t.Helper()
	catalog := integrationCatalog()
	res := buildResolveResult(agg)

	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult: res,
		CatalogLookup: catalog,
	})
	if err != nil {
		t.Fatalf("topology.Build: %v", err)
	}

	graph, _, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   res,
		CatalogLookup:   catalog,
		CompilerVersion: "test",
	})
	if err != nil {
		t.Fatalf("ir.BuildIR: %v", err)
	}

	analysis := analyzer.Analyze(graph, catalog)
	return graph, analysis
}

func TestPipeline_SimpleTopology(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "simple-app", Version: "1.0.0"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api":     {Type: "core/server/api-server", Uses: map[string]interface{}{"databases": []interface{}{"main-db"}}},
				"main-db": {Type: "core/database/postgres"},
			},
		},
	}

	graph, analysis := runPipeline(t, agg)

	if len(graph.Spec.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Spec.Nodes))
	}
	if len(graph.Spec.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Spec.Edges))
	}
	if len(analysis.Errors) != 0 {
		t.Errorf("expected 0 selector errors, got %d: %s", len(analysis.Errors), analysis.Errors[0].Message)
	}
	if len(analysis.Paths) < 1 {
		t.Errorf("expected at least 1 path, got %d", len(analysis.Paths))
	}
}

func TestPipeline_PolymorphicUses(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "poly-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api-server",
					Uses: map[string]interface{}{
						"databases": map[string]interface{}{
							"hr-db": map[string]interface{}{
								"target": "main-db",
								"inject": map[string]interface{}{
									"env": map[string]interface{}{"DB_URL": "host"},
								},
							},
						},
					},
				},
				"main-db": {Type: "core/database/postgres"},
			},
		},
	}

	graph, analysis := runPipeline(t, agg)

	if len(graph.Spec.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Spec.Edges))
	}
	e := graph.Spec.Edges[0]
	if e.Alias != "hr-db" || e.Target != "main-db" || e.Socket != "databases" {
		t.Errorf("edge: source=%s target=%s socket=%s alias=%s", e.Source, e.Target, e.Socket, e.Alias)
	}
	if len(analysis.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(analysis.Errors))
	}
}

func TestPipeline_SubAggregateFlattening(t *testing.T) {
	t.Parallel()

	childAgg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "data-module"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"child-db": {Type: "core/database/postgres"},
			},
			ExportedComponents: map[string]string{"db": "child-db"},
		},
	}

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/server/api-server", Uses: map[string]interface{}{"databases": []interface{}{"data-mod.child-db"}}},
			},
			Aggregates: map[string]types.SubAggregate{
				"data-mod": {Type: "modules/data-layer"},
			},
		},
	}

	catalog := integrationCatalog()
	res := buildResolveResult(agg)

	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult: res,
		CatalogLookup: catalog,
		LoadSubAggregate: func(typeName string) (*types.Aggregate, error) {
			if typeName == "modules/data-layer" {
				return childAgg, nil
			}
			return nil, fmt.Errorf("unknown: %s", typeName)
		},
	})
	if err != nil {
		t.Fatalf("topology.Build: %v", err)
	}

	if topoResult.Nodes["data-mod.child-db"] == nil {
		t.Fatal("expected data-mod.child-db node")
	}
	if topoResult.ExportedComponents["data-mod.db"] != "data-mod.child-db" {
		t.Errorf("exported: %v", topoResult.ExportedComponents)
	}
}

func TestPipeline_CyclicEdges(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "cyclic-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"agent-a": {Type: "core/agent", Uses: map[string]interface{}{"delegates": []interface{}{"agent-b"}}},
				"agent-b": {Type: "core/agent", Uses: map[string]interface{}{"delegates": []interface{}{"agent-a"}}},
			},
		},
	}

	graph, analysis := runPipeline(t, agg)

	if len(graph.Spec.Edges) != 2 {
		t.Fatalf("expected 2 edges (cyclic), got %d", len(graph.Spec.Edges))
	}
	// Selector: delegates requires worker type — both agents are workers, should pass
	if len(analysis.Errors) != 0 {
		t.Errorf("expected 0 errors (agents are workers), got %d: %s", len(analysis.Errors), analysis.Errors[0].Message)
	}
	// Paths should terminate despite cycle
	if len(analysis.Paths) != 2 {
		t.Errorf("expected 2 paths (each agent→other), got %d", len(analysis.Paths))
	}
}

func TestPipeline_FeatureUsesCreatesEdge(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "feat-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api-server",
					Features: map[string]types.FeatureInstance{
						"mon": {
							Type: "core/monitoring",
							Uses: map[string]interface{}{"targets": []interface{}{"api"}},
						},
					},
				},
			},
		},
	}

	graph, _ := runPipeline(t, agg)

	// The feature uses should create an edge with OriginFeature
	found := false
	for _, e := range graph.Spec.Edges {
		if e.OriginFeature == "core/monitoring" {
			found = true
		}
	}
	if !found {
		t.Error("expected edge with OriginFeature=core/monitoring")
	}
}

func TestPipeline_FeatureNormalization(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "feat-norm-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api-server",
					Features: map[string]types.FeatureInstance{
						"mon": {Type: "core/monitoring", Values: map[string]interface{}{"level": "warn"}},
					},
					Secrets: map[string]types.SecretInstance{
						"creds": {Type: "core/db-creds", Values: map[string]interface{}{"path": "/secret"}},
					},
				},
			},
		},
	}

	graph, _ := runPipeline(t, agg)

	// Feature should be in feature_values_mapping
	fvm := graph.Spec.FeatureValuesMapping["api"]
	if fvm == nil {
		t.Fatal("expected feature_values_mapping for api")
	}
	if fvm["mon"].Type != "core/monitoring" {
		t.Errorf("feature type: %q", fvm["mon"].Type)
	}

	// Secret should be in secrets (credential partition)
	sec := graph.Spec.Secrets["api"]
	if sec == nil {
		t.Fatal("expected secrets for api")
	}
	if sec["creds"].Type != "core/db-creds" {
		t.Errorf("secret type: %q", sec["creds"].Type)
	}

	// Markers should include both
	markerSet := map[string]bool{}
	for _, m := range graph.Spec.FeatureMarkers {
		markerSet[m] = true
	}
	if !markerSet["core/monitoring"] || !markerSet["core/db-creds"] {
		t.Errorf("markers: %v", graph.Spec.FeatureMarkers)
	}
}

func TestPipeline_SelectorViolation(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "bad-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api":   {Type: "core/server/api-server", Uses: map[string]interface{}{"databases": []interface{}{"cache"}}},
				"cache": {Type: "core/redis"}, // resource, not stateful-server
			},
		},
	}

	_, analysis := runPipeline(t, agg)

	if len(analysis.Errors) != 1 {
		t.Fatalf("expected 1 selector violation, got %d", len(analysis.Errors))
	}
	e := analysis.Errors[0]
	if e.Source != "api" || e.Target != "cache" || e.Socket != "databases" {
		t.Errorf("error: %+v", e)
	}
}

func TestPipeline_FeatureTypeMismatch(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "mismatch-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api-server",
					Features: map[string]types.FeatureInstance{
						"feat": {Type: "type-a", Values: map[string]interface{}{"x": 1}},
					},
				},
			},
		},
	}

	catalog := integrationCatalog()
	res := buildResolveResult(agg)

	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult: res,
		CatalogLookup: catalog,
	})
	if err != nil {
		t.Fatalf("topology.Build: %v", err)
	}

	// Inject a conflicting feature attachment to simulate Aggregate+Release type mismatch
	topoResult.FeatureAttachments["api"] = append(topoResult.FeatureAttachments["api"],
		topology.FeatureAttachment{InstanceName: "feat", Feature: types.FeatureInstance{Type: "type-b"}},
	)

	_, _, err = ir.BuildIR(ir.BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: res,
		CatalogLookup: catalog,
	})
	if err == nil {
		t.Fatal("expected ErrFeatureTypeMismatch")
	}
}

func TestPipeline_DeterministicOutput(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "det-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"z-svc": {Type: "core/server/api-server", Uses: map[string]interface{}{"databases": []interface{}{"db"}}},
				"a-svc": {Type: "core/server/api-server", Uses: map[string]interface{}{"databases": []interface{}{"db"}}},
				"m-svc": {Type: "core/server/api-server", Uses: map[string]interface{}{"databases": []interface{}{"db"}}},
				"db":    {Type: "core/database/postgres"},
			},
		},
	}

	type snapshot struct {
		edgeSources []string
		markers     []string
		pathCount   int
	}

	var first *snapshot
	for i := 0; i < 10; i++ {
		graph, analysis := runPipeline(t, agg)

		sources := make([]string, len(graph.Spec.Edges))
		for j, e := range graph.Spec.Edges {
			sources[j] = e.Source
		}

		s := &snapshot{
			edgeSources: sources,
			markers:     graph.Spec.FeatureMarkers,
			pathCount:   len(analysis.Paths),
		}

		if i == 0 {
			first = s
		} else {
			for j, src := range s.edgeSources {
				if src != first.edgeSources[j] {
					t.Errorf("run %d: edge source changed at %d: %q vs %q", i, j, src, first.edgeSources[j])
				}
			}
			if s.pathCount != first.pathCount {
				t.Errorf("run %d: path count changed: %d vs %d", i, s.pathCount, first.pathCount)
			}
		}
	}

	// Verify sorted edge sources
	if first.edgeSources[0] != "a-svc" || first.edgeSources[1] != "m-svc" || first.edgeSources[2] != "z-svc" {
		t.Errorf("edges not sorted: %v", first.edgeSources)
	}
}
