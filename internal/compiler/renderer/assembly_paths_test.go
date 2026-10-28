// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// assemblyFixture builds a Release, the graph rendered from it, and the inputs the
// render reads before its per-node loop:
//
//   - boundary "bridge" (directory: bridge) whose children "frontend" (adminer)
//     and "backend" (db) take their parent's directory, so both assemble into one
//     file at bridge/compose;
//   - boundary "apart" (directory: apart, subDirectory templated on the
//     component) holding "left" and "right", which therefore assemble into two
//     files, apart/left and apart/right.
//
// Every node is bound to a compose Recipe; adminer uses db, left uses right.
func assemblyFixture(t *testing.T) (*types.Release, *types.IRGraph, RendererInput, map[string]*types.Recipe, map[string]string, map[string]types.OrderedMap[string]) {
	t.Helper()
	components := func(names ...string) types.OrderedMap[types.BoundaryComponent] {
		m := types.OrderedMap[types.BoundaryComponent]{}
		for _, n := range names {
			m.Set(n, types.BoundaryComponent{})
		}
		return m
	}
	bridge := types.Boundary{Directory: "bridge"}
	bridge.Boundaries.Set("frontend", types.Boundary{Directory: "{{ .parent_boundary.directory }}", Components: components("adminer")})
	bridge.Boundaries.Set("backend", types.Boundary{Directory: "{{ .parent_boundary.directory }}", Components: components("db")})
	apart := types.Boundary{Directory: "apart", SubDirectory: "{{ .component_id }}", Components: components("left", "right")}
	rel := &types.Release{}
	rel.Spec.Boundaries.Set("bridge", bridge)
	rel.Spec.Boundaries.Set("apart", apart)

	perComponent, perBoundary := resolver.WireBoundaryInfra(types.NormalizeRelease(rel))
	dirEvals, err := evaluateAllBoundaryDirectories(rel, nil, perBoundary)
	if err != nil {
		t.Fatalf("evaluate the boundary directories: %v", err)
	}

	graph := &types.IRGraph{}
	recipes := map[string]*types.Recipe{}
	for _, id := range []string{"adminer", "db", "left", "right"} {
		bi := perComponent[id]
		if bi == nil {
			t.Fatalf("the Release places no component %q", id)
		}
		graph.Spec.Nodes.Set(id, types.IRNode{Component: "core/example/" + id, ResolvedVersion: "1.0.0", BoundaryPath: bi.BoundaryPath})
		r := &types.Recipe{}
		r.Metadata.Name = id + "-recipe"
		r.Spec.Capability = "compose"
		recipes[id] = r
	}
	graph.Spec.Edges = []types.IREdge{
		{Source: "adminer", Target: "db", Socket: "databases", Alias: "db"},
		{Source: "left", Target: "right", Socket: "apis", Alias: "right"},
	}
	input := RendererInput{ResolveResult: &resolver.ResolveResult{Release: rel, BoundaryInfra: perComponent}}
	return rel, graph, input, recipes, dirEvals, perBoundary
}

// TestAssemblyPaths_AreTheDispatchPathsThePerNodeLoopEvaluates holds every answer
// the edge views carry to the dispatch path the per-node loop evaluates for the
// same node, through the loop's own input reader, and to the paths the fixture's
// layout fixes: two boundaries sharing a directory assemble at one path, and a
// subDirectory templated on the component places two components of one boundary
// at two.
func TestAssemblyPaths_AreTheDispatchPathsThePerNodeLoopEvaluates(t *testing.T) {
	t.Parallel()
	_, graph, input, recipes, dirEvals, perBoundary := assemblyFixture(t)
	got := assemblyPathsByNode(graph, recipes, input, dirEvals, perBoundary)

	want := map[string]string{"adminer": "bridge/compose", "db": "bridge/compose", "left": "apart/left", "right": "apart/right"}
	for nodeID, wantPath := range want {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		bi, parameters := nodeBoundaryInputs(input, nodeID)
		loop, err := computeFinalPath(nodeID, node.BoundaryPath, "compose", recipes[nodeID].Metadata.Name, bi, parameters, dirEvals, perBoundary)
		if err != nil {
			t.Fatalf("%s: the loop's evaluation: %v", nodeID, err)
		}
		if got[nodeID] != loop.path {
			t.Errorf("%s: assembly path %q, the per-node loop evaluates %q", nodeID, got[nodeID], loop.path)
		}
		if got[nodeID] != wantPath {
			t.Errorf("%s: assembly path %q, want %q", nodeID, got[nodeID], wantPath)
		}
	}
	if len(got) != len(want) {
		t.Errorf("answers for %d node(s), want %d: %v", len(got), len(want), got)
	}
}

// TestAssemblyPaths_EdgeViewsCarryEachEndpointsOwn builds each boundary's view and
// reads the edge's two endpoints: the boundary paths stay the Release boundary
// paths, and the assembly paths are each endpoint's own — one path for two
// boundaries sharing a file, two paths for two components a templated
// subDirectory places apart.
func TestAssemblyPaths_EdgeViewsCarryEachEndpointsOwn(t *testing.T) {
	t.Parallel()
	rel, graph, input, recipes, dirEvals, perBoundary := assemblyFixture(t)
	paths := assemblyPathsByNode(graph, recipes, input, dirEvals, perBoundary)

	for _, tc := range []struct {
		boundary, source, target       string
		wantSourcePath, wantTargetPath string
		wantSourceAsm, wantTargetAsm   string
	}{
		{"bridge", "adminer", "db", "bridge/frontend", "bridge/backend", "bridge/compose", "bridge/compose"},
		{"apart", "left", "right", "apart", "apart", "apart/left", "apart/right"},
	} {
		view, err := buildBoundaryContext(nil, tc.boundary, graph, rel, perBoundary, paths)
		if err != nil {
			t.Fatalf("the view of boundary %q: %v", tc.boundary, err)
		}
		if len(view.InternalUsesEdges) != 1 {
			t.Fatalf("boundary %q: %d internal edge(s), want 1", tc.boundary, len(view.InternalUsesEdges))
		}
		e := view.InternalUsesEdges[0]
		if e.Source != tc.source || e.Target != tc.target {
			t.Fatalf("boundary %q: edge %s -> %s, want %s -> %s", tc.boundary, e.Source, e.Target, tc.source, tc.target)
		}
		if e.SourceBoundaryPath != tc.wantSourcePath || e.TargetBoundaryPath != tc.wantTargetPath {
			t.Errorf("boundary %q: boundary paths %q, %q; want %q, %q", tc.boundary, e.SourceBoundaryPath, e.TargetBoundaryPath, tc.wantSourcePath, tc.wantTargetPath)
		}
		if e.SourceAssemblyPath != tc.wantSourceAsm || e.TargetAssemblyPath != tc.wantTargetAsm {
			t.Errorf("boundary %q: assembly paths %q, %q; want %q, %q", tc.boundary, e.SourceAssemblyPath, e.TargetAssemblyPath, tc.wantSourceAsm, tc.wantTargetAsm)
		}
	}
}
