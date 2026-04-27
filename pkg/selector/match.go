// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package selector

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// MatchLabels evaluates a matchLabels map (AND logic) against the provided
// labels. Every key in matchLabels must exist in labels with the same value
// for the result to be true; missing keys and value mismatches both fail.
// An empty or nil matchLabels matches everything — the "no constraint"
// case the analyzer relies on to skip evaluation when a Feature declares no
// AppliesTo entries.
//
// Both arguments are types.OrderedMap[string] so callers can pass
// Feature.Spec.AppliesTo and IRGraph node Labels directly without copying;
// the boolean result is order-independent, but the OrderedMap signature
// keeps the call site uncluttered.
func MatchLabels(matchLabels types.OrderedMap[string], labels types.OrderedMap[string]) bool {
	for k, v := range matchLabels.All() {
		got, ok := labels.Get(k)
		if !ok || got != v {
			return false
		}
	}
	return true
}

// MatchExpressions evaluates a list of LabelSelectorRequirement entries
// against labels with AND logic — every requirement must hold. The four
// operators recognised here ("In", "NotIn", "Exists", "DoesNotExist") are
// the K8s API LabelSelectorRequirement vocabulary used by Feature
// AppliesToExpressions and SelectorTerm.MatchExpressions; this is a
// deliberately distinct surface from the string-selector path's six
// operator constants in parse.go (which add Equals / NotEquals because the
// "key=value" / "key!=value" string forms produce them). Any operator
// outside the four returns false — the function does not error, mirroring
// the K8s convention that an unknown operator is a structural mismatch
// rather than a parse-time concern.
//
// "In" and "NotIn" semantics treat a missing key asymmetrically: "In"
// fails (the key must exist AND its value must be in the set), while
// "NotIn" succeeds (the value-not-in-set predicate is vacuously true when
// the value does not exist). This matches K8s label-selector behaviour
// exactly and is asserted in match_test.go.
//
// An empty or nil exprs slice matches everything — same "no constraint"
// behaviour as MatchLabels.
func MatchExpressions(exprs []types.LabelSelectorRequirement, labels types.OrderedMap[string]) bool {
	for _, expr := range exprs {
		if !matchExpression(expr, labels) {
			return false
		}
	}
	return true
}

func matchExpression(expr types.LabelSelectorRequirement, labels types.OrderedMap[string]) bool {
	val, exists := labels.Get(expr.Key)
	switch expr.Operator {
	case "In":
		if !exists {
			return false
		}
		for _, v := range expr.Values {
			if val == v {
				return true
			}
		}
		return false
	case "NotIn":
		if !exists {
			return true
		}
		for _, v := range expr.Values {
			if val == v {
				return false
			}
		}
		return true
	case "Exists":
		return exists
	case "DoesNotExist":
		return !exists
	default:
		return false
	}
}

// MatchAnyOf evaluates a list of SelectorTerm with OR logic between terms
// and AND logic within each term (a term's MatchLabels and MatchExpressions
// must both pass; any single passing term satisfies the whole expression).
// This shape mirrors the Feature catalog's `appliesToAnyOf` field exactly —
// each YAML entry is one term, and the list of entries reads as a
// disjunction.
//
// MatchAnyOf is structurally distinct from MatchExpressions rather than a
// flag on a unified function because the two surfaces compose differently:
// AppliesToExpressions is a flat AND-list; AppliesToAnyOf is an OR-of-AND
// list. Collapsing them into one entry point would either force callers to
// wrap every flat list in a single-element terms slice (noisy at the call
// site) or branch internally on a flag, which is the same dispatch
// duplicated in the type. Keeping the two entry points lets the analyzer's
// per-Feature evaluation in applies_to.go AND the three constraint forms
// (AppliesTo, AppliesToExpressions, AppliesToAnyOf) directly without a
// shape adapter.
//
// An empty or nil terms list matches everything — the explicit early
// return preserves the "no constraint" contract even though the for loop's
// natural fall-through would return false (zero terms means zero
// disjuncts succeed).
func MatchAnyOf(terms []types.SelectorTerm, labels types.OrderedMap[string]) bool {
	if len(terms) == 0 {
		return true
	}
	for _, term := range terms {
		if matchTerm(term, labels) {
			return true
		}
	}
	return false
}

func matchTerm(term types.SelectorTerm, labels types.OrderedMap[string]) bool {
	if !MatchLabels(term.MatchLabels, labels) {
		return false
	}
	if !MatchExpressions(term.MatchExpressions, labels) {
		return false
	}
	return true
}
