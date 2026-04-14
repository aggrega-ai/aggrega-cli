package analyzer

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func analyzerCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/api-server":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/api-server",
						Labels: map[string]string{types.ComponentTypeLabel: "server"},
					},
					Spec: types.ComponentSpec{
						UsesSchema: map[string]types.UsesSchemaEntry{
							"databases": {Selector: "component.aggrega.ai/type=stateful-server"},
						},
					},
				}, true
			case "core/postgres":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/postgres",
						Labels: map[string]string{types.ComponentTypeLabel: "stateful-server"},
					},
				}, true
			case "core/redis":
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:   "core/redis",
						Labels: map[string]string{types.ComponentTypeLabel: "resource"},
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
			Nodes: map[string]types.IRNode{
				"api":     {Component: "core/api-server", Labels: map[string]string{types.ComponentTypeLabel: "server"}},
				"main-db": {Component: "core/postgres", Labels: map[string]string{types.ComponentTypeLabel: "stateful-server"}},
			},
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
			Nodes: map[string]types.IRNode{
				"api":   {Component: "core/api-server", Labels: map[string]string{types.ComponentTypeLabel: "server"}},
				"cache": {Component: "core/redis", Labels: map[string]string{types.ComponentTypeLabel: "resource"}},
			},
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

func TestAnalyze_PathsCalculated(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"gateway": {Component: "core/api-server"},
				"api":     {Component: "core/api-server"},
				"db":      {Component: "core/postgres"},
			},
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
			Nodes: map[string]types.IRNode{
				"api": {Component: "core/api-server", ResolvedVersion: "1.0.0", Labels: map[string]string{"a": "b"}},
			},
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
	if pn.Component != "core/api-server" || pn.ResolvedVersion != "1.0.0" {
		t.Errorf("policy node: %+v", pn)
	}
}

func TestAnalyze_EmptyGraph(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{},
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
