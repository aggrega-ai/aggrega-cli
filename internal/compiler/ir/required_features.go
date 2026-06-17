// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"errors"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// validateNodeRequiredFeatures enforces a flattened sub-aggregate node's
// component requiredFeatures contract against that node's merged attachment
// set. It is the post-flatten counterpart of the Step-1 resolver guard
// (guardRequiredFeatures): the Step-1 guard runs over the NormalizedAggregate
// before topology flattening and therefore covers root and subsystem
// components only, while flattened sub-aggregate nodes (SourceAggregate != "")
// do not exist until topology.Build. This pass closes that gap with parity to
// the Step-3 attachment-conformance gate, walking the same per-node merged
// feature set.
//
// The satisfying set is the union of feature types across the node's merged
// Features and Secrets buckets — a credential feature routes into Secrets, so
// a required feature satisfied by a credential lives there; excluding Secrets
// would false-halt. A required type matches by exact type equality.
//
// Silent skips: components whose type is not in the catalog, and components
// whose catalog entry declares an empty requiredFeatures array. Returns nil
// when the catalog has no Component lookup. Errors are returned unsorted;
// joinRequiredFeatures canonicalises the combined set.
func validateNodeRequiredFeatures(
	nodeID string,
	componentType string,
	feats types.OrderedMap[types.IRFeatureEntry],
	secrets types.OrderedMap[types.IRSecretEntry],
	catalog resolver.CatalogLookup,
	entity string,
) []*resolver.ResolverError {
	if catalog.Component == nil {
		return nil
	}
	catComp, found := catalog.Component(componentType)
	if !found || len(catComp.Spec.RequiredFeatures) == 0 {
		return nil
	}

	attached := attachedFeatureTypes(feats, secrets)

	var errs []*resolver.ResolverError
	for _, required := range catComp.Spec.RequiredFeatures {
		if !attached[required] {
			errs = append(errs, &resolver.ResolverError{
				Entity:  entity,
				Field:   fmt.Sprintf(".spec.components[%q]", nodeID),
				Message: fmt.Sprintf("component type %q requires feature %q but it is not attached", componentType, required),
				Err:     resolver.ErrMissingRequiredFeature,
			})
		}
	}
	return errs
}

// attachedFeatureTypes returns the per-node satisfying set: the union of
// non-empty feature types across the merged Features and Secrets buckets. The
// map is a presence-set; values are always true.
func attachedFeatureTypes(feats types.OrderedMap[types.IRFeatureEntry], secrets types.OrderedMap[types.IRSecretEntry]) map[string]bool {
	attached := map[string]bool{}
	for _, entry := range feats.All() {
		if entry.Type != "" {
			attached[entry.Type] = true
		}
	}
	for _, entry := range secrets.All() {
		if entry.Type != "" {
			attached[entry.Type] = true
		}
	}
	return attached
}

// joinRequiredFeatures canonicalises the collected missing-required-feature
// violations by (Entity, Field) — byte-identical across runs of the same
// input, matching the Step-1 guard's ordering — and joins them into one error.
// The joined error wraps every violation, so errors.Is(err,
// ErrMissingRequiredFeature) holds for the build halt.
func joinRequiredFeatures(errs []*resolver.ResolverError) error {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Entity != errs[j].Entity {
			return errs[i].Entity < errs[j].Entity
		}
		return errs[i].Field < errs[j].Field
	})
	wrapped := make([]error, len(errs))
	for i, e := range errs {
		wrapped[i] = e
	}
	return errors.Join(wrapped...)
}
