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
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

func TestBuild_SimpleTopology(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"main-db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	components.Set("main-db", types.ComponentInstance{Type: "core/datastore/postgres"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	catalog := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/compute/api-server":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/compute/api-server",
						Labels: labels,
					},
				}, true
			case "core/datastore/postgres":
				labels2 := types.OrderedMap[string]{}
				labels2.Set(types.ComponentTypeLabel, "stateful-server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/datastore/postgres",
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
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Features: features})
	components.Set("metrics-db", types.ComponentInstance{Type: "core/datastore/postgres"})
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

// scopeUsesEdges filters a result's edges to those targeting `target` and
// originating from `originFeature`, returning the set of source node IDs.
// The scope projection's headline assertion is "every component in the
// scope gets an edge to the shared target", so the test checks the source
// set rather than edge order.
func scopeUsesEdges(edges []NormalizedEdge, target, originFeature, originInstance string) map[string]bool {
	srcs := map[string]bool{}
	for _, e := range edges {
		if e.Target == target && e.OriginFeature == originFeature && e.OriginFeatureInstance == originInstance {
			srcs[e.Source] = true
		}
	}
	return srcs
}

// TestBuild_SubsystemFeatureUsesProjectsPerComponent proves, from real
// Aggregate YAML, that a feature attached subsystem-wide carrying a uses
// block contributes that uses-edge from every component in the subsystem.
// A subsystem `tier` holds components x and y plus a subsystem-wide
// telemetry feature whose uses targets the shared-cache component, which
// lives in a sibling subsystem; the build must emit x→shared-cache and
// y→shared-cache, each stamped with the feature as origin. Reverting the
// scope projection drops both edges (the only edges in this graph), so the
// green is non-vacuous.
//
// shared-cache sits in its own subsystem rather than at the top-level
// `.spec.components` surface because top-level components and subsystems are
// mutually exclusive in an Aggregate — a document declaring both is rejected
// before it is parsed, so a fixture declaring both would exercise a shape the
// product never builds.
func TestBuild_SubsystemFeatureUsesProjectsPerComponent(t *testing.T) {
	t.Parallel()
	const aggYAML = `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test-app
  version: 1.0.0
spec:
  subsystems:
    cache:
      components:
        shared-cache:
          type: core/redis
    tier:
      features:
        telemetry:
          type: core/telemetry
          uses:
            link:
              - shared-cache
      components:
        x:
          type: core/compute/api-server
        y:
          type: core/compute/api-server
`
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(aggYAML), &agg); err != nil {
		t.Fatalf("unmarshal aggregate: %v", err)
	}
	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srcs := scopeUsesEdges(result.Edges, "shared-cache", "core/telemetry", "telemetry")
	if !srcs["x"] || !srcs["y"] {
		t.Errorf("subsystem feature uses must project to every component in the scope; got sources %v, want both x and y → shared-cache (OriginFeature core/telemetry)", srcs)
	}
}

