// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// This file owns the regex source-of-truth for catalog-entity name
// patterns and the reserved annotation/label prefixes. Two consumers
// depend on these patterns:
//
//  1. The build-time validator at pkg/validator/ consumes the compiled
//     *regexp.Regexp values directly (in-process Go).
//  2. The publishing-linter Wasm plugin consumes the string constants,
//     re-compiles them in its own runtime, and enforces the same
//     patterns at aggrega push time.
//
// The exported strings and *Re variables MUST produce identical match
// behavior. The accompanying name_shape_test.go exercises this parity
// within the Go runtime; cross-runtime parity (Go vs the publishing-
// linter plugin's regex engine) is exercised by the publishing-linter
// plugin's own test surface, not here.
//
// Per-entity-kind name shapes are documented at
// aggrega-docs/language-reference/02-naming-conventions.md "Catalog
// Entity Naming" and detailed in the per-entity reference pages under
// aggrega-docs/language-reference/entities/. The annotation/label
// reserved-prefix family ("aggrega.ai/", "feature.aggrega.ai/", and
// the future per-entity-kind sub-prefixes) is documented at
// aggrega-docs/language-reference/vocabulary/social-contracts.md.

package catalog

import "regexp"

// --- Reserved annotation/label prefixes ---
//
// Catalog content MUST NOT bind annotation or label keys under these
// reserved prefixes without authority. The publishing-linter rejects
// catalog content that does; the build-time linter emits a warning.

// ReservedAggregaAIPrefix matches the bare "aggrega.ai/" annotation
// prefix. This prefix is reserved for platform-native annotation keys
// declared by Aggrega itself; catalog content carrying a key with this
// prefix triggers the publishing-linter's reserved-prefix rule.
const ReservedAggregaAIPrefix = `^aggrega\.ai/`

// ReservedFeatureAggregaAIPrefix matches the "feature.aggrega.ai/"
// sub-prefix. This sub-namespace is reserved for Feature-scoped
// catalog conventions (well-known Feature-entity annotations). Catalog
// content carrying a key with this prefix outside a Feature-authoring
// context triggers the publishing-linter's reserved-sub-prefix rule.
const ReservedFeatureAggregaAIPrefix = `^feature\.aggrega\.ai/`

// ReservedAggregaAIPrefixRe is the compiled form of
// ReservedAggregaAIPrefix.
var ReservedAggregaAIPrefixRe = regexp.MustCompile(ReservedAggregaAIPrefix)

// ReservedFeatureAggregaAIPrefixRe is the compiled form of
// ReservedFeatureAggregaAIPrefix.
var ReservedFeatureAggregaAIPrefixRe = regexp.MustCompile(ReservedFeatureAggregaAIPrefix)

// TODO: per-entity-kind reserved sub-prefix authority (e.g.,
// "component.aggrega.ai/", "recipe.aggrega.ai/") is deferred until the
// per-entity-kind label/annotation expansion lands. The reserved-prefix
// social contract at
// aggrega-docs/language-reference/vocabulary/social-contracts.md names
// the families that exist today; the per-entity-kind families ship as
// follow-on work.
//
// TODO: the well-known annotation exemption list (predefined keys
// allowed under the reserved prefix) lives at
// aggrega-docs/language-reference/providers/well-known-annotations.md.
// The publishing-linter rule that consumes this list embeds it at
// rule-implementation time; the source-of-truth in this file is regex
// only, not data lists.

// --- Per-entity-kind name shapes ---
//
// Each pattern matches the metadata.name shape for one catalog entity
// kind. The patterns reflect the hub catalog's empirical reality and
// match the per-entity reference pages.

// ComponentNameShape matches three kebab-case segments separated by
// "/". Example matches: "core/datastore/mariadb",
// "core/gateway/ai-gateway", "core/ai-model/azure-openai".
const ComponentNameShape = `^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$`

// FeatureNameShape matches two kebab-case segments separated by "/".
// Example matches: "core/cryptographic-signature",
// "core/human-approval-gate".
const FeatureNameShape = `^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$`

// RecipeNameShape matches a single kebab-case leaf. The canonical
// reference URI combining provider directory + leaf is constructed at
// catalog load from filesystem path segments. Example matches:
// "mariadb", "mariadb-bitnami", "mariadb-rds".
const RecipeNameShape = `^[a-z][a-z0-9-]*$`

// FeatureRecipeNameShape matches a single kebab-case leaf, symmetric
// with RecipeNameShape. Example matches: "k8s-secret-from-parameter",
// "dotenv-secret", "compose-bridge-fr".
const FeatureRecipeNameShape = `^[a-z][a-z0-9-]*$`

// ProviderNameShape matches a single kebab-case leaf. Example matches:
// "helm", "helm-chart", "terraform", "compose", "cloudformation".
const ProviderNameShape = `^[a-z][a-z0-9-]*$`

// AggregateNameShape matches two kebab-case segments separated by "/".
// Starting pattern; the hub catalog carries no Aggregate fixtures
// today (Aggregates are developer-authored, not catalog-distributed).
// Verified against the first hub Aggregate when one ships.
const AggregateNameShape = `^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$`

// SchemaLibraryNameShape matches a PascalCase single identifier. This
// pattern is also enforced by the JSON Schema envelope at
// aggrega-schemas/json-schemas/schemalibrary-v1alpha1.schema.json;
// structural validation rejects non-conforming names at parse time.
// Example matches: "BaseComponents", "AIDatastores", "WebPrimitives".
const SchemaLibraryNameShape = `^[A-Z][A-Za-z0-9]*$`

// MappingLibraryNameShape matches the same PascalCase shape as
// SchemaLibraryNameShape; same JSON Schema envelope enforcement.
const MappingLibraryNameShape = `^[A-Z][A-Za-z0-9]*$`

// ComponentNameShapeRe is the compiled form of ComponentNameShape.
var ComponentNameShapeRe = regexp.MustCompile(ComponentNameShape)

// FeatureNameShapeRe is the compiled form of FeatureNameShape.
var FeatureNameShapeRe = regexp.MustCompile(FeatureNameShape)

// RecipeNameShapeRe is the compiled form of RecipeNameShape.
var RecipeNameShapeRe = regexp.MustCompile(RecipeNameShape)

// FeatureRecipeNameShapeRe is the compiled form of FeatureRecipeNameShape.
var FeatureRecipeNameShapeRe = regexp.MustCompile(FeatureRecipeNameShape)

// ProviderNameShapeRe is the compiled form of ProviderNameShape.
var ProviderNameShapeRe = regexp.MustCompile(ProviderNameShape)

// AggregateNameShapeRe is the compiled form of AggregateNameShape.
var AggregateNameShapeRe = regexp.MustCompile(AggregateNameShape)

// SchemaLibraryNameShapeRe is the compiled form of SchemaLibraryNameShape.
var SchemaLibraryNameShapeRe = regexp.MustCompile(SchemaLibraryNameShape)

// MappingLibraryNameShapeRe is the compiled form of MappingLibraryNameShape.
var MappingLibraryNameShapeRe = regexp.MustCompile(MappingLibraryNameShape)
