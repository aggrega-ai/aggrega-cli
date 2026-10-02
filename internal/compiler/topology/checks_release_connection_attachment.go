// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"fmt"
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Three rules over what the Release writes about a component's connection
// attachments, all asked once the composition is flattened. Each reads the
// attachments the developer put on a component's connections, and a component a
// sub-module contributes keeps its connections in that module's document, which
// is read only when the composition is flattened — so this is the earliest point
// holding any of their evidence for every component alike.

// One surface per concept is the rule the first two enforce, at the two scopes a
// Release writes attachments on. A connection's attachment is written on its
// connection entry — the developer's on the Aggregate's connection, the
// operator's on the Release's entry for it — and lowered onto the consuming
// component under a composed name: the connection's alias, a dot, and the slot
// the attachment fills. A Release component entry's own `features` block admits a
// dot in its keys, and so does a boundary's, whose attachments reach every
// component beneath it; so the composed name can be written at either scope as
// well, and the two would then fold into one attachment through the merge every
// same-named contribution goes through, with neither document showing the whole
// of it. A socket's slot is filled per connection, never by a scope cascading
// onto it. Each rule halts rather than choosing one, and names both spellings.
//
// The two are one rule asked at two scopes, and they share their whole body:
// connectionAttachmentsWrittenElsewhere decides which of a component's connection
// attachments the Release also wrote under their composed name, and each
// declaration supplies only what the Release wrote at its scope and where the
// finding sits.

// connectionAttachmentSpelling is one attachment a component's connection
// carries: the socket and alias the connection is written under, the slot the
// attachment fills, and the composed name it takes on the consuming component.
type connectionAttachmentSpelling struct {
	socket, alias, slot, name string
}

// connectionAttachmentsWrittenElsewhere returns each attachment one node's
// connections carry whose composed name written reports the Release also wrote
// at another scope — the developer's connections first, then the operator's,
// each in author order, and one composed name once whichever documents put an
// attachment under it.
//
// The composed name is built from the connection's alias and slot, which the
// facts carry as data; nothing is read out of a key the Release wrote. A
// connection's attachments are read from both documents: the developer's off the
// flattened node, so a component a sub-module contributes answers exactly as one
// the Aggregate declares, and the operator's off the Release's own entries for
// the component.
func connectionAttachmentsWrittenElsewhere(
	v constraint.TopologyView,
	node string,
	written func(name string) bool,
) []connectionAttachmentSpelling {
	var out []connectionAttachmentSpelling
	var reported []string
	consider := func(socket, alias, slot string) {
		name := types.JoinConnectionAttachmentName(alias, slot)
		if slices.Contains(reported, name) {
			return
		}
		if !written(name) {
			return
		}
		reported = append(reported, name)
		out = append(out, connectionAttachmentSpelling{socket: socket, alias: alias, slot: slot, name: name})
	}
	developer := v.FlattenedConnectionAttachmentNames(node)
	for socket, aliases := range developer.All() {
		for alias, slots := range aliases.All() {
			for _, slot := range slots {
				consider(socket, alias, slot)
			}
		}
	}
	operator := v.ReleaseAuthoredUsesSocketConnections(node)
	for _, socket := range operator.Keys() {
		for _, alias := range v.ReleaseAuthoredUsesSocketEntryAliases(node, socket) {
			for _, slot := range v.ReleasePerEdgeAttachmentNames(node, socket, alias) {
				consider(socket, alias, slot)
			}
		}
	}
	return out
}

// connectionAttachmentDetail is what a finding about one spelling names after
// the key the Release wrote: the component, the connection, the socket and the
// slot. Both scopes name them alike, so two findings about one connection read
// alike whichever scope the key was written at.
func connectionAttachmentDetail(node string, c connectionAttachmentSpelling) []constraint.DetailValue {
	return []constraint.DetailValue{
		constraint.Uncaptioned(c.name),
		constraint.Captioned("of component", node),
		constraint.Captioned("on the connection", c.alias),
		constraint.Captioned("through socket", c.socket),
		constraint.Captioned("in the slot", c.slot),
	}
}

