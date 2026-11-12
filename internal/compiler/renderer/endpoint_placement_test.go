// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// placementFixture builds a Release and the graph rendered from it in which every
// caller connects to "gateway", placed in boundary "app" under namespace ns-app:
//
//   - peer, beside gateway in "app";
//   - guard, in boundary "edge" under namespace ns-edge;
//   - odd, in "edge", whose own Release entry sets the namespace to ns-odd;
//   - quiet, in "edge", whose own Release entry clears the namespace;
//   - deep, in "edge/inner", a nested boundary declaring no namespace of its own.
//
// Each node's boundary path and annotations come from the resolver's own walk of
// that Release, copied onto the node the way the graph builder copies them, so a
// node carries exactly the placement its own render is handed. The per-boundary
// annotation map the same walk returns is returned beside them, because it is what
// a boundary's view is handed and what a reading of the boundary's namespace would
// read.
func placementFixture(t *testing.T) (*types.Release, *types.IRGraph, map[string]types.OrderedMap[string]) {
	t.Helper()
	scalar := func(s string) *types.AnnotationValue { return &types.AnnotationValue{Scalar: &s} }
	namespaced := func(v *types.AnnotationValue) types.OrderedMap[*types.AnnotationValue] {
		m := types.OrderedMap[*types.AnnotationValue]{}
		m.Set(AnnotationNamespace, v)
		return m
	}
	components := func(entries ...string) types.OrderedMap[types.BoundaryComponent] {
		m := types.OrderedMap[types.BoundaryComponent]{}
		for _, name := range entries {
			m.Set(name, types.BoundaryComponent{})
		}
		return m
	}

	app := types.Boundary{Annotations: namespaced(scalar("ns-app")), Components: components("gateway", "peer")}
	edgeComponents := components("guard")
	edgeComponents.Set("odd", types.BoundaryComponent{Annotations: namespaced(scalar("ns-odd"))})
	edgeComponents.Set("quiet", types.BoundaryComponent{Annotations: namespaced(nil)})
	edge := types.Boundary{Annotations: namespaced(scalar("ns-edge")), Components: edgeComponents}
	edge.Boundaries.Set("inner", types.Boundary{Components: components("deep")})
	rel := &types.Release{}
	rel.Spec.Boundaries.Set("app", app)
	rel.Spec.Boundaries.Set("edge", edge)

	perComponent, perBoundary := resolver.WireBoundaryInfra(types.NormalizeRelease(rel))

	graph := &types.IRGraph{}
	for _, id := range []string{"gateway", "peer", "guard", "odd", "quiet", "deep"} {
		bi := perComponent[id]
		if bi == nil {
			t.Fatalf("the Release places no component %q", id)
		}
		node := types.IRNode{Component: "core/example/" + id, ResolvedVersion: "1.0.0", BoundaryPath: bi.BoundaryPath}
		for k, v := range bi.Annotations.All() {
			node.ReleaseAnnotations.Set(k, v)
		}
		graph.Spec.Nodes.Set(id, node)
		if id != "gateway" {
			graph.Spec.Edges = append(graph.Spec.Edges, types.IREdge{Source: id, Target: "gateway", Socket: "upstream", Alias: "to_gateway"})
		}
	}
	return rel, graph, perBoundary
}

// TestCallerPlacement_IsTheCallersOwnOnEveryViewThatShowsIt holds the one reading
// of a connection endpoint's placement on both views that carry it: the
// connections made to a node, which a FeatureRecipe attached to that node reads at
// .context.used_by, and a boundary's view of its edges, which a boundary-scope
// FeatureRecipe and the compose network bridge read. Each row names a caller of
// "gateway" and the namespace and boundary path both views must give it.
//
// The control row is a caller placed under another boundary's namespace, so a
// reading that answered with the host's namespace, or with none, fails the
// control as well as its own row. The rows whose caller's own Release entry sets or
// clears the namespace fail a reading of the boundary's annotations instead of the
// caller's own. On the boundary's view the connection's other end must read as the
// host's own placement, because that end is read the same way.
func TestCallerPlacement_IsTheCallersOwnOnEveryViewThatShowsIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		caller        string
		wantPath      string
		wantNamespace string
	}{
		{name: "control: a caller placed under another boundary's namespace", caller: "guard", wantPath: "edge", wantNamespace: "ns-edge"},
		{name: "a caller placed beside the host", caller: "peer", wantPath: "app", wantNamespace: "ns-app"},
		{name: "a caller whose own Release entry sets its namespace", caller: "odd", wantPath: "edge", wantNamespace: "ns-odd"},
		{name: "a caller whose own Release entry clears its namespace", caller: "quiet", wantPath: "edge", wantNamespace: ""},
		{name: "a caller in a nested boundary inheriting its parent's namespace", caller: "deep", wantPath: "edge/inner", wantNamespace: "ns-edge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rel, graph, perBoundary := placementFixture(t)

			var inbound *types.InboundEdge
			for _, e := range AssembleInboundEdges("gateway", graph, NodeProvides{}, nil) {
				if e.ComponentID == tc.caller {
					inbound = &e
					break
				}
			}
			if inbound == nil {
				t.Fatalf("the connections made to gateway carry no entry for %q", tc.caller)
			}
			if inbound.SourceNamespace != tc.wantNamespace || inbound.SourceBoundaryPath != tc.wantPath {
				t.Errorf("the connection %s makes to gateway reads namespace %q, boundary path %q; want %q, %q",
					tc.caller, inbound.SourceNamespace, inbound.SourceBoundaryPath, tc.wantNamespace, tc.wantPath)
			}

			view, err := buildBoundaryContext(nil, tc.wantPath, graph, rel, perBoundary, nil, "")
			if err != nil {
				t.Fatalf("the view of boundary %q: %v", tc.wantPath, err)
			}
			var viewed *types.EnrichedEdge
			for _, list := range [][]types.EnrichedEdge{view.InternalUsesEdges, view.OutgoingCrossBoundaryUsesEdges} {
				for i := range list {
					if list[i].Source == tc.caller && list[i].Target == "gateway" {
						viewed = &list[i]
					}
				}
			}
			if viewed == nil {
				t.Fatalf("boundary %q's view carries no edge from %q to gateway", tc.wantPath, tc.caller)
			}
			if viewed.SourceNamespace != tc.wantNamespace || viewed.SourceBoundaryPath != tc.wantPath {
				t.Errorf("boundary %q's view reads %s's namespace %q, boundary path %q; want %q, %q",
					tc.wantPath, tc.caller, viewed.SourceNamespace, viewed.SourceBoundaryPath, tc.wantNamespace, tc.wantPath)
			}
			if viewed.TargetNamespace != "ns-app" || viewed.TargetBoundaryPath != "app" {
				t.Errorf("boundary %q's view reads the host's namespace %q, boundary path %q; want %q, %q",
					tc.wantPath, viewed.TargetNamespace, viewed.TargetBoundaryPath, "ns-app", "app")
			}
		})
	}
}
