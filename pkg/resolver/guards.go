// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
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
	// Plugin returns the catalog Plugin entity for an entity URI, plus a
	// found flag. A plugin socket fronts the artifact-resolving cascade with this
	// lookup: entity URI → entity → its bare-identity artifact reference → cascade.
	// May be nil in catalog-less paths; a
	// socket that needs it then resolves nothing and halts as not-an-entity.
	Plugin func(uri string) (types.Plugin, bool)
	// PluginFromCache resolves a Plugin entity from the pull-populated cache by its
	// entity URI WITHOUT a lockfile pin — the cache-not-pin read a verdict socket
	// (policy / linter) falls back to when the pinned / Tier-1 path does not
	// surface the gate plugin. It returns the parsed entity, the .wasm layer
	// content digest the artifact cache slot is keyed by, and a found flag; ok is
	// false on a clean cache miss. May be nil in catalog-less paths.
	PluginFromCache func(uri string) (types.Plugin, string, bool, error)
	// ProviderPlugin resolves a provider binding to the provider-rendering Plugin
	// entity serving it, plus a found flag and an error. The binding is the bare
	// provider name ("helm") or a version-pinned form ("helm:1.0.0", "helm:^1.0.0")
	// where the pin constrains the plugin version. A pluginType: provider Plugin
	// serves the provider names in its targets; this accessor finds the
	// unique such Plugin whose version satisfies the pin (highest-in-range for a
	// caret, highest overall when unpinned). A bare miss returns (_, false, nil) so
	// the caller falls back to the in-tree engine; a version pin nothing satisfies
	// returns a non-nil error so the caller halts loudly. May be nil in catalog-less
	// paths; a consumer that needs it then resolves nothing.
	ProviderPlugin func(ref string) (types.Plugin, bool, error)
	// SchemaLibraries is the catalog's resolved SchemaLibrary set keyed by
	// library metadata.name, used to resolve cross-library schema `$ref`s when
	// compiling a Component's schema (whole-pillar and base+flavor composition).
	// May be nil in catalog-less or not-yet-primed paths; consumers that need it
	// guard on nil and fall back to inline-only behavior.
	SchemaLibraries map[string]types.SchemaLibrary
	// EffectiveSchema returns the composed (parent+child) schema for an entity
	// that declares extends:, keyed by type URI, in compactform form. Present
	// only for extends-ing entities; consumers (the schema-conformance gate,
	// the inject-key provides extraction) prefer it over compiling the entity's
	// own authored schema, so an inherited constraint is enforced on a child.
	// Returns ("", false) — i.e. (nil, false) — for a non-extends entity, which
	// falls the caller back to its authored schema. May itself be nil in
	// catalog-less / not-yet-resolved paths.
	EffectiveSchema func(typeURI string) (compactform.Schema, bool)
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
//  8. Schema conformance — authored values/infra pillars match the declared schema.
//  9. Release uses-socket validation — Release per-edge sockets exist in usesSchema.
//  10. Per-edge conformance — per-edge values/infra match the socket's per-edge schema.
//
// All violations are collected; no guard short-circuits the others. Within a
// guard, errors sort by (Entity, Field) for byte-identical output across
// runs of the same input. Across guards, the per-guard grouping is preserved
// so error output reads as a per-violation-class report.
//
// Returns nil when agg is nil. Individual guards skip silently when their
// required CatalogLookup function is nil.
func RunGuards(na types.NormalizedAggregate, release *types.Release, catalog CatalogLookup, boundaryInfra map[string]*BoundaryInfra, parameters map[string]any) []*ResolverError {
	var errs []*ResolverError
	errs = append(errs, guardUsesSocket(na, catalog)...)
	errs = append(errs, guardReleaseUsesSocket(na, release, catalog)...)
	errs = append(errs, guardSelfInject(na, catalog)...)
	errs = append(errs, guardInjectKeys(na, catalog)...)
	errs = append(errs, guardCredentialPartition(na, catalog)...)
	errs = append(errs, guardRequiredFeatures(na, release, catalog)...)
	errs = append(errs, guardFeatureMerge(na, release)...)
	errs = append(errs, guardRequiredUsesSockets(na, catalog)...)
	errs = append(errs, guardSchemaConformance(na, catalog, boundaryInfra, parameters)...)
	errs = append(errs, guardEdgeConformance(na, release, catalog, parameters)...)
	return errs
}
