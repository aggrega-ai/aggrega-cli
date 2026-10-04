// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// markingLookup resolves the marking Component and nothing else.
func markingLookup() resolver.CatalogLookup {
	component := markingComponent()
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == markingComponentType {
				return component, true
			}
			return types.Component{}, false
		},
	}
}

// markingContext assembles the marking graph's gateway context the way a render
// does, with one resolved locator bag for every attachment.
func markingContext(t *testing.T) *types.TemplateContext {
	t.Helper()
	locators := types.OrderedMap[string]{}
	locators.Set("name", "gw-secret")
	locators.Set("key", "token")
	inject, injectRaw := BuildInjectPipe(nil)
	return AssembleNodeContext("gw", markingGraph(), NodeProvides{"gw": types.OrderedMap[string]{}}, nil, inject, injectRaw, nil,
		map[string]types.OrderedMap[string]{
			"token": locators,
			types.JoinConnectionAttachmentName("gpt", "credential"): locators,
			types.JoinConnectionAttachmentName("gpt", "audit"):      locators,
		})
}

// TestAttachmentProvidesFromContext_CarriesEachConnectionsAttachmentByItsComposedName
// holds the lookup a workload reference is resolved through to every attachment
// the node holds: its own under the names they fill on `.features`, and each one a
// connection carries under the name the compiler composes for it — which is the
// name a folded Workload entry names. The connection's attachments come from the
// projection, and are never added to `.features` to be found.
func TestAttachmentProvidesFromContext_CarriesEachConnectionsAttachmentByItsComposedName(t *testing.T) {
	t.Parallel()
	ctx := markingContext(t)
	got := AttachmentProvidesFromContext(ctx)
	for _, tc := range []struct {
		name    string
		present bool
	}{
		{name: "token", present: true},
		{name: types.JoinConnectionAttachmentName("gpt", "credential"), present: true},
		{name: types.JoinConnectionAttachmentName("gpt", "audit"), present: true},
		{name: "credential"},
		{name: "gpt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provides, present := got[tc.name]
			if present != tc.present {
				t.Fatalf("lookup holds %q = %v; want %v (lookup: %v)", tc.name, present, tc.present, got)
			}
			if !present {
				return
			}
			if name, _ := provides.Get("name"); name != "gw-secret" {
				t.Errorf("%q resolves name %q; want the resolved bag's gw-secret", tc.name, name)
			}
		})
	}
	if _, own := ctx.Features.Get(types.JoinConnectionAttachmentName("gpt", "credential")); own {
		t.Error("the connection's attachment reached the node's own .features")
	}
}

// TestNodeWorkloadSlots_ReadsEachConnectionsSocketSlots holds the slot set a
// node's Workload is checked against: the Component's own slots, and for each
// connection in the order the context projects them, the slots its socket
// declares — an ad-hoc socket declaring none, and a Component that does not
// resolve contributing nothing of either kind.
func TestNodeWorkloadSlots_ReadsEachConnectionsSocketSlots(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("gpt", types.EnrichedEdge{Socket: "models"})
	uses.Set("notes", types.EnrichedEdge{Socket: "adhoc"})

	type connection struct {
		socket string
		slots  []string
	}
	for _, tc := range []struct {
		name          string
		componentType string
		own           []string
		connections   map[string]connection
		order         []string
	}{
		{
			name:          "a Component that resolves",
			componentType: markingComponentType,
			own:           []string{"token"},
			connections: map[string]connection{
				"gpt":   {socket: "models", slots: []string{"credential", "audit"}},
				"notes": {socket: "adhoc", slots: []string{}},
			},
			order: []string{"gpt", "notes"},
		},
		{
			name:          "a Component that does not resolve",
			componentType: "app/unknown",
			own:           []string{},
			connections: map[string]connection{
				"gpt":   {socket: "models", slots: []string{}},
				"notes": {socket: "adhoc", slots: []string{}},
			},
			order: []string{"gpt", "notes"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NodeWorkloadSlots(markingLookup(), tc.componentType, uses)
			if keys := got.Own.Keys(); !reflect.DeepEqual(keys, tc.own) {
				t.Errorf("own slots = %v; want %v", keys, tc.own)
			}
			if keys := got.Connections.Keys(); !reflect.DeepEqual(keys, tc.order) {
				t.Errorf("connections = %v; want %v in projection order", keys, tc.order)
			}
			for alias, want := range tc.connections {
				conn, ok := got.Connections.Get(alias)
				if !ok {
					t.Fatalf("connection %q absent", alias)
				}
				if conn.Socket != want.socket || !reflect.DeepEqual(conn.Slots.Keys(), want.slots) {
					t.Errorf("connection %q = socket %q slots %v; want socket %q slots %v", alias, conn.Socket, conn.Slots.Keys(), want.socket, want.slots)
				}
			}
		})
	}
}

