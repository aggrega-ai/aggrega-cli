// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The reading over what the recipe a Manifest mapping entry names declares it
// implements, expressed as a constraint declaration. See checks.go for what a
// declaration here is.
//
// It sits in its own file rather than beside the reading over the SHAPE of the
// same mapping value, for the reason that one states: rules sharing a stage and a
// subject are still separate rules, and one file holding several would suggest a
// reader looking for either would find both. The two also differ in what they
// read — the shape rule needs nothing looked up, this one needs the catalog — so
// they do not even share a stage.
var manifestMappingRecipeImplementsTheMappedType = constraint.Check[
	constraint.CatalogView,
	constraint.DocumentMappingEntrySubject,
]{
	Sentinel: ErrManifestImplementsMismatch(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.CatalogView,
		s constraint.DocumentMappingEntrySubject,
	) []constraint.Objection {
		// The name axis is passed over, and that bound is the rule's rather than
		// an omission. A name-axis entry keys an instance NAME, which the recipe's
		// implements says nothing about: a Recipe declares the type it implements
		// and never which instance may use it, so a comparison there would refuse
		// every instance whose name differs from a type — which is every one of
		// them. The type axis is where the two values are about the same thing.
		if s.MappingAxis() != constraint.MappingAxisType {
			return nil
		}
		reference, declared := v.ManifestRecipeMappingReference(
			s.SourceFile(), s.MappingTier(), s.MappingAxis(), s.MappingKey())
		if !declared {
			return nil
		}
		// An entry whose reference resolves to nothing is passed over rather than
		// reported. A missing or unresolvable recipe is a resolution concern owned
		// by the path that resolves references, which surfaces it with richer
		// context — including, for an entity skipped at a tolerant load, a replay
		// of WHY it went missing. Reporting "not found" here too would preempt
		// that better diagnostic, so this rule is the found-but-mismatched reading
		// only.
		implements, resolved := v.MappedRecipeImplements(s.MappingTier(), reference)
		if !resolved {
			return nil
		}
		key := s.MappingKey()
		if manifestMappingConsistent(implements, key) {
			return nil
		}
		// Feature mappings are judged on the direct comparison alone. A Feature's
		// extends composes its pillars rather than inheriting them, so whether a
		// FeatureRecipe written against a base serves an extending Feature is a
		// separate question this rule does not answer — and resolving an ancestry
		// in order to accept a mapping on it would be answering it.
		if s.MappingTier() != constraint.MappingTierFeatures {
			// Ancestry is read only after the direct comparison has failed, so a
			// mapping that matches outright costs no catalog walk at all.
			bases, walkable := v.MappedTypeExtendsAncestry(key)
			// A chain that did not walk leaves the mapping unjudged rather than
			// refused: the entry may be perfectly well authored, and reporting it
			// as the fault would send the author to the wrong file. The walk's own
			// fault is reported by whatever holds the resolution, once, so raising
			// here as well would report one defect twice.
			if !walkable {
				return nil
			}
			if manifestMappingSatisfiedByAncestry(implements, bases) {
				return nil
			}
		}
		// The detail leads with the mapped key, which is what the registered
		// wording is about and so carries no caption. What the entry maps to, what
		// that recipe declares, and the tier it is written under follow, each
		// captioned, because the registered wording names none of them. The tier
		// is carried because the two arms of this rule read differently in the
		// published sentence and an objection carries no subject: the conversion
		// in manifest_implements.go composes from this order, and it is the one
		// place that grammar lives.
		return []constraint.Objection{s.RaiseWith(ErrManifestImplementsMismatch(),
			constraint.Uncaptioned(key),
			constraint.Captioned("maps to", reference),
			constraint.Captioned("whose spec.implements is", implements),
			constraint.Captioned("written under mappings", s.MappingTier()),
		)}
	},
}
