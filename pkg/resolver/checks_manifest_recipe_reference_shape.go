// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The reading over the entries of a Manifest's recipe mapping table, expressed
// as a constraint declaration. See checks.go for what a declaration here is.
//
// It sits in its own file rather than beside the annotation-binding readings
// because the two share a stage and a source field without sharing a subject: those
// are invoked once per declared binding, this once per mapping entry, and
// grouping them under one file name would suggest a reader looking for either
// would find both.

// The words a mis-shaped reference is reported against, one per mapping tier.
//
// They are the reference KIND the tier's entries name — what the author has to
// go and correct — rather than the tier's own key, because the sentence reads "a
// recipe reference accepts …" and a tier key would read as the block rather than
// as the thing being judged. They are named here rather than written inline so
// the rule's two arms have one spelling each.
const (
	componentMappingReferenceSubject = "recipe reference"
	featureMappingReferenceSubject   = "feature recipe reference"
)

// manifestRecipeReferenceIsWellShaped refuses a Manifest mapping value carrying
// more slash-separated segments than a Recipe or FeatureRecipe reference can
// express.
//
// A reference names a recipe by its own name, optionally prefixed with the
// capability it is declared under and suffixed with a version. A value carrying
// more separators than that — a component-type-style path, most often — names no
// entity the catalog could hold, whatever the catalog holds. Judging the shape
// before anything is looked up is what makes the diagnostic honest: the recipe
// the value meant is frequently present and correctly named, and reporting it as
// absent sends the author looking for the wrong thing.
//
// It reads the two facts complete once one authored document is in hand: the
// mapping entries the table declares, which the traversal it is invoked from
// walks, and the reference one entry names. Neither needs the entity resolved,
// the catalog reached, or anything normalized — both are answers about keys the
// author wrote in the document being presented, so the rule runs as that document
// arrives.
//
// It is invoked once per entry rather than once per document, which is what lets
// it report every mis-shaped value rather than the first: the framework
// accumulates what each invocation raised, so an author who wrote several
// mis-shaped mappings sees all of them in one build instead of one per build.
//
// An entry the presented Manifest does not declare is passed over rather than
// reported. The rule is about the shape of a value the author wrote, so
// coordinates nobody wrote are not a violation of it but an absence it has
// nothing to say about — which is the question the fact's second result answers.
//
// Its detail leads with the mapping value, which is what the registered wording
// is about and so carries no caption. The segment count and the reference kind
// follow, each captioned, because the registered wording names neither and the
// locator names the entry's own site rather than either of them. The order is the
// contract the conversion in manifest_reference_shape.go composes the published
// sentence from.
var manifestRecipeReferenceIsWellShaped = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentMappingEntrySubject,
]{
	Sentinel: ErrMalformedRecipeReference(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentMappingEntrySubject,
	) []constraint.Objection {
		reference, declared := v.ManifestRecipeMappingReference(
			s.SourceFile(), s.MappingTier(), s.MappingAxis(), s.MappingKey())
		if !declared {
			return nil
		}
		segments, tooMany := RecipeReferenceSegments(reference)
		if !tooMany {
			return nil
		}
		subject := componentMappingReferenceSubject
		if s.MappingTier() == constraint.MappingTierFeatures {
			subject = featureMappingReferenceSubject
		}
		return []constraint.Objection{s.RaiseWith(ErrMalformedRecipeReference(),
			constraint.Uncaptioned(reference),
			constraint.Captioned("slash-separated segments", strconv.Itoa(segments)),
			constraint.Captioned("accepted by", subject),
		)}
	},
}

// manifestRecipeReferenceNamesABareCapability refuses a Manifest mapping value
// whose capability segment carries a version pin.
//
// A pin is written on the `capability` a Recipe or FeatureRecipe declares, where
// it selects which version of the plugin serving the capability renders it; it
// is no part of the recipe's name, and a reference names the recipe by the bare
// capability. A value spelling the pin into the reference therefore names no
// recipe whatever the catalog holds — and a lookup reaching a registry would look
// it up under the bare name it does not spell. Refusing it as a shape fault before
// anything is looked up is what keeps a pin from renaming a recipe in either
// direction.
//
// It is the sibling of [manifestRecipeReferenceIsWellShaped] over the grammar's
// other fault, and it ends silent on a value that rule refuses, so a value at
// fault both ways is reported once, as its segment count. It reads the same two
// facts, is invoked over the same entries, and raises the same condition with a
// sentence of its own, composed by its own conversion.
//
// Its detail is the mapping value, uncaptioned, then the capability segment and
// the reference kind, each captioned — the order the conversion in
// manifest_reference_shape.go composes the sentence from.
var manifestRecipeReferenceNamesABareCapability = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentMappingEntrySubject,
]{
	Sentinel: ErrMalformedRecipeReference(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentMappingEntrySubject,
	) []constraint.Objection {
		reference, declared := v.ManifestRecipeMappingReference(
			s.SourceFile(), s.MappingTier(), s.MappingAxis(), s.MappingKey())
		if !declared {
			return nil
		}
		if _, tooMany := RecipeReferenceSegments(reference); tooMany {
			return nil
		}
		segment, pinned := RecipeReferenceCapabilityPin(reference)
		if !pinned {
			return nil
		}
		subject := componentMappingReferenceSubject
		if s.MappingTier() == constraint.MappingTierFeatures {
			subject = featureMappingReferenceSubject
		}
		return []constraint.Objection{s.RaiseWith(ErrMalformedRecipeReference(),
			constraint.Uncaptioned(reference),
			constraint.Captioned("capability segment", segment),
			constraint.Captioned("accepted by", subject),
		)}
	},
}
