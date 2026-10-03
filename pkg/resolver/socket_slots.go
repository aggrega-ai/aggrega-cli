// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SocketAttachmentSlots resolves the attachment slots one socket of a Component
// declares — the slots each connection wired through that socket may fill — in
// the entity's own declaration order, together with whether the declared set is
// KNOWN at all.
//
// The second return distinguishes two answers a caller must not conflate: a
// Component that resolves, whose socket declares no slot, and a Component that
// does not resolve, whose slot set is unknown rather than empty. A Component that
// resolves and does not declare the socket at all answers an empty, known set:
// such a socket declares no slots, which is a determination rather than an
// absence of one. A caller reading only the map would treat "unknown" as
// "declares nothing" and report a determination the compiler never made.
//
// It is the one home for "the slots of this connection". The binding a
// connection attachment records, the sensitivity it takes from its slot, and
// every rule over a socket's slots — which names a connection may carry, what a
// slot's selector admits, whether a slot must be filled — read the set through
// this function, so no two of them can come to disagree about what a socket
// declares. An empty component type or a lookup with no Component resolver
// reports an unknown set rather than asking the catalog a question it cannot
// answer.
func SocketAttachmentSlots(catalog CatalogLookup, componentType, socket string) (types.OrderedMap[types.AttachmentSlot], bool) {
	if componentType == "" {
		return types.OrderedMap[types.AttachmentSlot]{}, false
	}
	comp, found := lookupComponent(catalog, componentType)
	if !found {
		return types.OrderedMap[types.AttachmentSlot]{}, false
	}
	entry, _ := comp.Spec.UsesSchema.Get(socket)
	return entry.FeaturesSchema, true
}
