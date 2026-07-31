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
	if originFeature != "" {
		if lookup.Feature == nil {
			return types.SocketDeclarationAdHoc
		}
		catFeat, found := lookup.Feature(originFeature)
		if !found {
			return types.SocketDeclarationAdHoc
		}
		if _, ok := catFeat.Spec.UsesSchema.Get(socket); ok {
			return types.SocketDeclarationDeclared
		}
		return types.SocketDeclarationAdHoc
	}

	if lookup.Component == nil {
		return types.SocketDeclarationAdHoc
	}
	catComp, found := lookup.Component(componentType)
	if !found {
		return types.SocketDeclarationAdHoc
	}
	if _, ok := catComp.Spec.UsesSchema.Get(socket); ok {
		return types.SocketDeclarationDeclared
	}
	return types.SocketDeclarationAdHoc
}
