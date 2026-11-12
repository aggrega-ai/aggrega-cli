// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

// FlatLeaf pairs a flat-dotted-leaf path with the primitive value
// stored at that path inside a nested OrderedMap[any] tree.
// FlattenLeaves emits these in author order through the tree.
type FlatLeaf struct {
	Path  string
	Value any
}

// FlattenLeaves walks a nested OrderedMap[any] tree and emits one
// FlatLeaf per primitive leaf, joining segments with "." at every
// nesting level. Author order through OrderedMap.All() recursion
// preserves deterministic ordering across runs of identical input.
//
// Nested intermediates stored as *OrderedMap[any] (the shape leaf
// writers like setLeafAtPath produce) and OrderedMap[any] (the shape
// YAML-unmarshalled nested objects may produce) both descend through
// the same recursive walk. Empty OrderedMaps at any level yield a
// single FlatLeaf at the current accumulated path so consumers can
// still attribute the empty container.
//
// Non-OrderedMap values at any level — scalars, slices, plain maps
// without OrderedMap typing — yield a single FlatLeaf at the current
// path; the consumer treats the value as a primitive leaf.
//
// Callers requiring parallel sequences (e.g., the IR builder
// recording per-leaf provenance and the inspect renderer reading it
// back) consume this single helper so the two sequences align by
// construction. Divergence between near-identical walkers silently
// breaks provenance lookup discipline; the shared helper consolidates
// the invariant in one place.
func FlattenLeaves(om OrderedMap[any]) []FlatLeaf {
	var out []FlatLeaf
	flattenLeavesWalk("", om, &out)
	return out
}

func flattenLeavesWalk(prefix string, om OrderedMap[any], out *[]FlatLeaf) {
	for k, v := range om.All() {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		switch n := v.(type) {
		case *OrderedMap[any]:
			if n == nil || n.Len() == 0 {
				*out = append(*out, FlatLeaf{Path: path, Value: v})
				continue
			}
			flattenLeavesWalk(path, *n, out)
		case OrderedMap[any]:
			if n.Len() == 0 {
				*out = append(*out, FlatLeaf{Path: path, Value: v})
				continue
			}
			flattenLeavesWalk(path, n, out)
		default:
			*out = append(*out, FlatLeaf{Path: path, Value: v})
		}
	}
}
