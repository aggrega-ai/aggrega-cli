// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LintAttachmentSlotNearMiss reports each attachment whose name is not a
// declared slot on its component's catalog type but is within a small edit
// distance of one — the shape a slot-name typo takes.
//
// An attachment name that matches no declared slot is legal: the attachment
// surface is open, so a deployment may attach a Feature under any name and the
// entity's declared slots constrain only the fills that bind them. That
// openness is what lets one entity serve both a slot-filled attachment and an
// attachment shared at a broader scope, and it is deliberately not a halt.
//
// The cost of the openness is that a misspelled slot name silently becomes a
// free attachment instead of filling the slot the author meant. This lint is
// the mitigation: it names both spellings so the author can see the pair. It is
// advisory and never changes the build's verdict — the compiler does not police
// what an author names an attachment.
//
// Each finding carries a Field locator naming the authoring site, so a front end
// that maps locators to source positions can place the advisory on the offending
// key rather than at the head of the file.
//
// Walk order. Component instances are walked in the order
// collectComponentInstances returns (sorted by instance name); within an
// instance, attachment names are walked in [sort.Strings] order and each
// attachment's candidate slots are compared in [sort.Strings] order, so
// repeated runs over identical input emit an identical advisory sequence.
//
// Returns nil when agg is nil or when catalog.Component is nil — a catalog-less
// path has no slot declarations to compare against.
func LintAttachmentSlotNearMiss(na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
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

// catalogComponentFor returns the catalog Component for a type URI, or nil on a
// miss. A miss is silent: an unresolvable type is another guard's report, and an
// advisory lint must not double-report it.
func catalogComponentFor(typeURI string, catalog CatalogLookup) *types.Component {
	comp, found := catalog.Component(typeURI)
	if !found {
		return nil
	}
	return &comp
}
