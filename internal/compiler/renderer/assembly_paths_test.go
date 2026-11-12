// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
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

// unansweringProvider is a provider engine that answers no name-bound question,
// so the composed identity settled against it is the composition as composed.
// Nothing in these tests renders through it.
type unansweringProvider struct{ Provider }

// boundingProvider is a provider engine that answers the name-bound question
// from its oracle. Nothing in these tests renders through it.
type boundingProvider struct {
	Provider
	*boundOracle
}

// TestBoundaryFRAssemblyPath_IsTheRoutingsOwnDispatchPath attaches a
// boundary-scope compose FeatureRecipe at both of the fixture's boundaries and
// reads, for each attachment, the dispatch key the routing places its output
// under and the assembly path its own boundary view carries — the view built
// through the same builder, from the same answer, the provides pre-pass and the
// dispatch build it from. The two must be one value: as written, where the
// attachment assembles into the file its boundary's components do, and where the
// boundary's subDirectory is templated on the component, so the attachment's own
// identity decides its directory and no component's path can stand in for it.
func TestBoundaryFRAssemblyPath_IsTheRoutingsOwnDispatchPath(t *testing.T) {
	t.Parallel()
	rel, graph, input, _, dirEvals, perBoundary := assemblyFixture(t)
	for _, boundary := range []string{"bridge", "apart"} {
		inner := types.OrderedMap[types.IRBoundaryFeature]{}
		inner.Set("gateway", types.IRBoundaryFeature{Type: "example/gateway:1.0.0"})
		graph.Spec.BoundaryFeatures.Set(boundary, inner)
	}
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = "gateway-fr"
	fr.Spec.Capability = "compose"
	fr.Spec.ExecutionScope = executionScopeBoundary
	lookup := RecipeLookup{FeatureRecipe: func(instanceName, featureType string) (*types.FeatureRecipe, bool) {
		return fr, instanceName == "gateway" && featureType == "example/gateway:1.0.0"
	}}
	factory := func(string) (Provider, error) { return unansweringProvider{}, nil }
	state := NewBoundaryFRState()
	own := assemblyPathsByBoundaryFR(context.Background(), graph, lookup, factory, input, dirEvals, perBoundary, state)

	for _, tc := range []struct {
		boundary, want string
	}{
		{"bridge", "bridge/compose"},
		// Written out rather than computed from the composition: a value taken from
		// it would agree with it whatever it did.
		{"apart", "apart/bfr--apart--gateway"},
	} {
		key := boundaryFRKey{BoundaryPath: tc.boundary, FRInstance: "gateway"}
		routed, eval, err := boundaryFRDispatch(key, fr, state.ComponentIDs[key], input, dirEvals, perBoundary)
		if err != nil {
			t.Fatalf("%s: the routing's evaluation: %v", tc.boundary, err)
		}
		if routed.Path != eval.path || routed.Provider != "compose" {
			t.Errorf("%s: routing key %+v does not carry its evaluation's path %q under the compose provider", tc.boundary, routed, eval.path)
		}
		frCtx, err := buildBoundaryFRTemplateContext(nil, fr, "gateway", key, graph, rel, perBoundary, nil, own[key])
		if err != nil {
			t.Fatalf("%s: the attachment's view: %v", tc.boundary, err)
		}
		if got := frCtx.Boundary.AssemblyPath; got != routed.Path {
			t.Errorf("%s: the view's assembly path %q, the routing places the output at %q", tc.boundary, got, routed.Path)
		}
		if routed.Path != tc.want {
			t.Errorf("%s: routed to %q, want %q", tc.boundary, routed.Path, tc.want)
		}
		if frCtx.Boundary.Path != tc.boundary {
			t.Errorf("%s: the view's boundary path %q, want the Release boundary %q", tc.boundary, frCtx.Boundary.Path, tc.boundary)
		}
	}
	if len(own) != 2 {
		t.Errorf("answers for %d attachment(s), want 2: %v", len(own), own)
	}
}

