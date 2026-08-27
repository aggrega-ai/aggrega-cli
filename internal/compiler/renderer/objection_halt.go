// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	stderrors "errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// objectionsAsHalt renders what a rule raised as the render's own halt, and nil
// when it raised nothing.
//
// Each objection becomes a diagnostic of its own carrying its own position: the
// file the subject was authored in, and the authored key inside it. That is the
// whole reason the findings are not folded into one message — a position is
// something an author can navigate to, and one message naming several positions
// can be navigated to none of them.
//
// One objection is returned as itself rather than as a set of one, so the
// ordinary case reads as the single diagnostic it is. Several are joined under an
// envelope, which is the shape a display surface already renders every member of.
// The envelope wording is the caller's because only the caller knows what its own
// findings are about; everything above it is the same wherever objections are
// turned into a halt.
//
// # Why this is one function and not one per site
//
// The per-objection wrap is a single mechanic: the category a raised objection
// carries, the position it is reported at, and the message an author reads are
// each derived from the objection the same way whatever rule raised it. A second
// copy would agree on the day it was written and be free to drift the moment
// either half gains a term — a severity read, a second locator segment, a change
// to how a detail is rendered — and the drift would surface as two rules
// reporting the same class of fault in two shapes, which is exactly what a reader
// comparing them cannot tell from a deliberate difference.
//
// The category is this function's rather than its caller's, and it is the catalog
// one. Every rule reaching here is about what an entity declares, so the fault is
// in a catalog document and the remedy is an edit to one; the category is
// therefore a property of the readings that use this entry rather than a choice
// each of them makes.
//
// A rule reporting a different class of fault reaches the same rendering through
// its own entry naming its own category — see [objectionsAsTranslationHalt],
// which is what a rule about output the compiler produced rather than about
// authoring needs. That is why the rendering below takes the category while this
// entry does not: the two callers differ on the attribution and on nothing else,
// so parameterising the attribution is what keeps the wording, the position and
// the envelope shape a single mechanic instead of two that agree today.
func objectionsAsHalt(objections []constraint.Objection, envelope string) error {
	return objectionsAsCategorisedHalt(objections, cerrors.CatalogError, envelope)
}

// objectionsAsCategorisedHalt is the whole of that rendering, with the fault's
// attribution supplied.
//
// It is unexported and takes the category as its only variable so no reading can
// arrive at a different message shape by writing its own loop: the per-objection
// wrap, the position it is reported at, and the envelope several findings are
// joined under are one mechanic here and cannot be re-implemented per category.
// The category itself is genuinely per-reading — a rule about a catalog document
// and a rule about produced output send an author to different places — which is
// the one axis on which the callers legitimately differ.
func objectionsAsCategorisedHalt(
	objections []constraint.Objection, category cerrors.Category, envelope string,
) error {
	if len(objections) == 0 {
		return nil
	}
	halts := make([]error, 0, len(objections))
	for _, o := range objections {
		halts = append(halts, cerrors.Wrap(
			o.Sentinel(),
			category,
			cerrors.SourceLocation{File: o.SourceFile(), Locator: o.Locator()},
			constraint.ObjectionMessage(o),
		))
	}
	if len(halts) == 1 {
		return halts[0]
	}
	return cerrors.Wrap(
		stderrors.Join(halts...),
		category,
		cerrors.SourceLocation{},
		envelope,
	)
}

// objectionsAsAdvisories renders what an advisory rule raised into the warning
// stream a render already carries, and nil when it raised nothing.
//
// It is the other side of this package's reporting boundary from
// [objectionsAsCategorisedHalt], and the two exist separately because the two
// sides carry different consequences: a raised violation stops the build and a
// raised advisory does not, so a rule whose declaration says advisory must not
// reach the value the render returns as its error. Routing one through the halt
// conversion would turn a build that renders into a build that fails, which is
// the opposite of what the declaration says.
//
// The wording is not decided here. It comes from [constraint.ObjectionMessage],
// the one renderer every boundary reads, so a rule carries one wording whichever
// side of this boundary reports it. The position is not composed here either:
// [cerrors.SourceLocation] is where a file and an authored locator are joined,
// including the handling for either half standing alone, so a warning's position
// reads in the same shape as every other positioned diagnostic rather than in a
// second one that agrees today.
//
// A warning is a plain string with no field to carry a locator, so the position
// is composed into the text rather than dropped: it is the only thing naming
// which authored site the finding is about, and an advisory a reader cannot
// navigate to is one they cannot act on. A finding whose subject carries neither
// a file nor a locator renders as its message alone rather than as a message with
// an empty position hanging off it.
//
// Advisories come back in the order the objections were raised, which is the
// order the rule was invoked in — so a render over identical input produces the
// same warning stream in the same order.
func objectionsAsAdvisories(objections []constraint.Objection) []string {
	if len(objections) == 0 {
		return nil
	}
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		message := constraint.ObjectionMessage(o)
		position := cerrors.SourceLocation{File: o.SourceFile(), Locator: o.Locator()}.String()
		if position == "" {
			out = append(out, message)
			continue
		}
		out = append(out, position+": "+message)
	}
	return out
}
