// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"slices"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The rules over what a delivery entry reads from, expressed as constraint
// declarations. See checks.go for what a declaration here is.
//
// One rule over two surfaces, declared twice because the two resolve their
// reference against different components: an entry in a component's own
// injection block reads from that component's own published contract, while an
// entry on a connection reads from the contract of the component the connection
// points at. The surfaces are separately authored and independently absent, so
// neither declaration can stand in for the other.

// selfInjectReferenceIsPublished reports each delivery entry in a component's
// own injection block that reads from a key the component's type does not
// publish.
//
// An entry may carry a literal instead of a reference, and a literal is nothing
// to resolve — which is why the rule reads whether the entry references a
// published key at all before reading which one. A single answer would merge a
// literal with a reference to the empty key and report the entry that referenced
// nothing rather than the one that referenced wrongly.
//
// A component type the catalog holds no entry for ends the rule: the published
// set reads empty for it exactly as it does for a type publishing nothing, so
// refusing every reference against it would report the wrong thing. What that
// absence means is a separate rule's question.
//
// The detail is the key the entry reads from, the component type it was resolved
// against, and then the keys that type publishes, sorted — the alternatives an
// author picks from.
var selfInjectReferenceIsPublished = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeMechanismDeliveryKeySubject,
]{
	Sentinel: ErrInvalidInjectKey(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeMechanismDeliveryKeySubject,
	) []constraint.Objection {
		reference, isReference := v.AuthoredInjectProvidesReference(
			s.NodeID(), s.MechanismName(), s.DeliveryKey())
		if !isReference {
			return nil
		}
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		published := v.DeclaredProvidesKeys(componentType)
		if slices.Contains(published, reference) {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrInvalidInjectKey(),
			append([]string{reference, componentType}, published...)...)}
	},
}

// connectionInjectReferenceIsPublished reports each delivery entry on one
// connection that reads from a key the component the connection points at does
// not publish.
//
// The component it resolves against is the one the connection names, falling
// back to the connection's own alias when the entry names no target — which is
// the same fallback that draws the edge, so the rule resolves against the
// component the build wires rather than against a second reading of the same
// authoring.
//
// A target naming no component the Aggregate declares ends the rule. Such a
// component exists only once the composition is flattened, so there is no
// published contract here to measure the reference against.
//
// Delivery entries are read out of the connection's own authored payload, and
// the entries of one mechanism are reported in delivery-key order: the reading
// of a payload this rule shares with the rule over every connection the
// composition drew returns them sorted, so the two report one connection's
// entries in one sequence.
//
// The detail is the mechanism, the delivery key, the key the entry reads from,
// the component type it was resolved against, and then the keys that type
// publishes, sorted.
var connectionInjectReferenceIsPublished = constraint.Check[
	constraint.NormalizedView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrInvalidInjectKey(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.EdgeSubject,
	) []constraint.Objection {
		dictated := v.AuthoredPerEdgeInject(s.NodeID(), s.SocketName(), s.AliasName())
		if dictated.Len() == 0 {
			return nil
		}
		target := v.PerEdgeTarget(s.NodeID(), s.SocketName(), s.AliasName())
		if target == "" {
			target = s.AliasName()
		}
		targetType, present := v.InstanceComponentType(target)
		if !present || !v.CatalogComponentPresent(targetType) {
			return nil
		}
		published := v.DeclaredProvidesKeys(targetType)

		var out []constraint.Objection
		for mechanism, payload := range dictated.All() {
			references := connectionProvidesReferences(payload)
			for _, entry := range references {
				if slices.Contains(published, entry.providesKey) {
					continue
				}
				out = append(out, s.Raise(ErrInvalidInjectKey(),
					append([]string{mechanism, entry.deliveryKey, entry.providesKey, targetType},
						published...)...))
			}
		}
		return out
	},
}

