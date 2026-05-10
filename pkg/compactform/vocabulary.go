// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

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
// the field's declared `type:` — see emit.go for the dispatch.
var constraintKeywords = map[string]bool{
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
// template body. `$jsonschema` embeds full JSON Schema YAML at any node.
// `$mode` provides explicit dispatch override for ambiguous content
// shapes.
var aggregaSpecificKeywords = map[string]bool{
	"validate":        true,
	"validateMessage": true,
	"when":            true,
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
// compact-form-specific. Their presence on a node steers dispatch toward
// the JSON Schema YAML path; per-node passthrough emits them unchanged.
var jsonSchemaReservedKeywords = map[string]bool{
	"$schema":          true,
	"$id":              true,
	"$comment":         true,
	"$vocabulary":      true,
	"$anchor":          true,
	"$dynamicRef":      true,
	"$dynamicAnchor":   true,
	"const":            true,
	"minimum":          true,
	"maximum":          true,
	"exclusiveMinimum": true,
	"exclusiveMaximum": true,
	"multipleOf":       true,
	"minItems":         true,
	"maxItems":         true,
	"uniqueItems":      true,
	"contains":         true,
	"minContains":      true,
	"maxContains":      true,
	"prefixItems":      true,
	"if":               true,
	"then":             true,
	"else":             true,
	"not":              true,
	"propertyNames":    true,
	"dependentRequired": true,
	"dependentSchemas":  true,
	"unevaluatedItems":      true,
	"unevaluatedProperties": true,
	"deprecated":  true,
	"readOnly":    true,
	"writeOnly":   true,
	"examples":    true,
	"title":       true,
	"contentEncoding":  true,
	"contentMediaType": true,
	"contentSchema":    true,
}

// isKnownKeyword reports whether the given key is recognised by the
// compactform compiler in any role (compact-form keyword, JSON Schema-
// reserved keyword, or reference keyword). Unknown keys surface as
// ErrUnknownKeyword during dispatch.
func isKnownKeyword(key string) bool {
	return typeIdentityKeywords[key] ||
		constraintKeywords[key] ||
		compositionKeywords[key] ||
		containerKeywords[key] ||
		aggregaSpecificKeywords[key] ||
		referenceKeywords[key] ||
		jsonSchemaReservedKeywords[key]
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

// isJSONSchemaReservedKeyword reports whether the given key is one of
// JSON Schema 2020-12's reserved keywords (including the ones compact
// form also borrows by name like `allOf`). Used by dispatch to detect
// JSON-Schema-flavoured content.
func isJSONSchemaReservedKeyword(key string) bool {
	return jsonSchemaReservedKeywords[key] ||
		compositionKeywords[key] && key != "valuesType" ||
		containerKeywords[key] ||
		referenceKeywords[key] ||
		key == "type" || key == "description" || key == "default" ||
		key == "enum" || key == "pattern" || key == "format" ||
		key == "minLength" || key == "maxLength"
}
