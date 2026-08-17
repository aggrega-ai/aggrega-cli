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

// AdvisoriesFromObjections renders what an advisory check raised into the
// warning shape this package's callers already read.
//
// It is the sibling of [ErrorsFromObjections] on the other side of this stage's
// reporting boundary, and the two exist separately because the two sides carry
// different consequences: a violation halts the build and a warning does not,
// so a rule the author declared advisory must not reach the slice a build gates
// on. Routing one through the violation conversion would turn a build that
// passes into a build that fails, which is the opposite of what an advisory is.
//
// The wording comes from [constraint.ObjectionMessage], the same renderer the
// violation conversion reads, so one rule carries one wording whichever side it
// reports on.
//
// A warning is a plain string and carries no locator field, so the locator is
// composed into the text rather than dropped: it is the only thing naming which
// authored key the finding is about, and an advisory that cannot be navigated
// to is an advisory the author cannot act on. The file precedes it where the
// finding carries one, so the pair reads as a position. A finding whose
// provenance names neither is rendered as its message alone, rather than as a
// message with an empty position hanging off it.
//
// Advisories come back in the order the objections were raised, which is the
// order the check was invoked in.
//
// Returns nil when nothing was raised.
func AdvisoriesFromObjections(objections []constraint.Objection) []string {
	if len(objections) == 0 {
		return nil
	}
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		message := constraint.ObjectionMessage(o)
		switch position := advisoryPosition(o.SourceFile(), o.Locator()); position {
		case "":
			out = append(out, message)
		default:
			out = append(out, position+": "+message)
		}
	}
	return out
}

// advisoryPosition composes the file and the authored locator an objection
// carries into the position an advisory names, or returns empty when it carries
// neither.
//
// Either half may be absent on its own — a finding about an element whose
// authored site this stage cannot name still knows the file, and a synthetic
// graph carries a locator and no file — so each is emitted when it is there
// rather than both being conditioned on one of them.
//
// The two are joined by a word rather than run together. A locator opens with a
// separator of its own, so concatenating the pair yields one string that reads
// as a single file name with a nonsense extension, and a reader cannot see
// where the document ends and the key begins. The joining word is the one the
// product's other positioned diagnostics already use, so one shape names a
// position everywhere it is named.
func advisoryPosition(sourceFile, locator string) string {
	switch {
	case sourceFile != "" && locator != "":
		return sourceFile + " at " + locator
	case sourceFile != "":
		return sourceFile
	default:
		return locator
	}
}
