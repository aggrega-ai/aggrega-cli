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

// RecipeReferenceCapabilityPin reports the capability segment a Recipe /
// FeatureRecipe reference names and whether that segment carries a version pin.
//
// A pin is no part of a recipe's name. It is written on the `capability` the
// recipe itself declares, where it selects which version of the plugin serving
// the capability renders the recipe, and a reference names the recipe by the
// capability alone — so a reference whose capability segment carries one names
// no recipe whatever the catalog holds, and is a shape fault rather than an
// absent recipe. It is judged here, beside the segment ceiling, so every reader
// of a reference's shape asks the same question.
//
// It reads the reference string only, as [RecipeReferenceSegments] does, and the
// pin is found by the one split every versioned reference goes through. A
// reference naming no capability segment reports false.
func RecipeReferenceCapabilityPin(uri string) (segment string, pinned bool) {
	segment, _, qualified := strings.Cut(uri, "/")
	if !qualified {
		return "", false
	}
	_, _, pinned = types.SplitVersionedURI(segment)
	return segment, pinned
}

// RecipeReferenceMisShaped reports whether a Recipe / FeatureRecipe reference is
// one its grammar cannot express: more segments than the ceiling, or a version
// pin in its capability segment. It is the one question a reader asks that only
// needs to know whether the reference is at fault and not which way.
func RecipeReferenceMisShaped(uri string) bool {
	if _, tooMany := RecipeReferenceSegments(uri); tooMany {
		return true
	}
	_, pinned := RecipeReferenceCapabilityPin(uri)
	return pinned
}

// PinnedCapabilityReferenceFault composes the refusal of a reference whose
// capability segment carries a version pin, naming the condition every
// mis-shaped reference reports.
//
// where says where the reader meets the reference, ending in the words that
// introduce it — a mapping entry's coordinate and "maps to", or the instance a
// mapping resolved for and "the Manifest maps it to" — so every reading that
// reports the fault composes one sentence. subject is the reference kind: a
// recipe reference or a feature recipe reference.
func PinnedCapabilityReferenceFault(where, reference, segment, subject string) error {
	return fmt.Errorf(
		"%w: %s %q, which carries a version pin in its capability segment %q; a pin belongs on the "+
			"`capability` the recipe declares, and a %s names the recipe by its bare capability, as %s; "+
			"correct the mapping value in the Manifest",
		ErrMalformedRecipeReference(), where, reference, segment, subject, AcceptedRecipeReferenceShapes)
}

// ManifestRecipeReferenceFaults reports every mapping value in a composed
// Manifest whose shape a Recipe or FeatureRecipe reference cannot express, one
// fault per value, in the order the Manifest declares them.
//
// A value is mis-shaped two ways, each a rule of its own with its own sentence:
// it carries more slash-separated segments than a reference accepts, or its
// capability segment carries a version pin, which belongs on the `capability` a
// recipe declares and is no part of the name a reference uses.
//
// The verdict is purely syntactic: it reads the separators and the capability
// segment of a string and needs no catalog at all. That is what lets it run
// before anything resolves — and running it there is the point. A mis-shaped
// value can never match a catalog entity, so whatever gate looks the value up
// first will report it as an entity the catalog does not hold — or, for a pinned
// capability, look it up in a registry under the bare name it does not spell —
// which sends the author looking for a missing recipe that is in fact present and
// correctly named. Judging the shape first means the shape fault is reported as
// a shape fault, before anything is fetched, and every absent-entity report that
// follows is true.
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
	// The two rules are asked of each entry in turn, the segment ceiling first, so
	// the faults arrive in the order the Manifest declares its entries whichever
	// rule raised each; the pin rule ends silent on a value the ceiling refuses, so
	// one value is reported once.
	var faults []error
	for _, entry := range presented.ManifestRecipeMappingEntries(composedManifestHasNoFile) {
		one := []constraint.ManifestRecipeMapping{entry}
		for _, o := range constraint.RunOnDocumentMappingEntries(
			composedManifestHasNoFile, one, presented, manifestRecipeReferenceIsWellShaped) {
			faults = append(faults, recipeReferenceFaultFrom(o))
		}
		for _, o := range constraint.RunOnDocumentMappingEntries(
			composedManifestHasNoFile, one, presented, manifestRecipeReferenceNamesABareCapability) {
			faults = append(faults, pinnedReferenceFaultFrom(o))
		}
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

// pinnedReferenceFaultFrom renders one objection of the pinned-capability rule,
// the sibling of [recipeReferenceFaultFrom] for the rule's other fault. Its
// sentence is composed by [PinnedCapabilityReferenceFault], the one composer
// every reading of that fault shares, and its values are read in the order
// [manifestRecipeReferenceNamesABareCapability] supplies them, asserted for the
// reason the sibling asserts its own.
func pinnedReferenceFaultFrom(o constraint.Objection) error {
	detail := o.Detail()
	if len(detail) != pinnedReferenceDetailCount {
		panic(fmt.Sprintf(
			"resolver: the pinned-capability reference rule supplied %d value(s) and this conversion "+
				"composes its sentence from %d; the rule and its conversion are two halves of one "+
				"sentence, so change them together",
			len(detail), pinnedReferenceDetailCount))
	}
	return PinnedCapabilityReferenceFault(
		strings.TrimPrefix(o.Locator(), recipeReferenceLocatorRoot)+" maps to",
		detail[pinnedReferenceDetailValue],
		detail[pinnedReferenceDetailSegment],
		detail[pinnedReferenceDetailSubject],
	)
}

// The order the pinned-capability reference rule supplies its values in.
const (
	pinnedReferenceDetailValue = iota
	pinnedReferenceDetailSegment
	pinnedReferenceDetailSubject
	pinnedReferenceDetailCount
)

// recipeReferenceLocatorRoot is the separator a locator opens with, which the
// published sentence does not carry. It is named rather than spelled inline so
// the trim reads as "drop the dialect's root marker" rather than as an arbitrary
// character.
const recipeReferenceLocatorRoot = "."
