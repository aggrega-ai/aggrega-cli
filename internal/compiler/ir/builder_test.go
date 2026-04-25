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
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				annotations := types.OrderedMap[string]{}
				annotations.Set("catalog.aggrega.ai/tier", "standard")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:        "core/server/api-server",
						Version:     "1.0.0",
						Labels:      labels,
						Annotations: annotations,
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
			default:
				return types.Component{}, false
			}
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == "core/db-creds" {
				labels := types.OrderedMap[string]{}
				labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:   "core/db-creds",
						Labels: labels,
					},
				}, true
			}
			return types.Feature{}, false
		},
	}
}

func TestBuildIR_SimpleGraph(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	values := types.OrderedMap[interface{}]{}
	values.Set("port", 8080)
	labels2 := types.OrderedMap[string]{}
	labels2.Set(types.ComponentTypeLabel, "stateful-server")
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server", Labels: labels, Values: values})
	nodes.Set("main-db", &topology.TopologyNode{InstanceID: "main-db", ComponentType: "core/database/postgres", Labels: labels2})
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		Edges: []topology.NormalizedEdge{
			{Source: "api", Target: "main-db", Socket: "databases", Alias: "main-db"},
		},
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}
	annotations := types.OrderedMap[string]{}
	annotations.Set("env", "local")

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
			"api": {BoundaryPath: "local/default", Annotations: annotations},
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
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	values := types.OrderedMap[interface{}]{}
	values.Set("level", "warn")
	values2 := types.OrderedMap[interface{}]{}
	values2.Set("path", "/secret")
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server", Labels: labels})
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		Edges: nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "logging", Feature: types.FeatureInstance{Type: "core/logging", Values: values}},
			},
		},
		SecretAttachments: map[string][]topology.SecretAttachment{
			"api": {
				{InstanceName: "db-creds", Secret: types.SecretInstance{Type: "core/db-creds", Values: values2}},
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

	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("db", &topology.TopologyNode{InstanceID: "db", ComponentType: "core/database/postgres"})
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server"})
	topoResult := &topology.TopologyResult{
		Nodes:              nodes,
		Edges:              nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}
	syncWaves := types.OrderedMap[[]string]{}
	syncWaves.Set("1-infra", []string{"db"})
	syncWaves.Set("2-services", []string{"api"})
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release: &types.Release{
			Spec: types.ReleaseSpec{
				SyncWaves:  syncWaves,
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

	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server"})
	topoResult := &topology.TopologyResult{
		Nodes:              nodes,
		Edges:              nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}
	infra := types.OrderedMap[interface{}]{}
	infra.Set("replicas", 3)
	infra.Set("cpu", "500m")
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"api": {Infra: infra},
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
