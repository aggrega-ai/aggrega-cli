// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// This file owns the regex source-of-truth strings for catalog-entity
// metadata.name shapes. It declares patterns only; no Go code in this
// module matches against them. The name-shape rule itself runs in the
// publishing linter, whose Rust implementation carries its own copy of
// each pattern and reports a finding when an author runs aggrega lint.
//
// Two consumer relationships exist, and both read this file as SOURCE
// TEXT rather than importing it:
//
//   - The publishing linter's symmetry test
//     (plugins/publishing-linter/tests/name_shape_symmetry_test.rs)
//     greps for the literal prefix "const <Name> = " followed by a
//     backtick-quoted string, extracts every per-kind pattern, and
//     compares each to the corresponding Rust constant in
//     plugins/publishing-linter/src/rules/name_shape.rs, re-compiling
//     in its own regex engine. The comparison runs in both directions,
//     so a pattern declared on one side only fails there.
//   - This package's own coverage test (name_shape_coverage_test.go)
//     extracts the same declarations to hold this set against the
//     entity-kind vocabulary, so a kind the conventions rules apply to
//     cannot end up with its name checked by nothing.
//
// The constant name is derived rather than listed by both: the
// constant for a kind is exactly "<Kind>NameShape".
//
// The literal declaration form is therefore load-bearing. Each pattern
// MUST stay on one line as `const <Name> = ` plus a backtick-quoted
// string. Grouping the declarations into a const block, reflowing them
// across lines, switching to double quotes, or turning one into a
// reference to another package's constant all break the extraction
// silently. The symmetry test skips gracefully when this file is
// unreachable from a standalone plugin checkout, and fails hard rather
// than skipping when UMBRELLA_CHECKOUT is set.
//
// Per-entity-kind name shapes are documented at
// aggrega-docs/language-reference/02-naming-conventions.md "Catalog
// Entity Naming" and detailed in the per-entity reference pages under
// aggrega-docs/language-reference/entities/.

package catalog

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
//
// A second spelling of this pattern lives in pkg/types, which is the
// one the compiler consults when it validates a required-attachment
// type value. Neither can be folded into the other: this spelling is
// pinned by path and literal form by the cross-language symmetry check
// described in the file comment above, and pkg/catalog sits above the
// compiler in the import graph, so a compiler pass importing it would
// make an earlier pipeline stage depend on a later one. The pair is
// held byte-identical by TestFeatureNameShapeParity.
const FeatureNameShape = `^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$`

// RecipeNameShape matches a single kebab-case leaf. The canonical
// reference URI combining capability + leaf is constructed at catalog
// load. Example matches: "mariadb", "mariadb-bitnami", "mariadb-rds".
const RecipeNameShape = `^[a-z][a-z0-9-]*$`

// FeatureRecipeNameShape matches a single kebab-case leaf, symmetric
// with RecipeNameShape. Example matches: "k8s-secret-from-parameter",
// "dotenv-secret", "compose-bridge-fr".
const FeatureRecipeNameShape = `^[a-z][a-z0-9-]*$`

// AggregateNameShape matches two kebab-case segments separated by "/".
// Starting pattern; the hub catalog carries no Aggregate fixtures
// today (Aggregates are developer-authored, not catalog-distributed).
// Verified against the first hub Aggregate when one ships.
const AggregateNameShape = `^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$`

// --- Library name shapes ---
//
// Every library kind carries the same PascalCase shape, and every
// library kind's JSON Schema envelope declares that pattern on
// metadata.name, so structural validation rejects a non-conforming
// name at parse time and the name-shape check below reports it at
// author time. The four patterns are declared separately rather than
// as one shared constant because each is pinned individually against
// its counterpart in the linter, and a shared constant would collapse
// four independent pins into one.

// SchemaLibraryNameShape matches a PascalCase single identifier.
// Example matches: "BaseComponents", "AIDatastores", "WebPrimitives".
const SchemaLibraryNameShape = `^[A-Z][A-Za-z0-9]*$`

// MappingLibraryNameShape matches the same PascalCase shape as
// SchemaLibraryNameShape. Example match: "BaseMappings".
const MappingLibraryNameShape = `^[A-Z][A-Za-z0-9]*$`

// WorkloadLibraryNameShape matches the same PascalCase shape as
// SchemaLibraryNameShape. Example match: "BaseWorkloads".
const WorkloadLibraryNameShape = `^[A-Z][A-Za-z0-9]*$`

// ArtifactsLibraryNameShape matches the same PascalCase shape as
// SchemaLibraryNameShape.
const ArtifactsLibraryNameShape = `^[A-Z][A-Za-z0-9]*$`
