// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package completion

// EntityKinds enumerates the entity kinds the daemon recognizes.
// Order follows the typical operator-authoring sequence — the most
// common entry-point kinds come first; sortText generation uses the
// slice index so the client's lexicographic sort matches this order.
//
// The set is closed: these twelve are every value an author may write in
// an entity document's kind field. Two kinds an author might expect are
// deliberately absent. A credential is not a kind of its own — it is a
// Feature carrying the label feature.aggrega.ai/kind: secret, realized by
// a FeatureRecipe. Neither is Provider a kind: it has no schema and no Go
// type, and the word names a provider by name and a Plugin's spec.provider
// block.
//
// Derive an authoring-kind list from this slice rather than from
// envelope.Kind. That type is deliberately wider — beyond these twelve it
// carries AggregaConfig, Lockfile, Workload and IR, so a switch generated
// from it would offer an author four values no entity document accepts.
var EntityKinds = []string{
	"Aggregate",
	"Manifest",
	"Release",
	"Component",
	"Feature",
	"Recipe",
	"FeatureRecipe",
	"Plugin",
	"SchemaLibrary",
	"MappingLibrary",
	"WorkloadLibrary",
	"ArtifactsLibrary",
}

// APIVersions enumerates the known apiVersion values. Single-item
// today; future schema iterations may add v1beta1, v2alpha1, etc.
var APIVersions = []string{
	"aggrega.ai/v1alpha1",
}

// ComponentCategories is the closed-set candidate list for the
// component.aggrega.ai/category label. Empty today — the
// canonical category enumeration has not been surfaced. The handler
// fires on the cursor position regardless; an empty candidate list is
// the LSP convention for "no completions available." When a canonical
// source surfaces, the slice populates without dispatcher changes.
var ComponentCategories = []string{}

// JSONSchemaKeywords enumerates the vanilla JSON Schema 2020-12
// keywords completion offers for cursor positions inside spec.schema.
// Alphabetical order; sortText sorts these BEFORE Aggrega extensions.
var JSONSchemaKeywords = []string{
	"$id",
	"$ref",
	"$schema",
	"additionalProperties",
	"allOf",
	"anyOf",
	"const",
	"description",
	"else",
	"enum",
	"format",
	"if",
	"items",
	"maxLength",
	"maximum",
	"minLength",
	"minimum",
	"not",
	"oneOf",
	"pattern",
	"properties",
	"required",
	"then",
	"type",
}

// SchemaTypePrimitives enumerates the JSON Schema 2020-12 primitive
// type names that appear as values of the `type:` keyword. Used by
// the schema-VALUE completion handler for both scalar and array
// forms of the type: keyword (e.g., `properties.<f>.type: <here>`,
// `items.type: <here>`, `spec.schema.type: <here>`, AND array-form
// `type: [<here>, ...]`).
//
// Order matches the JSON Schema spec's natural listing — `object` and
// `string` (the two most common authoring choices) come first.
var SchemaTypePrimitives = []string{
	"object",
	"string",
	"integer",
	"number",
	"boolean",
	"array",
	"null",
}

// AggregaSchemaExtensions enumerates the x-aggrega-* schema extension
// keywords Aggrega catalogs use. Sorted alphabetically; the
// sortText prefix places them AFTER vanilla JSON Schema keywords so
// operators see them grouped at the bottom of completion lists.
//
// Grep-discoverable from the codebase. New extensions added in future
// schema work require updating this table.
var AggregaSchemaExtensions = []string{
	"x-aggrega-anything-at-all",
	"x-aggrega-discouragement",
	"x-aggrega-extensions",
	"x-aggrega-inject",
	"x-aggrega-narrowed-from",
	"x-aggrega-selector",
	"x-aggrega-sensitive",
	"x-aggrega-source",
	"x-aggrega-source-chain",
	"x-aggrega-validate",
	"x-aggrega-violator",
}
