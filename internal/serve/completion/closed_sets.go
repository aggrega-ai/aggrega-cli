// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package completion

// EntityKinds enumerates the entity kinds the daemon recognizes.
// Order follows the typical operator-authoring sequence — the most
// common entry-point kinds come first; sortText generation uses the
// slice index so the client's lexicographic sort matches this order.
var EntityKinds = []string{
	"Aggregate",
	"Manifest",
	"Release",
	"Component",
	"Feature",
	"Recipe",
	"FeatureRecipe",
	"Provider",
	"SchemaLibrary",
	"MappingLibrary",
}

// APIVersions enumerates the known apiVersion values. Single-item
// today; future schema iterations may add v1beta1, v2alpha1, etc.
var APIVersions = []string{
	"aggrega.ai/v1alpha1",
}

// ComponentCategories is the closed-set candidate list for the
// component.aggrega.ai/category label. Empty at this WI — the
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
	"x-aggrega-source",
	"x-aggrega-source-chain",
	"x-aggrega-validate",
	"x-aggrega-violator",
}
