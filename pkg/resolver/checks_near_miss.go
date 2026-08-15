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
// attachment's block and a deployment's edge surface may each name only a
// declared socket, so a near miss there is already refused and reporting it
// again would report it twice.
//
// It is invoked once per component rather than once per socket, for the reason
// the halting rule on the deployment surface is: the socket axis enumerates what
// a component type declares, and the socket this rule is about is by definition
// not among them, so a per-socket invocation would never be presented with its
// own subject. The set the component wrote is read as a fact instead.
//
// The detail is the component type, the socket the author wrote, and the
// declared socket it sits within one edit of. One objection is raised per pair,
// so a name near two declared sockets names both.
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
			for _, candidate := range nearMissCandidates(socket, declared) {
				out = append(out, s.Raise(ErrUsesSocketNearMiss(),
					componentType, socket, candidate))
			}
		}
		return out
	},
}

// attachmentNameNearADeclaredSlot reports each attachment an application wrote
// under a name its component's type declares no slot for while declaring one
// within a single edit of it.
//
// Declaring slots does not close the attachment surface, so an attachment under
// an undeclared name is a free attachment rather than an error. The cost is that
// a mistyped slot name silently becomes one instead of filling the slot the
// author meant, and this rule is what makes the pair visible.
//
// It is asked of the application's own declaration. An attachment only the
// deployment declares is the supported way to add one, and the two surfaces are
// separately authored, so a rule reading one for the other would advise about a
// name the application never wrote.
//
// The detail is the component type, the name the attachment was written under,
// and the declared slot it sits within one edit of.
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
		if _, written := v.AuthoredAttachmentType(s.NodeID(), s.AttachmentName()); !written {
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
		var out []constraint.Objection
		for _, candidate := range nearMissCandidates(s.AttachmentName(), declared) {
			out = append(out, s.Raise(ErrAttachmentSlotNearMiss(),
				componentType, s.AttachmentName(), candidate))
		}
		return out
	},
}
