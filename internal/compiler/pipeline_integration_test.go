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
						Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(),
					},
					Spec: types.ComponentSpec{
						UsesSchema: func() types.OrderedMap[types.UsesSchemaEntry] { _m := types.OrderedMap[types.UsesSchemaEntry]{}; _m.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"}); _m.Set("tools", types.UsesSchemaEntry{Selector: "tool.aggrega.ai/impact=read-only"}); return _m }(),
					},
				}, true
			case "core/database/postgres":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/database/postgres",
						Version: "2.0.0",
						Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "stateful-server"); return _m }(),
					},
				}, true
			case "core/redis":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/redis",
						Version: "1.0.0",
						Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "resource"); return _m }(),
					},
				}, true
			case "core/agent":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/agent",
						Version: "1.0.0",
						Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "worker"); return _m }(),
					},
					Spec: types.ComponentSpec{
						UsesSchema: func() types.OrderedMap[types.UsesSchemaEntry] { _m := types.OrderedMap[types.UsesSchemaEntry]{}; _m.Set("delegates", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=worker"}); return _m }(),
					},
				}, true
			case "core/worker":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/worker",
						Version: "1.0.0",
						Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "worker"); return _m }(),
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
						UsesSchema: func() types.OrderedMap[types.UsesSchemaEntry] { _m := types.OrderedMap[types.UsesSchemaEntry]{}; _m.Set("targets", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=server"}); return _m }(),
					},
				}, true
			case "core/db-creds":
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:   "core/db-creds",
						Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.FeatureKindLabel, types.FeatureKindSecret); return _m }(),
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
				Boundaries: func() types.OrderedMap[types.Boundary] { _m := types.OrderedMap[types.Boundary]{}; _m.Set("local", types.Boundary{Directory: "./output"}); return _m }(),
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/server/api-server", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", []interface{}{"main-db"}); return _m }()}); _m.Set("main-db", types.ComponentInstance{Type: "core/database/postgres"}); return _m }(),
		},
	}

	graph, analysis := runPipeline(t, agg)

	if graph.Spec.Nodes.Len() != 2 {
		t.Fatalf("expected 2 nodes, got %d", graph.Spec.Nodes.Len())
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
					Type: "core/server/api-server",
					Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", map[string]interface{}{
							"hr-db": map[string]interface{}{
								"target": "main-db",
								"inject": map[string]interface{}{
									"env": map[string]interface{}{"DB_URL": "host"},
								},
							},
						}); return _m }(),
				}); _m.Set("main-db", types.ComponentInstance{Type: "core/database/postgres"}); return _m }(),
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("child-db", types.ComponentInstance{Type: "core/database/postgres"}); return _m }(),
			ExportedComponents: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("db", "child-db"); return _m }(),
		},
	}

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root-app"},
		Spec: types.AggregateSpec{
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/server/api-server", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", []interface{}{"data-mod.child-db"}); return _m }()}); return _m }(),
			Aggregates: func() types.OrderedMap[types.SubAggregate] { _m := types.OrderedMap[types.SubAggregate]{}; _m.Set("data-mod", types.SubAggregate{Type: "modules/data-layer"}); return _m }(),
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("agent-a", types.ComponentInstance{Type: "core/agent", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("delegates", []interface{}{"agent-b"}); return _m }()}); _m.Set("agent-b", types.ComponentInstance{Type: "core/agent", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("delegates", []interface{}{"agent-a"}); return _m }()}); return _m }(),
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
					Type: "core/server/api-server",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("mon", types.FeatureInstance{
							Type: "core/monitoring",
							Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("targets", []interface{}{"api"}); return _m }(),
						}); return _m }(),
				}); return _m }(),
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
					Type: "core/server/api-server",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("mon", types.FeatureInstance{Type: "core/monitoring", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("level", "warn"); return _m }()}); return _m }(),
					Secrets: func() types.OrderedMap[types.SecretInstance] { _m := types.OrderedMap[types.SecretInstance]{}; _m.Set("creds", types.SecretInstance{Type: "core/db-creds", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("path", "/secret"); return _m }()}); return _m }(),
				}); return _m }(),
		},
	}

	graph, _ := runPipeline(t, agg)

	// Feature should be in feature_values_mapping
	fvm, ok := graph.Spec.FeatureValuesMapping.Get("api")
	if !ok || fvm.Len() == 0 {
		t.Fatal("expected feature_values_mapping for api")
	}
	monEntry, _ := fvm.Get("mon")
	if monEntry.Type != "core/monitoring" {
		t.Errorf("feature type: %q", monEntry.Type)
	}

	// Secret should be in secrets (credential partition)
	sec, ok := graph.Spec.Secrets.Get("api")
	if !ok || sec.Len() == 0 {
		t.Fatal("expected secrets for api")
	}
	credsEntry, _ := sec.Get("creds")
	if credsEntry.Type != "core/db-creds" {
		t.Errorf("secret type: %q", credsEntry.Type)
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{Type: "core/server/api-server", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", []interface{}{"cache"}); return _m }()}); _m.Set("cache", types.ComponentInstance{Type: "core/redis"}); return _m }(),
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
					Type: "core/server/api-server",
					Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("feat", types.FeatureInstance{Type: "type-a", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("x", 1); return _m }()}); return _m }(),
				}); return _m }(),
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("z-svc", types.ComponentInstance{Type: "core/server/api-server", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", []interface{}{"db"}); return _m }()}); _m.Set("a-svc", types.ComponentInstance{Type: "core/server/api-server", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", []interface{}{"db"}); return _m }()}); _m.Set("m-svc", types.ComponentInstance{Type: "core/server/api-server", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", []interface{}{"db"}); return _m }()}); _m.Set("db", types.ComponentInstance{Type: "core/database/postgres"}); return _m }(),
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