// TestSensitiveSocketSlots_NamesEachConnectionsSensitiveSlot holds the socket
// half of the marking's declarations: each sensitive slot under the name a folded
// entry names it by, with the socket that declares it — two connections through
// one socket sharing its declaration — and nothing for a slot declaring nothing.
func TestSensitiveSocketSlots_NamesEachConnectionsSensitiveSlot(t *testing.T) {
	t.Parallel()
	socket := types.OrderedMap[types.AttachmentSlot]{}
	socket.Set("credential", types.AttachmentSlot{Sensitive: true})
	socket.Set("audit", types.AttachmentSlot{})
	connections := types.OrderedMap[validator.WorkloadConnection]{}
	connections.Set("gpt", validator.WorkloadConnection{Socket: "models", Slots: socket})
	connections.Set("nim", validator.WorkloadConnection{Socket: "models", Slots: socket})

	got := SensitiveSocketSlots(validator.WorkloadSlots{Connections: connections})
	want := map[string]rendercore.SocketSlot{
		"gpt.credential": {Socket: "models", Slot: "credential"},
		"nim.credential": {Socket: "models", Slot: "credential"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("socket slots = %v; want %v", got, want)
	}
	if none := SensitiveSocketSlots(validator.WorkloadSlots{}); none != nil {
		t.Errorf("no connection declares nothing; got %v", none)
	}
}

// TestSensitiveRenderedKeys_SocketSlotCoordinateDoesNotCollide marks, in one
// workload, an entry naming a Component slot called `credential` and an entry
// naming the `credential` slot a connection's socket declares: the two carry two
// different coordinates, so a consumer can tell which declaration each points at.
// The unmarked entry is the control that the join reads declarations alone.
func TestSensitiveRenderedKeys_SocketSlotCoordinateDoesNotCollide(t *testing.T) {
	t.Parallel()
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Attachments.Set("OWN", types.AttachmentRef{From: "credential", As: "env"})
	c.Attachments.Set("CONNECTION", types.AttachmentRef{From: types.JoinConnectionAttachmentName("gpt", "credential"), As: "env"})
	c.Attachments.Set("PUBLIC", types.AttachmentRef{From: "audit", As: "env"})
	w := &types.Workload{}
	w.Containers.Set("main", c)

	got := SensitiveRenderedKeys(DeclaredSensitivity{
		Slots:       map[string]bool{"credential": true},
		SocketSlots: map[string]rendercore.SocketSlot{"gpt.credential": {Socket: "models", Slot: "credential"}},
	}, types.OrderedMap[any]{}, w)
	want := map[string]string{
		"OWN":        "featuresSchema.credential",
		"CONNECTION": "usesSchema.models.featuresSchema.credential",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("marking = %v; want %v", got, want)
	}
}

// TestRenderWorkloadForDisplay_FoldsAsTheEmittedDocumentIs holds the trace's
// re-render to the emitted document's shape for an entry naming a connection: the
// shown document carries the folded slot and no connection, as the emitted one
// does. A connection the fold refuses leaves the re-render unproduced.
func TestRenderWorkloadForDisplay_FoldsAsTheEmittedDocumentIs(t *testing.T) {
	t.Parallel()
	ctx := markingContext(t)
	slots := NodeWorkloadSlots(markingLookup(), markingComponentType, ctx.Context.Uses)
	for _, tc := range []struct {
		name       string
		connection string
		produced   bool
	}{
		{name: "a connection the component draws", connection: "gpt", produced: true},
		{name: "a connection the component does not draw", connection: "claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: gateway:1.0
    attachments:
      API_KEY:
        from: credential
        connection: ` + tc.connection + `
        as: env
`
			shown, produced := renderWorkloadForDisplay("gateway", text, ctx, redact.Keys{}, slots)
			if produced != tc.produced {
				t.Fatalf("produced = %v; want %v", produced, tc.produced)
			}
			if !produced {
				return
			}
			c, _ := shown.Containers.Get("main")
			ref, _ := c.Attachments.Get("API_KEY")
			if ref.From != "gpt.credential" || ref.Connection != "" {
				t.Errorf("shown entry = %+v; want the folded slot and no connection", ref)
			}
		})
	}
}
