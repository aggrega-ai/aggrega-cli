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
//
// It shares its rule, though not its file, with the reading over a malformed pin
// declared in checks_manifest_mapping_pin.go. Both ask manifestMappingRefused
// whether the mapping-consistency rule refuses an entry, and they part on what the
// refusal is: a refusal whose key and implements are well-formed pins is a
// mismatch and is reported here, and one whose key or implements carries a
// malformed pin is reported there.
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
		refused, ok := manifestMappingRefused(s,
			v.ManifestRecipeMappingReference, v.MappedRecipeImplements, v.MappedTypeExtendsAncestry)
		if !ok {
			return nil
		}
		// A refusal whose key or implements carries a malformed pin is passed over
		// here and reported by the reading over the pin: the pin names no version,
		// so the two sides could never have matched, and "does not match" would
		// send the author comparing versions when the fix is the pin's text.
		if _, _, malformed := mappingPinAtFault(refused.key, refused.implements); malformed {
			return nil
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
			constraint.Uncaptioned(refused.key),
			constraint.Captioned("maps to", refused.reference),
			constraint.Captioned("whose spec.implements is", refused.implements),
			constraint.Captioned("written under mappings", refused.tier),
		)}
	},
}
