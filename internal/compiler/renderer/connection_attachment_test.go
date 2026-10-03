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
		own, onConnections := projectAttachments(graph.Spec.FeatureValuesMapping, "gw", nil, nil)
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
		ctx := AssembleNodeContext("gw", graph, NodeProvides{"gw": types.OrderedMap[string]{}}, nil, inject, injectRaw, nil, nil)
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

// connectionViewSecretType and connectionViewValueType are the two credential
// Features the connection-view fixture uses. The node's own attachment shares the
// first type with one connection's, so a read that matches attachments by type is
// what tells the two placements apart.
const (
	connectionViewSecretType = "core/referenced-secret"
	connectionViewValueType  = "core/secret-value"
)

// connectionViewGraph is a router whose three connections on one socket are
// authored nim, gpt, plain — deliberately not their key order — where nim and gpt
// each carry a credential of a different Feature, plain carries none, and the
// router's own block holds a credential of the same Feature gpt's carries.
func connectionViewGraph() *types.IRGraph {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("router", types.IRNode{Component: "app/router"})
	nodes.Set("nvidia", types.IRNode{Component: "app/model"})
	nodes.Set("openai", types.IRNode{Component: "app/model"})
	nodes.Set("echo", types.IRNode{Component: "app/model"})

	origin := func(v string) types.OrderedMap[any] {
		m := types.OrderedMap[any]{}
		m.Set("origin", v)
		return m
	}
	entries := types.OrderedMap[types.IRFeatureEntry]{}
	entries.Set("credential", types.IRFeatureEntry{Type: connectionViewSecretType, Values: origin("router-own")})
	entries.Set(types.JoinConnectionAttachmentName("nim", "credential"), types.IRFeatureEntry{
		Type:       connectionViewValueType,
		Values:     origin("nvidia-team-b"),
		Connection: &types.AttachmentConnection{Socket: "models", Alias: "nim", Attachment: "credential"},
	})
	entries.Set(types.JoinConnectionAttachmentName("gpt", "credential"), types.IRFeatureEntry{
		Type:       connectionViewSecretType,
		Values:     origin("openai-team-a"),
		Connection: &types.AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"},
	})

	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "test"},
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "router", Target: "nvidia", Socket: "models", Alias: "nim"},
				{Source: "router", Target: "openai", Socket: "models", Alias: "gpt"},
				{Source: "router", Target: "echo", Socket: "models", Alias: "plain"},
			},
		},
	}
	graph.Spec.FeatureValuesMapping.Set("router", entries)
	return graph
}

// connectionViewProvides is the node's resolved attachment bag, keyed by the name
// each attachment takes on the node, as the provides resolution returns it.
func connectionViewProvides() map[string]types.OrderedMap[string] {
	bag := func(name string) types.OrderedMap[string] {
		m := types.OrderedMap[string]{}
		m.Set("name", name)
		m.Set("key", "api-key")
		return m
	}
	return map[string]types.OrderedMap[string]{
		"credential": bag("router-own-secret"),
		types.JoinConnectionAttachmentName("nim", "credential"): bag("nvidia-team-b"),
		types.JoinConnectionAttachmentName("gpt", "credential"): bag("openai-team-a"),
	}
}

func connectionViewContext(t *testing.T) *types.TemplateContext {
	t.Helper()
	inject, injectRaw := BuildInjectPipe(nil)
	return AssembleNodeContext("router", connectionViewGraph(), NodeProvides{}, nil, inject, injectRaw, nil, connectionViewProvides())
}

