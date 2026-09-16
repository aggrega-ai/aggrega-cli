// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The shape a Recipe / FeatureRecipe reference accepts has ONE home: the two
// declarations below, sitting beside the rule that enforces them. The resolution
// ladder that consumes a reference reads the ceiling through
// [RecipeReferenceSegments], and the shape diagnostic quotes the shape statement
// verbatim. Keeping it single-sourced is load-bearing rather than tidy: a ladder
// that grows a reference position beyond the capability one widens the accepted
// shape, and a count or a shape list restated anywhere else would go quietly
// false, because no gate reads prose. Whoever adds the position edits these two
// lines and every reader follows.
const (
	recipeReferenceMaxSegments = 2

	// AcceptedRecipeReferenceShapes is the shape statement every diagnostic
	// about a mis-shaped reference quotes. It is exported because the ladder
	// that resolves a reference reports the same shape from the package that
	// holds the catalog, and a second spelling there would be free to drift
	// from the ceiling above it.
	AcceptedRecipeReferenceShapes = `"name", "name:version", "capability/name" or "capability/name:version"`
)

// RecipeReferenceSegments counts the slash-separated segments in a Recipe /
// FeatureRecipe reference and reports whether that count exceeds what the
// resolution ladder accepts.
//
// It reads the reference string only — no catalog lookup — which is what lets a
// mis-shaped reference be reported as a shape fault rather than as an entity the
// catalog does not hold.
//
// It is exported because the resolution ladder lives in the package that holds
// the catalog, which depends on this one: the grammar travels with the rule that
// enforces it, and the ladder reads it from here rather than carrying a second
// count that agrees today.
func RecipeReferenceSegments(uri string) (int, bool) {
	n := strings.Count(uri, "/") + 1
	return n, n > recipeReferenceMaxSegments
}

// ManifestRecipeReferenceFaults reports every mapping value in a composed
// Manifest whose shape a Recipe or FeatureRecipe reference cannot express, one
// fault per value, in the order the Manifest declares them.
//
// The verdict is purely syntactic: it counts the separators in a string and
// needs no catalog at all. That is what lets it run before anything resolves —
// and running it there is the point. A mapping value carrying more segments than
// a reference accepts can never match a catalog entity, so whatever gate looks
// the value up first will report it as an entity the catalog does not hold, which
// sends the author looking for a missing recipe that is in fact present and
// correctly named. Judging the shape first means the shape fault is reported as
// a shape fault, and every absent-entity report that follows is true.
//
// It is scoped to Recipe and FeatureRecipe references — the Manifest's mapping
// values — and nothing else. A Component, Feature or Aggregate reference is the
// entity's name verbatim and carries no segment ceiling to violate, so the same
// count applied there would reject valid authoring.
//
// It presents the document and names the rule rather than walking the mapping
// table itself: the rule is declared in this package as a constraint, so where it
// runs follows from the facts it reads, and the site each fault is positioned at
// is supplied by the framework rather than assembled here. The entries are read
// back off the presentation rather than off the Manifest beside it — they are one
// of the facts the presentation states, so reading them anywhere else would be
// answering from a second source that happens to agree.
//
// The subject carries no source file, and that is the composed Manifest's own
// shape rather than an omission. A composed Manifest is the fold of an extends
// chain, so an entry in it may have been written in any file of that chain and
// the composition does not record which — there is no single file for a subject
// to name, and naming the chain's head would attribute a parent's entry to the
// child. The fault's message names the authored site inside the document, which
// is the coordinate an author edits by.
//
// Returns nil when every mapping value is well-shaped, and for a nil Manifest.
func ManifestRecipeReferenceFaults(manifest *types.Manifest) []error {
	if manifest == nil {
		return nil
	}
	var presented constraint.DocumentView = newManifestDocumentView(composedManifestHasNoFile, manifest)
	objections := constraint.RunOnDocumentMappingEntries(
		composedManifestHasNoFile,
		presented.ManifestRecipeMappingEntries(composedManifestHasNoFile),
		presented,
		manifestRecipeReferenceIsWellShaped,
	)
	if len(objections) == 0 {
		return nil
	}
	faults := make([]error, 0, len(objections))
	for _, o := range objections {
		faults = append(faults, recipeReferenceFaultFrom(o))
	}
	return faults
}

// composedManifestHasNoFile is the source file a composed Manifest is presented
// under: none. It is named rather than written as an empty literal at the three
// sites above so that the absence reads as the decision documented at
// [ManifestRecipeReferenceFaults] rather than as a value somebody forgot to fill.
const composedManifestHasNoFile = ""

// recipeReferenceFaultFrom renders one objection into the failure shape this
// rule's two callers already read.
//
// The sentence is composed here rather than taken from the framework's own
// renderer, and the reason is that this rule's wording is published: the
// halting-conditions reference reproduces it verbatim, so the sentence is a
// contract with a reader rather than a presentation choice. The framework's
// renderer composes a different, general shape — the registered wording followed
// by the occurrence's values — which is right for a rule whose message nothing
// transcribes and wrong for one whose message a page quotes.
//
// The site the fault names is read from the objection's locator rather than
// assembled again, so the position a consumer would navigate to and the
// coordinate the sentence names are one value. The locator's leading separator is
// dropped because the sentence names a coordinate inside the document and not a
// path from its root, which is the spelling the published reproduction carries.
//
// The rule's own values arrive in the order [manifestRecipeReferenceIsWellShaped]
// supplies them, which is the contract between the two and is asserted rather
// than assumed: a rule and the conversion that renders it are two halves of one
// sentence, and a silent mismatch would produce a message with the values in the
// wrong slots.
func recipeReferenceFaultFrom(o constraint.Objection) error {
	detail := o.Detail()
	if len(detail) != recipeReferenceDetailCount {
		panic(fmt.Sprintf(
			"resolver: the mis-shaped reference rule supplied %d value(s) and this conversion "+
				"composes its sentence from %d; the rule and its conversion are two halves of one "+
				"sentence, so change them together",
			len(detail), recipeReferenceDetailCount))
	}
	return fmt.Errorf(
		"%w: %s maps to %q, which carries %s slash-separated segments; a %s accepts %s; "+
			"correct the mapping value in the Manifest",
		ErrMalformedRecipeReference(),
		strings.TrimPrefix(o.Locator(), recipeReferenceLocatorRoot),
		detail[recipeReferenceDetailValue],
		detail[recipeReferenceDetailSegments],
		detail[recipeReferenceDetailSubject],
		AcceptedRecipeReferenceShapes,
	)
}

// The order the mis-shaped reference rule supplies its values in, named so the
// conversion above reads them by name rather than by position at the call site.
//
// They are positions and not keys because that is what an objection's detail is:
// an ordered list of values whose meaning belongs to the rule that raised them.
// Naming the positions here is what keeps the two halves legible as one sentence
// — the rule raises in this order, the conversion reads in this order, and the
// count below is what makes a divergence loud.
const (
	recipeReferenceDetailValue = iota
	recipeReferenceDetailSegments
	recipeReferenceDetailSubject
	recipeReferenceDetailCount
)

// recipeReferenceLocatorRoot is the separator a locator opens with, which the
// published sentence does not carry. It is named rather than spelled inline so
// the trim reads as "drop the dialect's root marker" rather than as an arbitrary
// character.
const recipeReferenceLocatorRoot = "."
