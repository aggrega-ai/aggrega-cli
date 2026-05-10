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
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
				usesSchema.Set("tools", types.UsesSchemaEntry{Selector: "tool.aggrega.ai/impact=read-only"})
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/server/api-server",
						Version: "1.0.0",
						Labels:  labels,
					},
					Spec: types.ComponentSpec{
						UsesSchema: usesSchema,
					},
				}, true
			case "core/database/postgres":
				labels2 := types.OrderedMap[string]{}
				labels2.Set(types.ComponentTypeLabel, "stateful-server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/database/postgres",
						Version: "2.0.0",
						Labels:  labels2,
					},
				}, true
			case "core/redis":
				labels3 := types.OrderedMap[string]{}
				labels3.Set(types.ComponentTypeLabel, "resource")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/redis",
						Version: "1.0.0",
						Labels:  labels3,
					},
				}, true
			case "core/agent":
				labels4 := types.OrderedMap[string]{}
				labels4.Set(types.ComponentTypeLabel, "worker")
				usesSchema2 := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema2.Set("delegates", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=worker"})
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/agent",
						Version: "1.0.0",
						Labels:  labels4,
					},
					Spec: types.ComponentSpec{
						UsesSchema: usesSchema2,
					},
				}, true
			case "core/worker":
				labels5 := types.OrderedMap[string]{}
				labels5.Set(types.ComponentTypeLabel, "worker")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/worker",
						Version: "1.0.0",
						Labels:  labels5,
					},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/monitoring":
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("targets", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=server"})
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/monitoring", Version: "1.0.0"},
					Spec: types.FeatureSpec{
						UsesSchema: usesSchema,
					},
				}, true
			case "core/db-creds":
				labels := types.OrderedMap[string]{}
				labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:   "core/db-creds",
						Labels: labels,
					},
				}, true
			default:
				return types.Feature{}, false
			}
		},
	}
}

func buildResolveResult(agg *types.Aggregate) *resolver.ResolveResult {
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("local", types.Boundary{Directory: "./output"})
	return &resolver.ResolveResult{
		Aggregate: agg,
		Release: &types.Release{
			Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			Spec: types.ReleaseSpec{
				Boundaries: boundaries,
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
	uses := types.OrderedMap[any]{}
	uses.Set("databases", []any{"main-db"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server", Uses: uses})
	components.Set("main-db", types.ComponentInstance{Type: "core/database/postgres"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "simple-app", Version: "1.0.0"},
		Spec: types.AggregateSpec{
			Components: components,
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
	uses := types.OrderedMap[any]{}
	hrDB := types.NewOrderedMap[any]()
	hrDB.Set("target", "main-db")
	envOM := types.NewOrderedMap[any]()
	envOM.Set("DB_URL", "host")
	injectOM := types.NewOrderedMap[any]()
	injectOM.Set("env", envOM)
	hrDB.Set("inject", injectOM)
	verbose := types.NewOrderedMap[any]()
	verbose.Set("hr-db", hrDB)
	uses.Set("databases", verbose)
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server", Uses: uses})
	components.Set("main-db", types.ComponentInstance{Type: "core/database/postgres"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "poly-app"},
		Spec: types.AggregateSpec{
			Components: components,
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
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("child-db", types.ComponentInstance{Type: "core/database/postgres"})
	exportedComponents := types.OrderedMap[string]{}
	exportedComponents.Set("db", "child-db")

	childAgg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "data-module"},
		Spec: types.AggregateSpec{
			Components:         components,
			ExportedComponents: exportedComponents,
		},
	}
	uses := types.OrderedMap[any]{}
	uses.Set("databases", []any{"data-mod.child-db"})
	components2 := types.OrderedMap[types.ComponentInstance]{}
	components2.Set("api", types.ComponentInstance{Type: "core/server/api-server", Uses: uses})
	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("data-mod", types.SubAggregate{Type: "modules/data-layer"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root-app"},
		Spec: types.AggregateSpec{
			Components: components2,
			Aggregates: aggregates,
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

	childDB, ok := topoResult.Nodes.Get("data-mod.child-db")
	if !ok || childDB == nil {
		t.Fatal("expected data-mod.child-db node")
	}
	if topoResult.ExportedComponents["data-mod.db"] != "data-mod.child-db" {
		t.Errorf("exported: %v", topoResult.ExportedComponents)
	}
}

func TestPipeline_CyclicEdges(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[any]{}
	uses.Set("delegates", []any{"agent-b"})
	uses2 := types.OrderedMap[any]{}
	uses2.Set("delegates", []any{"agent-a"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("agent-a", types.ComponentInstance{Type: "core/agent", Uses: uses})
	components.Set("agent-b", types.ComponentInstance{Type: "core/agent",

		// Selector: delegates requires worker type — both agents are workers, should pass
		Uses: uses2})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "cyclic-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	graph, analysis := runPipeline(t, agg)

	if len(graph.Spec.Edges) != 2 {
		t.Fatalf("expected 2 edges (cyclic), got %d", len(graph.Spec.Edges))
	}

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
	uses := types.OrderedMap[any]{}
	uses.Set("targets", []any{"api"})
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("mon", types.FeatureInstance{Type: "core/monitoring",

		// The feature uses should create an edge with OriginFeature
		Uses: uses})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "feat-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	graph, _ := runPipeline(t, agg)

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
	values := types.OrderedMap[any]{}
	values.Set("level", "warn")
	values2 := types.OrderedMap[any]{}
	values2.Set("path", "/secret")
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("mon", types.FeatureInstance{Type: "core/monitoring",

		// Feature should be in feature_values_mapping
		Values: values})
	secrets := types.OrderedMap[types.SecretInstance]{}
	secrets.Set("creds", types.SecretInstance{Type: "core/db-creds", Values: values2})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server", Features: features, Secrets:

	// Secret should be in secrets (credential partition)
	secrets})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "feat-norm-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	graph, _ := runPipeline(t, agg)

	fvm, ok := graph.Spec.FeatureValuesMapping.Get("api")
	if !ok || fvm.Len() == 0 {
		t.Fatal("expected feature_values_mapping for api")
	}
	monEntry, _ := fvm.Get("mon")
	if monEntry.Type != "core/monitoring" {
		t.Errorf("feature type: %q", monEntry.Type)
	}

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
	uses := types.OrderedMap[any]{}
	uses.Set("databases", []any{"cache"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server", Uses: uses})
	components.Set("cache", types.ComponentInstance{Type: "core/redis"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "bad-app"},
		Spec: types.AggregateSpec{
			Components: components,
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
	values := types.OrderedMap[any]{}
	values.Set("x", 1)
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("feat", types.FeatureInstance{Type: "type-a", Values: values})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "mismatch-app"},
		Spec: types.AggregateSpec{
			Components: components,
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
	uses := types.OrderedMap[any]{}
	uses.Set("databases", []any{"db"})
	uses2 := types.OrderedMap[any]{}
	uses2.Set("databases", []any{"db"})
	uses3 := types.OrderedMap[any]{}
	uses3.Set("databases", []any{"db"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("z-svc", types.ComponentInstance{Type: "core/server/api-server", Uses: uses})
	components.Set("a-svc", types.ComponentInstance{Type: "core/server/api-server", Uses: uses2})
	components.Set("m-svc", types.ComponentInstance{Type: "core/server/api-server", Uses: uses3})
	components.Set("db", types.ComponentInstance{Type: "core/database/postgres"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "det-app"},
		Spec: types.AggregateSpec{
			Components: components,
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
