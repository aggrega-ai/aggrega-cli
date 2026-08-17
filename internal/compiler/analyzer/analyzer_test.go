// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func analyzerCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/compute/api-server":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/compute/api-server",
						Labels: labels,
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
						Name:   "core/datastore/postgres",
						Labels: labels2,
					},
				}, true
			case "core/redis":
				labels3 := types.OrderedMap[string]{}
				labels3.Set(types.ComponentTypeLabel, "resource")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/redis",
						Labels: labels3,
					},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			return types.Feature{}, false
		},
	}
}

func TestAnalyze_PathsCalculated(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("gateway", types.IRNode{Component: "core/compute/api-server"})
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server"})
	nodes.Set("db", types.IRNode{Component: "core/datastore/postgres"})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "api", Target: "db", Socket: "databases"},
				{Source: "gateway", Target: "api", Socket: "apis"},
			},
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if len(result.Paths) < 2 {
		t.Errorf("expected at least 2 paths, got %d", len(result.Paths))
	}
	// gateway→api→db should be one path
	found := false
	for _, p := range result.Paths {
		if len(p.Nodes) == 3 && p.Nodes[0] == "api" && p.Nodes[1] == "db" {
			// api→db is a 2-node path (wrong)
		}
		if len(p.Nodes) == 3 && p.Nodes[0] == "gateway" {
			found = true
		}
	}
	if !found {
		t.Error("expected gateway→api→db path")
	}
}

func TestAnalyze_PolicyInputAssembled(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set("a", "b")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", ResolvedVersion: "1.0.0", Labels: labels})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: nil,
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if result.PolicyInput == nil {
		t.Fatal("expected policy input")
	}
	// Under b-full the policy input carries the full IR graph; the nodes live at
	// Graph.Spec.Nodes (an OrderedMap), not a narrow projection.
	if result.PolicyInput.Graph.Spec.Nodes.Len() != 1 {
		t.Errorf("expected 1 IR node, got %d", result.PolicyInput.Graph.Spec.Nodes.Len())
	}
	pn, ok := result.PolicyInput.Graph.Spec.Nodes.Get("api")
	if !ok || pn.Component != "core/compute/api-server" || pn.ResolvedVersion != "1.0.0" {
		t.Errorf("IR node: %+v (found=%v)", pn, ok)
	}
}

func TestAnalyze_EmptyGraph(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: types.OrderedMap[types.IRNode]{},
			Edges: nil,
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(result.Errors))
	}
	if len(result.Paths) != 0 {
		t.Errorf("expected 0 paths, got %d", len(result.Paths))
	}
}
