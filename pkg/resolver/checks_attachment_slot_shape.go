// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// attachmentDeliveryIsSlotPermitted reports each delivery mechanism an
// attachment binding a declared slot dictates that the slot does not permit.
//
// A slot's allowlist is the whole of what a fill bound to it may dictate
// delivery through. A slot naming no mechanism states that its material is
// consumed by the Recipe internally rather than delivered to the workload, so it
// permits none — the empty allowlist is the most constrained answer rather than
// an absent one, and any delivery dictated against it is refused.
//
// It is the whole of what this rule adds over the authoring contract the
// attachment's own document already answers to. That contract names which keys a
// fill may carry and what shape each takes; what it cannot know is which
// mechanisms THIS component's slot permits, because that is declared on the
// component type rather than on the attachment. So the mechanism names are the
// slot's to constrain and the shape of a delivery entry is the contract's, and
// restating the shape here would give one rule two homes.
//
// An attachment under a name no slot declares is a free attachment and ends the
// rule. Declaring slots does not close the attachment surface — attaching a
// Feature under a name no slot declares is supported — so a rule reading the
// declared slot names as the permitted attachment names would refuse a shape the
// product supports. What the set answers is which names carry a declaration for
// a fill to be measured against.
//
// PRESENCE IS NOT THIS RULE'S QUESTION. Whether a slot declared as one that must
// be filled ends up bound is asked of a component's attachment set after the
// whole cascade, because an attachment contributed at a scope above the
// component binds the slot while leaving the component's own block empty.
//
// A component type the catalog holds no entry for ends the rule, because the
// declared slot names read empty for it exactly as they do for a type declaring
// none. What that absence means is a separate rule's question.
//
// The detail is the slot the attachment binds, the mechanism it dictated, and
// then the mechanisms that slot permits, in author order.
var attachmentDeliveryIsSlotPermitted = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrAttachmentSlotShape(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		if !slices.Contains(v.DeclaredSlotNames(componentType), s.AttachmentName()) {
			return nil
		}
		authored := v.AuthoredAttachmentBlock(s.NodeID())
		fill, written := authored.Get(s.AttachmentName())
		if !written {
			return nil
		}
		dictated := InjectMechanismNames(fill.Inject)
		if len(dictated) == 0 {
			return nil
		}
		permitted := v.SlotInjectMechanismNames(componentType, s.AttachmentName())
		var out []constraint.Objection
		for _, mechanism := range dictated {
			if slices.Contains(permitted, mechanism) {
				continue
			}
			out = append(out, s.Raise(ErrAttachmentSlotShape(),
				append([]string{s.AttachmentName(), mechanism}, permitted...)...))
		}
		return out
	},
}
