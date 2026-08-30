// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// FindingsFromObjections renders what a check raised into the finding shape this
// package's callers already read.
//
// A check hands back the sentinel naming the rule and the provenance of what it
// objected about, and nothing else: it is deliberately not a diagnostic, so the
// shape a finding takes is decided at the boundary it crosses rather than inside
// the rule. This is that decision for the boundary a resolve reports across.
//
// Every field is derived and none is re-typed. The sentinel becomes the wrapped
// error, so a caller matching the finding by identity keeps matching — both the
// caller that drops one rule's findings out of a halt set and the caller that
// matches a rule through the error a failed resolve returns. The locator becomes
// the field the finding positions at, which is what lets a front end place it on
// the offending authoring key rather than at the head of the file.
//
// 🔴 The message is built from the rule's detail VALUES and never from
// [constraint.ObjectionMessage]. The finding renders through the one composition
// every diagnostic naming a condition follows, which reads the rule's registered
// wording off the sentinel itself; [constraint.ObjectionMessage] already opens
// with that same wording, so handing its result over as a value would state the
// rule's sentence twice in one line. Taking the values instead is not a second
// wording — it is the same rule reporting through a boundary that supplies
// subject of its own, the document a finding is attributed to and the locator it
// sits at, which the rule cannot know. The values arrive already spelled as a
// message reads them, from [constraint.Objection.RenderedDetail], the same
// spelling [constraint.ObjectionMessage] renders from: one wording, one detail
// spelling, two shapes.
//
// They are joined with the separator the composition itself puts between values,
// so a rule's values read as further entries in the finding's value list rather
// than as one opaque entry inside it.
//
// The entity names the document the findings are about, and it is supplied
// rather than derived because the rules reporting through here span both
// authoring documents: a rule over what the deployment wrote is not attributable
// to the application, and a conversion that named one document for everything
// would send an operator to a file they did not write in.
//
// Findings come back in the order the objections were raised, which is the order
// the check was invoked in.
//
// Returns nil when nothing was raised.
//
// It is exported because a check placed at a later stage of the compilation is
// invoked from the package that holds that stage's product, and every such check
// reporting into this finding shape reaches it here rather than re-deriving it.
// A stage whose callers read a different diagnostic shape converts for itself and
// shares the wording through the sentinel, so the two shapes stay two shapes and
// the wording stays one.
func FindingsFromObjections(
	objections []constraint.Objection,
	entity string,
) []*ResolverError {
	return findings(objections, func(o constraint.Objection) (string, string) {
		return entity, o.Locator()
	})
}

// findingsWithNoSubject renders what a check raised for a condition no authored
// document is at fault for: no entity reference and no locator.
//
// It is the conversion for a rule whose subject is neither of the two documents a
// resolve is about, and is not a third document either — a divergence inside the
// compiler, which every author's file is equally uninvolved in. The other two
// conversions choose WHICH document a finding names; this one is the case where
// the honest answer is none, and naming one anyway would send a reader to a key
// they have no reason to open and no way to change.
//
// The locator goes with the entity rather than surviving it. A locator is a path
// INSIDE the document a finding names, so a finding naming no document cannot
// carry one: the path would be read against whichever document the reader
// assumed, which is the misdirection in its most confusing form rather than a
// milder version of it.
//
// It shares the per-objection derivation with the other two conversions, so the
// three differ in the subject they attach and in nothing else — the sentinel, the
// message and the ordering are derived identically.
func findingsWithNoSubject(objections []constraint.Objection) []*ResolverError {
	return findings(objections, func(constraint.Objection) (string, string) {
		return "", ""
	})
}

// findingsAtTheirOwnDocument renders what a check raised into the finding shape,
// attributing each finding to the document its own subject was authored in rather
// than to one document supplied for the whole set.
//
// It exists for a rule whose subject is authored in neither of the two documents a
// resolve is about. Such a rule's locator names a path inside a third document —
// a Recipe's own entry, say — and attributing it to the application would name a
// path that document does not contain, which sends a reader to a key they cannot
// find. The document kind is supplied because a subject carries the file it was
// authored in and not what kind of entity that file holds, and the entity
// reference a finding renders reads as a kind and a name.
//
// fallback is used for a subject carrying no file at all, which is the shape every
// coordinate derived from an authoring document takes here: it names no file
// because the finding's document is the one the caller already knows.
//
// It shares the per-objection derivation with [FindingsFromObjections], so the two
// differ in where the entity comes from and in nothing else.
func findingsAtTheirOwnDocument(
	objections []constraint.Objection,
	kind string,
	fallback string,
) []*ResolverError {
	return findings(objections, func(o constraint.Objection) (string, string) {
		if o.SourceFile() == "" {
			return fallback, o.Locator()
		}
		return kind + " " + o.SourceFile(), o.Locator()
	})
}

// findings is the per-objection derivation the conversions above share: every
// field of a finding read off the objection, with the subject — the document the
// finding names and the locator inside it — supplied by the caller's own rule for
// it.
//
// The two travel together, in one function returning both, because they are one
// decision: a locator is a path inside the document the entity names, so a
// conversion that chose them separately could produce a path belonging to a
// document the finding does not name. Returning them from one call makes that
// pairing unspellable rather than merely discouraged.
func findings(
	objections []constraint.Objection,
	subjectOf func(constraint.Objection) (entity string, field string),
) []*ResolverError {
	if len(objections) == 0 {
		return nil
	}
	out := make([]*ResolverError, 0, len(objections))
	for _, o := range objections {
		entity, field := subjectOf(o)
		out = append(out, &ResolverError{
			Entity:  entity,
			Field:   field,
			Message: strings.Join(o.RenderedDetail(), ", "),
			Err:     o.Sentinel(),
		})
	}
	return out
}
