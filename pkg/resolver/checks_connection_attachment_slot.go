// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The declared-slots rule for a connection's attachments, expressed as constraint
// declarations. See checks.go for what a declaration here is.
//
// A connection's attachment surface is CLOSED, which is the difference between it
// and a component's own attachment block. The socket a connection is wired through
// declares, on the consuming Component, every slot a connection on it may fill, and
// an attachment under any other name is refused. A socket declaring no slot admits
// no attachment on its connections, and a socket an Aggregate names without the
// catalog declaring it declares none — so any attachment on one of its connections
// is refused too.
//
// One rule is declared four times because it is asked of two documents at two
// points. The developer writes attachments on the Aggregate's connection entries
// and the operator on the Release's, and a finding has to name the document its
// author wrote. And the rule reads the consuming Component's socket declaration,
// so it can be asked as soon as that component's type is known: while the two
// authoring documents are read for a component the Aggregate declares, and only
// once the composition is flattened for a component a sub-module contributes,
// whose document is read then. The two stages split the components between them
// rather than overlapping: the later one skips every component the earlier one was
// already asked about, so one fault is reported once.
//
// The comparison itself is shared, so the four cannot disagree about what a
// declared slot is; what differs is which document's names they read and which
// stage supplies the component's type.

// connectionAttachmentSlotObjections is the comparison at the heart of the rule,
// asked of every name one connection carries an attachment under: each name has to
// be one of the slots the socket declares.
//
// It takes the names rather than the view they were read from, for the reason
// attachmentSocketObjection does: a rule's point in the compilation is computed
// from the facts its own body reads, so each declaration below reads its facts and
// passes the values here.
//
// The detail is the attachment's name, then the connection's alias, the socket and
// the component it was written on, and then the slots the socket declares — the
// alternatives an author picks from. The remedy depends on why no slot matched:
// a socket declaring slots asks for one of them, a declared socket declaring none
// admits no attachment, and a socket the component's type does not declare at all
// declares none either. So it travels as advice, per occurrence.
func connectionAttachmentSlotObjections(
	s constraint.Subject,
	component, socket, alias string,
	names, slots []string,
	socketDeclared bool,
) []constraint.Objection {
	var out []constraint.Objection
	for _, name := range names {
		if slices.Contains(slots, name) {
			continue
		}
		detail := []constraint.DetailValue{
			constraint.Uncaptioned(name),
			constraint.Captioned("on the connection", alias),
			constraint.Captioned("through socket", socket),
			constraint.Captioned("of component", component),
		}
		var advice string
		switch {
		case len(slots) > 0:
			detail = append(detail, constraint.Captioned("the socket declaring the slots", strings.Join(slots, ", ")))
			advice = "Write the attachment under one of the slots the socket declares, or declare this slot in " +
				"the socket's featuresSchema on the Component."
		case socketDeclared:
			advice = "The socket declares no slots, so no connection wired through it carries an attachment: " +
				"remove the attachment, or declare the slot in the socket's featuresSchema on the Component."
		default:
			advice = "The component's type does not declare this socket, so the socket declares no slots and no " +
				"connection wired through it carries an attachment: remove the attachment, or declare the socket " +
				"and its slot in the Component's usesSchema."
		}
		out = append(out, s.RaiseWithAdvice(ErrConnectionAttachmentSlotUndeclared(), advice, detail...))
	}
	return out
}

// connectionAttachmentFillsADeclaredSlot reports each attachment a connection of
// a component's own dependency block carries under a name the socket declares no
// slot for, for a component the Aggregate declares.
//
// It is invoked once per connection. A connection only the Release wrote carries
// no developer attachment and ends the rule at once; the Release's own names are
// its twin's to judge.
//
// A component whose type the catalog holds no entry for ends the rule: its socket
// declaration cannot be read, and what that absence means is a separate rule's
// question.
var connectionAttachmentFillsADeclaredSlot = constraint.Check[
	constraint.NormalizedView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrConnectionAttachmentSlotUndeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.EdgeSubject,
	) []constraint.Objection {
		names := v.PerEdgeAttachmentNames(s.NodeID(), s.SocketName(), s.AliasName())
		if len(names) == 0 {
			return nil
		}
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		return connectionAttachmentSlotObjections(s, s.NodeID(), s.SocketName(), s.AliasName(), names,
			v.SocketDeclaredSlotNames(componentType, s.SocketName()),
			slices.Contains(v.DeclaredSocketNames(componentType), s.SocketName()))
	},
}

