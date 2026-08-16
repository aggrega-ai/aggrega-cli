// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// threeAttachmentGraph builds a one-node graph whose attachment set is authored
// in a deliberately non-alphabetical order, with mixed sensitivity markings. The
// order matters: an alphabetically-authored set could not tell an order-preserving
// carry from a sorted one, so a test over it would prove nothing.
func threeAttachmentGraph(t *testing.T, nodeID string) *types.IRGraph {
	t.Helper()
	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set("zeta-cache", types.IRFeatureEntry{Type: "core/cache", ResolvedVersion: "1.0.0"})
	attachments.Set("alpha-db", types.IRFeatureEntry{Type: "core/referenced-secret", ResolvedVersion: "1.0.0", Sensitive: true})
	attachments.Set("mid-queue", types.IRFeatureEntry{Type: "core/queue", ResolvedVersion: "1.0.0", Sensitive: true})

	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(nodeID, attachments)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(nodeID, types.IRNode{Component: "local/compute/app"})

	return &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, FeatureValuesMapping: fvm}}
}

// TestBuildNodeRenderContext_CarriesAttachmentsInAuthorOrder pins the wiring that
// puts attachment data on the render surface at all: the node render context is
// built from the graph's own attachment map, in the author's order, so the
// per-node projection handed to a provider guest carries the same entries the IR
// carries — including each one's derived sensitivity marking. A regression that
// stopped populating the context would leave every guest unable to tell a
// credential attachment from an operational one, and no rendered byte would move
// to signal it.
func TestBuildNodeRenderContext_CarriesAttachmentsInAuthorOrder(t *testing.T) {
	t.Parallel()
	const nodeID = "app"
	graph := threeAttachmentGraph(t, nodeID)
	node, _ := graph.Spec.Nodes.Get(nodeID)

	nrc := buildNodeRenderContext(nodeID, node, graph, RendererInput{})

	want := []string{"zeta-cache", "alpha-db", "mid-queue"}
	if got := nrc.Attachments.Keys(); len(got) != len(want) {
		t.Fatalf("attachment count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i, alias := range nrc.Attachments.Keys() {
		if alias != want[i] {
			t.Errorf("attachment %d = %q, want %q: the context did not preserve author order", i, alias, want[i])
		}
	}
	for _, tc := range []struct {
		alias     string
		sensitive bool
	}{
		{"zeta-cache", false},
		{"alpha-db", true},
		{"mid-queue", true},
	} {
		entry, ok := nrc.Attachments.Get(tc.alias)
		if !ok {
			t.Fatalf("attachment %q absent from the node render context", tc.alias)
		}
		if entry.Sensitive != tc.sensitive {
			t.Errorf("attachment %q carries sensitive=%v, want %v", tc.alias, entry.Sensitive, tc.sensitive)
		}
	}
}

// TestBuildNodeRenderContext_NoAttachmentsIsEmpty pins the other end: a node the
// graph records no attachments for carries an empty set rather than a
// partially-populated one, which is what keeps the projection's attachment field
// absent from the wire for such a node.
func TestBuildNodeRenderContext_NoAttachmentsIsEmpty(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("bare", types.IRNode{Component: "local/compute/app"})
	graph := &types.IRGraph{Spec: types.IRSpec{
		Nodes:                nodes,
		FeatureValuesMapping: types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{},
	}}
	node, _ := graph.Spec.Nodes.Get("bare")

	nrc := buildNodeRenderContext("bare", node, graph, RendererInput{})
	if got := nrc.Attachments.Len(); got != 0 {
		t.Errorf("attachment count = %d for a node with no attachments, want 0", got)
	}
}

// TestAttachmentProvidesFromContext_ProjectsResolvedProvides pins the one
// attachment lookup the workload projection carries: each attachment's resolved
// provides, keyed by the instance name a workload attachment reference names, and
// drawn from the template context alone. The template context is the only source,
// so an attachment the IR records but the context does not carry cannot appear —
// the fixture asserts that directly rather than trusting the walk's shape.
//
// A guest reading whether an attachment handles declared-sensitive material does
// not read it here; it reads the node projection's feature entry for the same
// attachment, which TestBuildNodeRenderContext_CarriesAttachmentsInAuthorOrder
// above pins.
func TestAttachmentProvidesFromContext_ProjectsResolvedProvides(t *testing.T) {
	t.Parallel()
	tmplCtx := &types.TemplateContext{}
	pv := types.OrderedMap[string]{}
	pv.Set("name", "app-secret")
	tmplCtx.Features.Set("alpha-db", types.TemplateAttachment{Type: "core/referenced-secret", Provides: pv})
	tmplCtx.Features.Set("zeta-cache", types.TemplateAttachment{Type: "core/cache"})

	got := AttachmentProvidesFromContext(tmplCtx)
	if len(got) != 2 {
		t.Fatalf("provides lookup has %d entries, want 2", len(got))
	}
	alphaProvides := got["alpha-db"]
	if v, _ := alphaProvides.Get("name"); v != "app-secret" {
		t.Errorf("alpha-db provides name = %q, want %q", v, "app-secret")
	}
	if _, present := got["stray"]; present {
		t.Error("stray is absent from the template context yet carries a provides entry")
	}
	if AttachmentProvidesFromContext(nil) == nil {
		t.Error("a nil template context yielded a nil lookup rather than an empty one")
	}
}
