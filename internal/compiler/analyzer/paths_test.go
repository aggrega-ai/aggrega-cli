// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestCalculatePaths_LinearChain(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("a", types.IRNode{})
	nodes.Set("b", types.IRNode{})
	nodes.Set("c", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "a", Target: "b"},
				{Source: "b", Target: "c"},
			},
		},
	}

	paths := calculatePaths(graph)
	// a→b→c (full path), a→b (partial — no, b has outgoing so DFS continues)
	// Actually: from a: a→b→c (c is terminal). from b: b→c.
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	// First path from "a": a→b→c
	if paths[0].Nodes[0] != "a" || paths[0].Nodes[len(paths[0].Nodes)-1] != "c" {
		t.Errorf("path 0: %v", paths[0].Nodes)
	}
	// Second path from "b": b→c
	if paths[1].Nodes[0] != "b" || paths[1].Nodes[1] != "c" {
		t.Errorf("path 1: %v", paths[1].Nodes)
	}
}

func TestCalculatePaths_CyclicGraph(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("a", types.IRNode{})
	nodes.Set("b", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "a", Target: "b"},
				{Source: "b", Target: "a"},
			},
		},
	}

	paths := calculatePaths(graph)
	// a→b (b tries to go back to a, cycle — emits a→b)
	// b→a (a tries to go back to b, cycle — emits b→a)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths (cycle-safe), got %d", len(paths))
	}
	for _, p := range paths {
		if len(p.Nodes) != 2 {
			t.Errorf("expected 2-node path, got %v", p.Nodes)
		}
	}
}

func TestCalculatePaths_Diamond(t *testing.T) {
	t.Parallel()
	nodes :=

		// a → b, a → c, b → d, c → d
		types.OrderedMap[types.IRNode]{}
	nodes.Set("a", types.IRNode{})
	nodes.Set("b", types.IRNode{})
	nodes.Set("c", types.IRNode{})
	nodes.Set("d", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "a", Target: "b"},
				{Source: "a", Target: "c"},
				{Source: "b", Target: "d"},
				{Source: "c", Target: "d"},
			},
		},
	}

	paths := calculatePaths(graph)
	// From a: a→b→d, a→c→d
	// From b: b→d
	// From c: c→d
	if len(paths) != 4 {
		t.Fatalf("expected 4 paths, got %d", len(paths))
	}
}

func TestCalculatePaths_NoEdges(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("a", types.IRNode{})
	nodes.Set("b", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: nil,
		},
	}

	paths := calculatePaths(graph)
	if len(paths) != 0 {
		t.Fatalf("expected 0 paths, got %d", len(paths))
	}
}

func TestCalculatePaths_AttachedFeatures(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("a", types.IRNode{})
	nodes.Set("b", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "a", Target: "b"},
			},
			FeatureValuesMapping: func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
				aInner := types.OrderedMap[types.IRFeatureEntry]{}
				aInner.Set("monitoring", types.IRFeatureEntry{Type: "core/monitoring"})
				bInner := types.OrderedMap[types.IRFeatureEntry]{}
				bInner.Set("logging", types.IRFeatureEntry{Type: "core/logging"})
				outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
				outer.Set("a", aInner)
				outer.Set("b", bInner)
				return outer
			}(),
		},
	}

	paths := calculatePaths(graph)
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	if len(paths[0].AttachedFeatures) != 2 {
		t.Errorf("expected 2 attached features, got %v", paths[0].AttachedFeatures)
	}
	// Sorted: core/logging < core/monitoring
	if paths[0].AttachedFeatures[0] != "core/logging" || paths[0].AttachedFeatures[1] != "core/monitoring" {
		t.Errorf("features not sorted: %v", paths[0].AttachedFeatures)
	}
}

func TestCalculatePaths_DeterministicOrder(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("z", types.IRNode{})
	nodes.Set("a", types.IRNode{})
	nodes.Set("m", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "z", Target: "a"},
				{Source: "m", Target: "a"},
				{Source: "a", Target: "m"},
			},
		},
	}

	var firstPathIDs []string
	for i := 0; i < 10; i++ {
		paths := calculatePaths(graph)
		ids := make([]string, len(paths))
		for j, p := range paths {
			ids[j] = p.Nodes[0] + "→" + p.Nodes[len(p.Nodes)-1]
		}
		if i == 0 {
			firstPathIDs = ids
		} else {
			for j, id := range ids {
				if id != firstPathIDs[j] {
					t.Errorf("run %d: path order changed at %d: %q vs %q", i, j, id, firstPathIDs[j])
				}
			}
		}
	}
}

func TestCalculatePaths_PathIDs(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("a", types.IRNode{})
	nodes.Set("b", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "a", Target: "b"},
			},
		},
	}

	paths := calculatePaths(graph)
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	if paths[0].PathID != "path_01" {
		t.Errorf("path ID: %q", paths[0].PathID)
	}
}
