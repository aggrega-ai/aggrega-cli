// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// connectionAttachmentGraph is a graph whose one node holds an attachment of its
// own block and an attachment one of its connections carries, in that order.
func connectionAttachmentGraph() *types.IRGraph {
	values := types.OrderedMap[any]{}
	values.Set("origin", "openai-team-a")
	infra := types.OrderedMap[any]{}
	infra.Set("rotation", "hourly")
	entries := types.OrderedMap[types.IRFeatureEntry]{}
	entries.Set("audit", types.IRFeatureEntry{Type: "core/audit-log"})
	entries.Set(types.JoinConnectionAttachmentName("gpt", "credential"), types.IRFeatureEntry{
		Type:       "core/referenced-secret",
		Values:     values,
		Infra:      infra,
		Connection: &types.AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"},
	})
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("gw", types.IRNode{Component: "app/gateway"})
	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "test"},
		Spec:     types.IRSpec{Nodes: nodes},
	}
	graph.Spec.FeatureValuesMapping.Set("gw", entries)
	return graph
}

// TestConnectionAttachmentsStayOutOfTheNodesOwnFeatures proves an attachment one
// of a node's connections carries is not one of the node's own attachments on
// any surface a template or an assertion reads them from — the render context's
// `.features` and the assertion context's — while a FeatureRecipe bound to it
// still finds its values and infra by its name.
func TestConnectionAttachmentsStayOutOfTheNodesOwnFeatures(t *testing.T) {
	t.Parallel()
	name := types.JoinConnectionAttachmentName("gpt", "credential")

	t.Run("the projection puts each attachment in exactly one set", func(t *testing.T) {
		t.Parallel()
		graph := connectionAttachmentGraph()
		own, onConnections := projectAttachments(graph.Spec.FeatureValuesMapping, "gw")
		if got, want := own.Keys(), []string{"audit"}; !reflect.DeepEqual(got, want) {
			t.Errorf("own attachments = %v; want %v", got, want)
		}
		if got, want := onConnections.Keys(), []string{name}; !reflect.DeepEqual(got, want) {
			t.Errorf("connection attachments = %v; want %v", got, want)
		}
	})

	t.Run("the render context's features hold the node's own only", func(t *testing.T) {
		t.Parallel()
		graph := connectionAttachmentGraph()
		inject, injectRaw := BuildInjectPipe(nil)
		ctx := AssembleNodeContext("gw", graph, NodeProvides{"gw": types.OrderedMap[string]{}}, nil, inject, injectRaw, nil)
		if got, want := ctx.Features.Keys(), []string{"audit"}; !reflect.DeepEqual(got, want) {
			t.Errorf(".features = %v; want %v", got, want)
		}
		attachment, ok := ctx.AttachmentPillars(name)
		if !ok {
			t.Fatalf("a FeatureRecipe bound to %q finds no attachment to render against", name)
		}
		if origin, _ := attachment.Values.Get("origin"); origin != "openai-team-a" {
			t.Errorf("values.origin = %v; want openai-team-a", origin)
		}
		if rotation, _ := attachment.Infra.Get("rotation"); rotation != "hourly" {
			t.Errorf("infra.rotation = %v; want hourly", rotation)
		}
		if own, ok := ctx.AttachmentPillars("audit"); !ok || own.Type != "core/audit-log" {
			t.Errorf("the node's own attachment = %+v, %v; want it found", own, ok)
		}
		if _, ok := ctx.AttachmentPillars("absent"); ok {
			t.Errorf("an attachment the node does not hold was found")
		}
	})

	t.Run("the assertion context's features hold the node's own only", func(t *testing.T) {
		t.Parallel()
		base := ExtractNodeBaseContext("gw", connectionAttachmentGraph())
		if _, has := base.Features[name]; has {
			t.Errorf("assertion .features carries the connection's attachment %q", name)
		}
		if _, has := base.Features["audit"]; !has {
			t.Errorf("assertion .features lost the node's own attachment; got %v", base.Features)
		}
	})
}
