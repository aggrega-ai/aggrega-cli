// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// socketCarriesAConnection reports whether one socket carries at least one
// connection on the surface a connection count was taken over.
//
// A socket absent from such a count is one that surface named nowhere, and a
// socket present with a count of zero is one it named and left carrying nothing.
// Neither populates the socket, which is why one reader serves both and the rule
// below does not have to tell them apart.
func socketCarriesAConnection(counted types.OrderedMap[int], socket string) bool {
	count, named := counted.Get(socket)
	return named && count > 0
}

// RequiredSocketIsPopulated reports each dependency socket a component type
// declares as one that must carry a connection, and which carries none.
//
// It is exported because the composition it reads is held by another package:
// the rule is invoked from the pass that owns the flattened composition, over
// that pass's own presentation and enumeration, rather than from here.
//
// Three surfaces can populate such a socket and the rule reads all three,
// because a socket any one of them binds is wired in the graph that gets
// rendered. The component's own dependency block and the Release's edge
// surface are what the two authors wrote; the connection set the build resolved
// is what the composition contributed, and it is the only one that answers for a
// component a module contributes, whose own dependency block lives in a document
// loaded when the composition is flattened.
//
// Reading the resolved set is what places this rule where it sits. The two
// authored surfaces are complete as soon as both documents are in view, and a
// rule reading only those answers for components the Aggregate declares
// directly and refuses to judge the rest — which is one rule in two mechanisms,
// each seeing a different half of the input. Reading all three states the rule
// once, at the point the last of its evidence is complete.
//
// The resolved set carries only connections a dependency block drew. A
// connection an attachment contributes is deliberately absent from it, so an
// attachment cannot satisfy a component's own socket — which is the same
// division the authored surfaces already draw, since an attachment's sockets are
// declared on the Feature it attaches rather than on the consuming component.
//
// A component type the catalog holds no entry for ends the rule, because there
// is no declaration to read the requirement from. What that absence means is a
// separate rule's question.
//
// The detail is the socket and, captioned, the component type that declared it
// required. The registered text names the socket, so that value stays bare; the
// component type is the other thing an author has to look at and the sentence
// does not name it.
var RequiredSocketIsPopulated = constraint.Check[
	constraint.TopologyView,
	constraint.NodeSocketSubject,
]{
	Sentinel: ErrMissingRequiredSocket(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.NodeSocketSubject,
	) []constraint.Objection {
		componentType, present := v.FlattenedNodeComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		if !v.SocketRequired(componentType, s.SocketName()) {
			return nil
		}
		if socketCarriesAConnection(v.AuthoredUsesSocketConnections(s.NodeID()), s.SocketName()) {
			return nil
		}
		if socketCarriesAConnection(v.ReleaseAuthoredUsesSocketConnections(s.NodeID()), s.SocketName()) {
			return nil
		}
		if socketCarriesAConnection(v.ResolvedUsesSocketConnections(s.NodeID()), s.SocketName()) {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWith(ErrMissingRequiredSocket(),
				constraint.Uncaptioned(s.SocketName()),
				constraint.Captioned("declared required by component type", componentType)),
		}
	},
}
