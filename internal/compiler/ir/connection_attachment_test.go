// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"reflect"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestConnectionAttachmentsMergeByPillarAndKeepTheirConnection proves an
// attachment written on a connection is one of the consuming node's attachments
// in the graph: the developer's values and the operator's infra under the one
// composed name fold into one entry by the merge every same-named contribution
// goes through, and the entry keeps the connection it was written on — whichever
// document wrote it. An attachment of the node's own block carries none.
func TestConnectionAttachmentsMergeByPillarAndKeepTheirConnection(t *testing.T) {
	t.Parallel()

	gpt := &types.AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"}
	name := types.JoinConnectionAttachmentName("gpt", "credential")
	lowered := topology.FeatureAttachment{
		InstanceName: name,
		Feature: types.FeatureInstance{
			Type:   "core/db-creds",
			Values: valuesMap("key", "openai-team-a"),
		},
		Connection: gpt,
	}
	own := topology.FeatureAttachment{
		InstanceName: "mon",
		Feature:      types.FeatureInstance{Type: "core/monitoring"},
	}
	// releaseOnConnection is a Release writing one attachment on the node's
	// "gpt" connection.
	releaseOnConnection := func(att types.ReleaseConnectionAttachment) *types.Release {
		entry := types.ReleaseUsesEntry{}
		entry.Features.Set("credential", att)
		aliases := types.OrderedMap[types.ReleaseUsesEntry]{}
		aliases.Set("gpt", entry)
		comp := types.BoundaryComponent{}
		comp.Uses.Set("models", aliases)
		prod := types.Boundary{}
		prod.Components.Set("gw", comp)
		release := &types.Release{}
		release.Spec.Boundaries.Set("prod", prod)
		return release
	}

	type entry struct {
		featureType string
		connection  *types.AttachmentConnection
		values      types.OrderedMap[any]
		infra       types.OrderedMap[any]
	}
	cases := []struct {
		name        string
		attachments []topology.FeatureAttachment
		release     *types.Release
		instance    string
		want        entry
	}{
		{
			name:        "the developer's attachment keeps its connection",
			attachments: []topology.FeatureAttachment{own, lowered},
			instance:    name,
			want: entry{
				featureType: "core/db-creds",
				connection:  gpt,
				values:      valuesMap("key", "openai-team-a"),
			},
		},
		{
			name:        "the operator's infra folds into the developer's attachment",
			attachments: []topology.FeatureAttachment{own, lowered},
			release:     releaseOnConnection(types.ReleaseConnectionAttachment{Infra: valuesMap("rotation", "hourly")}),
			instance:    name,
			want: entry{
				featureType: "core/db-creds",
				connection:  gpt,
				values:      valuesMap("key", "openai-team-a"),
				infra:       valuesMap("rotation", "hourly"),
			},
		},
		{
			name:        "an attachment only the operator wrote keeps its connection",
			attachments: []topology.FeatureAttachment{own},
			release: releaseOnConnection(types.ReleaseConnectionAttachment{
				Type: "core/db-creds", Infra: valuesMap("rotation", "daily"),
			}),
			instance: name,
			want: entry{
				featureType: "core/db-creds",
				connection:  gpt,
				infra:       valuesMap("rotation", "daily"),
			},
		},
		{
			name:        "an attachment of the node's own block carries no connection",
			attachments: []topology.FeatureAttachment{own, lowered},
			instance:    "mon",
			want:        entry{featureType: "core/monitoring"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gw := &topology.TopologyNode{InstanceID: "gw", BoundaryPath: "prod"}
			nodes := types.OrderedMap[*topology.TopologyNode]{}
			nodes.Set("gw", gw)
			topoResult := &topology.TopologyResult{
				Nodes:              nodes,
				FeatureAttachments: map[string][]topology.FeatureAttachment{"gw": tc.attachments},
			}
			resolveResult := &resolver.ResolveResult{
				Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
				Release:   tc.release,
			}
			result, err := collectNodeFeatures("gw", gw, topoResult, resolveResult, featureCatalog())
			if err != nil {
				t.Fatalf("collecting the node's attachments: %v", err)
			}
			got, ok := result.Features.Get(tc.instance)
			if !ok {
				t.Fatalf("the node carries no attachment %q; it carries %v", tc.instance, result.Features.Keys())
			}
			if got.Type != tc.want.featureType {
				t.Errorf("type = %q; want %q", got.Type, tc.want.featureType)
			}
			if got.OnConnection() != (tc.want.connection != nil) {
				t.Errorf("OnConnection() = %v; want %v", got.OnConnection(), tc.want.connection != nil)
			}
			if !reflect.DeepEqual(got.Connection, tc.want.connection) {
				t.Errorf("connection = %+v; want %+v", got.Connection, tc.want.connection)
			}
			for _, p := range []struct {
				pillar    string
				got, want types.OrderedMap[any]
			}{
				{"values", got.Values, tc.want.values},
				{"infra", got.Infra, tc.want.infra},
			} {
				for _, k := range p.want.Keys() {
					gv, _ := p.got.Get(k)
					wv, _ := p.want.Get(k)
					if gv != wv {
						t.Errorf("%s.%s = %v; want %v", p.pillar, k, gv, wv)
					}
				}
				if p.got.Len() != p.want.Len() {
					t.Errorf("%s carries %v; want exactly %v", p.pillar, p.got.Keys(), p.want.Keys())
				}
			}
		})
	}
}

