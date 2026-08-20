// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// lintAttachmentSlotNearMiss reads each attachment whose name is not a declared
// slot on its component's catalog type but is within a small edit distance of
// one — the shape a slot-name typo takes.
//
// 🔴 It is NOT the production reading. The rule reaches a build through the
// declared check the advisory suite invokes; this is the second, independent
// implementation the equivalence cross-check compares that reading against, and
// it is retained for exactly that. A cross-check sharing an implementation with
// what it checks cannot see the two drift apart, so this must not be folded into
// the declared check or re-expressed in terms of it — and it must not be wired
// into a build, which would report one authoring fault twice. It is unexported
// so that no caller outside this package can restore it to a production path.
//
// What the two legitimately share is one definition of what counts as a near
// miss: both reach [nearMissCandidates]. What the cross-check compares are their
// conclusions.
//
// An attachment name that matches no declared slot is legal: the attachment
// surface is open, so a deployment may attach a Feature under any name and the
// entity's declared slots constrain only the fills that bind them. That
// openness is what lets one entity serve both a slot-filled attachment and an
// attachment shared at a broader scope, and it is deliberately not a halt.
//
// The cost of the openness is that a misspelled slot name silently becomes a
// free attachment instead of filling the slot the author meant. Naming both
// spellings so the author can see the pair is the mitigation.
//
// Walk order. Component instances are walked in the order
// collectComponentInstances returns (sorted by instance name); within an
// instance, attachment names are walked in [sort.Strings] order and each
// attachment's candidate slots are compared in [sort.Strings] order. That order
// is this implementation's own and is deliberately NOT the declared check's,
// which invokes in the order the document declares; the cross-check therefore
// compares the two findings as sets.
//
// Returns nil when agg is nil or when catalog.Component is nil — a catalog-less
// path has no slot declarations to compare against.
func lintAttachmentSlotNearMiss(na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var out []*ResolverError

	for _, p := range collectComponentInstances(na) {
		comp := catalogComponentFor(p.inst.Type, catalog)
		if comp == nil || comp.Spec.FeaturesSchema.Len() == 0 {
			continue
		}
		slotNames := comp.Spec.FeaturesSchema.Keys()
		base := componentFieldPath(p.subsystemPath, p.name, p.synthesized)

		names := append([]string(nil), p.inst.Features.Keys()...)
		sort.Strings(names)
		for _, name := range names {
			if _, declared := comp.Spec.FeaturesSchema.Get(name); declared {
				continue
			}
			for _, slot := range nearMissCandidates(name, slotNames) {
				out = append(out, &ResolverError{
					Entity: entity,
					Field:  fmt.Sprintf("%s.features[%q]", base, name),
					Message: fmt.Sprintf(
						"attachment %q on component %q is not a declared slot of type %q, but the type declares the slot %q — if %q was meant, the attachment is currently a free attachment and does not fill it",
						name, p.name, p.inst.Type, slot, slot,
					),
					Err: ErrAttachmentSlotNearMiss(),
				})
			}
		}
	}

	return out
}
