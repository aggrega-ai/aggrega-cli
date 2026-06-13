// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// guardFeatureMerge validates type agreement on features that the Aggregate
// and Release both declare for the same component instance. When a feature
// name appears in both surfaces, the two declarations merge into a single
// feature attachment in downstream phases — the Aggregate side carries
// values, the Release side carries infra. The merge only makes sense when
// the two sides agree on the type URI; a disagreement indicates a
// configuration error.
//
// Emits ErrFeatureMergeConflict for each (component, feature) pair where
// both sides declare a non-empty type and the types differ. Features that
// appear on only one side are not errors — the Aggregate may declare a
// feature the Release does not (the Release adds infra optionally), and
// the Release may add features absent from the Aggregate (per the
// boundary-feature semantics). Errors sort by (Entity, Field) for
// byte-identical output. Returns nil when either input is nil.
func guardFeatureMerge(na types.NormalizedAggregate, release *types.Release) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || release == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for _, p := range collectComponentInstances(na) {
		compName, comp := p.name, p.inst
		base := componentFieldPath(p.subsystemPath, p.name, p.synthesized)
		releaseFeatures := collectReleaseBoundaryFeaturesForComponent(release.Spec.Boundaries, compName)

		for featName, aggFeat := range comp.Features.All() {
			relFeat, ok := releaseFeatures[featName]
			if !ok {
				continue // Feature only in Aggregate — no merge needed
			}
			// If Release specifies a type, it must match the Aggregate's type
			if relFeat.Type != "" && aggFeat.Type != "" && relFeat.Type != aggFeat.Type {
				errs = append(errs, &ResolverError{
					Entity:  entity,
					Field:   fmt.Sprintf("%s.features[%q]", base, featName),
					Message: fmt.Sprintf("feature type mismatch: Aggregate declares %q but Release declares %q", aggFeat.Type, relFeat.Type),
					Err:     ErrFeatureMergeConflict,
				})
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

// collectReleaseBoundaryFeaturesForComponent walks the boundary tree
// recursively and returns the union of feature attachments declared for the
// named component across every boundary that contains it. The walk is
// depth-first over the OrderedMap; on duplicate feature names across
// nested boundaries (the same feature attached at more than one boundary
// level), the deepest write wins.
func collectReleaseBoundaryFeaturesForComponent(boundaries types.OrderedMap[types.Boundary], compName string) map[string]types.BoundaryFeature {
	result := map[string]types.BoundaryFeature{}
	for _, boundary := range boundaries.All() {
		if bc, ok := boundary.Components.Get(compName); ok {
			for featName, feat := range bc.Features.All() {
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