// TestConnectionViewCarriesEachConnectionsAttachments proves a template reads each
// attachment a connection carries under that connection, at
// .context.uses.<alias>.features.<slot>, in the attachment shape and with the
// provides the one resolved bag holds for it — and reads it nowhere else: the
// node's own .features, and a loop over it that matches attachments by type the
// way catalog Recipes do, see only the node's own attachment, although a
// connection carries one of the same Feature. Each row is evaluated through the
// value-template path a Recipe's valuesMapping, workload and artifacts take.
//
// The loop row's positive half is its control: it does find the node's own
// attachment of that type, so its silence about the connection's is not a loop
// that matches nothing.
func TestConnectionViewCarriesEachConnectionsAttachments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "a connection's attachment is read under its connection, by slot, with its resolved provides",
			template: `{{ .context.uses.gpt.features.credential.provides.name }}`,
			want:     "openai-team-a",
		},
		{
			name:     "each connection reads its own attachment, of its own Feature",
			template: `{{ .context.uses.nim.features.credential.type }}|{{ .context.uses.nim.features.credential.provides.name }}`,
			want:     connectionViewValueType + "|nvidia-team-b",
		},
		{
			name:     "the connection view carries the attachment's own values",
			template: `{{ .context.uses.gpt.features.credential.values.origin }}`,
			want:     "openai-team-a",
		},
		{
			name:     "a connection carrying no attachment shows none",
			template: `{{ if .context.uses.plain.features }}some{{ else }}none{{ end }}`,
			want:     "none",
		},
		{
			name:     "the node's own features hold its own attachment only",
			template: `{{ range $name, $f := .features }}{{ $name }}={{ $f.values.origin }};{{ end }}`,
			want:     "credential=router-own;",
		},
		{
			name: "a loop over the node's features matching by type sees only the node's own",
			template: `{{- range $name, $f := (default dict .features) -}}` +
				`{{- if eq (first (splitList ":" $f.type)) "` + connectionViewSecretType + `" -}}{{ $name }};{{- end -}}` +
				`{{- end -}}`,
			want: "credential;",
		},
		{
			name: "the node's own attachment is under no connection",
			template: `{{ range $alias, $edge := .context.uses }}{{ range $slot, $f := $edge.features }}` +
				`{{ $alias }}.{{ $slot }}={{ $f.values.origin }};{{ end }}{{ end }}`,
			want: "gpt.credential=openai-team-a;nim.credential=nvidia-team-b;",
		},
		{
			name:     "a template ranges the connections in key order, not in the order they were written",
			template: `{{ range $alias, $_ := .context.uses }}{{ $alias }},{{ end }}`,
			want:     "gpt,nim,plain,",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := EvalTemplate("connection-view", tc.template, connectionViewContext(t))
			if err != nil {
				t.Fatalf("EvalTemplate: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q; want %q", got, tc.want)
			}
		})
	}
}

// TestConnectionViewIsTheOneProjectedAttachment holds the connection view to the
// projection a FeatureRecipe bound to the same attachment renders against: the
// value under the connection and the value found by the attachment's name are one
// projected value, provides included, so the two cannot come to disagree. It also
// holds the serialized connections in the order they were written, which is the
// order every output carrying them is built in.
func TestConnectionViewIsTheOneProjectedAttachment(t *testing.T) {
	t.Parallel()
	ctx := connectionViewContext(t)
	if got, want := ctx.Context.Uses.Keys(), []string{"nim", "gpt", "plain"}; !reflect.DeepEqual(got, want) {
		t.Errorf("uses keys = %v; want the authored order %v", got, want)
	}
	for _, tc := range []struct {
		alias string
		slots []string
	}{
		{alias: "nim", slots: []string{"credential"}},
		{alias: "gpt", slots: []string{"credential"}},
		{alias: "plain"},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			t.Parallel()
			edge, ok := ctx.Context.Uses.Get(tc.alias)
			if !ok {
				t.Fatalf("no uses entry %q", tc.alias)
			}
			if got := edge.Features.Keys(); !reflect.DeepEqual(got, tc.slots) && len(got)+len(tc.slots) != 0 {
				t.Fatalf("features under %q = %v; want %v", tc.alias, got, tc.slots)
			}
			for _, slot := range tc.slots {
				view, _ := edge.Features.Get(slot)
				pillars, ok := ctx.AttachmentPillars(types.JoinConnectionAttachmentName(tc.alias, slot))
				if !ok {
					t.Fatalf("no attachment named for %s.%s", tc.alias, slot)
				}
				if !reflect.DeepEqual(view, pillars) {
					t.Errorf("connection view %+v differs from the attachment's own projection %+v", view, pillars)
				}
				if want := connectionViewProvides()[types.JoinConnectionAttachmentName(tc.alias, slot)]; !reflect.DeepEqual(view.Provides, want) {
					t.Errorf("provides under %s.%s = %v; want the resolved bag %v", tc.alias, slot, view.Provides, want)
				}
			}
		})
	}
	if got, want := ctx.Features.Keys(), []string{"credential"}; !reflect.DeepEqual(got, want) {
		t.Errorf(".features = %v; want %v", got, want)
	}
}
