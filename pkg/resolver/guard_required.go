// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardRequiredFeatures verifies that every type in a Component's
// requiredFeatures array is satisfied by at least one feature attachment
// on each instance of that component.
//
// The check operates on the union of feature types declared on the
// component instance (Aggregate side) plus feature types declared on
// the same component name across every Release boundary (recursively). The
// "merged" set is type-keyed: distinct feature instances of the same type
// count as one entry. Each requirement matches against this set by exact
// type equality.
//
// Scope: top-level Aggregate components only. Components inside subsystems
// are not walked here; their requiredFeatures are not validated by this
// guard.
//
// Silent skips: components whose type is not in the catalog, components
// whose catalog entry has an empty requiredFeatures array. Errors sort
// by (Entity, Field) for byte-identical output. Returns nil when agg is
// nil or catalog.Component is nil.
func guardRequiredFeatures(agg *types.Aggregate, release *types.Release, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for compName, comp := range agg.Spec.Components.All() {
		catComp, found := catalog.Component(comp.Type)
		if !found {
			continue
		}
		if len(catComp.Spec.RequiredFeatures) == 0 {
			continue
		}

		// Collect all attached feature types from Aggregate
		attachedTypes := collectAttachedFeatureTypes(comp)

		// Collect Release-level feature types for this component
		if release != nil {
			collectReleaseBoundaryFeatureTypes(release.Spec.Boundaries, compName, attachedTypes)
		}

		// Check each required feature is satisfied
		for _, required := range catComp.Spec.RequiredFeatures {
			if !attachedTypes[required] {
				errs = append(errs, &ResolverError{
					Entity:  entity,
					Field:   fmt.Sprintf(".spec.components[%q]", compName),
					Message: fmt.Sprintf("component type %q requires feature %q but it is not attached", comp.Type, required),
					Err:     ErrMissingRequiredFeature,
				})
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

// collectAttachedFeatureTypes returns the set of non-empty feature type URIs
// declared on a component instance's Aggregate-side features block.
// The map is a presence-set; values are always true.
func collectAttachedFeatureTypes(comp types.ComponentInstance) map[string]bool {
	attached := map[string]bool{}
	for _, feat := range comp.Features.All() {
		if feat.Type != "" {
			attached[feat.Type] = true
		}
	}
	return attached
}

// collectReleaseBoundaryFeatureTypes walks the boundary tree recursively
// and adds every non-empty feature type declared for the named component
// to the passed-in presence-set. Mutates the input map; does not return
// a value.
func collectReleaseBoundaryFeatureTypes(boundaries types.OrderedMap[types.Boundary], compName string, attached map[string]bool) {
	for _, boundary := range boundaries.All() {
		if bc, ok := boundary.Components.Get(compName); ok {
			for _, feat := range bc.Features.All() {
				if feat.Type != "" {
					attached[feat.Type] = true
				}
			}
		}
		// Recurse into nested boundaries
		collectReleaseBoundaryFeatureTypes(boundary.Boundaries, compName, attached)
	}
}