// releaseConnectionAttachmentFillsADeclaredSlot reports each attachment a Release
// per-edge entry carries under a name the socket declares no slot for, for a
// component the Aggregate declares.
//
// A socket the component's type does not declare ends the rule: the Release may
// address only declared sockets, and the rule over the Release's edge surface
// already refuses that entry with the socket names that do exist, which is the
// better diagnostic for it.
var releaseConnectionAttachmentFillsADeclaredSlot = constraint.Check[
	constraint.NormalizedView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrConnectionAttachmentSlotUndeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.EdgeSubject,
	) []constraint.Objection {
		names := v.ReleasePerEdgeAttachmentNames(s.NodeID(), s.SocketName(), s.AliasName())
		if len(names) == 0 {
			return nil
		}
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		if !slices.Contains(v.DeclaredSocketNames(componentType), s.SocketName()) {
			return nil
		}
		return connectionAttachmentSlotObjections(s, s.NodeID(), s.SocketName(), s.AliasName(), names,
			v.SocketDeclaredSlotNames(componentType, s.SocketName()), true)
	},
}

// ModuleConnectionAttachmentFillsADeclaredSlot reports each attachment a
// connection of a component's own dependency block carries under a name the socket
// declares no slot for, for a component a sub-module contributes.
//
// It is the later half of the rule, asked once the composition is flattened,
// because that is when a sub-module's document is read and the type of a component
// it declares becomes known. A component the Aggregate declares ends the rule at
// once: the earlier half was asked about it while the authoring documents were
// read, and asking again here would report one fault twice.
//
// It is invoked once per node and walks that node's connections itself, because
// the flattened composition enumerates no connections of its own.
var ModuleConnectionAttachmentFillsADeclaredSlot = constraint.Check[
	constraint.TopologyView,
	constraint.NodeSubject,
]{
	Sentinel: ErrConnectionAttachmentSlotUndeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.NodeSubject,
	) []constraint.Objection {
		if _, declared := v.InstanceComponentType(s.NodeID()); declared {
			return nil
		}
		connections := v.FlattenedConnectionAttachmentNames(s.NodeID())
		if connections.Len() == 0 {
			return nil
		}
		componentType, held := v.FlattenedNodeComponentType(s.NodeID())
		if !held || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		declaredSockets := v.DeclaredSocketNames(componentType)
		var out []constraint.Objection
		for socket, aliases := range connections.All() {
			slots := v.SocketDeclaredSlotNames(componentType, socket)
			for alias, names := range aliases.All() {
				out = append(out, connectionAttachmentSlotObjections(s, s.NodeID(), socket, alias, names,
					slots, slices.Contains(declaredSockets, socket))...)
			}
		}
		return out
	},
}

// ModuleReleaseConnectionAttachmentFillsADeclaredSlot reports each attachment a
// Release per-edge entry carries under a name the socket declares no slot for, for
// a component a sub-module contributes.
//
// It is the Release-side twin of the later half and is skipped for the same
// components, for the same reason. A socket the component's type does not declare
// ends it, for the reason its earlier twin gives: the entry is already refused for
// naming that socket at all.
var ModuleReleaseConnectionAttachmentFillsADeclaredSlot = constraint.Check[
	constraint.TopologyView,
	constraint.ReleaseEdgeConfigurationSubject,
]{
	Sentinel: ErrConnectionAttachmentSlotUndeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.ReleaseEdgeConfigurationSubject,
	) []constraint.Objection {
		node, socket, alias := s.NodeID(), s.SocketName(), s.AliasName()
		names := v.ReleasePerEdgeAttachmentNames(node, socket, alias)
		if len(names) == 0 {
			return nil
		}
		if _, declared := v.InstanceComponentType(node); declared {
			return nil
		}
		componentType, held := v.FlattenedNodeComponentType(node)
		if !held || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		if !slices.Contains(v.DeclaredSocketNames(componentType), socket) {
			return nil
		}
		return connectionAttachmentSlotObjections(s, node, socket, alias, names,
			v.SocketDeclaredSlotNames(componentType, socket), true)
	},
}
