// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package selector

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// MatchLabels evaluates a flat key-value map (AND logic, equality only)
// against the provided labels. Every key in matchLabels must exist in
// labels with the same value for the result to be true; missing keys and
// value mismatches both fail. An empty or nil matchLabels matches
// everything — the "no constraint declared" contract shared by every
// selector surface.
//
// Both arguments are types.OrderedMap[string] so callers can pass a
// SelectorTerm's flat-map arm and IRGraph node Labels directly without
// copying; the boolean result is order-independent, but the OrderedMap
// signature keeps the call site uncluttered.
func MatchLabels(matchLabels types.OrderedMap[string], labels types.OrderedMap[string]) bool {
	for k, v := range matchLabels.All() {
		got, ok := labels.Get(k)
		if !ok || got != v {
			return false
		}
	}
	return true
}

// MatchAnyOf evaluates a list of SelectorTerm with OR logic between items
// (any single matching item satisfies the whole expression). Each item is
// evaluated per its arm: a string-form item goes through MatchString (the
// same grammar appliesTo accepts; requirements inside the string AND
// together), a flat-map item goes through MatchLabels (equality-only).
// This shape mirrors the Feature catalog's `appliesToAnyOf` field exactly —
// each YAML entry is one item, and the list of entries reads as a
// disjunction.
//
// An empty or nil items list matches everything — the explicit early
// return preserves the "no constraint" contract even though the for loop's
// natural fall-through would return false (zero items means zero
// disjuncts succeed).
//
// The error reports the first string-form item that fails to parse,
// evaluated in author order; the boolean is false in that case. An item
// before the malformed one may already have satisfied the disjunction, in
// which case the parse error is never reached — evaluation is
// short-circuiting and deterministic. Parse errors surface rather than
// being swallowed so a malformed selector cannot silently change a
// Feature's attachment surface.
func MatchAnyOf(terms []types.SelectorTerm, labels types.OrderedMap[string]) (bool, error) {
	if len(terms) == 0 {
		return true, nil
	}
	for _, term := range terms {
		ok, err := matchTerm(term, labels)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func matchTerm(term types.SelectorTerm, labels types.OrderedMap[string]) (bool, error) {
	if term.IsMap() {
		return MatchLabels(term.Labels, labels), nil
	}
	return MatchString(term.Selector, labels)
}
