// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	stderrors "errors"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
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

// objectionsAsCategorisedHalt is that rendering with one attribution for every
// finding a reading raises.
//
// It is unexported and takes the category as its only variable so no reading can
// arrive at a different message shape by writing its own loop: the per-objection
// wrap, the position it is reported at, and the envelope several findings are
// joined under are one mechanic, in [objectionsAsHaltFiledBy], and cannot be
// re-implemented per category. The category itself is genuinely per-reading — a
// rule about a catalog document and a rule about produced output send an author
// to different places — which is the one axis on which the callers legitimately
// differ.
func objectionsAsCategorisedHalt(
	objections []constraint.Objection, category cerrors.Category, envelope string,
) error {
	return objectionsAsHaltFiledBy(objections, func(constraint.Objection) cerrors.Category {
		return category
	}, envelope)
}

// objectionsAsHaltFiledBy is the whole of that rendering, with each finding's
// attribution read from the finding by categoryOf.
//
// The attribution is per finding rather than per reading for the readings whose
// findings are filed by the document their subject was written in, where one
// rule's findings can belong to two authors. The envelope several findings are
// joined under takes the first finding's attribution, so the header a reader
// meets first agrees with the first finding beneath it; for a reading with one
// attribution that is the attribution of all of them.
func objectionsAsHaltFiledBy(
	objections []constraint.Objection,
	categoryOf func(constraint.Objection) cerrors.Category,
	envelope string,
) error {
	if len(objections) == 0 {
		return nil
	}
	halts := make([]error, 0, len(objections))
	for _, o := range objections {
		halts = append(halts, cerrors.Wrap(
			o.Sentinel(),
			categoryOf(o),
			cerrors.SourceLocation{File: o.SourceFile(), Locator: o.Locator()},
			constraint.ObjectionMessage(o),
		))
	}
	if len(halts) == 1 {
		return halts[0]
	}
	return cerrors.Wrap(
		stderrors.Join(halts...),
		categoryOf(objections[0]),
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
// The position is composed into the text as well as carried in the authored-key
// field, and the reason for the duplication is that the two halves of a position
// are not equally expressible. An advisory's location field identifies a node
// and an authored key inside it, and a rule's subject is authored in a
// DOCUMENT — so the locator has a field of its own while the file does not, and
// writing the file into the node slot would attribute the finding to a node of
// that name. Composing the whole position into the text is what keeps a reader
// able to navigate to it; carrying the locator as a field is what lets a
// consumer that renders no text act on it. A finding whose subject carries
// neither a file nor a locator renders as its message alone rather than as a
// message with an empty position hanging off it.
//
// No class code is carried, and that is deliberate rather than an omission. An
// objection does hold a registered halting-condition identity, and publishing
// that identity as an advisory's class code would put a code on this channel
// that has never carried one — which is a decision about the code register
// rather than about this conversion, so it is not taken here.
//
// Advisories come back in the order the objections were raised, which is the
// order the rule was invoked in — so a render over identical input produces the
// same warning stream in the same order.
func objectionsAsAdvisories(objections []constraint.Objection) []Diagnostic {
	if len(objections) == 0 {
		return nil
	}
	out := make([]Diagnostic, 0, len(objections))
	for _, o := range objections {
		message := constraint.ObjectionMessage(o)
		position := cerrors.SourceLocation{File: o.SourceFile(), Locator: o.Locator()}.String()
		if position != "" {
			message = position + ": " + message
		}
		d := Diagnostic{Severity: abi.SeverityWarn, Message: message}
		if locator := o.Locator(); locator != "" {
			d.Location = &DiagnosticLocation{KeyPath: locator}
		}
		out = append(out, d)
	}
	return out
}
