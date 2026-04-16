// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// CatalogLookup resolves a component or feature type URI to its catalog definition.
type CatalogLookup struct {
	// Component returns the catalog Component for a type URI. Returns false if not found.
	Component func(typeURI string) (types.Component, bool)
	// Feature returns the catalog Feature for a type URI. Returns false if not found.
	Feature func(typeURI string) (types.Feature, bool)
}

// RunGuards executes all resolver guard checks in defined order:
//  1. Uses socket validation (socket names must exist in usesSchema)
//  2. Self-inject validation (allowlist + collision)
//  3. Inject key validation
//  4. Credential label partition
//  5. requiredFeatures completeness
//  6. Release Feature merge
//  7. cueSchema pillar validation (.values from Aggregate, .infra from Release boundary)
//
// All violations are collected — earlier guards do not mask later ones.
func RunGuards(agg *types.Aggregate, release *types.Release, catalog CatalogLookup, boundaryInfra map[string]*BoundaryInfra) []*ResolverError {
	var errs []*ResolverError
	errs = append(errs, guardUsesSocket(agg, catalog)...)
	errs = append(errs, guardSelfInject(agg, catalog)...)
	errs = append(errs, guardInjectKeys(agg, catalog)...)
	errs = append(errs, guardCredentialPartition(agg, catalog)...)
	errs = append(errs, guardRequiredFeatures(agg, release, catalog)...)
	errs = append(errs, guardFeatureMerge(agg, release)...)
	errs = append(errs, guardCueSchema(agg, catalog, boundaryInfra)...)
	return errs
}
