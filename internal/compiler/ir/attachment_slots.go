// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ComponentAttachmentSlots resolves the attachment slots one Component declares,
// returning them in the entity's own declaration order together with whether the
// declared set is KNOWN at all.
//
// The second return distinguishes two answers a caller must not conflate: an
// entity that resolves and declares no slot, and an entity that does not resolve,
// where the slot set is unknown rather than empty. A caller reading only the map
// would treat "unknown" as "declares nothing" and report a determination the
// compiler never made.
//
// It is the one home for this resolution, so every reader of a Component's
// declared slots reads the same set from the same lookup. An empty component type
// or a lookup with no Component resolver reports an unknown set rather than
// asking the catalog a question it cannot answer.
func ComponentAttachmentSlots(catalogLookup resolver.CatalogLookup, componentType string) (types.OrderedMap[types.AttachmentSlot], bool) {
	if componentType == "" || catalogLookup.Component == nil {
		return types.OrderedMap[types.AttachmentSlot]{}, false
	}
	comp, found := catalogLookup.Component(componentType)
	if !found {
		return types.OrderedMap[types.AttachmentSlot]{}, false
	}
	return comp.Spec.FeaturesSchema, true
}

// SocketAttachmentSlots resolves the attachment slots one socket of a Component
// declares — the slots each connection wired through that socket may fill — in
// the entity's own declaration order, together with whether the declared set is
// KNOWN, on the same terms as [ComponentAttachmentSlots].
//
// A Component that resolves and does not declare the socket answers an empty,
// known set: such a socket declares no slots, which is a determination rather
// than an absence of one. An unresolved Component answers an unknown set, since
// none of its sockets can be read.
func SocketAttachmentSlots(catalogLookup resolver.CatalogLookup, componentType, socket string) (types.OrderedMap[types.AttachmentSlot], bool) {
	if componentType == "" || catalogLookup.Component == nil {
		return types.OrderedMap[types.AttachmentSlot]{}, false
	}
	comp, found := catalogLookup.Component(componentType)
	if !found {
		return types.OrderedMap[types.AttachmentSlot]{}, false
	}
	entry, _ := comp.Spec.UsesSchema.Get(socket)
	return entry.FeaturesSchema, true
}

// attachmentSlotSet is one declared slot set as the per-attachment facts read
// it: the slots, the ones among them declared sensitive, and whether the set is
// known at all. A Component's own slots and a socket's slots are both held in
// this one shape, so the binding and sensitivity facts are read from either by
// the same code.
type attachmentSlotSet struct {
	slots     types.OrderedMap[types.AttachmentSlot]
	sensitive map[string]bool
	resolved  bool
}

// newAttachmentSlotSet pairs a resolved slot set with its sensitive subset, read
// through [SensitiveSlots] so a socket's slots and a Component's are read for
// sensitivity by the same reader.
func newAttachmentSlotSet(slots types.OrderedMap[types.AttachmentSlot], resolved bool) attachmentSlotSet {
	return attachmentSlotSet{slots: slots, sensitive: SensitiveSlots(slots), resolved: resolved}
}

// SensitiveSlots names the declared attachment slots whose declaration states
// that whatever binds them handles secret material.
//
// It is the ONE reader of that declaration. Every consumer of the fact — the
// per-attachment roll-up the IR carries, and the per-node marking a provider
// guest receives over its rendered keys — asks this function, so the two cannot
// disagree about what a slot declares, and a later change to how the declaration
// is spelled moves one implementation rather than two that agree today.
//
// A name absent from the result means either that the slot declares no secret
// material or that no slot of that name is declared; the two are the same answer
// to this question, and a caller needing to tell them apart reads the declared
// set itself. Returns nil when nothing is declared, so a caller pays nothing for
// a Component that declares no sensitive slot.
func SensitiveSlots(slots types.OrderedMap[types.AttachmentSlot]) map[string]bool {
	var marked map[string]bool
	for name, slot := range slots.All() {
		if !slot.Sensitive {
			continue
		}
		if marked == nil {
			marked = map[string]bool{}
		}
		marked[name] = true
	}
	return marked
}
