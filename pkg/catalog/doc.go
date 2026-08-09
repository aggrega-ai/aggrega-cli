// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package catalog is the single source of truth for entity loading from
// filesystem-resident YAML. It walks one or more directory trees, identifies
// each .yaml / .yml file's entity kind from the top-level "kind:" field
// (Component, Feature, Recipe, FeatureRecipe, Aggregate, SchemaLibrary,
// MappingLibrary, WorkloadLibrary, ArtifactsLibrary, Plugin — the
// catalog-resident kinds; Manifest and Release are deployment-side and live
// outside the catalog), unmarshals into the matching pkg/types struct, and
// indexes the result for in-process lookup.
//
// The Catalog struct exposes two surfaces over the same loaded data:
//
//   - Public maps (Components, Features, Recipes, FeatureRecipes, Aggregates)
//     — direct read access for callers that want to iterate or
//     inspect (e.g. cmd/aggrega/inspect.go, the build command's deprecation
//     warning loop).
//   - Lookup-callback returners (CatalogLookup, RecipeLookup,
//     SubAggregateLoader) — adapter shapes the resolver,
//     renderer, and topology packages each define on their own side. The
//     catalog returns instances that conform to the consumer-defined
//     callback interface, which lets each consumer evolve its data needs
//     without forcing the catalog to re-export internal map shapes.
//
// ResolveAllExtends walks the loaded Component / Feature entries and
// resolves each extends chain: labels and annotations merge per key with
// the extending entity winning a shared key, and the entity's authored
// Schema content composes under JSON Schema composition. Every other
// metadata field is the extending entity's own, so identity and provenance
// never inherit. The merge reports a child that widens a parent constraint as a
// non-halting side-band signal; a narrow-only rule is opt-in policy
// (the publishing-discipline plugin, at publish time), not a compiler check.
// Recipe and FeatureRecipe split into separate index maps keyed by
// spec.implements so their distinct schemas and roles stay distinct.
//
// LoadAll merges multiple directories with last-write-wins semantics, which
// supports layered catalogs of the form `base/` + `recipes/<variant>/` —
// the variant directory's Recipe (keyed by spec.implements) replaces the
// base entry of the same key without further ceremony. Layering is the
// authoring path the in-tree fixture testdata and the aggrega-hub catalog
// repo both rely on.
//
// Status. The package ships filesystem-only resolution. Aggrega's
// reference-resolution model has three tiers — explicit local path,
// workspace+cache, remote OCI registry — and this package implements the
// first tier only; the workspace+cache and remote-OCI tiers land when the
// registry-side wiring is taken off the deferral list.
//
// Determinism. Iteration over loaded entities is sorted by name where
// ordering is observable (ResolveAllExtends, DeprecationWarnings); load
// order from filepath.WalkDir is filesystem-defined and feeds only the
// unordered last-write-wins maps.
package catalog
