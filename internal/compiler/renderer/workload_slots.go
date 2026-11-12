// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// NodeWorkloadSlots assembles every slot a node's Workload may name: the slots
// its Component declares for its own attachments, and, for each connection the
// node draws — in the order its connections are projected, keyed by the alias
// each is written under — the slots that connection's socket declares.
//
// Both halves are read through their one home: the Component's own slots through
// [ir.ComponentAttachmentSlots], and a socket's through
// [resolver.SocketAttachmentSlots], the accessor every rule over a socket's slots
// reads. So the slots a Workload entry is checked against and the slots a
// connection's attachments were held to when they were written cannot come to
// differ. A Component that does not resolve contributes no slot of either kind.
//
// uses is the node's projected connections, the same map a Recipe ranges as
// `.context.uses`, so a Workload entry names a connection by the alias the
// Recipe reads it under.
func NodeWorkloadSlots(catalog resolver.CatalogLookup, componentType string, uses types.OrderedMap[types.EnrichedEdge]) validator.WorkloadSlots {
	own, _ := ir.ComponentAttachmentSlots(catalog, componentType)
	connections := types.OrderedMap[validator.WorkloadConnection]{}
	for alias, edge := range uses.All() {
		socketSlots, _ := resolver.SocketAttachmentSlots(catalog, componentType, edge.Socket)
		connections.Set(alias, validator.WorkloadConnection{Socket: edge.Socket, Slots: socketSlots})
	}
	return validator.WorkloadSlots{Own: own, Connections: connections}
}

// SensitiveSocketSlots answers, for each connection a node draws, the slots its
// socket declares sensitive, keyed by the name a folded Workload entry names
// each by — the alias and the slot joined as the compiler joins a connection
// attachment's name — so the per-rendered-key marking reaches an entry that read
// a connection's slot.
//
// Each socket's slots are asked of [ir.SensitiveSlots], the one reader of a
// slot's sensitivity declaration, and are taken from the same assembly the
// Workload fold reads, so the entry the fold produced and the declaration the
// marking joins it to are named by one composition. Returns nil when no socket
// declares a sensitive slot.
func SensitiveSocketSlots(slots validator.WorkloadSlots) map[string]rendercore.SocketSlot {
	var out map[string]rendercore.SocketSlot
	for alias, conn := range slots.Connections.All() {
		for slot := range ir.SensitiveSlots(conn.Slots) {
			if out == nil {
				out = map[string]rendercore.SocketSlot{}
			}
			out[types.JoinConnectionAttachmentName(alias, slot)] = rendercore.SocketSlot{Socket: conn.Socket, Slot: slot}
		}
	}
	return out
}
