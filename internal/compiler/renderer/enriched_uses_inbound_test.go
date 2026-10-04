// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// inboundProvides builds a provides bag from key/value pairs, in the order given.
func inboundProvides(pairs ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

// inboundGraph builds a graph holding the named nodes, in the order given, and
// the given edges, in the order given — the order the graph records them in is
// the order the projection must keep.
func inboundGraph(nodeIDs []string, edges ...types.IREdge) *types.IRGraph {
	nodes := types.OrderedMap[types.IRNode]{}
	for _, id := range nodeIDs {
		nodes.Set(id, types.IRNode{Component: "core/example/" + id, ResolvedVersion: "1.0.0"})
	}
	return &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, Edges: edges}}
}

// spellInbound renders the projection as one comparable line per entry — the
// connecting component, the socket, then the provides bag in the order it is
// held — so a row's expectation names the order as well as the content.
func spellInbound(edges []types.InboundEdge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		var kv []string
		for k, v := range e.Provides.All() {
			kv = append(kv, k+"="+v)
		}
		out = append(out, e.ComponentID+"|"+e.Socket+"|"+strings.Join(kv, ","))
	}
	return out
}

// TestAssembleInboundEdges_ProjectsTheConnectionsMadeToANode holds the projection
// an attached Feature reads at .context.used_by. The control row is two callers
// written in an order that is not alphabetical, so a projection that sorted its
// callers fails the control as well as the row that names the fault. Every other
// row changes one thing about the control.
func TestAssembleInboundEdges_ProjectsTheConnectionsMadeToANode(t *testing.T) {
	t.Parallel()

	nodes := []string{"host", "worker", "frontend", "db", "reporter"}
	workerToHost := types.IREdge{Source: "worker", Target: "host", Socket: "apis", Alias: "jobs"}
	frontendToHost := types.IREdge{Source: "frontend", Target: "host", Socket: "apis", Alias: "backend"}
	hostToDB := types.IREdge{Source: "host", Target: "db", Socket: "databases", Alias: "main"}
	provides := NodeProvides{
		"worker":   inboundProvides("selector", "app=worker"),
		"frontend": inboundProvides("selector", "app=frontend", "url", "http://frontend"),
		"db":       inboundProvides("host", "db.svc"),
	}

	for _, tc := range []struct {
		name     string
		graph    *types.IRGraph
		provides NodeProvides
		declared NodeDeclaredProvides
		want     []string
	}{
		{
			name:     "control: two callers keep the order they were written in",
			graph:    inboundGraph(nodes, workerToHost, frontendToHost, hostToDB),
			provides: provides,
			want: []string{
				"worker|apis|selector=app=worker",
				"frontend|apis|selector=app=frontend,url=http://frontend",
			},
		},
		{
			name:     "callers written alphabetically ahead are not moved behind",
			graph:    inboundGraph(nodes, frontendToHost, workerToHost, hostToDB),
			provides: provides,
			want: []string{
				"frontend|apis|selector=app=frontend,url=http://frontend",
				"worker|apis|selector=app=worker",
			},
		},
		{
			name:     "the node's own outgoing edge is not a caller",
			graph:    inboundGraph(nodes, hostToDB),
			provides: provides,
			want:     []string{},
		},
		{
			name: "a caller connecting twice appears once per connection",
			graph: inboundGraph(nodes, workerToHost,
				types.IREdge{Source: "worker", Target: "host", Socket: "metrics", Alias: "scrape"}),
			provides: provides,
			want: []string{
				"worker|apis|selector=app=worker",
				"worker|metrics|selector=app=worker",
			},
		},
		{
			name:     "a caller's provides are narrowed to what its Component declares",
			graph:    inboundGraph(nodes, frontendToHost),
			provides: provides,
			declared: NodeDeclaredProvides{"frontend": {"selector": true}},
			want:     []string{"frontend|apis|selector=app=frontend"},
		},
		{
			name:     "a caller publishing nothing carries an empty bag",
			graph:    inboundGraph(nodes, types.IREdge{Source: "reporter", Target: "host", Socket: "apis", Alias: "reports"}),
			provides: provides,
			want:     []string{"reporter|apis|"},
		},
		{
			name:     "an edge from a node the graph does not hold is skipped",
			graph:    inboundGraph(nodes, types.IREdge{Source: "ghost", Target: "host", Socket: "apis", Alias: "x"}, workerToHost),
			provides: provides,
			want:     []string{"worker|apis|selector=app=worker"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := spellInbound(AssembleInboundEdges("host", tc.graph, tc.provides, tc.declared))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("inbound edges of host:\n got:  %q\n want: %q", got, tc.want)
			}
		})
	}
}

