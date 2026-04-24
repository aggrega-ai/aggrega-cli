// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func analyzerCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server/api-server":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/server/api-server",
						Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(),
					},
					Spec: types.ComponentSpec{
						UsesSchema: func() types.OrderedMap[types.UsesSchemaEntry] { _m := types.OrderedMap[types.UsesSchemaEntry]{}; _m.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"}); return _m }(),
					},
				}, true
			case "core/database/postgres":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/database/postgres",
						Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "stateful-server"); return _m }(),
					},
				}, true
			case "core/redis":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/redis",
						Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "resource"); return _m }(),
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

func TestAnalyze_SelectorPass(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: func() types.OrderedMap[types.IRNode] { _m := types.OrderedMap[types.IRNode]{}; _m.Set("api", types.IRNode{Component: "core/server/api-server", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }()}); _m.Set("main-db", types.IRNode{Component: "core/database/postgres", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "stateful-server"); return _m }()}); return _m }(),
			Edges: []types.IREdge{
				{Source: "api", Target: "main-db", Socket: "databases"},
			},
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 selector errors, got %d: %v", len(result.Errors), result.Errors[0].Message)
	}
}

func TestAnalyze_SelectorFail(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: func() types.OrderedMap[types.IRNode] { _m := types.OrderedMap[types.IRNode]{}; _m.Set("api", types.IRNode{Component: "core/server/api-server", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }()}); _m.Set("cache", types.IRNode{Component: "core/redis", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "resource"); return _m }()}); return _m }(),
			Edges: []types.IREdge{
				{Source: "api", Target: "cache", Socket: "databases"},
			},
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 selector error, got %d", len(result.Errors))
	}
	e := result.Errors[0]
	if e.Source != "api" || e.Target != "cache" || e.Socket != "databases" {
		t.Errorf("error: %+v", e)
	}
	if e.Selector != "component.aggrega.ai/type=stateful-server" {
		t.Errorf("selector: %q", e.Selector)
	}
}

func analyzerCatalogWithFeature(feat types.Feature) resolver.CatalogLookup {
	base := analyzerCatalog()
	return resolver.CatalogLookup{
		Component: base.Component,
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == feat.Metadata.Name {
				return feat, true
			}
			return types.Feature{}, false
		},
	}
}

func TestAnalyze_AppliesToPass(t *testing.T) {
	t.Parallel()

	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesTo: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(),
		},
	}

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: func() types.OrderedMap[types.IRNode] { _m := types.OrderedMap[types.IRNode]{}; _m.Set("api", types.IRNode{Component: "core/server/api-server", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }()}); return _m }(),
			FeatureValuesMapping: func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
				inner := types.OrderedMap[types.IRFeatureEntry]{}
				inner.Set("monitoring", types.IRFeatureEntry{Type: "core/monitoring"})
				outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
				outer.Set("api", inner)
				return outer
			}(),
		},
	}

	result := Analyze(graph, analyzerCatalogWithFeature(feat))
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d: %+v", len(result.Errors), result.Errors)
	}
}

func TestAnalyze_AppliesToFail(t *testing.T) {
	t.Parallel()

	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesTo: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(),
		},
	}

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: func() types.OrderedMap[types.IRNode] { _m := types.OrderedMap[types.IRNode]{}; _m.Set("db", types.IRNode{Component: "core/database/postgres", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "stateful-server"); return _m }()}); return _m }(),
			FeatureValuesMapping: func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
				inner := types.OrderedMap[types.IRFeatureEntry]{}
				inner.Set("monitoring", types.IRFeatureEntry{Type: "core/monitoring"})
				outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
				outer.Set("db", inner)
				return outer
			}(),
		},
	}

	result := Analyze(graph, analyzerCatalogWithFeature(feat))
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(result.Errors), result.Errors)
	}
	e := result.Errors[0]
	if e.Source != "db" || e.Target != "monitoring" {
		t.Errorf("error fields: %+v", e)
	}
	if !strings.Contains(e.Message, "appliesTo constraint not satisfied") {
		t.Errorf("message: %q", e.Message)
	}
	if !strings.Contains(e.Selector, "component.aggrega.ai/type=server") {
		t.Errorf("selector: %q", e.Selector)
	}
}

func TestAnalyze_AppliesToExpressions(t *testing.T) {
	t.Parallel()

	feat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring"},
		Spec: types.FeatureSpec{
			AppliesToExpressions: []types.LabelSelectorRequirement{
				{Key: types.ComponentTypeLabel, Operator: "In", Values: []string{"server", "worker"}},
			},
		},
	}

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: func() types.OrderedMap[types.IRNode] { _m := types.OrderedMap[types.IRNode]{}; _m.Set("worker", types.IRNode{Component: "core/server/api-server", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "worker"); return _m }()}); return _m }(),
			FeatureValuesMapping: func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
				inner := types.OrderedMap[types.IRFeatureEntry]{}
				inner.Set("mon", types.IRFeatureEntry{Type: "core/monitoring"})
				outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
				outer.Set("worker", inner)
				return outer
			}(),
		},
	}

	result := Analyze(graph, analyzerCatalogWithFeature(feat))
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors (worker matches In [server, worker]), got %d: %+v", len(result.Errors), result.Errors)
	}
}

func TestAnalyze_PathsCalculated(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: func() types.OrderedMap[types.IRNode] { _m := types.OrderedMap[types.IRNode]{}; _m.Set("gateway", types.IRNode{Component: "core/server/api-server"}); _m.Set("api", types.IRNode{Component: "core/server/api-server"}); _m.Set("db", types.IRNode{Component: "core/database/postgres"}); return _m }(),
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

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: func() types.OrderedMap[types.IRNode] { _m := types.OrderedMap[types.IRNode]{}; _m.Set("api", types.IRNode{Component: "core/server/api-server", ResolvedVersion: "1.0.0", Labels: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("a", "b"); return _m }()}); return _m }(),
			Edges: nil,
		},
	}

	result := Analyze(graph, analyzerCatalog())
	if result.PolicyInput == nil {
		t.Fatal("expected policy input")
	}
	if len(result.PolicyInput.Graph.Nodes) != 1 {
		t.Errorf("expected 1 policy node, got %d", len(result.PolicyInput.Graph.Nodes))
	}
	pn := result.PolicyInput.Graph.Nodes["api"]
	if pn.Component != "core/server/api-server" || pn.ResolvedVersion != "1.0.0" {
		t.Errorf("policy node: %+v", pn)
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
