// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// declarationCatalog declares one socket on the consuming Component
// (`databases`) and a DIFFERENT one on the Feature (`peer`), so a derivation that
// consults the wrong entity produces the wrong answer rather than accidentally the
// right one.
func declarationCatalog() resolver.CatalogLookup {
	compUses := types.OrderedMap[types.UsesSchemaEntry]{}
	compUses.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
	featUses := types.OrderedMap[types.UsesSchemaEntry]{}
	featUses.Set("peer", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})

	serverLabels := types.OrderedMap[string]{}
	serverLabels.Set(types.ComponentTypeLabel, "server")
	dbLabels := types.OrderedMap[string]{}
	dbLabels.Set(types.ComponentTypeLabel, "stateful-server")

	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/compute/api-server":
				return types.Component{
					Metadata: types.ComponentMetadata{Name: typeURI, Version: "1.0.0", Labels: serverLabels},
					Spec:     types.ComponentSpec{UsesSchema: compUses},
				}, true
			case "core/datastore/postgres":
				return types.Component{
					Metadata: types.ComponentMetadata{Name: typeURI, Version: "2.0.0", Labels: dbLabels},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == "core/sidecar" {
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: typeURI, Version: "1.0.0"},
					Spec:     types.FeatureSpec{UsesSchema: featUses},
				}, true
			}
			return types.Feature{}, false
		},
	}
}

// TestBuildIR_SocketDeclarationCoversEveryEdgeOrigin enumerates every authoring
// origin an edge can arrive at the construction site with — Aggregate-authored,
// Feature-authored, and Release-wired — in both the declared and the ad-hoc case,
// and pins the marker on each. The declaration marker is a THIRD axis: within one
// origin both values must be reachable, and across origins the same value must be
// reachable, or the marker would be a re-spelling of authorship.
//
// The Feature rows are the ones that would silently mis-mark: `peer` is declared
// on the Feature and NOT on the consuming Component, while `databases` is declared
// on the Component and not on the Feature, so a derivation reading the wrong
// entity inverts both rows rather than failing loudly.
func TestBuildIR_SocketDeclarationCoversEveryEdgeOrigin(t *testing.T) {
	t.Parallel()

	serverLabels := types.OrderedMap[string]{}
	serverLabels.Set(types.ComponentTypeLabel, "server")
	dbLabels := types.OrderedMap[string]{}
	dbLabels.Set(types.ComponentTypeLabel, "stateful-server")
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/compute/api-server", Labels: serverLabels})
	nodes.Set("main-db", &topology.TopologyNode{InstanceID: "main-db", ComponentType: "core/datastore/postgres", Labels: dbLabels})

	edges := []topology.NormalizedEdge{
		// Aggregate-authored, socket declared on the consuming Component.
		{Source: "api", Target: "main-db", Socket: "databases", Alias: "a-declared"},
		// Aggregate-authored, socket declared nowhere.
		{Source: "api", Target: "main-db", Socket: "freeform", Alias: "a-adhoc"},
		// Feature-authored, socket declared on the originating Feature.
		{Source: "api", Target: "main-db", Socket: "peer", Alias: "f-declared",
			OriginFeature: "core/sidecar", OriginFeatureInstance: "side"},
		// Feature-authored, socket declared on neither entity. `databases` is
		// deliberately reused: it IS on the Component, so a derivation that consults
		// the Component for a Feature-authored edge would call this declared.
		{Source: "api", Target: "main-db", Socket: "databases", Alias: "f-adhoc",
			OriginFeature: "core/sidecar", OriginFeatureInstance: "side"},
		// Release-wired, socket declared on the consuming Component.
		{Source: "api", Target: "main-db", Socket: "databases", Alias: "r-declared", OriginRelease: true},
		// Release-wired into a socket declared nowhere. The resolver still refuses to
		// let a Release name an undeclared socket; this row pins that IF such an edge
		// reaches construction it is marked honestly rather than defaulting to
		// declared.
		{Source: "api", Target: "main-db", Socket: "freeform", Alias: "r-adhoc", OriginRelease: true},
	}

	graph, _, _, err := buildIR(BuildIRConfig{
		Topology: &topology.TopologyResult{
			Nodes:              nodes,
			Edges:              edges,
			FeatureAttachments: map[string][]topology.FeatureAttachment{},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app", Version: "1.0.0"}},
			Manifest:      &types.Manifest{Metadata: types.ManifestMetadata{Version: "1.0.0"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "local/default"}},
		},
		CatalogLookup:   declarationCatalog(),
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("BuildIRContext: %v", err)
	}

	want := map[string]string{
		"a-declared": types.SocketDeclarationDeclared,
		"a-adhoc":    types.SocketDeclarationAdHoc,
		"f-declared": types.SocketDeclarationDeclared,
		"f-adhoc":    types.SocketDeclarationAdHoc,
		"r-declared": types.SocketDeclarationDeclared,
		"r-adhoc":    types.SocketDeclarationAdHoc,
	}
	if len(graph.Spec.Edges) != len(want) {
		t.Fatalf("edge count = %d, want %d", len(graph.Spec.Edges), len(want))
	}
	for _, e := range graph.Spec.Edges {
		w, ok := want[e.Alias]
		if !ok {
			t.Fatalf("unexpected edge alias %q", e.Alias)
		}
		if e.SocketDeclaration != w {
			t.Errorf("alias %q (socket %q, originFeature %q, originRelease %v): SocketDeclaration = %q, want %q",
				e.Alias, e.Socket, e.OriginFeature, e.OriginRelease, e.SocketDeclaration, w)
		}
		delete(want, e.Alias)
	}
	if len(want) != 0 {
		t.Errorf("edges missing from the graph: %v", want)
	}
}

// TestBuildIR_SocketDeclarationNeverEmpty pins the field's always-populated
// contract at the construction site, independently of the schema enum that
// enforces it on the wire. An empty value is the failure mode that would read as
// "declared" to a consumer, so it must be unreachable rather than merely unlikely.
func TestBuildIR_SocketDeclarationNeverEmpty(t *testing.T) {
	t.Parallel()

	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "unknown/type"})
	nodes.Set("db", &topology.TopologyNode{InstanceID: "db", ComponentType: "core/datastore/postgres"})

	for _, tc := range []struct {
		name    string
		catalog resolver.CatalogLookup
	}{
		{"unknown component type", declarationCatalog()},
		{"no catalog lookups at all", resolver.CatalogLookup{}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			graph, _, _, err := buildIR(BuildIRConfig{
				Topology: &topology.TopologyResult{
					Nodes: nodes,
					Edges: []topology.NormalizedEdge{
						{Source: "api", Target: "db", Socket: "databases", Alias: "x"},
						{Source: "ghost-source", Target: "db", Socket: "databases", Alias: "y"},
					},
					FeatureAttachments: map[string][]topology.FeatureAttachment{},
				},
				ResolveResult: &resolver.ResolveResult{
					Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app", Version: "1.0.0"}},
					Manifest:      &types.Manifest{Metadata: types.ManifestMetadata{Version: "1.0.0"}},
					BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "local/default"}},
				},
				CatalogLookup:   tc.catalog,
				CompilerVersion: "0.1.0",
			})
			if err != nil {
				t.Fatalf("BuildIRContext: %v", err)
			}
			for _, e := range graph.Spec.Edges {
				switch e.SocketDeclaration {
				case types.SocketDeclarationDeclared, types.SocketDeclarationAdHoc:
				default:
					t.Errorf("alias %q: SocketDeclaration = %q, want one of the two defined values",
						e.Alias, e.SocketDeclaration)
				}
			}
		})
	}
}