// connectionAttachmentIsWrittenOnItsConnection reports each attachment a Release
// component entry's own `features` block carries under the name an attachment on
// one of that component's connections takes.
//
// An entry addressing a component the composition does not hold passes, because
// the rule over what a boundary names reports exactly that, and a component a
// false activation toggle removed takes its connections with it.
//
// The finding sits at the component entry. The key at fault carries the locator
// dialect's separator, which no locator can address, so the entry is the deepest
// position the dialect can name and the key itself is the value the rule raises.
var connectionAttachmentIsWrittenOnItsConnection = constraint.Check[
	constraint.TopologyView,
	constraint.BoundaryComponentReferenceSubject,
]{
	Sentinel: ErrConnectionAttachmentWrittenOffItsConnection(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.BoundaryComponentReferenceSubject,
	) []constraint.Objection {
		node := s.ComponentReference()
		if _, held := v.FlattenedNodeComponentType(node); !held {
			return nil
		}
		spelled := connectionAttachmentsWrittenElsewhere(v, node, func(name string) bool {
			_, written := v.ReleaseAttachmentType(node, name)
			return written
		})
		var out []constraint.Objection
		for _, c := range spelled {
			out = append(out, s.RaiseWithAdvice(
				ErrConnectionAttachmentWrittenOffItsConnection(),
				fmt.Sprintf("Write it on the connection entry, under uses.%s.%s.features.%s, and remove %q "+
					"from this entry's features: an attachment a connection carries is written on that "+
					"connection and nowhere else.", c.socket, c.alias, c.slot, c.name),
				connectionAttachmentDetail(node, c)...,
			))
		}
		return out
	},
}

// boundaryAttachmentIsNotAConnectionAttachment reports each attachment a Release
// boundary declares on itself under the name an attachment on one of the
// connections of a component the boundary reaches takes.
//
// A boundary-wide attachment reaches every component placed in the boundary or
// in a boundary nested inside it, a component a routed sub-module contributes
// included, so the rule is asked of every component the attachment reaches. A
// key no connection attachment of any of them takes is an ordinary attachment
// name, and the rule passes it.
//
// One finding is raised per component whose connection carries the name, because
// the fix is made on each of those connections: one key written once can collide
// with the connections of several components, and merging those findings would
// say that something is wrong without saying which connections it reaches. The
// findings share a position because they share an authored key; the component
// they name is what tells them apart.
//
// The finding sits at the boundary's own `features` block. The key carries the
// locator dialect's separator, so the block is the deepest position the dialect
// can name and the key itself is the value the rule raises.
var boundaryAttachmentIsNotAConnectionAttachment = constraint.Check[
	constraint.TopologyView,
	constraint.BoundaryAttachmentSubject,
]{
	Sentinel: ErrConnectionAttachmentWrittenOffItsConnection(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.BoundaryAttachmentSubject,
	) []constraint.Objection {
		key := s.AttachmentName()
		var out []constraint.Objection
		for _, node := range v.BoundaryAttachmentReachedNodes(s.BoundaryPath(), key) {
			spelled := connectionAttachmentsWrittenElsewhere(v, node, func(name string) bool {
				return name == key
			})
			for _, c := range spelled {
				out = append(out, s.RaiseWithAdvice(
					ErrConnectionAttachmentWrittenOffItsConnection(),
					fmt.Sprintf("Write it on component %q's connection entry, under uses.%s.%s.features.%s, and "+
						"remove %q from this boundary's features: an attachment a connection carries is written "+
						"on that connection and nowhere else.", node, c.socket, c.alias, c.slot, c.name),
					connectionAttachmentDetail(node, c)...,
				))
			}
		}
		return out
	},
}

