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
			case "core/api-server":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/api-server",
						Version: "1.0.0",
						Labels:  map[string]string{types.ComponentTypeLabel: "server"},
						Annotations: map[string]string{
							"catalog.aggrega.ai/tier": "standard",
						},
					},
				}, true
			case "core/postgres":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/postgres",
						Version: "2.0.0",
						Labels:  map[string]string{types.ComponentTypeLabel: "stateful-server"},
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
						Labels: map[string]string{types.FeatureKindLabel: types.FeatureKindSecret},
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
			"api":     {InstanceID: "api", ComponentType: "core/api-server", Labels: map[string]string{types.ComponentTypeLabel: "server"}, Values: map[string]interface{}{"port": 8080}},
			"main-db": {InstanceID: "main-db", ComponentType: "core/postgres", Labels: map[string]string{types.ComponentTypeLabel: "stateful-server"}},
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
			Spec:     types.ReleaseSpec{Boundaries: map[string]types.Boundary{}},
		},
		Manifest: &types.Manifest{
			Metadata: types.ManifestMetadata{Version: "1.0.0"},
		},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"api": {BoundaryPath: "local/default", Annotations: map[string]string{"env": "local"}},
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
	if len(graph.Spec.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Spec.Nodes))
	}
	apiNode := graph.Spec.Nodes["api"]
	if apiNode.ResolvedVersion != "1.0.0" {
		t.Errorf("api resolved version: %q", apiNode.ResolvedVersion)
	}
	if apiNode.Component != "core/api-server" {
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
	if graph.Spec.ValuesMapping["api"] == nil {
		t.Error("expected api values mapping")
	}

	// Annotations
	if apiNode.CatalogAnnotations["catalog.aggrega.ai/tier"] != "standard" {
		t.Errorf("catalog annotations: %v", apiNode.CatalogAnnotations)
	}
	if apiNode.ReleaseAnnotations["env"] != "local" {
		t.Errorf("release annotations: %v", apiNode.ReleaseAnnotations)
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
	if graph.Metadata.AppName != "test-app" {
		t.Errorf("app name: %q", graph.Metadata.AppName)
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
			"api": {InstanceID: "api", ComponentType: "core/api-server", Labels: map[string]string{types.ComponentTypeLabel: "server"}},
		},
		Edges: nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "logging", Feature: types.FeatureInstance{Type: "core/logging", Values: map[string]interface{}{"level": "warn"}}},
			},
		},
		SecretAttachments: map[string][]topology.SecretAttachment{
			"api": {
				{InstanceName: "db-creds", Secret: types.SecretInstance{Type: "core/db-creds", Values: map[string]interface{}{"path": "/secret"}}},
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
	if fvm := graph.Spec.FeatureValuesMapping["api"]; fvm == nil {
		t.Fatal("expected feature values mapping for api")
	} else if fvm["logging"].Type != "core/logging" {
		t.Errorf("feature type: %q", fvm["logging"].Type)
	}

	// Secrets
	if sec := graph.Spec.Secrets["api"]; sec == nil {
		t.Fatal("expected secrets for api")
	} else if sec["db-creds"].Type != "core/db-creds" {
		t.Errorf("secret type: %q", sec["db-creds"].Type)
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
			"db":  {InstanceID: "db", ComponentType: "core/postgres"},
			"api": {InstanceID: "api", ComponentType: "core/api-server"},
		},
		Edges:              nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release: &types.Release{
			Spec: types.ReleaseSpec{
				SyncWaves: map[string][]string{
					"1-infra":    {"db"},
					"2-services": {"api"},
				},
				Boundaries: map[string]types.Boundary{},
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
	if graph.Spec.Nodes["db"].SyncWave != "1-infra" {
		t.Errorf("db sync wave: %q", graph.Spec.Nodes["db"].SyncWave)
	}
	if graph.Spec.Nodes["api"].SyncWave != "2-services" {
		t.Errorf("api sync wave: %q", graph.Spec.Nodes["api"].SyncWave)
	}
}

func TestBuildIR_InfraMapping(t *testing.T) {
	t.Parallel()

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"api": {InstanceID: "api", ComponentType: "core/api-server"},
		},
		Edges:              nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"api": {Infra: map[string]interface{}{"replicas": 3, "cpu": "500m"}},
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
	if graph.Spec.InfraMapping["api"] == nil {
		t.Fatal("expected infra mapping for api")
	}
}
