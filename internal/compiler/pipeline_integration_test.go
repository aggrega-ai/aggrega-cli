// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Shared catalog for integration tests.
func integrationCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/compute/api-server":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				// The `databases` socket carries the same env delivery allowlist the
				// catalogued api-server component declares for it, so an edge in this
				// fixture may dictate env delivery exactly as one in the worked example
				// does. Without it the socket would be a Black-Box declaration and any
				// fixture edge dictating delivery would be refused.
				usesSchema.Set("databases", types.UsesSchemaEntry{
					Selector: "component.aggrega.ai/type=stateful-server",
					Inject:   []string{"env"},
				})
				usesSchema.Set("tools", types.UsesSchemaEntry{Selector: "tool.aggrega.ai/impact=read-only"})
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/compute/api-server",
						Version: "1.0.0",
						Labels:  labels,
					},
					Spec: types.ComponentSpec{
						UsesSchema: usesSchema,
					},
				}, true
			case "core/datastore/postgres":
				labels2 := types.OrderedMap[string]{}
				labels2.Set(types.ComponentTypeLabel, "stateful-server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/datastore/postgres",
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

// runPipeline drives the front half of the compiler — topology build, IR
// construction, selector analysis — and returns the graph plus the analysis
// result. It deliberately stops before render and does NOT run the recipe-
// resolution-completeness check the CLI build runs between IR and render: the
// fixtures here are graph-only (a hand-stubbed catalog with no recipes), so
// they isolate node/edge construction and selector-analysis paths, not end-to-
// end rendering. This is an intentional stage-isolation harness, not a faithful
// end-to-end build; tests that render through the full build use
// pipelinetest.Build instead.
//
// One consequence for the determinism coverage: TestPipeline_DeterministicOutput
// below pins determinism at the graph/IR level — edge-source ordering, feature-
// marker ordering, and selector-path count across repeated front-half runs.
// That is a different surface from the rendered-output determinism pinned in
// determinism_test.go (byte-identical artifact maps across full builds). The two
// are complementary: this one guards the graph the front half produces; that one
// guards the bytes the renderer emits.
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
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"main-db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	components.Set("main-db", types.ComponentInstance{Type: "core/datastore/postgres"})
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
	envOM := types.NewOrderedMap[any]()
	envOM.Set("DB_URL", "host")
	inject := &types.InjectBlock{Env: envOM}
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("hr-db", types.VerboseUsesEntry{Target: "main-db", Inject: inject})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	components.Set("main-db", types.ComponentInstance{Type: "core/datastore/postgres"})
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
	components.Set("child-db", types.ComponentInstance{Type: "core/datastore/postgres"})
	exportedComponents := types.OrderedMap[string]{}
	exportedComponents.Set("db", "child-db")

	childAgg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "data-module"},
		Spec: types.AggregateSpec{
			Components:         components,
			ExportedComponents: exportedComponents,
		},
	}
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"data-mod.child-db"}})
	components2 := types.OrderedMap[types.ComponentInstance]{}
	components2.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
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
		LoadSubAggregate: func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
			if typeName == "modules/data-layer" {
				return childAgg, "", nil, nil
			}
			return nil, "", nil, fmt.Errorf("unknown: %s", typeName)
		},
	})
	if err != nil {
		t.Fatalf("topology.Build: %v", err)
	}

	childDB, ok := topoResult.Nodes.Get("data-mod.child-db")
	if !ok || childDB == nil {
		t.Fatal("expected data-mod.child-db node")
	}
	if got, _ := topoResult.ExportedComponents.Get("data-mod.db"); got != "data-mod.child-db" {
		t.Errorf("exported: %v", topoResult.ExportedComponents.Keys())
	}
}

func TestPipeline_CyclicEdges(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("delegates", types.UsesSocketValue{Compact: []string{"agent-b"}})
	uses2 := types.OrderedMap[types.UsesSocketValue]{}
	uses2.Set("delegates", types.UsesSocketValue{Compact: []string{"agent-a"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("agent-a", types.ComponentInstance{Type: "core/agent", Uses: uses})
	// Selector: delegates requires worker type — both agents are workers, should pass.
	components.Set("agent-b", types.ComponentInstance{Type: "core/agent", Uses: uses2})
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
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("targets", types.UsesSocketValue{Compact: []string{"api"}})
	features := types.OrderedMap[types.FeatureInstance]{}
	// The feature uses should create an edge with OriginFeature.
	features.Set("mon", types.FeatureInstance{Type: "core/monitoring", Uses: uses})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Features: features})
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
	// Both attachments — an operational Feature and a credential Feature — are
	// declared on the one attachment surface and land in the one IR bucket.
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("mon", types.FeatureInstance{Type: "core/monitoring", Values: values})
	features.Set("creds", types.FeatureInstance{Type: "core/db-creds", Values: values2})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Features: features})
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

	credsEntry, ok := fvm.Get("creds")
	if !ok {
		t.Fatal("expected the credential attachment in feature_values_mapping for api")
	}
	if credsEntry.Type != "core/db-creds" {
		t.Errorf("credential attachment type: %q", credsEntry.Type)
	}
}

func TestPipeline_SelectorViolation(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"cache"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
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
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Features: features})
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
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	uses2 := types.OrderedMap[types.UsesSocketValue]{}
	uses2.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	uses3 := types.OrderedMap[types.UsesSocketValue]{}
	uses3.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("z-svc", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	components.Set("a-svc", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses2})
	components.Set("m-svc", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses3})
	components.Set("db", types.ComponentInstance{Type: "core/datastore/postgres"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "det-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	type snapshot struct {
		edgeSources []string
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

	// Verify compositional author order: components declared in sequence
	// z-svc, a-svc, m-svc, db — edge slice (filtered to the three svc
	// sources) mirrors the declaration order.
	if first.edgeSources[0] != "z-svc" || first.edgeSources[1] != "a-svc" || first.edgeSources[2] != "m-svc" {
		t.Errorf("edges not in author order: %v (want [z-svc a-svc m-svc])", first.edgeSources)
	}
}
