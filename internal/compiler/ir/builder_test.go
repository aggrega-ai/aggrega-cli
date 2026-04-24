// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func testCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server/api-server":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/server/api-server",
						Version: "1.0.0",
						Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(),
						Annotations: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("catalog.aggrega.ai/tier", "standard"); return _m }(),
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
			default:
				return types.Component{}, false
			}
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == "core/db-creds" {
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:   "core/db-creds",
						Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.FeatureKindLabel, types.FeatureKindSecret); return _m }(),
					},
				}, true
			}
			return types.Feature{}, false
		},
	}
}

func TestBuildIR_SimpleGraph(t *testing.T) {
	t.Parallel()

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"api":     {InstanceID: "api", ComponentType: "core/server/api-server", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(), Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("port", 8080); return _m }()},
			"main-db": {InstanceID: "main-db", ComponentType: "core/database/postgres", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "stateful-server"); return _m }()},
		},
		Edges: []topology.NormalizedEdge{
			{Source: "api", Target: "main-db", Socket: "databases", Alias: "main-db"},
		},
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "test-app", Version: "1.0.0"},
		},
		Release: &types.Release{
			Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			Spec:     types.ReleaseSpec{Boundaries: types.OrderedMap[types.Boundary]{}},
		},
		Manifest: &types.Manifest{
			Metadata: types.ManifestMetadata{Version: "1.0.0"},
		},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"api": {BoundaryPath: "local/default", Annotations: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("env", "local"); return _m }()},
		},
	}

	graph, order, err := BuildIR(BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   testCatalog(),
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nodes
	if graph.Spec.Nodes.Len() != 2 {
		t.Fatalf("expected 2 nodes, got %d", graph.Spec.Nodes.Len())
	}
	apiNode, _ := graph.Spec.Nodes.Get("api")
	if apiNode.ResolvedVersion != "1.0.0" {
		t.Errorf("api resolved version: %q", apiNode.ResolvedVersion)
	}
	if apiNode.Component != "core/server/api-server" {
		t.Errorf("api component: %q", apiNode.Component)
	}

	// Edges
	if len(graph.Spec.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Spec.Edges))
	}
	if graph.Spec.Edges[0].Source != "api" || graph.Spec.Edges[0].Target != "main-db" {
		t.Errorf("edge: %+v", graph.Spec.Edges[0])
	}

	// Values mapping
	if _, ok := graph.Spec.ValuesMapping.Get("api"); !ok {
		t.Error("expected api values mapping")
	}

	// Annotations
	tier, _ := apiNode.CatalogAnnotations.Get("catalog.aggrega.ai/tier")
	if tier != "standard" {
		t.Errorf("catalog annotations: %v", apiNode.CatalogAnnotations.Keys())
	}
	env, _ := apiNode.ReleaseAnnotations.Get("env")
	if env != "local" {
		t.Errorf("release annotations: %v", apiNode.ReleaseAnnotations.Keys())
	}

	// Infra mapping
	// (no infra in this test)

	// Provenance
	if graph.Spec.Provenance.CompilerVersion != "0.1.0" {
		t.Errorf("compiler version: %q", graph.Spec.Provenance.CompilerVersion)
	}
	if graph.Spec.Provenance.AggregateVersion != "1.0.0" {
		t.Errorf("aggregate version: %q", graph.Spec.Provenance.AggregateVersion)
	}

	// Metadata
	if graph.Metadata.AggregateName != "test-app" {
		t.Errorf("app name: %q", graph.Metadata.AggregateName)
	}
	if graph.APIVersion != types.IRAPIVersion || graph.Kind != types.IRKind {
		t.Errorf("api/kind: %q/%q", graph.APIVersion, graph.Kind)
	}

	// Feature order (empty in this test)
	_ = order
}

func TestBuildIR_FeatureNormalization(t *testing.T) {
	t.Parallel()

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"api": {InstanceID: "api", ComponentType: "core/server/api-server", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }()},
		},
		Edges: nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "logging", Feature: types.FeatureInstance{Type: "core/logging", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("level", "warn"); return _m }()}},
			},
		},
		SecretAttachments: map[string][]topology.SecretAttachment{
			"api": {
				{InstanceName: "db-creds", Secret: types.SecretInstance{Type: "core/db-creds", Values: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("path", "/secret"); return _m }()}},
			},
		},
	}

	resolveResult := &resolver.ResolveResult{
		Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}

	graph, order, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: testCatalog(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Features
	if fvm, ok := graph.Spec.FeatureValuesMapping.Get("api"); !ok || fvm.Len() == 0 {
		t.Fatal("expected feature values mapping for api")
	} else {
		loggingEntry, _ := fvm.Get("logging")
		if loggingEntry.Type != "core/logging" {
			t.Errorf("feature type: %q", loggingEntry.Type)
		}
	}

	// Secrets
	if sec, ok := graph.Spec.Secrets.Get("api"); !ok || sec.Len() == 0 {
		t.Fatal("expected secrets for api")
	} else {
		dbEntry, _ := sec.Get("db-creds")
		if dbEntry.Type != "core/db-creds" {
			t.Errorf("secret type: %q", dbEntry.Type)
		}
	}

	// Feature order
	if len(order["api"]) != 1 || order["api"][0] != "logging" {
		t.Errorf("feature order: %v", order["api"])
	}

	// Markers
	found := map[string]bool{}
	for _, m := range graph.Spec.FeatureMarkers {
		found[m] = true
	}
	if !found["core/logging"] || !found["core/db-creds"] {
		t.Errorf("markers: %v", graph.Spec.FeatureMarkers)
	}
}

func TestBuildIR_SyncWave(t *testing.T) {
	t.Parallel()

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"db":  {InstanceID: "db", ComponentType: "core/database/postgres"},
			"api": {InstanceID: "api", ComponentType: "core/server/api-server"},
		},
		Edges:              nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release: &types.Release{
			Spec: types.ReleaseSpec{
				SyncWaves: func() types.OrderedMap[[]string] { _m := types.OrderedMap[[]string]{}; _m.Set("1-infra", []string{"db"}); _m.Set("2-services", []string{"api"}); return _m }(),
				Boundaries: types.OrderedMap[types.Boundary]{},
			},
		},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}

	graph, _, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: testCatalog(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dbNode, _ := graph.Spec.Nodes.Get("db")
	if dbNode.SyncWave != "1-infra" {
		t.Errorf("db sync wave: %q", dbNode.SyncWave)
	}
	apiNode, _ := graph.Spec.Nodes.Get("api")
	if apiNode.SyncWave != "2-services" {
		t.Errorf("api sync wave: %q", apiNode.SyncWave)
	}
}

func TestBuildIR_InfraMapping(t *testing.T) {
	t.Parallel()

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"api": {InstanceID: "api", ComponentType: "core/server/api-server"},
		},
		Edges:              nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"api": {Infra: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("replicas", 3); _m.Set("cpu", "500m"); return _m }()},
		},
	}

	graph, _, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: testCatalog(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := graph.Spec.InfraMapping.Get("api"); !ok {
		t.Fatal("expected infra mapping for api")
	}
}