// TestBoundaryFRComponentID_ReadsTheSettledIdentityAndGroupsAsBefore attaches one
// boundary-scope FeatureRecipe under the same instance name at two boundaries
// whose directories evaluate alike, and reads where each attachment's output is
// routed under two subDirectory: templates.
//
// Where the template does not read .component_id, the two attachments reach one
// dispatch key, exactly as they always have: the grouping is the evaluated path,
// and nothing the attachment is identified by enters it.
//
// Where it does, each attachment's .component_id is the composed identity
// settled for it against its own engine — the value recorded for its render to
// stamp on its resources — and never the dispatch marker. The engine here bounds
// names, so the settled identity is a SHORTENED composition: a path that
// recomposed the identity instead of reading the settled one would carry the
// unbounded form and fail below, and one handed the dispatch marker would carry
// `<`, `>` and `:` and put both attachments in one directory. The identity is
// settled once: reading it again asks the engine nothing.
func TestBoundaryFRComponentID_ReadsTheSettledIdentityAndGroupsAsBefore(t *testing.T) {
	t.Parallel()
	const bound = 40
	east, west := strings.Repeat("east", 12), strings.Repeat("west", 12)

	route := func(t *testing.T, subDirectory string) (map[string]dispatchKey, *BoundaryFRState, *boundOracle) {
		t.Helper()
		rel := &types.Release{}
		graph := &types.IRGraph{}
		for _, b := range []string{east, west} {
			rel.Spec.Boundaries.Set(b, types.Boundary{Directory: "shared", SubDirectory: subDirectory})
			inner := types.OrderedMap[types.IRBoundaryFeature]{}
			inner.Set("gateway", types.IRBoundaryFeature{Type: "example/gateway:1.0.0"})
			graph.Spec.BoundaryFeatures.Set(b, inner)
		}
		_, perBoundary := resolver.WireBoundaryInfra(types.NormalizeRelease(rel))
		dirEvals, err := evaluateAllBoundaryDirectories(rel, nil, perBoundary)
		if err != nil {
			t.Fatalf("evaluate the boundary directories: %v", err)
		}
		input := RendererInput{ResolveResult: &resolver.ResolveResult{Release: rel}}
		fr := &types.FeatureRecipe{}
		fr.Metadata.Name = "gateway-fr"
		fr.Spec.Capability = "compose"
		fr.Spec.ExecutionScope = executionScopeBoundary
		lookup := RecipeLookup{FeatureRecipe: func(instanceName, featureType string) (*types.FeatureRecipe, bool) {
			return fr, instanceName == "gateway" && featureType == "example/gateway:1.0.0"
		}}
		oracle := &boundOracle{answer: func(proposed string) DerivedNameVerdict {
			if len(proposed) > bound {
				return DerivedNameVerdict{Kind: "service name", Usable: false, Bound: boundTo(bound), Reason: "too long"}
			}
			return DerivedNameVerdict{Kind: "service name", Usable: true}
		}}
		factory := func(string) (Provider, error) { return boundingProvider{boundOracle: oracle}, nil }
		state := NewBoundaryFRState()
		own := assemblyPathsByBoundaryFR(context.Background(), graph, lookup, factory, input, dirEvals, perBoundary, state)

		routed := map[string]dispatchKey{}
		for _, b := range []string{east, west} {
			key := boundaryFRKey{BoundaryPath: b, FRInstance: "gateway"}
			dKey, _, err := boundaryFRDispatch(key, fr, state.ComponentIDs[key], input, dirEvals, perBoundary)
			if err != nil {
				t.Fatalf("%s: the routing's evaluation: %v", b, err)
			}
			if own[key] != dKey.Path {
				t.Errorf("%s: answered %q before the render, routed to %q after it", b, own[key], dKey.Path)
			}
			routed[b] = dKey
		}
		return routed, state, oracle
	}

	t.Run("a subDirectory that does not read the identity groups the two as before", func(t *testing.T) {
		t.Parallel()
		routed, _, _ := route(t, "{{ .capability }}")
		want := dispatchKey{Path: "shared/compose", Provider: "compose"}
		if routed[east] != want || routed[west] != want {
			t.Errorf("routed to %+v and %+v; both must reach the one group %+v", routed[east], routed[west], want)
		}
	})

	t.Run("a subDirectory reading the identity reads the settled composed identity", func(t *testing.T) {
		t.Parallel()
		routed, state, oracle := route(t, "{{ .component_id }}")
		for _, b := range []string{east, west} {
			key := boundaryFRKey{BoundaryPath: b, FRInstance: "gateway"}
			id := state.ComponentIDs[key]
			if id == "" {
				t.Fatalf("%s: no identity was settled before the paths were answered", b)
			}
			if len(id) > bound {
				t.Errorf("%s: settled %q, over the %d characters the engine accepts", b, id, bound)
			}
			if unbounded := labels.ComposeComponentIdentity(labels.BoundaryFeatureScope, labels.Unbounded, b, "gateway"); id == unbounded {
				t.Errorf("%s: settled %q, the composition as composed; the engine bounds names to %d", b, id, bound)
			}
			if want := "shared/" + id; routed[b].Path != want {
				t.Errorf("%s: routed to %q, want the settled identity's directory %q", b, routed[b].Path, want)
			}
			for _, refused := range []string{"<", ">", ":", "@", boundaryFRNodePrefix} {
				if strings.Contains(routed[b].Path, refused) {
					t.Errorf("%s: routed to %q, which carries %q from the dispatch marker", b, routed[b].Path, refused)
				}
			}
		}
		if routed[east] == routed[west] {
			t.Errorf("both attachments routed to %+v; a subDirectory reading .component_id gives each its own directory", routed[east])
		}
		asked := len(oracle.seen)
		key := boundaryFRKey{BoundaryPath: east, FRInstance: "gateway"}
		again, err := state.settledComponentID(context.Background(), boundingProvider{boundOracle: oracle}, &types.FeatureRecipe{}, key)
		if err != nil {
			t.Fatalf("reading the settled identity back: %v", err)
		}
		if again != state.ComponentIDs[key] || len(oracle.seen) != asked {
			t.Errorf("reading the identity back returned %q and asked the engine %d more time(s); the render must read %q without asking",
				again, len(oracle.seen)-asked, state.ComponentIDs[key])
		}
	})
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
		view, err := buildBoundaryContext(nil, tc.boundary, graph, rel, perBoundary, paths, "")
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