// TestAuthoredUsesCopyLeavesConnectionAttachmentValuesOut proves the graph's copy
// of a node's authored `uses` block keeps every connection and each attachment's
// type and description, and leaves the attachment's values out — the values are
// carried on the node's attachment, under the marking a surface displaying the
// graph withholds by. A block carrying no attachment is the block as authored,
// and the authored block itself is never changed.
func TestAuthoredUsesCopyLeavesConnectionAttachmentValuesOut(t *testing.T) {
	t.Parallel()

	withAttachment := func() types.OrderedMap[types.UsesSocketValue] {
		att := types.OrderedMap[types.ConnectionAttachment]{}
		att.Set("credential", types.ConnectionAttachment{
			Type:        "core/referenced-secret",
			Description: "the team key",
			Values:      valuesMap("origin", "openai-team-a"),
		})
		models := types.UsesSocketValue{}
		models.Verbose.Set("gpt", types.VerboseUsesEntry{
			Target:   "gpt",
			Values:   valuesMap("timeout", 30),
			Features: att,
		})
		models.Verbose.Set("nim", types.VerboseUsesEntry{Target: "nim"})
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("models", models)
		uses.Set("logs", types.UsesSocketValue{Compact: []string{"sink"}})
		return uses
	}
	withoutAttachment := func() types.OrderedMap[types.UsesSocketValue] {
		models := types.UsesSocketValue{}
		models.Verbose.Set("gpt", types.VerboseUsesEntry{Target: "gpt", Values: valuesMap("timeout", 30)})
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("models", models)
		return uses
	}

	t.Run("an attachment keeps its type and description and loses its values", func(t *testing.T) {
		t.Parallel()
		authored := withAttachment()
		copied := authoredUsesCopy(authored)

		if got, want := copied.Keys(), []string{"models", "logs"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("sockets = %v; want %v", got, want)
		}
		models, _ := copied.Get("models")
		if got, want := models.Verbose.Keys(), []string{"gpt", "nim"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("connections = %v; want %v", got, want)
		}
		gpt, _ := models.Verbose.Get("gpt")
		if gpt.Target != "gpt" {
			t.Errorf("target = %q; want gpt", gpt.Target)
		}
		if timeout, _ := gpt.Values.Get("timeout"); timeout != 30 {
			t.Errorf("the connection's own values = %v; want them carried as authored", gpt.Values.Keys())
		}
		cred, ok := gpt.Features.Get("credential")
		if !ok {
			t.Fatalf("the attachment is gone from the copy; it must stay, reduced")
		}
		if cred.Type != "core/referenced-secret" || cred.Description != "the team key" {
			t.Errorf("attachment = %+v; want its type and description kept", cred)
		}
		if cred.Values.Len() != 0 {
			t.Errorf("attachment values %v reached the copy; they must be left out", cred.Values.Keys())
		}
		logs, _ := copied.Get("logs")
		if !reflect.DeepEqual(logs.Compact, []string{"sink"}) {
			t.Errorf("compact socket = %v; want it carried as authored", logs.Compact)
		}

		original, _ := authored.Get("models")
		originalGPT, _ := original.Verbose.Get("gpt")
		originalCred, _ := originalGPT.Features.Get("credential")
		if origin, _ := originalCred.Values.Get("origin"); origin != "openai-team-a" {
			t.Errorf("the authored block was changed: attachment values now %v", originalCred.Values.Keys())
		}
	})

	t.Run("a block carrying no attachment is the block as authored", func(t *testing.T) {
		t.Parallel()
		authored := withoutAttachment()
		if copied := authoredUsesCopy(authored); !reflect.DeepEqual(copied, authored) {
			t.Errorf("copy = %+v; want the authored block unchanged", copied)
		}
	})

	t.Run("an empty block stays empty", func(t *testing.T) {
		t.Parallel()
		if copied := authoredUsesCopy(types.OrderedMap[types.UsesSocketValue]{}); copied.Len() != 0 {
			t.Errorf("copy of an empty block carries %v", copied.Keys())
		}
	})
}

