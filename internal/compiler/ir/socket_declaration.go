// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// socketDeclaration answers, for one edge, whether the socket it is wired into
// was declared by the catalog entity that owns it, and returns the
// [types.IREdge.SocketDeclaration] value for it.
//
// It is the ONE home for that question on the build path. Every edge-construction
// path — Aggregate-authored, Feature-authored, Release-wired, and flattened
// sub-aggregate — converges on a single IREdge literal, and that literal calls
// this function. A later path that computes the answer for itself would agree on
// the ordinary case and diverge on the awkward one, and a partially-WRONG marker
// reads as authoritative while a partially-absent one at least looks broken. So
// the derivation is a chokepoint: a new path can forget to call it, but it cannot
// answer differently.
//
// Which entity owns the socket depends on who authored the edge, and the two
// surfaces are not interchangeable: a Feature-authored edge's socket is declared
// on the originating Feature's usesSchema, not on the consuming Component's.
// Consulting the Component alone would mark every Feature-authored edge ad-hoc.
//
// A component or feature type absent from the catalog view yields ad-hoc, which is
// the literal truth of the question asked: no catalog entry declares the socket.
// The build's other guards own the separate question of whether that absence is
// itself a defect.
func socketDeclaration(lookup resolver.CatalogLookup, componentType, originFeature, socket string) string {
	if _, _, declared := socketEntry(lookup, componentType, originFeature, socket); declared {
		return types.SocketDeclarationDeclared
	}
	return types.SocketDeclarationAdHoc
}

// socketEntry resolves the usesSchema entry a socket was declared under, on the
// entity that OWNS the socket, and reports whether such an entry exists. It also
// returns the owning entity's type URI, so a diagnostic can name the entity whose
// declaration it is quoting rather than the entity the edge happens to hang off.
//
// It is the one resolution of "which entity declares this socket, and what does
// that declaration say", and [socketDeclaration] plus the socket's delivery
// allowlist check both read it. Keeping them on one lookup is what makes the
// declaration marker and the allowlist verdict incapable of disagreeing about
// which entity they consulted — a second lookup would agree on the ordinary case
// and diverge exactly on the Feature-authored edge, which is the case the marker
// exists to get right.
//
// The declaring surfaces are not interchangeable: a Feature-authored edge's socket
// is declared on the originating Feature's usesSchema, never on the consuming
// Component's. The entry returned is the resolved one, so a socket a Component
// inherits through `extends` is found here exactly as one it authored itself —
// inheritance is merged before the catalog view is built.
func socketEntry(lookup resolver.CatalogLookup, componentType, originFeature, socket string) (entry types.UsesSchemaEntry, owner string, declared bool) {
	if originFeature != "" {
		if lookup.Feature == nil {
			return types.UsesSchemaEntry{}, originFeature, false
		}
		catFeat, found := lookup.Feature(originFeature)
		if !found {
			return types.UsesSchemaEntry{}, originFeature, false
		}
		e, ok := catFeat.Spec.UsesSchema.Get(socket)
		return e, originFeature, ok
	}

	if lookup.Component == nil {
		return types.UsesSchemaEntry{}, componentType, false
	}
	catComp, found := lookup.Component(componentType)
	if !found {
		return types.UsesSchemaEntry{}, componentType, false
	}
	e, ok := catComp.Spec.UsesSchema.Get(socket)
	return e, componentType, ok
}
