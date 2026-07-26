// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nearMissMaxDistance is the largest edit distance at which two attachment
// names are treated as a probable typo of one another. One edit catches the
// realistic slip — a transposition, a dropped or doubled letter, a wrong
// character — while staying far enough below the length of a typical slot name
// that unrelated names (`api_key` vs `api_url`) are not paired up. Raising it
// trades a few more caught typos for a stream of confusing false pairs.
const nearMissMaxDistance = 1

// nearMissMinNameLength is the shortest attachment name the lint will pair.
// At two or three characters almost every name is within one edit of every
// other, so the signal is noise.
const nearMissMinNameLength = 4

// LintAttachmentSlotNearMiss reports each attachment whose name is not a
// declared slot on its component's catalog type but is within a small edit
// distance of one — the shape a slot-name typo takes.
//
// An attachment name that matches no declared slot is legal: the attachment
// surface is open, so a deployment may attach a Feature under any name and the
// entity's declared slots constrain only the fills that bind them. That
// openness is what lets one entity serve both a slot-filled attachment and an
// attachment shared at a broader scope, and it is deliberately not a halt.
//
// The cost of the openness is that a misspelled slot name silently becomes a
// free attachment instead of filling the slot the author meant. This lint is
// the mitigation: it names both spellings so the author can see the pair. It is
// advisory and never changes the build's verdict — the compiler does not police
// what an author names an attachment.
//
// Each finding carries a Field locator naming the authoring site, so a front end
// that maps locators to source positions can place the advisory on the offending
// key rather than at the head of the file.
//
// Walk order. Component instances are walked in the order
// collectComponentInstances returns (sorted by instance name); within an
// instance, attachment names are walked in [sort.Strings] order and each
// attachment's candidate slots are compared in [sort.Strings] order, so
// repeated runs over identical input emit an identical advisory sequence.
//
// Returns nil when agg is nil or when catalog.Component is nil — a catalog-less
// path has no slot declarations to compare against.
func LintAttachmentSlotNearMiss(na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var out []*ResolverError

	for _, p := range collectComponentInstances(na) {
		comp := catalogComponentFor(p.inst.Type, catalog)
		if comp == nil || comp.Spec.FeaturesSchema.Len() == 0 {
			continue
		}
		slotNames := comp.Spec.FeaturesSchema.Keys()
		base := componentFieldPath(p.subsystemPath, p.name, p.synthesized)

		names := append([]string(nil), p.inst.Features.Keys()...)
		sort.Strings(names)
		for _, name := range names {
			if _, declared := comp.Spec.FeaturesSchema.Get(name); declared {
				continue
			}
			for _, slot := range nearMissCandidates(name, slotNames) {
				out = append(out, &ResolverError{
					Entity: entity,
					Field:  fmt.Sprintf("%s.features[%q]", base, name),
					Message: fmt.Sprintf(
						"attachment %q on component %q is not a declared slot of type %q, but the type declares the slot %q — if %q was meant, the attachment is currently a free attachment and does not fill it",
						name, p.name, p.inst.Type, slot, slot,
					),
					Err: ErrAttachmentSlotNearMiss,
				})
			}
		}
	}

	return out
}

// catalogComponentFor returns the catalog Component for a type URI, or nil on a
// miss. A miss is silent: an unresolvable type is another guard's report, and
// this lint must not double-report it.
func catalogComponentFor(typeURI string, catalog CatalogLookup) *types.Component {
	comp, found := catalog.Component(typeURI)
	if !found {
		return nil
	}
	return &comp
}

// nearMissCandidates returns the declared slot names within
// nearMissMaxDistance edits of name, in sort.Strings order. An exact match is
// never a candidate — the caller has already excluded declared names.
func nearMissCandidates(name string, slotNames []string) []string {
	if len([]rune(name)) < nearMissMinNameLength {
		return nil
	}
	var hits []string
	for _, slot := range slotNames {
		if slot == name || len([]rune(slot)) < nearMissMinNameLength {
			continue
		}
		if editDistanceWithin(name, slot, nearMissMaxDistance) {
			hits = append(hits, slot)
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
// two substitutions, so a distance-1 bound would miss exactly the mistake this
// lint exists to catch while still catching the rarer single-character slips.
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
