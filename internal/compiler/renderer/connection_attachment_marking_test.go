// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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

// markingComponent declares one slot for the Component's own attachments and one
// slot on a socket, both sensitive and both admitting delivery as an environment
// variable, so the two differ only in where they are declared.
func markingComponent() types.Component {
	own := types.OrderedMap[types.AttachmentSlot]{}
	own.Set("token", types.AttachmentSlot{Sensitive: true, Inject: []string{"env"}})
	onSocket := types.OrderedMap[types.AttachmentSlot]{}
	onSocket.Set("credential", types.AttachmentSlot{Sensitive: true, Inject: []string{"env"}})
	sockets := types.OrderedMap[types.UsesSchemaEntry]{}
	sockets.Set("models", types.UsesSchemaEntry{FeaturesSchema: onSocket})
	return types.Component{
		Metadata: types.ComponentMetadata{Name: markingComponentType, Version: "1.0.0"},
		Spec:     types.ComponentSpec{FeaturesSchema: own, UsesSchema: sockets},
	}
}

// markingGraph holds one node of that Component carrying the attachment filling
// its own slot and the attachment one of its connections carries in the slot the
// socket declares.
func markingGraph() *types.IRGraph {
	entries := types.OrderedMap[types.IRFeatureEntry]{}
	entries.Set("token", types.IRFeatureEntry{Type: "core/referenced-secret"})
	entries.Set(types.JoinConnectionAttachmentName("gpt", "credential"), types.IRFeatureEntry{
		Type:       "core/referenced-secret",
		Connection: &types.AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"},
	})
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("gw", types.IRNode{Component: markingComponentType})
	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "test"},
		Spec:     types.IRSpec{Nodes: nodes},
	}
	graph.Spec.FeatureValuesMapping.Set("gw", entries)
	return graph
}

// TestRenderedKeyMarking_CannotReachAConnectionAttachment establishes that the
// marking a provider guest receives over its rendered keys cannot name a key an
// attachment written on a connection supplies, so its reading only the slots the
// Component declares for its own attachments loses nothing.
//
// The marking's slot half joins a slot declaration to a workload container's
// attachment entry through the slot the entry names. Every entry reaching that
// join has first passed the workload's slot check, which reads the slot set the
// render passes it — the Component's own — and has had its fill's published
// locators read from the node's own attachments. A slot a connection's socket
// declares is in neither, and an attachment a connection carries is not one of
// the node's own, so the rows naming either are refused before any marking is
// built. The row naming the Component's own slot is the control: it passes the
// same check over the same context and is marked, which is what shows the
// refusals are about where the slot is declared and not about the entry shape.
//
// The socket's slot is asserted sensitive first, so neither refusal can be
// explained by a slot that declared nothing.
func TestRenderedKeyMarking_CannotReachAConnectionAttachment(t *testing.T) {
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
	socketSlots, known := ir.SocketAttachmentSlots(lookup, markingComponentType, "models")
	if !known || !ir.SensitiveSlots(socketSlots)["credential"] {
		t.Fatal("the socket must declare its credential slot sensitive, or a refusal below proves nothing")
	}
	connectionAttachment := types.JoinConnectionAttachmentName("gpt", "credential")

	for _, tc := range []struct {
		name string
		from string
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
			name:    "the slot the connection's socket declares",
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
			ctx := AssembleNodeContext("gw", graph, NodeProvides{"gw": types.OrderedMap[string]{}}, nil, inject, injectRaw, nil)
			if _, own := ctx.Features.Get(connectionAttachment); own {
				t.Fatalf("the attachment the connection carries is among the node's own attachments")
			}
			// The published locators reach the context the way the render folds
			// them: by attachment name, onto the node's own attachments only.
			locators := types.OrderedMap[string]{}
			locators.Set("name", "gw-secret")
			locators.Set("key", "token")
			applyAttachmentProvides(&ctx.Features, map[string]types.OrderedMap[string]{
				"token":              locators,
				connectionAttachment: locators,
			})

			r := minimalRecipe("gateway")
			r.Spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: gateway:1.0
    attachments:
      API_TOKEN:
        from: "` + tc.from + `"
        as: env
`}
			state := NewPhaseBState(r, "gw", "default", "recipe.yaml")
			if err := phaseB0(ctx, state, types.OrderedMap[string]{}); err != nil {
				t.Fatalf("phaseB0: %v", err)
			}
			if err := phaseB1(ctx, state); err != nil {
				t.Fatalf("phaseB1: %v", err)
			}
			err := phaseB2(ctx, state, component.Spec.FeaturesSchema)
			if tc.refused {
				if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
					t.Fatalf("an entry naming %q must be refused by the workload's slot check; got %v", tc.from, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("phaseB2: %v", err)
			}
			declared := DeclaredSensitivity{Slots: ir.SensitiveSlots(ownSlots)}
			got := SensitiveRenderedKeys(declared, types.OrderedMap[any]{}, state.ParsedWorkload)
			if !reflect.DeepEqual(got, tc.marked) {
				t.Errorf("marking = %v; want %v", got, tc.marked)
			}
		})
	}
}
