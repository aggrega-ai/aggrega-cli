// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compactform

import "strings"

// Compact-form vocabulary — the closed enumeration of keywords the
// compiler recognises in compact-form positions. JSON Schema's reserved
// keywords (type, properties, items, additionalProperties, allOf,
// anyOf, oneOf, not, if, then, else, $ref, $defs, enum, const, format,
// minimum, maximum, minLength, maxLength, minItems, maxItems, pattern,
// patternProperties, required, default, description, etc.) are valid
// in compact form as-is — content-shape dispatch handles them through
// the JSON Schema YAML path without rejection.

// Type-and-identity compact-form keywords.
var typeIdentityKeywords = map[string]bool{
	"type":        true,
	"description": true,
	"default":     true,
	"required":    true, // per-leaf marker; accumulates to parent's required array
}

// Constraint compact-form keywords. `min` and `max` are polymorphic on
// the field's declared `type:` — see emit.go for the dispatch. `const` and
// `enum` are JSON Schema's own keywords, listed here because each is a
// complete constraint on its own: a node carrying either is a constraint leaf
// whether or not a `type:` stands beside it, as JSON Schema reads it.
var constraintKeywords = map[string]bool{
	"const":     true,
	"enum":      true,
	"min":       true, // -> minimum / minLength / minItems by type
	"max":       true, // -> maximum / maxLength / maxItems by type
	"minLength": true, // explicit (overrides min dispatch)
	"maxLength": true, // explicit
	"pattern":   true,
	"format":    true,
}

// Composition compact-form keywords. The composing entries are
// independently dispatched (each entry may be compact form, JSON Schema,
// or a $ref reference).
var compositionKeywords = map[string]bool{
	"allOf":             true,
	"anyOf":             true,
	"oneOf":             true,
	"patternProperties": true,
	"valuesType":        true, // -> additionalProperties: { type: X }
}

// Container compact-form keywords. `properties` is rarely needed in
// compact form (YAML hierarchy expresses object structure implicitly);
// supplied here for callers that explicitly nest a compact-form schema
// inside a `properties` map.
var containerKeywords = map[string]bool{
	"items":                true,
	"properties":           true,
	"additionalProperties": true,
}

// Aggrega-specific compact-form keywords. `validate` / `validateMessage`
// emit `x-aggrega-validate:` extension keyword; the validator adapter
// evaluates the Go-template predicate at validation time. `when` emits
// JSON Schema if/then/else plus `x-aggrega-when:` extension carrying the
// template body. `sensitive` emits the `x-aggrega-sensitive:` extension
// keyword, which marks the leaf as carrying secret material; the marker is
// read when deriving an attachment's sensitivity, and nothing validates or
// rejects a value because of it. `$jsonschema` embeds full JSON Schema
// YAML at any node. `$mode` provides explicit dispatch override for
// ambiguous content shapes — including the case where a schema declares a
// property whose own name collides with one of these keywords.
var aggregaSpecificKeywords = map[string]bool{
	"validate":        true,
	"validateMessage": true,
	"when":            true,
	"sensitive":       true,
	"$jsonschema":     true,
	"$mode":           true,
}

// Reference keywords. `$ref` is JSON Schema-native and works the same
// way in compact form; `$defs` is the standard JSON Schema definitions
// map.
var referenceKeywords = map[string]bool{
	"$ref":  true,
	"$defs": true,
}

