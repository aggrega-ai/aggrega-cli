// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The near-miss advisories, expressed as constraint declarations. See checks.go
// for what a declaration here is.
//
// An advisory has a severity and not a different nature, so it is declared the
// same way a halting rule is: the severity is part of the declaration, which is
// what keeps the consequence of a violation beside the rule rather than making
// it a property of which collection the output is appended to.
//
// Both rules exist because an open surface has a cost. A name matching no
// declaration is legal on either surface — an author may attach a Feature under
// any name, and may name a dependency socket their component type declares no
// entry for — and that openness is what lets one entity serve shapes its author
// never anticipated. What it cannot distinguish is a name meant to be new from a
// name meant to be one that exists and was mistyped, so the mitigation is to
// name both spellings and let the author see the pair.
//
// Neither can halt, and that is a property of the rule rather than a caution
// about it: pairing two names is a heuristic, and a heuristic that halted would
// refuse a name an author deliberately chose because it happens to sit one edit
// from a declared one.

// authoredSocketNameNearADeclaredOne reports each dependency socket a component
// wrote that its type declares no entry for while declaring one within a single
// edit of it.
//
// Its scope is the component's own dependency block alone, which is the one
// surface where a socket name matching no declaration is accepted. An
// attachment's block and a Release's edge surface may each name only a
// declared socket, so a near miss there is already refused and reporting it
// again would report it twice.
//
// It is invoked once per component rather than once per socket, for the reason
// the halting rule on the Release surface is: the socket axis enumerates what
// a component type declares, and the socket this rule is about is by definition
// not among them, so a per-socket invocation would never be presented with its
// own subject. The set the component wrote is read as a fact instead.
//
// The detail leads with the socket the author wrote, which is the value the
// rule's own wording is about and so carries no caption. The component type it
// was named on and the declared socket it sits within one edit of follow, each
// captioned, because the wording names neither and a reader handed three bare
// quoted names has no way to tell which is which. One objection is raised per
// pair, so a name near two declared sockets names both.
var authoredSocketNameNearADeclaredOne = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeSubject,
]{
	Sentinel: ErrUsesSocketNearMiss(),
	Severity: constraint.SeverityWarning,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeSubject,
	) []constraint.Objection {
		wired := v.AuthoredUsesSocketConnections(s.NodeID())
		if wired.Len() == 0 {
			return nil
		}
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		declared := v.DeclaredSocketNames(componentType)
		var out []constraint.Objection
		for socket := range wired.All() {
			if slices.Contains(declared, socket) {
				continue
			}
			for _, candidate := range NearMissCandidates(socket, declared) {
				out = append(out, s.RaiseWith(ErrUsesSocketNearMiss(),
					constraint.Uncaptioned(socket),
					constraint.Captioned("on component type", componentType),
					constraint.Captioned("near the declared socket", candidate)))
			}
		}
		return out
	},
}

// attachmentNameNearADeclaredSlot reports each attachment an Aggregate wrote
// under a name its component's type declares no slot for while declaring one
// within a single edit of it.
//
// Declaring slots does not close the attachment surface, so an attachment under
// an undeclared name is a free attachment rather than an error. The cost is that
// a mistyped slot name silently becomes one instead of filling the slot the
// author meant, and this rule is what makes the pair visible.
//
// It is asked of the Aggregate's own declaration. An attachment only the
// Release declares is the supported way to add one, and the two surfaces are
// separately authored, so a rule reading one for the other would advise about a
// name the Aggregate never wrote.
//
// A declared slot the attachment's Feature fits is not paired with it. That
// pairing is not a mistyped free attachment but a fill written under another
// name, and whether the slot it fits is left empty is known only once every
// scope has folded — which is where the rule halting on it is asked. Pairing it
// here as well would put an advisory beside that halt about the same attachment,
// and where the slot ends up bound by a broader scope the attachment is an
// ordinary extra one. Fit is decided by [FeatureFitsSlot], the predicate that
// rule reads, so the two cannot come to disagree about which pairing is whose.
//
// The detail leads with the name the attachment was written under, which is the
// value the rule's own wording is about and so carries no caption. The component
// type it sits on and the declared slot it sits within one edit of follow, each
// captioned, because the wording names neither.
var attachmentNameNearADeclaredSlot = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrAttachmentSlotNearMiss(),
	Severity: constraint.SeverityWarning,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		featureType, written := v.AuthoredAttachmentType(s.NodeID(), s.AttachmentName())
		if !written {
			return nil
		}
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		declared := v.DeclaredSlotNames(componentType)
		if slices.Contains(declared, s.AttachmentName()) {
			return nil
		}
		featureLabels := v.AttachmentFeatureLabels(featureType)
		var out []constraint.Objection
		for _, candidate := range NearMissCandidates(s.AttachmentName(), declared) {
			if FeatureFitsSlot(v.SlotSelector(componentType, candidate), featureLabels) {
				continue
			}
			out = append(out, s.RaiseWith(ErrAttachmentSlotNearMiss(),
				constraint.Uncaptioned(s.AttachmentName()),
				constraint.Captioned("on component type", componentType),
				constraint.Captioned("near the declared slot", candidate)))
		}
		return out
	},
}
