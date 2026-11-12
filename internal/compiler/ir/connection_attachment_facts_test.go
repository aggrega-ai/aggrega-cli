// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package ir

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// connectionFactsCatalog returns a CatalogLookup whose Component core/gateway
// declares two slot sets with deliberately crossed names and markings:
//
//	its own featuresSchema     vault (sensitive) · audit
//	socket models' slots       credential (sensitive) · audit
//
// so a derivation reading the wrong set for an attachment answers differently
// from one reading the right set. Feature core/plain declares nothing sensitive,
// so whatever an attachment of it is marked comes from the slot alone.
func connectionFactsCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	own := types.OrderedMap[types.AttachmentSlot]{}
	own.Set("vault", types.AttachmentSlot{Sensitive: true})
	own.Set("audit", types.AttachmentSlot{})
	socketSlots := types.OrderedMap[types.AttachmentSlot]{}
	socketSlots.Set("credential", types.AttachmentSlot{Sensitive: true})
	socketSlots.Set("audit", types.AttachmentSlot{})
	sockets := types.OrderedMap[types.UsesSchemaEntry]{}
	sockets.Set("models", types.UsesSchemaEntry{FeaturesSchema: socketSlots})
	gateway := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/gateway", Version: "1.0.0"},
		Spec:     types.ComponentSpec{FeaturesSchema: own, UsesSchema: sockets},
	}
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/gateway" {
				return gateway, true
			}
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == "core/plain" {
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/plain", Version: "1.0.0"},
					Spec: types.FeatureSpec{Schema: schemaNode(t, `values:
  key: { type: string }
`)},
				}, true
			}
			return types.Feature{}, false
		},
	}
}

// onConnection is one attachment written on a connection of the node, lowered
// under the composed name and carrying the connection, as the topology lowers it.
func onConnection(socket, alias, slot string) topology.FeatureAttachment {
	return topology.FeatureAttachment{
		InstanceName: types.JoinConnectionAttachmentName(alias, slot),
		Feature:      types.FeatureInstance{Type: "core/plain", Values: vmap("key", alias+"-key")},
		Connection:   &types.AttachmentConnection{Socket: socket, Alias: alias, Attachment: slot},
	}
}

