package resolver

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardFeatureMerge validates that when both Aggregate and Release declare the same
// feature on a component, their types match. The Aggregate provides values, the Release
// provides infra — type mismatches indicate a configuration error.
func guardFeatureMerge(agg *types.Aggregate, release *types.Release) []*ResolverError {
	if agg == nil || release == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for compName, comp := range agg.Spec.Components {
		releaseFeatures := collectReleaseBoundaryFeaturesForComponent(release.Spec.Boundaries, compName)

		for featName, aggFeat := range comp.Features {
			relFeat, ok := releaseFeatures[featName]
			if !ok {
				continue // Feature only in Aggregate — no merge needed
			}
			// If Release specifies a type, it must match the Aggregate's type
			if relFeat.Type != "" && aggFeat.Type != "" && relFeat.Type != aggFeat.Type {
				errs = append(errs, &ResolverError{
					Entity:  entity,
					Field:   fmt.Sprintf(".spec.components[%q].features[%q]", compName, featName),
					Message: fmt.Sprintf("feature type mismatch: Aggregate declares %q but Release declares %q", aggFeat.Type, relFeat.Type),
					Err:     ErrFeatureMergeConflict,
				})
			}
		}
	}

	return errs
}

func collectReleaseBoundaryFeaturesForComponent(boundaries map[string]types.Boundary, compName string) map[string]types.BoundaryFeature {
	result := map[string]types.BoundaryFeature{}
	for _, boundary := range boundaries {
		if bc, ok := boundary.Components[compName]; ok {
			for featName, feat := range bc.Features {
				result[featName] = feat
			}
		}
		// Recurse into nested boundaries
		nested := collectReleaseBoundaryFeaturesForComponent(boundary.Boundaries, compName)
		for k, v := range nested {
			result[k] = v
		}
	}
	return result
}
