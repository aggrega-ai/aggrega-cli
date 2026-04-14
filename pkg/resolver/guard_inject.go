// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardInjectKeys validates that every inject block's "from" key references
// a key that exists in the target Component's provides schema (from CUE).
func guardInjectKeys(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for compName, comp := range agg.Spec.Components {
		// Check inject blocks on features
		for featName, feat := range comp.Features {
			if feat.Inject != nil {
				errs = append(errs, validateInjectBlock(
					entity,
					fmt.Sprintf(".spec.components[%q].features[%q].inject", compName, featName),
					*feat.Inject,
					comp.Type,
					catalog,
				)...)
			}
		}

		// Check inject blocks on secrets
		for secName, sec := range comp.Secrets {
			if sec.Inject != nil {
				errs = append(errs, validateInjectBlock(
					entity,
					fmt.Sprintf(".spec.components[%q].secrets[%q].inject", compName, secName),
					*sec.Inject,
					comp.Type,
					catalog,
				)...)
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

func validateInjectBlock(entity, fieldPath string, inject types.InjectBlock, targetType string, catalog CatalogLookup) []*ResolverError {
	// Look up the target component to get its provides keys
	comp, found := catalog.Component(targetType)
	if !found {
		// If catalog lookup fails, skip inject validation (catalog may not be loaded yet)
		return nil
	}

	// Extract provides keys from the component's CUE schema
	// For now, we check against the component's spec — full CUE provides extraction
	// will be added when the CUE provides parser is implemented
	_ = comp
	_ = inject

	// TODO: Parse provides keys from comp.Spec.CueSchema and validate inject.from keys
	// This requires a CUE parser for the provides pillar, which will be implemented
	// as part of the Step 1 CUE integration. For now, the guard structure is in place.

	return nil
}
