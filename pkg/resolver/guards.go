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
//  1. Inject key validation
//  2. Credential label partition
//  3. requiredFeatures completeness
//  4. Release Feature merge
//
// All violations are collected — earlier guards do not mask later ones.
func RunGuards(agg *types.Aggregate, release *types.Release, catalog CatalogLookup) []*ResolverError {
	var errs []*ResolverError
	errs = append(errs, guardInjectKeys(agg, catalog)...)
	errs = append(errs, guardCredentialPartition(agg, catalog)...)
	errs = append(errs, guardRequiredFeatures(agg, release, catalog)...)
	errs = append(errs, guardFeatureMerge(agg, release)...)
	return errs
}
