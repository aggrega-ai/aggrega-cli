// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "strings"

// SetLeafAtPath walks a flat-dotted-leaf path through a nested
// OrderedMap[any] tree and writes the given primitive value at the
// path's terminal segment. Intermediate OrderedMap[any] nodes are
// created on demand for any path segment that does not yet exist on
// the tree.
//
// Path examples:
//
//   - SetLeafAtPath(&tree, "host", v) writes v at the top-level key
//     "host". { "host": v }
//   - SetLeafAtPath(&tree, "ingress.host", v) creates the intermediate
//     "ingress" node (if absent) and writes v at its "host" key.
//     { "ingress": OrderedMap[any]{ "host": v } }
//   - Subsequent calls against the same tree reuse existing
//     intermediates; SetLeafAtPath(&tree, "ingress.port", 443) after
//     the prior call produces { "ingress": { "host": v, "port": 443 } }.
//
// Intermediate shape handling. Nested intermediates may be stored
// value-typed (the shape YAML unmarshalling nested objects produces
// by default) or pointer-typed (the shape SetLeafAtPath itself
// produces on first creation). Both shapes are descended through the
// same switch; value-typed intermediates are upgraded to pointer-
// typed in place — a local copy is taken, its pointer is written
// back into the parent, and the walker descends into the upgraded
// entry so the terminal Set propagates to the parent tree. Without
// the upgrade, a value-typed-intermediate descent would write into an
// orphaned local copy and silently drop the contribution.
//
// Tree-shape collisions (e.g. writing through a path whose
// intermediate node is a scalar value from a prior write) are
// resolved by overwriting the offending intermediate with a fresh
// OrderedMap[any] and continuing the walk. Callers that require
// strict no-collision semantics validate the schema upstream; this
// helper trusts validated input and prefers progress over halting on
// shape mismatch.
//
// SetLeafAtPath is the paired primitive of FlattenLeaves — together
// they form the read / write surface for nested-OrderedMap trees
// keyed by flat-dotted-leaf paths. Callers that record per-leaf
// metadata (provenance, validation results, lint findings) consume
// both: SetLeafAtPath writes the value at the path, FlattenLeaves
// reads back the (path, value) sequence in author order for
// downstream attribution.
func SetLeafAtPath(tree *OrderedMap[any], path string, value any) {
	components := strings.Split(path, ".")
	current := tree
	for i, comp := range components {
		if i == len(components)-1 {
			current.Set(comp, value)
			return
		}
		next, exists := current.Get(comp)
		if !exists {
			child := OrderedMap[any]{}
			current.Set(comp, &child)
			current = &child
			continue
		}
		switch n := next.(type) {
		case *OrderedMap[any]:
			current = n
		case OrderedMap[any]:
			upgraded := n
			current.Set(comp, &upgraded)
			current = &upgraded
		default:
			child := OrderedMap[any]{}
			current.Set(comp, &child)
			current = &child
		}
	}
}