// OriginatedConnectionInjectReferenceIsPublished reports each delivery entry on
// one connection that reads from a key the component the connection points at
// does not publish, wherever that connection was written.
//
// It is the same rule as the one above over a wider set, and the two are
// separate declarations because they are asked at different stages of different
// facts. The rule above reads the Aggregate's own dependency block, which is
// complete as soon as the documents are normalized, and is asked there so a
// developer's own typo halts at the earliest point it can. This one reads the
// connection set the composition produced — every connection, whichever surface
// drew it — which is complete only once every surface has contributed.
//
// The rule the two share cannot be expressed once and asked twice: a check is
// invoked over one subject kind and reads one view, and these differ in both.
// What IS shared is the reading of a payload into published-key references, so
// the two read it through one function and cannot come to disagree about what a
// reference is.
//
// # Why the delivery is read as authored and not as the graph stores it
//
// A delivery entry may carry a literal instead of a reference, and the graph
// stores both as one string per delivery key. A rule reading that form would
// resolve every literal against the target's published set and refuse the ones
// that happened not to name a key — under this rule's condition, which names a
// mistyped published key, for an entry that names no key at all. A literal on a
// connection is refused, by the rule below and under its own condition; this
// rule reads the payload the author wrote, through the decoder the entity
// contract documents, so the two never report one entry twice under two names.
//
// # Why every payload the coordinate names is read
//
// One origin key can name more than one connection: an attachment instance name
// declared at two scopes draws one connection from each block, and the
// composition records the two identically. Reading the first would leave the
// second unexamined while this rule reported nothing, so the fact answers with
// all of them and this walks all of them.
//
// The component the delivery resolves against is the one THIS connection names,
// which is a coordinate the subject carries rather than a fact read back by the
// connection triple: two origins writing one socket and one alias may name
// different targets, and a reading keyed by the triple would measure one
// origin's delivery against the other's published set.
//
// A target naming no component the composition holds ends the rule, and so does
// a component type the catalog holds no entry for — for the reasons the rule
// above states. The detail is the mechanism, the delivery key, the key the entry
// reads from, the component type it was resolved against, and then the keys that
// type publishes, sorted.
var OriginatedConnectionInjectReferenceIsPublished = constraint.Check[
	constraint.GraphView,
	constraint.OriginatedConnectionSubject,
]{
	Sentinel: ErrInvalidInjectKey(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.GraphView,
		s constraint.OriginatedConnectionSubject,
	) []constraint.Objection {
		dictated := v.OriginatedConnectionAuthoredDelivery(
			s.NodeID(), s.OriginKind(), s.OriginAttachment(),
			s.SocketName(), s.AliasName(), s.TargetNodeID())
		if len(dictated) == 0 {
			return nil
		}
		targetType, present := v.FlattenedNodeComponentType(s.TargetNodeID())
		if !present || !v.CatalogComponentPresent(targetType) {
			return nil
		}
		published := v.DeclaredProvidesKeys(targetType)

		var out []constraint.Objection
		for _, payloads := range dictated {
			for mechanism, payload := range payloads.All() {
				for _, entry := range connectionProvidesReferences(payload) {
					if slices.Contains(published, entry.providesKey) {
						continue
					}
					out = append(out, s.Raise(ErrInvalidInjectKey(),
						append([]string{mechanism, entry.deliveryKey, entry.providesKey, targetType},
							published...)...))
				}
			}
		}
		return out
	},
}

// OriginatedConnectionInjectEntryNamesAKey reports each delivery entry on one
// connection that carries `value:`, wherever that connection was written.
//
// A connection delivers into the consumer what the component it points at
// publishes, so each of its entries names a published key with `from:`. A
// literal written there names nothing to read, and a build delivered nothing for
// it, without a word; it is refused instead. A literal a component delivers
// to itself belongs in the component's own injection block, where `value:` is
// admitted, and this rule does not read that block.
//
// It is asked once per connection the composition drew together with the surface
// that drew it — a component's own dependency block, an attachment, or the
// Release — for the reason the rule above is: every connection is in range, and
// each finding names the block its own author wrote. Every payload the coordinate
// names is read, as above.
//
// One finding is raised per offending entry, and its detail is the mechanism and
// the entry's `in:`. An entry this rule cannot read as an entry — one lacking
// `in:`, or not a mapping at all — is the delivery-shape rule's to report.
var OriginatedConnectionInjectEntryNamesAKey = constraint.Check[
	constraint.GraphView,
	constraint.OriginatedConnectionSubject,
]{
	Sentinel: ErrUsesInjectValue(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.GraphView,
		s constraint.OriginatedConnectionSubject,
	) []constraint.Objection {
		dictated := v.OriginatedConnectionAuthoredDelivery(
			s.NodeID(), s.OriginKind(), s.OriginAttachment(),
			s.SocketName(), s.AliasName(), s.TargetNodeID())
		var out []constraint.Objection
		for _, payloads := range dictated {
			for mechanism, payload := range payloads.All() {
				for _, entry := range literalDeliveryEntries(payload) {
					out = append(out, s.Raise(ErrUsesInjectValue(), mechanism, entry.in))
				}
			}
		}
		return out
	},
}

// connectionProvidesReferences is the published-key references one mechanism's
// authored delivery payload carries, in delivery-key order.
//
// It reads the payload through the same decoder the component's own injection
// block is read with, so a `from:` entry is a reference on both surfaces.
//
// The ordering is this reader's own and not the payload's: the diagnostics this
// reading feeds are emitted in delivery-key order.
func connectionProvidesReferences(payload any) []fromEntry {
	entries := decodeFromEntries(payload)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].deliveryKey < entries[j].deliveryKey
	})
	return entries
}