// buildConnectionFactsGraph builds one node "gw" of the given Component type
// carrying the given attachments, through the real builder.
func buildConnectionFactsGraph(t *testing.T, componentType string, feats []topology.FeatureAttachment) *types.IRGraph {
	t.Helper()
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("gw", &topology.TopologyNode{InstanceID: "gw", ComponentType: componentType})
	graph, _, _, err := buildIR(BuildIRConfig{
		Topology: &topology.TopologyResult{
			Nodes:              nodes,
			FeatureAttachments: map[string][]topology.FeatureAttachment{"gw": feats},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		CatalogLookup: connectionFactsCatalog(t),
	})
	if err != nil {
		t.Fatalf("BuildIRContext: %v", err)
	}
	return graph
}

// TestConnectionAttachmentFacts_ReadTheSocketSlot proves an attachment a
// connection carries takes its binding and the slot half of its sensitivity from
// the slot its connection's socket declares, and an attachment of the node's own
// block from the Component's own slot — each read from its own set, by the slot
// name it was written under. The crossed catalog makes each row a discriminator:
// an own attachment named like a socket slot, and a connection attachment named
// like an own slot, each answer from the set that governs them and from no other.
func TestConnectionAttachmentFacts_ReadTheSocketSlot(t *testing.T) {
	t.Parallel()
	graph := buildConnectionFactsGraph(t, "core/gateway", []topology.FeatureAttachment{
		feat("vault", "core/plain", vmap("key", "own-key")),
		feat("credential", "core/plain", vmap("key", "stray-key")),
		onConnection("models", "gpt", "credential"),
		onConnection("models", "nim", "audit"),
	})
	attachments, ok := graph.Spec.FeatureValuesMapping.Get("gw")
	if !ok {
		t.Fatalf("no attachments emitted for node gw")
	}

	cases := []struct {
		instance      string
		binding       string
		sensitive     bool
		socket, alias string
		why           string
	}{
		{"vault", "vault", true, "", "",
			"fills the Component's own sensitive slot"},
		{"credential", types.AttachmentBindingFree, false, "", "",
			"the Component declares no own slot of this name; the socket's slot of the same name does not govern an own attachment"},
		{"gpt.credential", "credential", true, "models", "gpt",
			"fills the socket's sensitive slot, paired by the slot name its connection records"},
		{"nim.audit", "audit", false, "models", "nim",
			"fills the socket's slot that declares nothing sensitive, so nothing marks it"},
	}
	for _, c := range cases {
		got := entry(t, attachments, c.instance)
		if got.Binding != c.binding {
			t.Errorf("%s: binding = %q, want %q — it %s", c.instance, got.Binding, c.binding, c.why)
		}
		if got.Sensitive != c.sensitive {
			t.Errorf("%s: sensitive = %v, want %v — it %s", c.instance, got.Sensitive, c.sensitive, c.why)
		}
		switch {
		case c.socket == "" && got.Connection != nil:
			t.Errorf("%s: an attachment of the node's own block carries connection %+v", c.instance, got.Connection)
		case c.socket != "" && (got.Connection == nil || got.Connection.Socket != c.socket || got.Connection.Alias != c.alias):
			t.Errorf("%s: connection = %+v, want socket %q alias %q", c.instance, got.Connection, c.socket, c.alias)
		}
	}

	// The wire form: the connection rides as socket and alias on the attachment
	// that has one, under the dotted key, and on no other — and the document
	// conforms to the IR schema, whose attachment-name pattern admits the dot.
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal IR: %v", err)
	}
	const want = `"connection":{"socket":"models","alias":"gpt"}`
	if !strings.Contains(string(raw), want) {
		t.Errorf("emitted IR does not carry %s:\n%s", want, raw)
	}
	if got := strings.Count(string(raw), `"connection":`); got != 2 {
		t.Errorf("emitted IR carries %d connection fields, want 2 — one per attachment a connection carries:\n%s", got, raw)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("re-decode IR: %v", err)
	}
	findings, err := envelope.ValidateInstance(envelope.KindIR, instance)
	if err != nil {
		t.Fatalf("IR schema engine error: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("emitted IR carrying attachments a connection carries violates the IR schema (%d finding(s)):\n%s\n%+v",
			len(findings), raw, findings)
	}
}

