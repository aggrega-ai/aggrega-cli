// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

// deepMergeYAML recursively merges child into base. Maps recurse; arrays and scalars
// in child replace base values. Nil maps are treated as empty. Returns a new map.
//
// Both parameters are plain map[string]any by contract, and the recursion depends
// on it: the assertions below recognise only map[string]any, so a nested
// *types.OrderedMap[any] would fall to the replace branch and discard the base
// subtree rather than merging it — silently, with no error. The values are the
// catalog-entry metadata a caller supplies, so nothing in this package decides
// their shape. Populating that metadata from an author-order-preserving decode
// needs an explicit *types.OrderedMap[any] arm here first.
func deepMergeYAML(base, child map[string]any) map[string]any {
	if base == nil && child == nil {
		return map[string]any{}
	}
	if base == nil {
		return copyMap(child)
	}
	if child == nil {
		return copyMap(base)
	}

	result := copyMap(base)
	for k, cv := range child {
		bv, exists := result[k]
		if !exists {
			result[k] = cv
			continue
		}

		bMap, bIsMap := bv.(map[string]any)
		cMap, cIsMap := cv.(map[string]any)
		if bIsMap && cIsMap {
			result[k] = deepMergeYAML(bMap, cMap)
		} else {
			result[k] = cv
		}
	}
	return result
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
