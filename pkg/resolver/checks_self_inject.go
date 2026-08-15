// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The rules over a component's own injection block, expressed as constraint
// declarations. See checks.go for what a declaration here is.
//
// All four rules the hand-written form carries are declared below. The one that
// turns on authorship — that a component type reserving delivery to its Recipe
// refuses an instance which writes an injection block AT ALL, whatever the block
// carries — reads a fact reporting whether the block was written, because the
// mechanisms it declares cannot answer that: a block written and left empty
// yields the same names as no block at all, none, while the two require opposite
// verdicts. A rule reading the mechanisms instead would accept exactly the
// document that rule exists to refuse.
//
// The two rules that a type permitting nothing bears on are separate
// declarations and each states its own finding, so a block dictating mechanisms
// on such a type is reported by both: once for having been written, and once per
// mechanism dictated against an allowlist admitting none. That follows from
// declaring them independently rather than in sequence, and it is why neither is
// written to fall silent when the other speaks.

// connectionDeliveryKeys is the keys one connection delivers through one
// mechanism, read out of that connection's authored delivery payload.
//
// It reads the payload through the same shape reader the component's own
// injection block is read with, so a payload shape one of them recognises is a
// shape both recognise. That matters because this feeds a collision rule: a
// reader returning no keys for a shape it does not recognise makes the rule
// approve a declaration it never examined, which from the outside is
// indistinguishable from having examined it and found nothing wrong.
//
// The result is a membership set and carries no order of its own: the payload
// arrives in whatever shape the author wrote, and only one of those shapes is
// ordered. Every reader below tests membership rather than iterating it.
func connectionDeliveryKeys(mechanism string, payload any) []string {
	keys := map[string][]string{}
	appendInjectDeliveryKeys(keys, mechanism, payload)
	return keys[mechanism]
}

// selfInjectBlockAllowed reports a component's own injection block written on a
// component type that permits no delivery mechanism at all.
//
// A type naming no mechanism states that its Recipe wires its material
// internally, so it permits an instance to dictate nothing for its own
// configuration. Writing the block is what this refuses, and what the block
// carries makes no difference to the verdict: an author who writes an empty
// block has dictated delivery on a type that reserves it, and an author who
// writes no block has dictated nothing, which is what most components ship.
//
// It reads whether the block was written rather than what it declares, because
// the two documents the rule must separate declare the same thing — no
// mechanism. A rule reading the mechanisms would accept the very document this
// one exists to refuse, and would do it silently, since the accepting answer and
// the refusing one are the same empty list.
//
// A component type the catalog holds no entry for ends the rule, because the
// allowlist reads empty for it exactly as it does for a type permitting nothing.
// What that absence means is a separate rule's question.
//
// The detail is the component type, which is what an author has to change: the
// block is correct authoring against a type that permits delivery, so the finding
// is about the pairing rather than about the block alone.
var selfInjectBlockAllowed = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeSubject,
]{
	Sentinel: ErrSelfInjectForbidden(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeSubject,
	) []constraint.Objection {
		if !v.AuthoredInjectBlockPresent(s.NodeID()) {
			return nil
		}
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		if len(v.DeclaredInjectMechanismNames(componentType)) > 0 {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrSelfInjectForbidden(), componentType)}
	},
}

// selfInjectMechanismAllowed reports each delivery mechanism a component's own
// injection block dictates that the component type does not permit.
//
// A component type's allowlist is what it permits an instance to dictate for its
// own configuration. A type naming no mechanism states that its Recipe wires its
// material internally, so it permits none — the empty allowlist is the most
// constrained answer rather than an absent one, and every mechanism an instance
// dictates against it is refused.
//
// A component type the catalog holds no entry for ends the rule, because the
// allowlist reads empty for it exactly as it does for a type permitting nothing.
// What that absence means is a separate rule's question.
//
// The detail is the component type, the mechanism the instance dictated, and
// then the mechanisms that type permits, in author order.
var selfInjectMechanismAllowed = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeMechanismSubject,
]{
	Sentinel: ErrSelfInjectForbidden(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeMechanismSubject,
	) []constraint.Objection {
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		permitted := v.DeclaredInjectMechanismNames(componentType)
		if slices.Contains(permitted, s.MechanismName()) {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrSelfInjectForbidden(),
			append([]string{componentType, s.MechanismName()}, permitted...)...)}
	},
}

// selfInjectKeyNotAlsoOnAConnection reports each delivery key a component claims
// both in its own injection block and on one of its connections, within one
// mechanism.
//
// Delivery cannot come from two sources at once, so a key claimed by both is a
// contradiction rather than a precedence question. The two sources are separate
// authoring surfaces, which is why the rule reads the connection's own authored
// delivery payload rather than anything the component's block carries about it.
//
// The connections searched are the ones the component's own dependency block
// wrote, read as the entries the author wrote rather than as the connections a
// build draws: the claim is made by writing the entry, so an entry that draws
// nothing still makes it.
//
// The detail names the mechanism and the key, then the socket and the alias of
// the first connection in author order that also claims it. The correction is to
// the pair rather than to that connection, so one objection is raised however
// many connections claim the key; the connection is named because it is what an
// author looks at first.
var selfInjectKeyNotAlsoOnAConnection = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeMechanismDeliveryKeySubject,
]{
	Sentinel: ErrInjectKeyCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeMechanismDeliveryKeySubject,
	) []constraint.Objection {
		wired := v.AuthoredUsesSocketConnections(s.NodeID())
		for socket := range wired.All() {
			for _, alias := range v.AuthoredUsesSocketEntryAliases(s.NodeID(), socket) {
				dictated := v.AuthoredPerEdgeInject(s.NodeID(), socket, alias)
				payload, ok := dictated.Get(s.MechanismName())
				if !ok {
					continue
				}
				if !slices.Contains(connectionDeliveryKeys(s.MechanismName(), payload), s.DeliveryKey()) {
					continue
				}
				return []constraint.Objection{s.Raise(ErrInjectKeyCollision(),
					s.MechanismName(), s.DeliveryKey(), socket, alias)}
			}
		}
		return nil
	},
}

// selfInjectKeyNotOnTwoMechanisms reports each delivery key a component's own
// injection block claims under more than one mechanism.
//
// It is the same contradiction the rule beside it refuses, between two
// mechanisms of one source rather than between two sources: the runtime cannot
// deliver one key from two places, so declaring it twice states something that
// cannot hold.
//
// One objection is raised per mechanism claiming the key, so an author correcting
// the contradiction sees every site that participates in it rather than one of
// them. The detail is the mechanism this objection is about, the key, and then
// the other mechanisms claiming it, in author order.
var selfInjectKeyNotOnTwoMechanisms = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeMechanismDeliveryKeySubject,
]{
	Sentinel: ErrInjectKeyCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeMechanismDeliveryKeySubject,
	) []constraint.Objection {
		var others []string
		for _, mechanism := range v.AuthoredInjectMechanismNames(s.NodeID()) {
			if mechanism == s.MechanismName() {
				continue
			}
			if slices.Contains(v.AuthoredInjectDeliveryKeys(s.NodeID(), mechanism), s.DeliveryKey()) {
				others = append(others, mechanism)
			}
		}
		if len(others) == 0 {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrInjectKeyCollision(),
			append([]string{s.MechanismName(), s.DeliveryKey()}, others...)...)}
	},
}