// TestConnectionAttachmentFactsAnswerWhatTheGraphRecords proves the derivation a
// reader holding no graph calls answers, for every attachment a connection
// carries, exactly the binding and sensitivity the graph build records on that
// attachment's entry — over a sensitive socket slot, a socket slot declaring
// nothing, a name the socket declares no slot for, a Feature declaring a
// sensitive leaf in a slot declaring nothing, and a component whose slot set is
// unknown.
//
// The graph is built through the real builder and each expectation is read off
// its entry rather than written here, so the rows hold the two answers to each
// other rather than to a value a test author chose; the per-row expectation is a
// second check that the fixture exercises the cell it names.
func TestConnectionAttachmentFactsAnswerWhatTheGraphRecords(t *testing.T) {
	t.Parallel()

	base := connectionFactsCatalog(t)
	catalog := base
	catalog.Feature = func(typeURI string) (types.Feature, bool) {
		if typeURI == "core/secretive" {
			return types.Feature{
				Metadata: types.FeatureMetadata{Name: "core/secretive", Version: "1.0.0"},
				Spec: types.FeatureSpec{Schema: schemaNode(t, `values:
  key: { type: string, sensitive: true }
`)},
			}, true
		}
		return base.Feature(typeURI)
	}

	secretive := onConnection("models", "vault", "audit")
	secretive.Feature.Type = "core/secretive"
	feats := []topology.FeatureAttachment{
		onConnection("models", "gpt", "credential"),
		onConnection("models", "nim", "audit"),
		onConnection("models", "stray", "token"),
		secretive,
	}

	for _, row := range []struct {
		name          string
		componentType string
		attachment    topology.FeatureAttachment
		binding       string
		sensitive     bool
	}{
		{"a sensitive socket slot", "core/gateway", feats[0], "credential", true},
		{"a socket slot declaring nothing", "core/gateway", feats[1], "audit", false},
		{"a name the socket declares no slot for", "core/gateway", feats[2], types.AttachmentBindingFree, false},
		{"a Feature declaring a sensitive leaf", "core/gateway", feats[3], "audit", true},
		{"a component whose slot set is unknown", "core/unknown", feats[0], "", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			nodes := types.OrderedMap[*topology.TopologyNode]{}
			nodes.Set("gw", &topology.TopologyNode{InstanceID: "gw", ComponentType: row.componentType})
			graph, _, _, err := buildIR(BuildIRConfig{
				Topology: &topology.TopologyResult{
					Nodes:              nodes,
					FeatureAttachments: map[string][]topology.FeatureAttachment{"gw": {row.attachment}},
				},
				ResolveResult: &resolver.ResolveResult{
					Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
					BoundaryInfra: map[string]*resolver.BoundaryInfra{},
				},
				CatalogLookup: catalog,
			})
			if err != nil {
				t.Fatalf("BuildIRContext: %v", err)
			}
			attachments, ok := graph.Spec.FeatureValuesMapping.Get("gw")
			if !ok {
				t.Fatalf("no attachments emitted for node gw")
			}
			recorded := entry(t, attachments, row.attachment.InstanceName)

			binding, sensitive := ConnectionAttachmentFacts(
				catalog, row.componentType, *row.attachment.Connection, row.attachment.Feature.Type)
			if binding != recorded.Binding || sensitive != recorded.Sensitive {
				t.Errorf("the derivation answers binding %q, sensitive %v; the graph records binding %q, "+
					"sensitive %v — a view reading the derivation would show what no build records",
					binding, sensitive, recorded.Binding, recorded.Sensitive)
			}
			if binding != row.binding || sensitive != row.sensitive {
				t.Errorf("binding %q, sensitive %v; want %q, %v — the fixture no longer exercises %s",
					binding, sensitive, row.binding, row.sensitive, row.name)
			}
		})
	}
}

// TestConnectionAttachmentFacts_BindingAbsentWhenTheSlotSetIsUnknown is the
// unknown-set row for the socket's slots: a node whose Component resolves to no
// entity has no socket to read, so an attachment its connection carries reports
// no binding rather than free. The connection itself is still carried, because it
// was recorded where the attachment was collected and does not depend on the
// catalog.
func TestConnectionAttachmentFacts_BindingAbsentWhenTheSlotSetIsUnknown(t *testing.T) {
	t.Parallel()
	graph := buildConnectionFactsGraph(t, "core/unknown", []topology.FeatureAttachment{
		onConnection("models", "gpt", "credential"),
	})
	attachments, ok := graph.Spec.FeatureValuesMapping.Get("gw")
	if !ok {
		t.Fatalf("no attachments emitted for node gw")
	}
	got := entry(t, attachments, "gpt.credential")
	if got.Binding != "" {
		t.Errorf("the host Component resolves to no entity, so no binding finding exists; got %q", got.Binding)
	}
	if got.Sensitive {
		t.Errorf("no slot declaration was read and the Feature declares nothing sensitive, so nothing marks it; got %+v", got)
	}
	if got.Connection == nil || got.Connection.Socket != "models" || got.Connection.Alias != "gpt" {
		t.Errorf("connection = %+v, want socket models alias gpt", got.Connection)
	}
}
