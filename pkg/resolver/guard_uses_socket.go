// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardUsesSocket validates that every socket name in uses blocks exists as a key
// in the source catalog entity's usesSchema. Components without usesSchema cannot
// accept uses declarations. This guarantees that every edge drawn in Step 2 has a
// corresponding usesSchema entry with a selector for Step 4 validation.
func guardUsesSocket(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	// Walk components in author order (OrderedMap iteration)
	for compName, comp := range agg.Spec.Components.All() {
		// Validate component uses against Component usesSchema
		if comp.Uses.Len() > 0 && catalog.Component != nil {
			errs = append(errs, validateUsesAgainstSchema(
				entity,
				fmt.Sprintf(".spec.components[%q].uses", compName),
				comp.Uses,
				comp.Type,
				catalog,
			)...)
		}

		// Validate feature uses against Feature usesSchema
		if catalog.Feature != nil {
			for featName, feat := range comp.Features.All() {
				if feat.Uses.Len() > 0 {
					errs = append(errs, validateFeatureUsesAgainstSchema(
						entity,
						fmt.Sprintf(".spec.components[%q].features[%q].uses", compName, featName),
						feat.Uses,
						feat.Type,
						catalog,
					)...)
				}
			}

			for secName, sec := range comp.Secrets.All() {
				if sec.Uses.Len() > 0 {
					errs = append(errs, validateFeatureUsesAgainstSchema(
						entity,
						fmt.Sprintf(".spec.components[%q].secrets[%q].uses", compName, secName),
						sec.Uses,
						sec.Type,
						catalog,
					)...)
				}
			}
		}
	}

	// Walk subsystems recursively (author order)
	for subName, sub := range agg.Spec.Subsystems.All() {
		errs = append(errs, guardUsesSocketSubsystem(entity, ".spec.subsystems."+subName, sub, catalog)...)
	}

	sortResolverErrors(errs)
	return errs
}

func guardUsesSocketSubsystem(entity, path string, sub types.Subsystem, catalog CatalogLookup) []*ResolverError {
	var errs []*ResolverError

	for compName, comp := range sub.Components.All() {
		if comp.Uses.Len() > 0 && catalog.Component != nil {
			errs = append(errs, validateUsesAgainstSchema(
				entity,
				fmt.Sprintf("%s.components[%q].uses", path, compName),
				comp.Uses,
				comp.Type,
				catalog,
			)...)
		}

		if catalog.Feature != nil {
			for featName, feat := range comp.Features.All() {
				if feat.Uses.Len() > 0 {
					errs = append(errs, validateFeatureUsesAgainstSchema(
						entity,
						fmt.Sprintf("%s.components[%q].features[%q].uses", path, compName, featName),
						feat.Uses,
						feat.Type,
						catalog,
					)...)
				}
			}

			for secName, sec := range comp.Secrets.All() {
				if sec.Uses.Len() > 0 {
					errs = append(errs, validateFeatureUsesAgainstSchema(
						entity,
						fmt.Sprintf("%s.components[%q].secrets[%q].uses", path, compName, secName),
						sec.Uses,
						sec.Type,
						catalog,
					)...)
				}
			}
		}
	}

	// Recurse into nested subsystems
	for nestedName, nested := range sub.Subsystems.All() {
		errs = append(errs, guardUsesSocketSubsystem(entity, path+".subsystems."+nestedName, nested, catalog)...)
	}

	return errs
}

func validateUsesAgainstSchema(entity, fieldPath string, uses types.OrderedMap[interface{}], compType string, catalog CatalogLookup) []*ResolverError {
	catComp, found := catalog.Component(compType)
	if !found {
		return nil // catalog not available, skip
	}

	if catComp.Spec.UsesSchema.Len() == 0 {
		// Component has no usesSchema — uses is not allowed
		var errs []*ResolverError
		for socket := range uses.All() {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("component type %q has no usesSchema — uses declarations are not allowed", compType),
				Err:     ErrUnknownUsesSocket,
			})
		}
		return errs
	}

	// Validate each socket name exists in usesSchema
	var errs []*ResolverError
	for socket := range uses.All() {
		if _, ok := catComp.Spec.UsesSchema.Get(socket); !ok {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("socket %q not defined in component type %q usesSchema (valid: %v)", socket, compType, catComp.Spec.UsesSchema.Keys()),
				Err:     ErrUnknownUsesSocket,
			})
		}
	}
	return errs
}

func validateFeatureUsesAgainstSchema(entity, fieldPath string, uses types.OrderedMap[interface{}], featType string, catalog CatalogLookup) []*ResolverError {
	catFeat, found := catalog.Feature(featType)
	if !found {
		return nil
	}

	if catFeat.Spec.UsesSchema.Len() == 0 {
		var errs []*ResolverError
		for socket := range uses.All() {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("feature type %q has no usesSchema — uses declarations are not allowed", featType),
				Err:     ErrUnknownUsesSocket,
			})
		}
		return errs
	}

	var errs []*ResolverError
	for socket := range uses.All() {
		if _, ok := catFeat.Spec.UsesSchema.Get(socket); !ok {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("socket %q not defined in feature type %q usesSchema (valid: %v)", socket, featType, catFeat.Spec.UsesSchema.Keys()),
				Err:     ErrUnknownUsesSocket,
			})
		}
	}
	return errs
}
