// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The reading over the version pins of a Manifest mapping entry the
// mapping-consistency rule refuses, expressed as a constraint declaration. See
// checks.go for what a declaration here is.
//
// It sits in its own file rather than beside the mismatch reading it shares a rule
// with, for the reason checks_manifest_implements_consistency.go states: rules
// sharing a stage and a subject are still separate rules, and one file holding
// several would suggest a reader looking for either would find both. The two are
// asked one question — whether the mapping-consistency rule refuses this entry —
// through one predicate, manifestMappingRefused, and they part on what the
// refusal is.

// manifestMappingPinsAreWellFormed reports a Manifest mapping entry the
// mapping-consistency rule refuses because the entry's key, or the spec.implements
// of the recipe it maps to, carries a malformed version pin.
//
// A malformed pin names no version, so no key and no implements could be
// consistent with it, and reporting the refusal as a mismatch would send the author
// comparing two versions when the fault is one pin's text. So the refusal is
// reported as the malformation, with the side of the entry that carries it.
//
// It raises only where the mapping-consistency rule refuses the entry, and it asks
// that rule rather than restating it. That is what keeps it from turning a
// consistent mapping into a failure: an identical key and implements are consistent
// even when both carry the same malformed pin, because the comparison matches them
// as strings, and an entry the rule passes over for any other reason — the name
// axis, a reference that resolves to nothing, a chain that did not walk — is passed
// over here too. The mismatch reading passes over exactly the refusals this one
// raises on, so one entry is reported once.
//
// The key is judged before the implements, because the key is the value the
// Manifest being judged authored; mappingPinAtFault is where that order, and the
// reading of the one pin grammar, live.
//
// Its detail leads with the reference whose pin is malformed, which is what the
// registered wording is about and so carries no caption. The side of the entry
// carrying it, the entry's key, what the entry maps to and the tier it is written
// under follow, each captioned, because the registered wording names none of them.
// The conversion in manifest_implements.go composes the published sentence from
// this order.
var manifestMappingPinsAreWellFormed = constraint.Check[
	constraint.CatalogView,
	constraint.DocumentMappingEntrySubject,
]{
	Sentinel: types.ErrMalformedVersionPin(),
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
		pinned, side, malformed := mappingPinAtFault(refused.key, refused.implements)
		if !malformed {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(types.ErrMalformedVersionPin(),
			constraint.Uncaptioned(pinned),
			constraint.Captioned("carried in the", side),
			constraint.Captioned("of the mapping entry", refused.key),
			constraint.Captioned("maps to", refused.reference),
			constraint.Captioned("written under mappings", refused.tier),
		)}
	},
}
