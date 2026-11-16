// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import "sort"

// WavePredecessors returns, for each unit i (waves[i] is its wave), the indexes
// of the units of the immediately preceding populated wave, in the order the
// caller listed them; it returns nil when fewer than two waves are populated.
//
// A unit names every unit of the one wave just before its own, never every
// earlier wave: a target resolves the rest transitively through the middle wave,
// so a three-wave group chains rather than fanning out and each list stays
// proportional to one wave. Waves need be neither contiguous nor non-negative —
// the populated waves are sorted and each chains to the one before it, so -5, 0
// and 7 chain exactly as 0, 1 and 2 do. A unit of the lowest wave has no
// predecessors, and its entry is nil.
//
// Order is the caller's: the indexes of one wave come out in ascending index
// order, so a caller that lists its units by name, or by contributor, gets
// predecessors in that same order. No map is iterated for output; the wave keys
// are sorted before anything is recorded. The caller keeps its own filter of which
// units take part and its own write of the result.
func WavePredecessors(waves []int) [][]int {
	indexesByWave := map[int][]int{}
	for i, w := range waves {
		indexesByWave[w] = append(indexesByWave[w], i)
	}
	if len(indexesByWave) < 2 {
		return nil
	}

	populated := make([]int, 0, len(indexesByWave))
	for w := range indexesByWave {
		populated = append(populated, w)
	}
	sort.Ints(populated)

	predecessors := make([][]int, len(waves))
	for i := 1; i < len(populated); i++ {
		before := indexesByWave[populated[i-1]]
		for _, k := range indexesByWave[populated[i]] {
			predecessors[k] = before
		}
	}
	return predecessors
}
