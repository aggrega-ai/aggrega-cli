// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// CatalogLookup is the resolver-side adapter to catalog reads. The two
// function fields return a typed entity for a type URI, plus a found flag;
// the resolver guards consult them to validate Aggregate-side type
// references against catalog declarations (usesSchema, requiredFeatures,
// secretsSchema, label partitions). The typical caller — cmd/aggrega/build.go
// — supplies the lookup via cat.CatalogLookup() after loading the catalog.
//
// Either function field may be nil; guards that need a missing function
// silently skip rather than erroring, since the resolver runs in catalog-less
// mode for some test paths.
type CatalogLookup struct {
	// Component returns the catalog Component for a type URI. Returns false if not found.
	Component func(typeURI string) (types.Component, bool)
	// Feature returns the catalog Feature for a type URI. Returns false if not found.
	Feature func(typeURI string) (types.Feature, bool)
	// SchemaLibraries is the catalog's resolved SchemaLibrary set keyed by
	// library metadata.name, used to resolve cross-library schema `$ref`s when
	// compiling a Component's schema (whole-pillar and base+flavor composition).
	// May be nil in catalog-less or not-yet-primed paths; consumers that need it
	// guard on nil and fall back to inline-only behavior.
	SchemaLibraries map[string]types.SchemaLibrary
}

// RunGuards executes the catalog-aware semantic checks of Step 1 against the
// resolved Aggregate and Release. Each guard returns an internally-sorted
// slice; RunGuards appends them in call order without re-sorting, so the
// returned violations are grouped by guard:
//
//  1. Uses-socket validation — socket names must exist in usesSchema.
//  2. Self-inject validation — allowlist membership and delivery-key collision.
//  3. Inject key validation — inject from-keys must reference target's provides.
//  4. Credential label partition — credentials in secrets:, non-credentials in features:.
//  5. requiredFeatures completeness — each declared requirement satisfied.
//  6. Release feature merge — type agreement on dual-declared features.
//  7. Required uses-socket population — usesSchema.required enforcement.
//
// All violations are collected; no guard short-circuits the others. Within a
// guard, errors sort by (Entity, Field) for byte-identical output across
// runs of the same input. Across guards, the per-guard grouping is preserved
// so error output reads as a per-violation-class report.
//
// Returns nil when agg is nil. Individual guards skip silently when their
// required CatalogLookup function is nil.
func RunGuards(agg *types.Aggregate, release *types.Release, catalog CatalogLookup, boundaryInfra map[string]*BoundaryInfra) []*ResolverError {
	var errs []*ResolverError
	errs = append(errs, guardUsesSocket(agg, catalog)...)
	errs = append(errs, guardSelfInject(agg, catalog)...)
	errs = append(errs, guardInjectKeys(agg, catalog)...)
	errs = append(errs, guardCredentialPartition(agg, catalog)...)
	errs = append(errs, guardRequiredFeatures(agg, release, catalog)...)
	errs = append(errs, guardFeatureMerge(agg, release)...)
	errs = append(errs, guardRequiredUsesSockets(agg, catalog)...)
	_ = boundaryInfra // reserved for the .infra pillar validator (Step 1c) once it lands
	return errs
}
