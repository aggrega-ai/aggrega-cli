// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import "sort"

// nearMissMaxDistance is the largest edit distance at which two authored names
// are treated as a probable typo of one another. One edit catches the
// realistic slip — a transposition, a dropped or doubled letter, a wrong
// character — while staying far enough below the length of a typical name
// that unrelated names (`api_key` vs `api_url`) are not paired up. Raising it
// trades a few more caught typos for a stream of confusing false pairs.
const nearMissMaxDistance = 1

// nearMissMinNameLength is the shortest authored name a near-miss lint will
// pair. At two or three characters almost every name is within one edit of
// every other, so the signal is noise.
const nearMissMinNameLength = 4

// NearMissCandidates returns the declared names within nearMissMaxDistance
// edits of name, in sort.Strings order. An exact match is never a candidate —
// the caller has already excluded declared names.
//
// The candidate set is whatever the calling lint declares as its own
// vocabulary: a Component's attachment slots, a Component's `usesSchema`
// sockets, the capabilities the provider-rendering plugins in reach serve, or
// any other declared-name list. The primitive holds no opinion about which.
//
// It is exported so that every surface pairing an authored name with the one it
// probably meant pairs them by one rule — the same distance, the same length
// floor, the same treatment of a transposition — rather than each reporting a
// typo by a threshold of its own.
func NearMissCandidates(name string, candidates []string) []string {
	if len([]rune(name)) < nearMissMinNameLength {
		return nil
	}
	var hits []string
	for _, candidate := range candidates {
		if candidate == name || len([]rune(candidate)) < nearMissMinNameLength {
			continue
		}
		if editDistanceWithin(name, candidate, nearMissMaxDistance) {
			hits = append(hits, candidate)
		}
	}
	sort.Strings(hits)
	return hits
}

// editDistanceWithin reports whether a and b are within max edits of one
// another, counting an insertion, a deletion, a substitution, or a
// transposition of two adjacent characters as one edit each
// (Damerau-Levenshtein, optimal string alignment).
//
// Transposition counts as ONE edit deliberately. It is the commonest typo — a
// pair of characters swapped while typing — yet plain Levenshtein scores it as
// two substitutions, so a distance-1 bound would miss exactly the mistake a
// near-miss lint exists to catch while still catching the rarer
// single-character slips.
//
// It answers the bounded question rather than computing the full distance: a
// length difference greater than max is an immediate no, and a row whose every
// cell already exceeds max cannot recover, so the walk stops there.
func editDistanceWithin(a, b string, max int) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra)-len(rb) > max || len(rb)-len(ra) > max {
		return false
	}

	// Three rolling rows: the transposition term reads two rows back.
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			best := min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				best = min(best, prev2[j-2]+1)
			}
			curr[j] = best
			if best < rowMin {
				rowMin = best
			}
		}
		if rowMin > max {
			return false
		}
		prev2, prev, curr = prev, curr, prev2
	}
	return prev[len(rb)] <= max
}