// TestBuild_BoundaryFeatureUsesProjectsPerComponent proves, from real
// Aggregate + Release YAML, that a feature attached boundary-wide carrying
// a uses block contributes that uses-edge from every component routed into
// the boundary. The Release attaches a telemetry feature boundary-wide on
// `cluster` (components x and y) whose uses targets the shared-cache
// component; the build must emit x→shared-cache and y→shared-cache, each
// stamped with the feature as origin — the OriginFeature/OriginFeatureInstance
// pair the injection pre-pass keys on to inject the target's provides.
// Reverting the scope projection drops both edges.
func TestBuild_BoundaryFeatureUsesProjectsPerComponent(t *testing.T) {
	t.Parallel()
	const aggYAML = `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test-app
  version: 1.0.0
spec:
  components:
    x:
      type: core/compute/api-server
    y:
      type: core/compute/api-server
    shared-cache:
      type: core/redis
`
	const relYAML = `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  version: "1.0.0"
  environment: testing
spec:
  aggregate: test-app
  manifest: test
  boundaries:
    cluster:
      features:
        telemetry:
          type: core/telemetry
          uses:
            link:
              - shared-cache
      components:
        x: {}
        y: {}
`
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(aggYAML), &agg); err != nil {
		t.Fatalf("unmarshal aggregate: %v", err)
	}
	var rel types.Release
	if err := yaml.Unmarshal([]byte(relYAML), &rel); err != nil {
		t.Fatalf("unmarshal release: %v", err)
	}
	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &agg,
			Release:   &rel,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				"x": {BoundaryPath: "cluster"},
				"y": {BoundaryPath: "cluster"},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srcs := scopeUsesEdges(result.Edges, "shared-cache", "core/telemetry", "telemetry")
	if !srcs["x"] || !srcs["y"] {
		t.Errorf("boundary feature uses must project to every component in the boundary; got sources %v, want both x and y → shared-cache (OriginFeature core/telemetry)", srcs)
	}
}

func TestBuild_BoundaryPath(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server"})

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
// TestGuardSubAggregateNodes_ResolvedCatalogRefAbsentHalts proves the Step-2
// topology fold: a sub-aggregate node whose type resolved as a catalog reference
// but is absent from the guard's view halts the build loudly — even with NO uses
// block (the value/infra-conformance case the uses-path alone did not cover). A
// resilient-inspection caller tolerates the same finding via the Step-1 sentinel
// filter; here, the build (no tolerance) halts.
func TestGuardSubAggregateNodes_ResolvedCatalogRefAbsentHalts(t *testing.T) {
	t.Parallel()
	childComps := types.OrderedMap[types.ComponentInstance]{}
	// A bare catalog reference (not a Tier-1 ./ path), no uses block.
	childComps.Set("svc", types.ComponentInstance{Type: "core/absent:1.0.0"})
	childAgg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "data-mod"},
		Spec:     types.AggregateSpec{Components: childComps},
	}

	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("data-mod", types.SubAggregate{Type: "modules/data-layer"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec:     types.AggregateSpec{Aggregates: aggregates},
	}

	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		if typeName == "modules/data-layer" {
			return childAgg, "", nil, nil
		}
		return nil, "", nil, fmt.Errorf("unknown: %s", typeName)
	}
	// A CatalogLookup whose Component lookup always misses — the resolved-but-absent
	// condition the fold turns into a loud halt for a bare catalog reference.
	notFound := resolver.CatalogLookup{Component: func(string) (types.Component, bool) {
		return types.Component{}, false
	}}

	_, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		LoadSubAggregate: loader,
		CatalogLookup:    notFound,
	})
	if err == nil {
		t.Fatal("expected a sub-aggregate view-inconsistency halt; got nil")
	}
	if !errors.Is(err, resolver.ErrGuardEntityViewInconsistency) {
		t.Errorf("expected ErrGuardEntityViewInconsistency; got %v", err)
	}
}

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

	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		if typeName == "modules/data-layer" {
			return childAgg, "", nil, nil
		}
		return nil, "", nil, fmt.Errorf("unknown: %s", typeName)
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
	// The three services wire socket "s" to "target"; declare it so the edges
	// resolve (the origin-agnostic dangling-target guardrail requires every edge
	// target to be a real node). It carries no uses, so it adds no edge — the
	// ordering assertion below is unaffected.
	components.Set("target", types.ComponentInstance{Type: "t"})
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

// exportModuleLoader returns a SubAggregateLoader that serves a single database
// module exposing its internal main-db component under the public alias
// public_db. It backs the export-alias edge-target tests below.
func exportModuleLoader() SubAggregateLoader {
	return func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		if typeName != "modules/db" {
			return nil, "", nil, fmt.Errorf("unknown sub-aggregate: %s", typeName)
		}
		comps := types.OrderedMap[types.ComponentInstance]{}
		comps.Set("main-db", types.ComponentInstance{Type: "core/datastore/postgres"})
		exports := types.OrderedMap[string]{}
		exports.Set("public_db", "main-db")
		return &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "db-module"},
			Spec: types.AggregateSpec{
				Components:         comps,
				ExportedComponents: exports,
			},
		}, "", nil, nil
	}
}