// TestBuildIR_NodeUsesCopyLeavesConnectionAttachmentValuesOut proves the graph the
// build produces carries each node's authored `uses` block with every attachment a
// connection holds reduced to its type and description, while the attachment
// lowered onto the node from that connection keeps its values. It drives the graph
// build rather than the copy the build delegates to, so a build that set the node's
// block from the authored one directly fails here even while the copy is correct.
//
// The topology fixture is shaped the way the topology stage shapes it: the lowered
// attachments are derived from the authored block by the same walk — the alias and
// the slot composed into the name, the connection carried as data — and share its
// values, so a copy that cleared the values in place would empty the lowered
// attachment too and fail here as well.
func TestBuildIR_NodeUsesCopyLeavesConnectionAttachmentValuesOut(t *testing.T) {
	t.Parallel()

	const socket, featureType = "models", "core/db-creds"
	// connection is one entry of the consumer's socket. slot, description and
	// origin describe the attachment it carries; an empty slot carries none.
	type connection struct {
		alias, slot, description, origin string
	}
	cases := []struct {
		name        string
		connections []connection
	}{
		{
			name: "a connection carrying an attachment beside one carrying none",
			connections: []connection{
				{alias: "gpt", slot: "credential", description: "the team key", origin: "openai-team-a"},
				{alias: "nim"},
			},
		},
		{
			name: "two connections each carrying an attachment of their own",
			connections: []connection{
				{alias: "gpt", slot: "credential", description: "the team key", origin: "openai-team-a"},
				{alias: "nim", slot: "credential", origin: "nvidia-team-b"},
			},
		},
		{
			name:        "connections carrying no attachment",
			connections: []connection{{alias: "gpt"}, {alias: "nim"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			models := types.UsesSocketValue{}
			for _, c := range tc.connections {
				entry := types.VerboseUsesEntry{Target: c.alias, Values: valuesMap("timeout", 30)}
				if c.slot != "" {
					entry.Features.Set(c.slot, types.ConnectionAttachment{
						Type:        featureType,
						Description: c.description,
						Values:      valuesMap("origin", c.origin),
					})
				}
				models.Verbose.Set(c.alias, entry)
			}
			authored := types.OrderedMap[types.UsesSocketValue]{}
			authored.Set(socket, models)

			var lowered []topology.FeatureAttachment
			var wantLowered []string
			for sock, sv := range authored.All() {
				for alias, entry := range sv.Verbose.All() {
					for slot, att := range entry.Features.All() {
						name := types.JoinConnectionAttachmentName(alias, slot)
						lowered = append(lowered, topology.FeatureAttachment{
							InstanceName: name,
							Feature: types.FeatureInstance{
								Type:        att.Type,
								Description: att.Description,
								Values:      att.Values,
							},
							Connection: &types.AttachmentConnection{Socket: sock, Alias: alias, Attachment: slot},
						})
						wantLowered = append(wantLowered, name)
					}
				}
			}

			nodes := types.OrderedMap[*topology.TopologyNode]{}
			nodes.Set("gw", &topology.TopologyNode{
				InstanceID: "gw", ComponentType: "core/compute/api-server", Uses: authored,
			})
			var edges []topology.NormalizedEdge
			for _, c := range tc.connections {
				nodes.Set(c.alias, &topology.TopologyNode{InstanceID: c.alias, ComponentType: "core/datastore/postgres"})
				edges = append(edges, topology.NormalizedEdge{
					Source: "gw", Target: c.alias, Socket: socket, Alias: c.alias, Values: valuesMap("timeout", 30),
				})
			}
			graph, _, _, err := buildIR(BuildIRConfig{
				Topology: &topology.TopologyResult{
					Nodes:              nodes,
					Edges:              edges,
					FeatureAttachments: map[string][]topology.FeatureAttachment{"gw": lowered},
				},
				ResolveResult: &resolver.ResolveResult{
					Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
					BoundaryInfra: map[string]*resolver.BoundaryInfra{},
				},
				CatalogLookup: testCatalog(),
			})
			if err != nil {
				t.Fatalf("building the graph: %v", err)
			}

			node, ok := graph.Spec.Nodes.Get("gw")
			if !ok {
				t.Fatalf("the graph carries no node gw; it carries %v", graph.Spec.Nodes.Keys())
			}
			copied, ok := node.Uses.Get(socket)
			if !ok {
				t.Fatalf("the node's uses block carries no socket %q; it carries %v", socket, node.Uses.Keys())
			}
			wantAliases := make([]string, 0, len(tc.connections))
			for _, c := range tc.connections {
				wantAliases = append(wantAliases, c.alias)
			}
			if got := copied.Verbose.Keys(); !slices.Equal(got, wantAliases) {
				t.Fatalf("connections = %v; want %v", got, wantAliases)
			}

			attachments, _ := graph.Spec.FeatureValuesMapping.Get("gw")
			var gotLowered []string
			for name, e := range attachments.All() {
				if e.OnConnection() {
					gotLowered = append(gotLowered, name)
				}
			}
			if !slices.Equal(gotLowered, wantLowered) {
				t.Errorf("attachments the node carries from its connections = %v; want %v", gotLowered, wantLowered)
			}

			for _, c := range tc.connections {
				entry, _ := copied.Verbose.Get(c.alias)
				if entry.Target != c.alias {
					t.Errorf("connection %q: target = %q; want %q", c.alias, entry.Target, c.alias)
				}
				if timeout, _ := entry.Values.Get("timeout"); timeout != 30 {
					t.Errorf("connection %q: its own values = %v; want them carried as authored", c.alias, entry.Values.Keys())
				}
				if c.slot == "" {
					if entry.Features.Len() != 0 {
						t.Errorf("connection %q: the copy carries attachments %v the author never wrote", c.alias, entry.Features.Keys())
					}
					continue
				}
				if got, want := entry.Features.Keys(), []string{c.slot}; !slices.Equal(got, want) {
					t.Errorf("connection %q: attachments = %v; want %v", c.alias, got, want)
				}
				att, _ := entry.Features.Get(c.slot)
				if att.Type != featureType || att.Description != c.description {
					t.Errorf("connection %q: attachment = %+v; want type %q and description %q kept",
						c.alias, att, featureType, c.description)
				}
				if att.Values.Len() != 0 {
					t.Errorf("connection %q: attachment values %v reached the graph's copy of the authored block; they must be left out",
						c.alias, att.Values.Keys())
				}

				name := types.JoinConnectionAttachmentName(c.alias, c.slot)
				onNode, ok := attachments.Get(name)
				if !ok {
					t.Errorf("connection %q: the node carries no attachment %q; it carries %v", c.alias, name, attachments.Keys())
					continue
				}
				if onNode.Type != featureType {
					t.Errorf("attachment %q: type = %q; want %q", name, onNode.Type, featureType)
				}
				wantConnection := &types.AttachmentConnection{Socket: socket, Alias: c.alias, Attachment: c.slot}
				if !reflect.DeepEqual(onNode.Connection, wantConnection) {
					t.Errorf("attachment %q: connection = %+v; want %+v", name, onNode.Connection, wantConnection)
				}
				if origin, _ := onNode.Values.Get("origin"); origin != c.origin {
					t.Errorf("attachment %q: values.origin = %v; want %q carried on the node's attachment", name, origin, c.origin)
				}

				authoredSocket, _ := authored.Get(socket)
				authoredEntry, _ := authoredSocket.Verbose.Get(c.alias)
				authoredAtt, _ := authoredEntry.Features.Get(c.slot)
				if origin, _ := authoredAtt.Values.Get("origin"); origin != c.origin {
					t.Errorf("connection %q: the build changed the topology's authored block; values.origin now %v", c.alias, origin)
				}
			}
		})
	}
}
