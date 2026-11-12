// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// markingComponentType names the Component the marking tests below resolve.
const markingComponentType = "app/gateway"

// markingComponent declares one slot for the Component's own attachments and two
// slots on a socket. The own slot and the socket's credential slot are both
// sensitive and both admit delivery as an environment variable, so the two differ
// only in where they are declared; the socket's audit slot admits the same
// delivery and declares nothing sensitive, so it is the control that lacks the
// declaration.
func markingComponent() types.Component {
	own := types.OrderedMap[types.AttachmentSlot]{}
	own.Set("token", types.AttachmentSlot{Sensitive: true, Inject: []string{"env"}})
	onSocket := types.OrderedMap[types.AttachmentSlot]{}
	onSocket.Set("credential", types.AttachmentSlot{Sensitive: true, Inject: []string{"env"}})
	onSocket.Set("audit", types.AttachmentSlot{Inject: []string{"env"}})
	sockets := types.OrderedMap[types.UsesSchemaEntry]{}
	sockets.Set("models", types.UsesSchemaEntry{FeaturesSchema: onSocket})
	return types.Component{
		Metadata: types.ComponentMetadata{Name: markingComponentType, Version: "1.0.0"},
		Spec:     types.ComponentSpec{FeaturesSchema: own, UsesSchema: sockets},
	}
}

// markingGraph holds one node of that Component drawing one connection, gpt,
// through the socket. The node carries the attachment filling its own slot, and
// the connection carries one attachment in each slot the socket declares.
func markingGraph() *types.IRGraph {
	entries := types.OrderedMap[types.IRFeatureEntry]{}
	entries.Set("token", types.IRFeatureEntry{Type: "core/referenced-secret"})
	for _, slot := range []string{"credential", "audit"} {
		entries.Set(types.JoinConnectionAttachmentName("gpt", slot), types.IRFeatureEntry{
			Type:       "core/referenced-secret",
			Connection: &types.AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: slot},
		})
	}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("gw", types.IRNode{Component: markingComponentType})
	nodes.Set("openai", types.IRNode{Component: "core/ai-model/openai-compatible"})
	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "test"},
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{{Source: "gw", Target: "openai", Socket: "models", Alias: "gpt"}},
		},
	}
	graph.Spec.FeatureValuesMapping.Set("gw", entries)
	return graph
}

