// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardCredentialPartition enforces that Features labeled feature.aggrega.ai/kind: secret
// appear only in secrets: blocks, and non-credential Features appear only in features: blocks.
func guardCredentialPartition(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Feature == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	var errs []*ResolverError

	for compName, comp := range agg.Spec.Components.All() {
		// Check features: block — must NOT contain credential Features
		for featName, feat := range comp.Features.All() {
			if isCredentialFeature(feat.Type, catalog) {
				errs = append(errs, &ResolverError{
					Entity:  entity,
					Field:   fmt.Sprintf(".spec.components[%q].features[%q]", compName, featName),
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
					Field:   fmt.Sprintf(".spec.components[%q].secrets[%q]", compName, secName),
					Message: fmt.Sprintf("Feature %q does not have label %s=%s and cannot be in secrets: block", sec.Type, types.FeatureKindLabel, types.FeatureKindSecret),
					Err:     ErrCredentialLabelPartition,
				})
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

// isCredentialFeature checks if a Feature type URI has the credential label.
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
