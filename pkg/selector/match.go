// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package selector

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// MatchLabels evaluates a matchLabels map (AND logic) against the provided labels.
// Returns true if every key in matchLabels exists in labels with the same value.
// An empty or nil matchLabels matches everything.
func MatchLabels(matchLabels types.OrderedMap[string], labels types.OrderedMap[string]) bool {
	for k, v := range matchLabels.All() {
		got, ok := labels.Get(k)
		if !ok || got != v {
			return false
		}
	}
	return true
}

// MatchExpressions evaluates a list of LabelSelectorRequirement (AND logic).
// Operators: In, NotIn, Exists, DoesNotExist.
// Returns true if all requirements are satisfied.
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

// MatchAnyOf evaluates appliesToAnyOf (OR between terms, AND within each term).
// Returns true if at least one SelectorTerm matches.
// An empty or nil list matches everything (no constraint).
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