// jsonSchemaReservedKeywords names the JSON Schema 2020-12 keywords the
// compactform compiler recognises as JSON-Schema-native rather than
// compact-form-specific; per-node passthrough emits them unchanged. Only those
// hasSchemaContainerShape also lists steer dispatch, so `minimum` alone does not.
var jsonSchemaReservedKeywords = map[string]bool{
	"$schema":               true,
	"$id":                   true,
	"$comment":              true,
	"$vocabulary":           true,
	"$anchor":               true,
	"$dynamicRef":           true,
	"$dynamicAnchor":        true,
	"minimum":               true,
	"maximum":               true,
	"exclusiveMinimum":      true,
	"exclusiveMaximum":      true,
	"multipleOf":            true,
	"minItems":              true,
	"maxItems":              true,
	"uniqueItems":           true,
	"minProperties":         true,
	"maxProperties":         true,
	"contains":              true,
	"minContains":           true,
	"maxContains":           true,
	"prefixItems":           true,
	"if":                    true,
	"then":                  true,
	"else":                  true,
	"not":                   true,
	"propertyNames":         true,
	"dependentRequired":     true,
	"dependentSchemas":      true,
	"unevaluatedItems":      true,
	"unevaluatedProperties": true,
	"deprecated":            true,
	"readOnly":              true,
	"writeOnly":             true,
	"examples":              true,
	"title":                 true,
	"contentEncoding":       true,
	"contentMediaType":      true,
	"contentSchema":         true,
}

// dataValuedKeywords are the JSON Schema 2020-12 keywords whose value is
// data — an instance value, a list of property names, a vocabulary map — and
// never a schema. Their value is carried as written wherever the keyword is
// read as a keyword: read as a schema, `default: {host: db}` would compile into
// an object schema declaring a property named host, and that schema would be
// what a default materialises as and what a const compares against.
var dataValuedKeywords = map[string]bool{
	"const":             true,
	"enum":              true,
	"default":           true,
	"examples":          true,
	"dependentRequired": true,
	"$vocabulary":       true,
}

// readsAsValue reports whether a keyword's value is data rather than a schema:
// one of dataValuedKeywords anywhere, and, inside a `$jsonschema:` escape body,
// an `x-aggrega-*` extension too, whose value is the extension's own content.
func readsAsValue(key string, ctx *compileCtx) bool {
	return dataValuedKeywords[key] || (ctx.inJSONSchema() && isAggregaExtensionKey(key))
}

// isAggregaExtensionKey reports whether the given key matches the
// `x-aggrega-*` extension-keyword namespace. Aggrega-specific extension
// annotations (`x-aggrega-source` from cross-document `$ref:` expansion,
// `x-aggrega-validate` / `x-aggrega-when` / `x-aggrega-validateMessage`
// from validate-and-when authoring, `x-aggrega-selector` from typed-
// reference fields, `x-aggrega-sensitive` from a leaf declared
// `sensitive: true`) all share this prefix. The pattern recognition lets
// new extension annotations enter the catalog without per-keyword edits
// to the vocabulary allowlist. The recognition is namespaced to
// `x-aggrega-*` rather than the broader JSON Schema 2020-12 `x-*`
// extension prefix so catalog authoring catches typos (e.g. `x-agrega-source`)
// at compile time rather than silently accepting third-party extensions.
func isAggregaExtensionKey(key string) bool {
	return strings.HasPrefix(key, "x-aggrega-")
}

// isKnownKeyword reports whether the given key is recognised by the
// compactform compiler in any role (compact-form keyword, JSON Schema-
// reserved keyword, reference keyword, or Aggrega extension annotation).
// Unknown keys surface as ErrUnknownKeyword during dispatch.
func isKnownKeyword(key string) bool {
	return typeIdentityKeywords[key] ||
		constraintKeywords[key] ||
		compositionKeywords[key] ||
		containerKeywords[key] ||
		aggregaSpecificKeywords[key] ||
		referenceKeywords[key] ||
		jsonSchemaReservedKeywords[key] ||
		isAggregaExtensionKey(key)
}

// isCompactKeyword reports whether the given key is specifically a
// compact-form keyword (not a JSON Schema reserved keyword). Used by
// dispatch to detect compact-form-flavoured content.
func isCompactKeyword(key string) bool {
	return typeIdentityKeywords[key] ||
		constraintKeywords[key] ||
		(compositionKeywords[key] && (key == "valuesType")) ||
		aggregaSpecificKeywords[key]
}