// TestRenderedKeyMarking_ReachesAConnectionSlotThroughTheFold establishes that
// the marking a provider guest receives over its rendered keys reaches a key a
// Workload entry delivers from a slot a connection's socket declares — and only
// through the entry naming the connection.
//
// The marking's slot half joins a slot declaration to a workload container's
// attachment entry through the slot the entry names. An entry naming a
// connection is folded onto the name the compiler composes for that connection's
// slot before the workload's slot check runs, and the marking reads the socket's
// declaration of that slot through the one sensitivity reader, under the same
// composed name. Its coordinate names the socket, so it cannot be read as a slot
// the Component declares for its own attachments.
//
// The rows naming the Component's own slot and the socket's undeclared-sensitive
// slot are the controls: the first is marked from the Component's declaration
// through the same check over the same context, which shows the socket row is
// marked for where its slot is declared rather than for the entry shape; the
// second is admitted and left unmarked, which shows the socket row is marked by
// its slot's declaration rather than by being on a connection. The rows naming
// the socket's slot bare, or the composed name directly, are refused by the
// workload's slot check, so a connection's slot is reached only by naming the
// connection.
//
// The socket's credential slot is asserted sensitive first, so neither the mark
// nor a refusal can be explained by a slot that declared nothing.
func TestRenderedKeyMarking_ReachesAConnectionSlotThroughTheFold(t *testing.T) {
	t.Parallel()
	component := markingComponent()
	lookup := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == markingComponentType {
				return component, true
			}
			return types.Component{}, false
		},
	}
	ownSlots, known := ir.ComponentAttachmentSlots(lookup, markingComponentType)
	if !known {
		t.Fatal("the Component's own slot set must resolve")
	}
	socketSlots, known := resolver.SocketAttachmentSlots(lookup, markingComponentType, "models")
	if !known || !ir.SensitiveSlots(socketSlots)["credential"] {
		t.Fatal("the socket must declare its credential slot sensitive, or a row below proves nothing")
	}
	if ir.SensitiveSlots(socketSlots)["audit"] {
		t.Fatal("the socket's audit slot must declare nothing sensitive, or its control row proves nothing")
	}
	connectionAttachment := types.JoinConnectionAttachmentName("gpt", "credential")

	for _, tc := range []struct {
		name       string
		from       string
		connection string
		// refused names whether the workload's slot check refuses the entry.
		refused bool
		// marked is the marking built over the workload the check admitted.
		marked map[string]string
	}{
		{
			name:   "the slot the Component declares for its own attachments",
			from:   "token",
			marked: map[string]string{"API_TOKEN": "featuresSchema.token"},
		},
		{
			name:       "the slot the connection's socket declares, named through the connection",
			from:       "credential",
			connection: "gpt",
			marked:     map[string]string{"API_TOKEN": "usesSchema.models.featuresSchema.credential"},
		},
		{
			name:       "a socket slot declaring nothing sensitive, named through the connection",
			from:       "audit",
			connection: "gpt",
		},
		{
			name:    "the slot the connection's socket declares, named bare",
			from:    "credential",
			refused: true,
		},
		{
			name:    "the qualified name of the attachment the connection carries",
			from:    connectionAttachment,
			refused: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			graph := markingGraph()
			inject, injectRaw := BuildInjectPipe(nil)
			// The published locators reach the context the way the render folds
			// them: one resolved bag keyed by attachment name, folded onto each
			// attachment where the projection places it — the connection's
			// attachments never among the node's own.
			locators := types.OrderedMap[string]{}
			locators.Set("name", "gw-secret")
			locators.Set("key", "token")
			ctx := AssembleNodeContext("gw", graph, NodeProvides{"gw": types.OrderedMap[string]{}}, nil, inject, injectRaw, nil,
				map[string]types.OrderedMap[string]{
					"token":              locators,
					connectionAttachment: locators,
					types.JoinConnectionAttachmentName("gpt", "audit"): locators,
				})
			if _, own := ctx.Features.Get(connectionAttachment); own {
				t.Fatalf("the attachment the connection carries is among the node's own attachments")
			}

			connectionLine := ""
			if tc.connection != "" {
				connectionLine = "\n        connection: \"" + tc.connection + "\""
			}
			r := minimalRecipe("gateway")
			r.Spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1beta1
kind: Workload
containers:
  main:
    image: gateway:1.0
    attachments:
      API_TOKEN:
        from: "` + tc.from + `"` + connectionLine + `
        as: env
`}
			state := NewPhaseBState(r, "gw", "default", "recipe.yaml")
			if err := phaseB0(ctx, state, types.OrderedMap[string]{}); err != nil {
				t.Fatalf("phaseB0: %v", err)
			}
			if err := phaseB1(ctx, state); err != nil {
				t.Fatalf("phaseB1: %v", err)
			}
			slots := NodeWorkloadSlots(lookup, markingComponentType, ctx.Context.Uses)
			err := phaseB2(ctx, state, slots)
			if tc.refused {
				if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
					t.Fatalf("an entry naming %q must be refused by the workload's slot check; got %v", tc.from, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("phaseB2: %v", err)
			}
			declared := DeclaredSensitivity{
				Slots:       ir.SensitiveSlots(ownSlots),
				SocketSlots: SensitiveSocketSlots(slots),
			}
			got := SensitiveRenderedKeys(declared, types.OrderedMap[any]{}, state.ParsedWorkload)
			if !reflect.DeepEqual(got, tc.marked) {
				t.Errorf("marking = %v; want %v", got, tc.marked)
			}
		})
	}
}
