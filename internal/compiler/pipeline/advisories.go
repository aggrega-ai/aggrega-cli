// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// AdvisoryLog collects every exit-code-neutral advisory a run raises, in the
// order the run raised them, so a caller that reads no terminal receives the
// same findings in the same sequence a terminal shows them.
//
// # Why the order is a property of the log rather than of a later assembly
//
// The advisories are produced at points scattered through the run — one inside
// the resolve scope, one after static analysis, two around the governance
// sockets, one after the render — and a display writes each at the moment it is
// produced. Assembling the list afterwards from per-class slices would mean
// re-deriving that interleaving by hand, and a re-derivation is free to disagree
// with the displays it is supposed to match. So an entry is recorded HERE, beside
// the display that shows it, in the same statement group: there is one append per
// display, which makes the order structural instead of reconstructed.
//
// # Why one log and not a field per class
//
// A second advisory channel beside the first would leave every consuming surface
// deciding which one to read, and the two would disagree the first time an
// advisory moved between them. One log carries every class, and a consumer that
// wants to distinguish them reads each entry's own severity and code.
//
// A nil *AdvisoryLog records nothing and is safe to use: a caller that drives a
// phase directly and wants no collection leaves the field unset, and the
// displays are unaffected either way.
type AdvisoryLog struct {
	entries []renderer.Diagnostic
}

// record appends one advisory. A nil receiver is a no-op, so a phase invoked
// without a log neither collects nor panics.
func (l *AdvisoryLog) record(d renderer.Diagnostic) {
	if l == nil {
		return
	}
	l.entries = append(l.entries, d)
}

// Entries returns the advisories recorded so far, in the order they were
// raised. The slice is a copy, so a consumer holding it cannot be surprised by a
// later append writing into the array behind it — the log outlives some of its
// readers.
func (l *AdvisoryLog) Entries() []renderer.Diagnostic {
	if l == nil || len(l.entries) == 0 {
		return nil
	}
	out := make([]renderer.Diagnostic, len(l.entries))
	copy(out, l.entries)
	return out
}

// advisoryFromResolverFinding renders one resolver-class finding as a structured
// advisory at the supplied severity.
//
// # The two wordings, and why the entry carries the subject-free one
//
// A resolver finding has two renderings that differ only in whether the subject
// — the entity reference and the locator — is spelled inside the sentence. A
// terminal has nowhere else to put the subject, so it shows the rendering that
// carries it. A surface that draws the finding ON its authoring key already shows
// the file, the position and the highlighted token, so it shows the rendering
// without it. Both come from one composer in the resolver, so the two cannot
// drift into two spellings of one condition.
//
// The entry carries the subject-free rendering and the subject STRUCTURALLY —
// the entity on SourceRef, the locator on the location's key path — which is what
// lets a consumer that positions the entry reproduce the subject-bearing line if
// it wants one, and lets a consumer that cannot position it fall back without
// having to unpick a composed sentence.
//
// # Why the location carries a key path and deliberately no URI
//
// The locator is a path into the authoring document, not a resolved line: the
// position is recovered by the surface that holds the parsed document, which is
// where that capability lives. Carrying the locator rather than a line is what
// keeps the recovery there and stops this function inventing a position it cannot
// know.
//
// The URI is left empty ON PURPOSE. The shared formatter every display reads
// interprets a location's URI as a NODE identity and its kind as a provider, so
// filling either with a document path would make the formatter print a document
// as though it were a graph node. With both empty the formatter renders the
// message alone, which is exactly the line a positioning surface wants.
func advisoryFromResolverFinding(e *resolver.ResolverError, severity string) renderer.Diagnostic {
	code, _ := cerrors.CodeOf(e)
	d := renderer.Diagnostic{
		Severity:  severity,
		Message:   e.MessageWithoutSubject(),
		Code:      code,
		SourceRef: e.Entity,
	}
	if e.Field != "" {
		d.Location = &renderer.DiagnosticLocation{KeyPath: e.Field}
	}
	return d
}

// advisoryFromDisplayLine renders one already-composed advisory line as a
// structured advisory at warn severity.
//
// It is the shape for a producer that hands the pipeline a finished sentence
// rather than a structured finding: the static-analysis advisory stream is a
// string slice, so the sentence is all there is by the time it arrives here. The
// entry therefore carries no location, and that absence is a measured property of
// the producer rather than a shortcut — there is no entity, no locator and no
// position on the value to carry.
//
// The message is the displayed line VERBATIM. Rewriting it here would give one
// advisory two wordings, and the displayed one is the wording every existing
// reader already knows.
func advisoryFromDisplayLine(line string) renderer.Diagnostic {
	return renderer.Diagnostic{Severity: abi.SeverityWarn, Message: line}
}

// advisoryFromPolicyVerdict renders one non-halting governance verdict as a
// structured advisory, keeping everything the guest set and replacing only the
// message with the attributed line the display shows.
//
// # Why this class carries its attribution INSIDE the message
//
// A render advisory carries its attribution structurally — the node and the
// provider live on its location — so a display composes the attributed line and a
// consumer reads the parts. A governance verdict's attribution is the identity of
// the PLUGIN that raised it, and the shared diagnostic shape has no field for an
// emitter: there is no location, code or reference field whose meaning is "who
// reported this". So the only way the attribution reaches a structured consumer
// at all is inside the message, which is also exactly the line the display shows.
// The asymmetry with the render class is therefore a property of the wire shape
// rather than an inconsistency to tidy away.
//
// Everything else the guest set survives — the severity it chose, its code, rule
// identifier, classification, source reference, suggestion, and its LOCATION.
// That last is the point: a guest that located its finding keeps that position,
// where flattening the verdict to a sentence would have destroyed it.
func advisoryFromPolicyVerdict(d renderer.Diagnostic, attributedMessage string) renderer.Diagnostic {
	d.Message = attributedMessage
	return d
}
