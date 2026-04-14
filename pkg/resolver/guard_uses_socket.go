package resolver

import (
	"fmt"
	"sort"

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

	// Walk components in sorted order for deterministic errors
	compNames := sortedKeys(agg.Spec.Components)
	for _, compName := range compNames {
		comp := agg.Spec.Components[compName]

		// Validate component uses against Component usesSchema
		if len(comp.Uses) > 0 && catalog.Component != nil {
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
			featNames := sortedKeysFeatures(comp.Features)
			for _, featName := range featNames {
				feat := comp.Features[featName]
				if len(feat.Uses) > 0 {
					errs = append(errs, validateFeatureUsesAgainstSchema(
						entity,
						fmt.Sprintf(".spec.components[%q].features[%q].uses", compName, featName),
						feat.Uses,
						feat.Type,
						catalog,
					)...)
				}
			}

			secNames := sortedKeysSecrets(comp.Secrets)
			for _, secName := range secNames {
				sec := comp.Secrets[secName]
				if len(sec.Uses) > 0 {
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

	// Walk subsystems recursively
	subNames := sortedKeysSubsystems(agg.Spec.Subsystems)
	for _, subName := range subNames {
		sub := agg.Spec.Subsystems[subName]
		errs = append(errs, guardUsesSocketSubsystem(entity, ".spec.subsystems."+subName, sub, catalog)...)
	}

	sortResolverErrors(errs)
	return errs
}

func guardUsesSocketSubsystem(entity, path string, sub types.Subsystem, catalog CatalogLookup) []*ResolverError {
	var errs []*ResolverError

	compNames := sortedKeysSubComponents(sub.Components)
	for _, compName := range compNames {
		comp := sub.Components[compName]
		if len(comp.Uses) > 0 && catalog.Component != nil {
			errs = append(errs, validateUsesAgainstSchema(
				entity,
				fmt.Sprintf("%s.components[%q].uses", path, compName),
				comp.Uses,
				comp.Type,
				catalog,
			)...)
		}

		if catalog.Feature != nil {
			featNames := sortedKeysFeatures(comp.Features)
			for _, featName := range featNames {
				feat := comp.Features[featName]
				if len(feat.Uses) > 0 {
					errs = append(errs, validateFeatureUsesAgainstSchema(
						entity,
						fmt.Sprintf("%s.components[%q].features[%q].uses", path, compName, featName),
						feat.Uses,
						feat.Type,
						catalog,
					)...)
				}
			}

			secNames := sortedKeysSecrets(comp.Secrets)
			for _, secName := range secNames {
				sec := comp.Secrets[secName]
				if len(sec.Uses) > 0 {
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
	nestedNames := sortedKeysSubsystems(sub.Subsystems)
	for _, nestedName := range nestedNames {
		nested := sub.Subsystems[nestedName]
		errs = append(errs, guardUsesSocketSubsystem(entity, path+".subsystems."+nestedName, nested, catalog)...)
	}

	return errs
}

func validateUsesAgainstSchema(entity, fieldPath string, uses map[string]interface{}, compType string, catalog CatalogLookup) []*ResolverError {
	catComp, found := catalog.Component(compType)
	if !found {
		return nil // catalog not available, skip
	}

	if len(catComp.Spec.UsesSchema) == 0 {
		// Component has no usesSchema — uses is not allowed
		socketNames := sortedKeysInterface(uses)
		var errs []*ResolverError
		for _, socket := range socketNames {
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
	socketNames := sortedKeysInterface(uses)
	var errs []*ResolverError
	for _, socket := range socketNames {
		if _, ok := catComp.Spec.UsesSchema[socket]; !ok {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("socket %q not defined in component type %q usesSchema (valid: %v)", socket, compType, sortedKeysUsesSchema(catComp.Spec.UsesSchema)),
				Err:     ErrUnknownUsesSocket,
			})
		}
	}
	return errs
}

func validateFeatureUsesAgainstSchema(entity, fieldPath string, uses map[string]interface{}, featType string, catalog CatalogLookup) []*ResolverError {
	catFeat, found := catalog.Feature(featType)
	if !found {
		return nil
	}

	if len(catFeat.Spec.UsesSchema) == 0 {
		socketNames := sortedKeysInterface(uses)
		var errs []*ResolverError
		for _, socket := range socketNames {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("feature type %q has no usesSchema — uses declarations are not allowed", featType),
				Err:     ErrUnknownUsesSocket,
			})
		}
		return errs
	}

	socketNames := sortedKeysInterface(uses)
	var errs []*ResolverError
	for _, socket := range socketNames {
		if _, ok := catFeat.Spec.UsesSchema[socket]; !ok {
			errs = append(errs, &ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf("%s[%q]", fieldPath, socket),
				Message: fmt.Sprintf("socket %q not defined in feature type %q usesSchema (valid: %v)", socket, featType, sortedKeysUsesSchema(catFeat.Spec.UsesSchema)),
				Err:     ErrUnknownUsesSocket,
			})
		}
	}
	return errs
}

// Sorted key helpers for deterministic iteration.

func sortedKeys(m map[string]types.ComponentInstance) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysFeatures(m map[string]types.FeatureInstance) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysSecrets(m map[string]types.SecretInstance) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysSubsystems(m map[string]types.Subsystem) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysSubComponents(m map[string]types.ComponentInstance) []string {
	return sortedKeys(m)
}

func sortedKeysInterface(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysUsesSchema(m map[string]types.UsesSchemaEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