// TestBuild_ExportAliasEdgeTargetResolves — a root component whose `uses` edge
// targets a sub-aggregate's exported alias (data.public_db) resolves to the
// module's internal node ID (data.main-db). The public alias is the module's
// interface; the resolved edge points at the real node so injection flows from
// the target's provides into the consuming node.
func TestBuild_ExportAliasEdgeTargetResolves(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"data.public_db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	subs := types.OrderedMap[types.SubAggregate]{}
	subs.Set("data", types.SubAggregate{Type: "modules/db"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Components: components,
			Aggregates: subs,
		},
	}

	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		LoadSubAggregate: exportModuleLoader(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(result.Edges))
	}
	e := result.Edges[0]
	if e.Source != "api" || e.Socket != "databases" {
		t.Fatalf("unexpected edge shape: %+v", e)
	}
	if e.Target != "data.main-db" {
		t.Errorf("export alias not resolved: got target %q, want %q", e.Target, "data.main-db")
	}
	if _, ok := result.Nodes.Get(e.Target); !ok {
		t.Errorf("resolved target %q is not a node in the topology", e.Target)
	}
}

// TestBuild_UnknownEdgeTargetHalts — a `uses` edge whose target is neither a
// node nor a known export alias still halts the build with ErrDanglingUsesTarget.
// Export-alias resolution must not soften the Loud Guardrail for genuinely
// dangling targets.
func TestBuild_UnknownEdgeTargetHalts(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"data.nonexistent"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	subs := types.OrderedMap[types.SubAggregate]{}
	subs.Set("data", types.SubAggregate{Type: "modules/db"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Components: components,
			Aggregates: subs,
		},
	}

	_, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		LoadSubAggregate: exportModuleLoader(),
	})
	if err == nil {
		t.Fatal("expected ErrDanglingUsesTarget for unknown edge target, got nil")
	}
	if !errors.Is(err, ErrDanglingUsesTarget) {
		t.Errorf("expected ErrDanglingUsesTarget, got %v", err)
	}
}

// reexportModuleLoader returns a SubAggregateLoader serving a two-level module
// stack. The grandchild database module (modules/db) exposes its internal
// main-db component under the alias public_db. The intermediate data-tier
// module (modules/data-tier) imports the grandchild under the prefix gc and
// re-exports the grandchild's alias under a NEW name (db_endpoint), so the
// stack exercises a rename across a re-export hop. midReexportRef is the
// intermediate's re-export target — the child-local path it re-publishes;
// passing a live path (gc.public_db) exercises transitive resolution, a dead
// path (gc.nonexistent) exercises the multi-hop dangling halt.
func reexportModuleLoader(midReexportRef string) SubAggregateLoader {
	return func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		switch typeName {
		case "modules/db":
			comps := types.OrderedMap[types.ComponentInstance]{}
			comps.Set("main-db", types.ComponentInstance{Type: "core/datastore/postgres"})
			exports := types.OrderedMap[string]{}
			exports.Set("public_db", "main-db")
			return &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "db-module"},
				Spec: types.AggregateSpec{
					Components:         comps,
					ExportedComponents: exports,
				},
			}, "", nil, nil
		case "modules/data-tier":
			subs := types.OrderedMap[types.SubAggregate]{}
			subs.Set("gc", types.SubAggregate{Type: "modules/db"})
			exports := types.OrderedMap[string]{}
			exports.Set("db_endpoint", midReexportRef)
			return &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "data-tier-module"},
				Spec: types.AggregateSpec{
					Aggregates:         subs,
					ExportedComponents: exports,
				},
			}, "", nil, nil
		default:
			return nil, "", nil, fmt.Errorf("unknown sub-aggregate: %s", typeName)
		}
	}
}

