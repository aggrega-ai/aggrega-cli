// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package completion

import (
	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/internal/serve/standalonekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// entryPointKinds are the kinds whose position in the offered list is a
// deliberate choice rather than the vocabulary's own order: an author starting a
// new document most often reaches for one of these, so they are offered first.
//
// It fixes positions and says nothing about membership. A kind absent from it is
// still offered — it takes the vocabulary's own place behind these — so a kind
// the offered set gains reaches this surface with no edit here. A name it holds
// that the offered set does not is passed over rather than placed, so the
// preference can reorder the set and never add to it.
var entryPointKinds = []entitykind.Kind{
	entitykind.Aggregate,
	entitykind.Manifest,
	entitykind.Release,
}

// EntityKinds enumerates the values an author writes in the kind field of a
// document of their own, ordered with the kinds a new document most often starts
// from first; sortText generation uses the slice index, so a client's
// lexicographic sort reproduces this order.
//
// Membership follows the declared entity-kind vocabulary rather than being
// written out here. A list written here would agree with that vocabulary on the
// day it was typed and disagree silently the day a kind was declared, and what
// an author would then see is their kind missing from the one surface meant to
// offer it to them.
//
// Three sets of values an author might expect are absent, for three different
// reasons.
//
// Two are absent because neither is a kind. A credential is not a kind of its
// own — it is a Feature carrying the label feature.aggrega.ai/kind: secret,
// realized by a FeatureRecipe. Neither is Provider a kind: it has no schema, and
// the word names a provider by name and a Plugin's spec.provider block.
//
// A kind whose content is written inside another kind's document is absent
// because completing it here would complete a document there is nowhere to put.
// Such content carries a kind field of its own and its schema fixes the value,
// so it is a real kind with a real schema — what the fixed value does is
// identify the content where it sits, not make it a file to write.
//
// The envelope families are the wrong set to derive an authoring list from, and
// the reason is worth keeping in view: that set is deliberately wider, covering
// generated and configuration documents as well as authored entities, so a list
// taken from it whole would offer an author values no entity document accepts.
var EntityKinds = offeredEntityKinds()

// offeredEntityKinds projects the kinds written as a document of their own into
// the order this surface offers them: the entry-point kinds first, then every
// remaining kind in the vocabulary's own declared order.
//
// An entry-point name that is not among them contributes nothing, so a name that
// stops being a kind — or stops being one an author writes a document of —
// cannot inject a value no document accepts; a test reports such a name rather
// than leaving it to be dropped in silence.
func offeredEntityKinds() []string {
	declared := standalonekind.Kinds()
	offered := make(map[entitykind.Kind]bool, len(declared))
	for _, k := range declared {
		offered[k] = true
	}
	// An entry-point name is checked against the offered set rather than against
	// the whole vocabulary, so the ordering preference can move a kind's position
	// and never add one. Checked against the vocabulary it would place a kind that
	// this surface does not offer, which is the one way an ordering preference
	// could change membership.
	out := make([]string, 0, len(declared))
	placed := make(map[entitykind.Kind]bool, len(entryPointKinds))
	for _, k := range entryPointKinds {
		if offered[k] {
			out = append(out, string(k))
			placed[k] = true
		}
	}
	for _, k := range declared {
		if !placed[k] {
			out = append(out, string(k))
		}
	}
	return out
}

// APIVersions enumerates the apiVersion values a document may declare: the
// one version this compiler reads. A retired version is never offered.
var APIVersions = []string{
	apiversion.APIVersion,
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
// The keywords are readable from the schemas that declare them. A new
// extension keyword requires an entry here before it is offered.
var AggregaSchemaExtensions = []string{
	"x-aggrega-anything-at-all",
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