// TestAssembleNodeContext_CarriesNoCallers holds the half of the property the
// projection above does not: the node's own context, which its Recipe renders
// against, carries none of the connections made to it, so it is byte-identical
// whether two components or three use the node. The control row is the node with
// no caller at all; the rows add callers and must not move a byte.
func TestAssembleNodeContext_CarriesNoCallers(t *testing.T) {
	t.Parallel()

	nodes := []string{"host", "worker", "frontend", "reporter"}
	provides := NodeProvides{
		"worker":   inboundProvides("selector", "app=worker"),
		"frontend": inboundProvides("selector", "app=frontend"),
		"reporter": inboundProvides("selector", "app=reporter"),
	}
	hostContext := func(t *testing.T, graph *types.IRGraph) (*types.TemplateContext, []byte) {
		t.Helper()
		inject, injectRaw := BuildInjectPipe(nil)
		ctx := AssembleNodeContext("host", graph, provides, nil, inject, injectRaw, nil, nil)
		b, err := json.Marshal(ctx)
		if err != nil {
			t.Fatalf("marshal context: %v", err)
		}
		return ctx, b
	}
	_, alone := hostContext(t, inboundGraph(nodes))

	for _, tc := range []struct {
		name  string
		graph *types.IRGraph
	}{
		{name: "control: nothing uses the node", graph: inboundGraph(nodes)},
		{name: "two components use the node", graph: inboundGraph(nodes,
			types.IREdge{Source: "worker", Target: "host", Socket: "apis", Alias: "jobs"},
			types.IREdge{Source: "frontend", Target: "host", Socket: "apis", Alias: "backend"})},
		{name: "a third component uses the node", graph: inboundGraph(nodes,
			types.IREdge{Source: "worker", Target: "host", Socket: "apis", Alias: "jobs"},
			types.IREdge{Source: "frontend", Target: "host", Socket: "apis", Alias: "backend"},
			types.IREdge{Source: "reporter", Target: "host", Socket: "apis", Alias: "reports"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, got := hostContext(t, tc.graph)
			if len(ctx.Context.UsedBy) != 0 {
				t.Errorf("the node's own context carries %d caller(s); it must carry none: %+v", len(ctx.Context.UsedBy), ctx.Context.UsedBy)
			}
			if string(got) != string(alone) {
				t.Errorf("the node's own context moved with its callers:\n with callers: %s\n alone:        %s", got, alone)
			}
		})
	}
}

// TestPhaseB6_InboundEdgesReachTheAttachmentRenderOnly holds where the
// connections made to a host travel inside the attachment render: onto the
// context the attachment's artifacts render against, and onto nothing else — not
// back onto the host's own context, and not onto the context its pipeline steps
// read, since a step may rewrite the host's own documents.
//
// The host provider is an assembler, so the attachment's pipeline is deferred and
// the context it would run against is returned rather than executed; that is what
// lets the test read it without a plugin. The control row carries two callers;
// the second row carries none, and its artifact must render no caller.
func TestPhaseB6_InboundEdgesReachTheAttachmentRenderOnly(t *testing.T) {
	t.Parallel()

	two := []types.InboundEdge{
		{ComponentID: "worker", Socket: "apis", Provides: inboundProvides("selector", "app=worker")},
		{ComponentID: "frontend", Socket: "apis", Provides: inboundProvides("selector", "app=frontend")},
	}
	for _, tc := range []struct {
		name        string
		inbound     []types.InboundEdge
		wantCallers string
	}{
		{name: "control: two callers reach the artifact in order", inbound: two, wantCallers: `callers: "worker/apis/app=worker;frontend/apis/app=frontend;"`},
		{name: "nothing uses the host", inbound: nil, wantCallers: `callers: ""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hostCtx := &types.TemplateContext{
				Context: types.ContextBlock{InstanceID: "api"},
				Values:  types.OrderedMap[any]{},
				Infra:   types.OrderedMap[any]{},
			}
			hostRecipe := &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec:     types.RecipeSpec{Capability: "compose", Implements: "core/api"},
			}
			state := NewPhaseBState(hostRecipe, "api", "local", "")
			state.InboundEdges = tc.inbound

			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set("callers.yaml", types.ArtifactEntry{Inline: `callers: "{{ range .context.used_by }}{{ .component_id }}/{{ .socket }}/{{ .provides.selector }};{{ end }}"` + "\n"})
			fr := &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "caller-guard", Version: "1.0.0"},
				Spec: types.FeatureRecipeSpec{
					Capability: "helm",
					Implements: "core/caller-guard",
					Artifacts:  art,
					Pipeline: []types.PipelineStep{{
						ID: "stamp", Plugin: "mutator/stamp", Function: "mutate",
						With: `{{ range .context.used_by }}{{ .component_id }}{{ end }}`,
					}},
				},
			}
			emitter := &stubArtifactsEmitter{}
			factory := func(string) (Provider, error) { return emitter, nil }

			out, _, _, deferred, err := phaseB6(context.Background(), hostCtx, state,
				[]FeatureRecipeRef{{InstanceName: "guard", FeatureRecipe: fr}}, factory,
				NodeProvides{}, RecipeLookup{}, NodeRenderContext{NodeID: "api", BoundaryPath: "local"},
				nil, nil, nil, nil, RendererInput{}, nil, true, "compose", false, renderctx.RenderContext{})
			if err != nil {
				t.Fatalf("phaseB6: %v", err)
			}

			var body string
			for name, content := range out {
				if strings.HasSuffix(name, "callers.yaml") {
					body = string(content)
				}
			}
			if !strings.Contains(body, tc.wantCallers) {
				t.Errorf("the attachment's artifact rendered the wrong callers; want %s in %q (files: %d)", tc.wantCallers, body, len(out))
			}
			if len(hostCtx.Context.UsedBy) != 0 {
				t.Errorf("the attachment render wrote its callers back onto the host's own context: %+v", hostCtx.Context.UsedBy)
			}
			if len(deferred) != 1 || deferred[0].WithCtx == nil {
				t.Fatalf("want the attachment's pipeline deferred once with its context, got %d record(s)", len(deferred))
			}
			if got := deferred[0].WithCtx.Context.UsedBy; len(got) != 0 {
				t.Errorf("the pipeline context carries the connections made to the host; a step could write them into the host's documents: %+v", got)
			}
			if deferred[0].WithCtx.AttachmentName != "guard" {
				t.Errorf("the pipeline context lost the attachment it renders for: %q", deferred[0].WithCtx.AttachmentName)
			}
		})
	}
}
