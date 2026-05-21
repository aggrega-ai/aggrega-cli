// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package selector

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// MatchString is the string-selector entry point: it threads selectorStr
// through Parse to obtain the slice of Requirement clauses, then ANDs the
// per-clause results from matchRequirement against the supplied labels.
// Comma-separated requirements combine with AND logic per the Kubernetes
// label-selector convention; an empty selector parses to zero requirements
// and the function returns (true, nil) — the "no constraint" case Step 4's
// usesSchema-selector validator (analyzer/selector.go) relies on to skip
// edges whose source socket declares no selector.
//
// The Parse/match split (this function = orchestration; parse.go = syntax;
// match.go = structured-selector evaluation) keeps the string parsing
// concerns separate from the predicate evaluation concerns. matchRequirement
// below covers all six Operator constants — including the parse-only
// Equals / NotEquals pair — because the string path lands clauses in
// Requirement form regardless of whether the surface input was "key=value"
// or "key in (value)".
//
// Errors propagate from Parse only; matchRequirement is total over the
// closed Operator set (unknown operators would mean a Requirement was
// constructed by someone other than Parse, which is not a supported usage).
func MatchString(selectorStr string, labels types.OrderedMap[string]) (bool, error) {
	reqs, err := Parse(selectorStr)
	if err != nil {
		return false, err
	}
	for _, req := range reqs {
		if !matchRequirement(req, labels) {
			return false, nil
		}
	}
	return true, nil
}

func matchRequirement(req Requirement, labels types.OrderedMap[string]) bool {
	val, exists := labels.Get(req.Key)
	switch req.Operator {
	case OpEquals:
		return exists && val == req.Values[0]
	case OpNotEquals:
		return !exists || val != req.Values[0]
	case OpIn:
		if !exists {
			return false
		}
		for _, v := range req.Values {
			if val == v {
				return true
			}
		}
		return false
	case OpNotIn:
		if !exists {
			return true
		}
		for _, v := range req.Values {
			if val == v {
				return false
			}
		}
		return true
	case OpExists:
		return exists
	case OpDoesNotExist:
		return !exists
	default:
		return false
	}
}
