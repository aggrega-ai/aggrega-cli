// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// guardCredentialPartition enforces a bidirectional partition between
// credential and non-credential Features on each component:
//
//   - A Feature whose catalog entry carries the label
//     feature.aggrega.ai/kind=secret must appear in the component's secrets:
//     block, never in features:.
//   - A Feature whose catalog entry does not carry that label must appear in
//     the component's features: block, never in secrets:.
//
// Returns ErrCredentialLabelPartition for each mismatched placement, sorted
// by (Entity, Field) for byte-identical output across runs. Returns nil
// when agg is nil or when catalog.Feature is nil. Feature types not present
// in the catalog are treated as non-credential — an unknown type in the
// secrets: block produces a partition error; an unknown type in features:
// is left to other guards (uses-socket, requiredFeatures) to surface.
func guardCredentialPartition(na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Feature == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for _, p := range collectComponentInstances(na) {
		comp := p.inst
		base := componentFieldPath(p.subsystemPath, p.name, p.synthesized)
		// Check features: block — must NOT contain credential Features
		for featName, feat := range comp.Features.All() {
			if isCredentialFeature(feat.Type, catalog) {
				errs = append(errs, &ResolverError{
					Entity:  entity,
					Field:   fmt.Sprintf("%s.features[%q]", base, featName),
					Message: fmt.Sprintf("Feature %q has label %s=%s and must be in secrets: block, not features:", feat.Type, types.FeatureKindLabel, types.FeatureKindSecret),
					Err:     ErrCredentialLabelPartition,
				})
			}
		}

		// Check secrets: block — must ONLY contain credential Features
		for secName, sec := range comp.Secrets.All() {
			if !isCredentialFeature(sec.Type, catalog) {
				errs = append(errs, &ResolverError{
					Entity:  entity,
					Field:   fmt.Sprintf("%s.secrets[%q]", base, secName),
					Message: fmt.Sprintf("Feature %q does not have label %s=%s and cannot be in secrets: block", sec.Type, types.FeatureKindLabel, types.FeatureKindSecret),
					Err:     ErrCredentialLabelPartition,
				})
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

// isCredentialFeature reports whether the catalog Feature for a type URI
// carries the credential label feature.aggrega.ai/kind=secret. Returns
// false when catalog.Feature is nil, when the type is not in the catalog,
// or when the label is absent or has any other value.
func isCredentialFeature(typeURI string, catalog CatalogLookup) bool {
	if catalog.Feature == nil {
		return false
	}
	feat, found := catalog.Feature(typeURI)
	if !found {
		return false
	}
	kind, _ := feat.Metadata.Labels.Get(types.FeatureKindLabel)
	return kind == types.FeatureKindSecret
}
