// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
// offending authoring key in the file that holds it. The authoring document the
// subject's site recorded becomes the violation's document, which is what the
// violation is filed under when it is converted. The sentinel becomes the
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
			Document:   o.AuthoringDocument(),
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
// # The position travels twice, and both copies are deliberate
//
// The locator is composed into the message text AND carried on the location,
// which reads like saying one thing twice and is not: the two copies serve
// surfaces that cannot use each other's.
//
// The composed prefix is what a terminal reads. A terminal has nowhere but the
// sentence to put a position, and the prefix is the line every existing reader
// already knows, so rewriting it would change what a build prints for no gain
// to anyone.
//
// The location is what a surface holding the parsed document reads. It walks
// the key path to a real line and column and draws the finding on the offending
// key, which no amount of parsing the prefix would let it do reliably. Carrying
// the key rather than a resolved line is what keeps position recovery with the
// surface that holds the document, which is the only place it can happen.
//
// So a reader in an editor meets a sentence whose prefix repeats a position the
// editor is also showing structurally. That redundancy is the whole cost, and it
// is strictly better than the alternative it replaces: an entry with no location
// at all lands at the head of the file, where the reader can see the position in
// the text and cannot navigate to it.
//
// The severity is warn on every entry, because this is the advisory side of the
// boundary by construction — a finding that halts is converted by
// [ErrorsFromObjections] instead, and the two never cross.
//
// A finding whose provenance names neither a file nor a locator carries its
// message alone and no location, rather than a message with an empty position
// hanging off it. That is a property of the finding rather than a shortcut: there
// is no key for a consumer to walk to.
//
// Advisories come back in the order the objections were raised, which is the
// order the check was invoked in.
//
// Returns nil when nothing was raised.
func AdvisoriesFromObjections(objections []constraint.Objection) []abi.Diagnostic {
	if len(objections) == 0 {
		return nil
	}
	out := make([]abi.Diagnostic, 0, len(objections))
	for _, o := range objections {
		message := constraint.ObjectionMessage(o)
		if position := advisoryPosition(o.SourceFile(), o.Locator()); position != "" {
			message = position + ": " + message
		}
		d := abi.Diagnostic{Severity: abi.SeverityWarn, Message: message}
		if locator := o.Locator(); locator != "" {
			d.Location = &abi.Location{KeyPath: locator}
		}
		out = append(out, d)
	}
	return out
}

// advisoryPosition renders the position an advisory names from the file and the
// authored locator an objection carries, and returns empty when it carries
// neither.
//
// It exists because of what an advisory is rather than because of how a position
// is composed: a warning is a plain string with no field to carry a locator, so
// its position has to reach the reader inside the text. A violation on the other
// side of this boundary has a structured pointer to put the same two halves on,
// which is why only this side needs a string at all.
//
// The composition itself is not done here. [cerrors.SourceLocation] holds it,
// including the reason the two halves are joined by a word rather than run
// together and the handling for either half being absent on its own — a finding
// about an element whose authored site this stage cannot name still knows the
// file, and a synthetic graph carries a locator and no file. Composing it a
// second time here would let a warning's position drift from the shape every
// other positioned diagnostic uses, and the two agreeing today is exactly what
// would hide that drift.
func advisoryPosition(sourceFile, locator string) string {
	return cerrors.SourceLocation{File: sourceFile, Locator: locator}.String()
}
