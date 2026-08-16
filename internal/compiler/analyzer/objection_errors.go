// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// ErrorsFromObjections renders what a check raised into the violation shape
// this package's callers already read.
//
// A check hands back the sentinel naming the rule and the provenance of what it
// objected about, and nothing else: it is deliberately not a diagnostic, so the
// shape a violation takes is decided at the boundary it crosses rather than
// inside the rule. This is that decision for the boundary this stage reports
// across, and without it a rule placed here has no way to report at all.
//
// The wording is NOT decided here. It comes from
// [constraint.ObjectionMessage], the one renderer every boundary shares, so a
// rule carries one wording whichever stage reports it and a second rendering
// cannot drift from the first. What is decided here is only which of this
// stage's own fields the objection's provenance lands on, which is the part
// that genuinely differs: the shape a resolve reports in carries the document
// the finding is about and no source file, and this one carries a source file
// and no document.
//
// The locator becomes the field the violation positions at, and the source file
// becomes its origin, which together let a front end place the violation on the
// offending authoring key in the file that holds it. The sentinel becomes the
// violation's own sentinel, so a caller matching by identity keeps matching
// through the compiler error this converts into.
//
// The edge fields stay empty and the kind says so. A rule reporting this way
// names its subject by a locator; filling Source, Target and Socket would mean
// re-deriving coordinates the rule did not supply, and a re-derived coordinate
// can disagree with what the rule actually read.
//
// Violations come back in the order the objections were raised, which is the
// order the check was invoked in.
//
// Returns nil when nothing was raised.
func ErrorsFromObjections(objections []constraint.Objection) []AnalysisError {
	if len(objections) == 0 {
		return nil
	}
	out := make([]AnalysisError, 0, len(objections))
	for _, o := range objections {
		out = append(out, AnalysisError{
			Kind:       AnalysisErrorLocated,
			Message:    constraint.ObjectionMessage(o),
			Field:      o.Locator(),
			OriginFile: o.SourceFile(),
			Sentinel:   o.Sentinel(),
		})
	}
	return out
}