// releaseConnectionAttachmentNamesItsType reports each attachment a Release
// per-edge entry carries under a slot the developer declares no attachment for on
// that connection, and names no Feature type for.
//
// An attachment the Release writes on a connection is one of two things. It
// sizes an attachment the developer declared on the same connection, and then it
// names no type because the developer's declaration names it; or it is an
// attachment only the Release makes, and then the entry is the only place its
// type can come from. The second without a type attaches nothing a realization
// can be chosen for.
//
// The developer's attachments are read off the flattened node, so a component a
// sub-module contributes is judged exactly as one the Aggregate declares. An
// entry on a component the composition does not hold, or on one a false
// activation toggle removed, passes: the rules over what the entry reaches own
// both, and the second is inert by the operator's own choice.
//
// The finding sits at the connection entry and leads with the slot, the shape
// the rule over a connection attachment's slot names takes, so the two findings
// about one entry read alike.
var releaseConnectionAttachmentNamesItsType = constraint.Check[
	constraint.TopologyView,
	constraint.ReleaseEdgeConfigurationSubject,
]{
	Sentinel: ErrReleaseConnectionAttachmentUntyped(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.ReleaseEdgeConfigurationSubject,
	) []constraint.Objection {
		node, socket, alias := s.NodeID(), s.SocketName(), s.AliasName()
		slots := v.ReleasePerEdgeAttachmentNames(node, socket, alias)
		if len(slots) == 0 {
			return nil
		}
		if _, held := v.FlattenedNodeComponentType(node); !held || v.ComponentDeactivated(node) {
			return nil
		}
		var developer []string
		connections := v.FlattenedConnectionAttachmentNames(node)
		if aliases, ok := connections.Get(socket); ok {
			developer, _ = aliases.Get(alias)
		}
		var out []constraint.Objection
		for _, slot := range slots {
			if slices.Contains(developer, slot) {
				continue
			}
			if featureType, _ := v.ReleasePerEdgeAttachmentType(node, socket, alias, slot); featureType != "" {
				continue
			}
			out = append(out, s.RaiseWithAdvice(
				ErrReleaseConnectionAttachmentUntyped(),
				"Name the Feature this attachment attaches under its type, or declare the attachment on "+
					"the Aggregate's connection entry and size it here.",
				constraint.Uncaptioned(slot),
				constraint.Captioned("on the connection", alias),
				constraint.Captioned("through socket", socket),
				constraint.Captioned("of component", node),
			))
		}
		return out
	},
}

// guardReleaseConnectionAttachments asks the three rules above: the spelling rule
// of every component entry the Release's boundaries declare and then of every
// attachment a boundary declares on itself, then the type rule of every entry the
// Release's per-edge surface writes.
//
// They are asked in that order because a key spelling a connection attachment
// at either scope is the earlier fault in a document carrying both — the author
// fixing it moves the attachment onto the connection entry, which is where the
// type rule then reads it. The component entries are asked before the boundaries
// so that one document's findings report the narrower scope first, in the order
// both are reported whenever both are present.
//
// The findings name the Release, which is the document whose author wrote what
// every rule objects to. A build holding no Release asks nothing: it declares no
// boundary, no component entry and no per-edge entry.
func guardReleaseConnectionAttachments(
	cancelled constraint.Cancellation,
	cfg BuildConfig,
	view *topologyView,
) ([]*resolver.ResolverError, error) {
	if view == nil || cfg.ResolveResult == nil || cfg.ResolveResult.Release == nil {
		return nil, nil
	}
	entity := releaseEntityName(cfg.ResolveResult.Release)
	spelled, err := constraint.RunOnBoundaryComponentReferences(
		cancelled, view, constraint.TopologyView(view), connectionAttachmentIsWrittenOnItsConnection)
	if err != nil {
		return nil, err
	}
	spelledWide, err := constraint.RunOnBoundaryAttachments(
		cancelled, view, view, constraint.TopologyView(view), boundaryAttachmentIsNotAConnectionAttachment)
	if err != nil {
		return nil, err
	}
	spelled = append(spelled, spelledWide...)
	if len(spelled) > 0 {
		return resolver.FindingsFromObjections(spelled, entity), nil
	}
	untyped, err := constraint.RunOnReleaseEdgeConfigurations(
		cancelled, view, constraint.TopologyView(view), releaseConnectionAttachmentNamesItsType)
	if err != nil {
		return nil, err
	}
	return resolver.FindingsFromObjections(untyped, entity), nil
}
