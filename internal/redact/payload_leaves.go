// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package redact

import "github.com/aggrega-ai/aggrega-cli/pkg/types"

// DeclaredLeaves selects the leaves one values/infra payload pair holds whose
// path an entity's schema declares sensitive, returning each as its
// pillar-prefixed coordinate: values.<path> or infra.<path>.
//
// It is the ONE home for the per-leaf marking. The graph builder asks it for the
// four places a graph carries pillar data — a merged attachment's own values
// and infra, a node's values and infra against its Component's declarations,
// a boundary attachment's infra against its Feature's, and a connection's values
// and infra against the per-edge contract its socket declares — and the
// label-transform input asks it for each node's declared values before handing
// them to a guest. Each caller supplies the payloads and the declarations; the
// rule joining them lives only here, so a surface added later cannot come to
// mark on a different reading from the surfaces already marked.
//
// covers is the declarations' own question, "does this entity's declarations
// reach this coordinate" — the entity's declared-sensitive-leaf set answers it —
// and is the same question every withholding surface asks of the same
// declarations. It is taken as a predicate rather than as that set because the
// package declaring the set already depends on this one. A declaration COVERS a
// leaf when it names that leaf or names an object containing it, so a leaf
// beneath an object an author declared sensitive is named by that declaration
// and marking it claims nothing the author did not write. Matching is
// segment-wise, so a declaration at "db" reaches "db.password" and not a sibling
// "dbPassword".
//
// The direction the covering rule is NOT allowed to slip into is worth naming
// where the join is made. Marking may not claim more than an author declared,
// because a marking becomes a refusal or a concealment downstream and there is
// no cheap undo for calling public data secret. Covering a subtree an author
// declared is not that: the over-claim would be marking a leaf no declaration
// reaches, which this join cannot do because every input is a declaration.
// Nothing reads a path's own text and no value is inspected — whether the author
// wrote the material itself at a marked leaf or a pointer to it is not knowable
// here and is not what the marking claims.
//
// Each payload is FLATTENED rather than read at its top level, which is
// load-bearing. A pillar is a tree: a nested leaf lives inside a nested map, so
// the pillar's own top-level keys name only the shallowest leaves. Walking those
// keys alone would name a top-level leaf and silently miss every nested one, and
// silence is the wrong direction to fail in for this axis.
//
// The two pillars are collected in declaration order, values before infra, and
// each pillar's leaves arrive in the order the payload holds them. That is the
// payload's own order throughout — the payloads are order-preserving
// containers, so no sort is needed and none is applied. Sorting would replace
// an order a consumer can act on with one it cannot.
//
// Returns nil when no leaf either payload carries is covered. That is the
// honest answer rather than an empty list: there is no marked leaf to name. A
// caller with only one pillar to offer passes the zero value for the other,
// which names nothing.
func DeclaredLeaves(values, infra types.OrderedMap[any], covers func(coordinate string) bool) []string {
	var out []string
	collect := func(pillar types.OrderedMap[any], root string) {
		if pillar.Len() == 0 {
			return
		}
		for _, leaf := range types.FlattenLeaves(pillar) {
			coordinate := root + leaf.Path
			if covers(coordinate) {
				out = append(out, coordinate)
			}
		}
	}
	collect(values, types.PillarRootValues)
	collect(infra, types.PillarRootInfra)
	return out
}

// WithholdDeclaredValues returns a copy of a node's values with every leaf its
// Component declares sensitive replaced by the withheld marker carrying the
// declared reason — the same leaves, chosen by DeclaredLeaves over the same
// declarations, and the same marker, left by the same descent, that Graph leaves
// in a graph's values mapping for the governance socket's verdict over the graph.
// So a governance plugin reads a declared-sensitive value as withheld at either
// role that hands it the graph's values, and a sibling leaf no declaration
// reaches arrives verbatim.
//
// covers is the Component's declarations, asked as DeclaredLeaves asks them. The
// input is never mutated: the values this reads are the topology node's own,
// which the graph builder goes on to read, so an in-place substitution here would
// reach every artifact rendered from them. When nothing is covered the input is
// returned as-is, so values declaring no sensitivity cross unchanged.
func WithholdDeclaredValues(values types.OrderedMap[any], covers func(coordinate string) bool) types.OrderedMap[any] {
	if values.Len() == 0 {
		return values
	}
	marked := pillarPaths(DeclaredLeaves(values, types.OrderedMap[any]{}, covers), valuesPillar)
	return PillarStructured(values, marked, WithheldDeclared)
}
