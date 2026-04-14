// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardRequiredFeatures verifies that the merged feature list (Aggregate + subsystem + Release)
// satisfies every type in each Component's requiredFeatures array.
func guardRequiredFeatures(agg *types.Aggregate, release *types.Release, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for compName, comp := range agg.Spec.Components {
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

func collectAttachedFeatureTypes(comp types.ComponentInstance) map[string]bool {
	types := map[string]bool{}
	for _, feat := range comp.Features {
		if feat.Type != "" {
			types[feat.Type] = true
		}
	}
	return types
}

func collectReleaseBoundaryFeatureTypes(boundaries map[string]types.Boundary, compName string, attached map[string]bool) {
	for _, boundary := range boundaries {
		if bc, ok := boundary.Components[compName]; ok {
			for _, feat := range bc.Features {
				if feat.Type != "" {
					attached[feat.Type] = true
				}
			}
		}
		// Recurse into nested boundaries
		collectReleaseBoundaryFeatureTypes(boundary.Boundaries, compName, attached)
	}
}