// TestBuild_TransitiveReexportEdgeTargetResolves — a root component's `uses`
// edge targets an intermediate module's re-exported alias (mid.db_endpoint)
// that ultimately re-publishes a grandchild module's exported alias under a new
// name. The edge resolves through both hops to the grandchild's internal node
// ID (mid.gc.main-db), and that target is a real node in the topology — the
// byte-identical-to-direct-target proof for a multi-hop re-export chain.
func TestBuild_TransitiveReexportEdgeTargetResolves(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"mid.db_endpoint"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	subs := types.OrderedMap[types.SubAggregate]{}
	subs.Set("mid", types.SubAggregate{Type: "modules/data-tier"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Components: components,
			Aggregates: subs,
		},
	}

	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		LoadSubAggregate: reexportModuleLoader("gc.public_db"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(result.Edges))
	}
	e := result.Edges[0]
	if e.Source != "api" || e.Socket != "databases" {
		t.Fatalf("unexpected edge shape: %+v", e)
	}
	if e.Target != "mid.gc.main-db" {
		t.Errorf("transitive re-export not resolved: got target %q, want %q", e.Target, "mid.gc.main-db")
	}
	if _, ok := result.Nodes.Get(e.Target); !ok {
		t.Errorf("resolved target %q is not a node in the topology", e.Target)
	}
}

// TestBuild_TransitiveReexportDanglingHalts — an intermediate module re-exports
// a nonexistent grandchild path. Transitive resolution leaves the export value
// unresolved (it names neither a node nor a live alias), so the consuming edge
// halts loudly with ErrDanglingUsesTarget rather than resolving to a phantom
// target. The Loud Guardrail survives the multi-hop re-export path.
func TestBuild_TransitiveReexportDanglingHalts(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"mid.db_endpoint"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Uses: uses})
	subs := types.OrderedMap[types.SubAggregate]{}
	subs.Set("mid", types.SubAggregate{Type: "modules/data-tier"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Components: components,
			Aggregates: subs,
		},
	}

	_, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     agg,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		LoadSubAggregate: reexportModuleLoader("gc.nonexistent"),
	})
	if err == nil {
		t.Fatal("expected ErrDanglingUsesTarget for a multi-hop dangling re-export, got nil")
	}
	if !errors.Is(err, ErrDanglingUsesTarget) {
		t.Errorf("expected ErrDanglingUsesTarget, got %v", err)
	}
}

// TestResolveExportTarget_CycleHalts — white-box backstop for the re-export
// resolver. A malformed alias table whose entries loop back on each other
// (a → b → a), with a node map containing neither, must halt with
// ErrReexportCycle naming the walked chain, and must TERMINATE rather than
// walk the loop forever. The condition is structurally unconstructible through
// valid authoring (the inclusion tree is acyclic and export values deepen at
// every hop); the direct call exercises the visited-set guard regardless.
func TestResolveExportTarget_CycleHalts(t *testing.T) {
	t.Parallel()
	childExports := types.OrderedMap[string]{}
	childExports.Set("a", "b")
	childExports.Set("b", "a")
	nodes := types.OrderedMap[*TopologyNode]{} // contains neither a nor b

	type outcome struct {
		resolved string
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		r, e := resolveExportTarget("a", nodes, childExports)
		done <- outcome{r, e}
	}()

	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("expected a cycle error, got resolved=%q with nil error", got.resolved)
		}
		if !errors.Is(got.err, ErrReexportCycle) {
			t.Errorf("expected ErrReexportCycle, got %v", got.err)
		}
		if !strings.Contains(got.err.Error(), "a -> b -> a") {
			t.Errorf("cycle error does not name the walked chain: %q", got.err.Error())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resolveExportTarget did not terminate on a cyclic alias table")
	}
}
