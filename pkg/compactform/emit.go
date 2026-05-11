// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"gopkg.in/yaml.v3"
)

// emitCompactConstraint emits a compact-form leaf constraint as a JSON
// Schema 2020-12 object. The leaf carries a `type:` (or another
// compact-form keyword) plus zero or more constraint keywords. The
// emitter dispatches `min` / `max` to the appropriate JSON Schema
// keyword (`minimum` / `maximum` for numeric, `minLength` / `maxLength`
// for string, `minItems` / `maxItems` for array) based on `type:`.
func emitCompactConstraint(node *yaml.Node, path string) (Schema, error) {
	out := Schema{}
	keys := mappingKeyOrder(node)
	typ := ""

	// First pass: collect `type:` so subsequent dispatch knows the
	// constraint-keyword mapping target.
	for _, k := range keys {
		v := mappingValue(node, k)
		if k == "type" && v != nil && v.Kind == yaml.ScalarNode {
			typ = v.Value
			out["type"] = v.Value
		}
	}

	// Second pass: emit each keyword in author order.
	for _, k := range keys {
		v := mappingValue(node, k)
		if k == "type" {
			continue // already emitted above
		}
		if k == "required" {
			continue // accumulated to parent in implicit-object emission
		}
		if k == "$mode" {
			continue // dispatch-time hint only
		}

		switch k {
		case "min":
			jk, err := minMaxKeyForType(typ, true, path, k, nodeLine(node))
			if err != nil {
				return nil, err
			}
			out[jk] = decodeScalar(v)
		case "max":
			jk, err := minMaxKeyForType(typ, false, path, k, nodeLine(node))
			if err != nil {
				return nil, err
			}
			out[jk] = decodeScalar(v)
		case "valuesType":
			// `valuesType: X` -> additionalProperties: { type: X }
			if v == nil || v.Kind != yaml.ScalarNode {
				return nil, &CompileError{
					Path:    path,
					Line:    nodeLine(node),
					Keyword: "valuesType",
					Message: "expected a scalar type name",
					Err:     ErrConstraintTypeMismatch,
				}
			}
			out["additionalProperties"] = Schema{"type": v.Value}
		case "validate":
			// validate: <go template> -> x-aggrega-validate carries body
			out["x-aggrega-validate"] = decodeScalar(v)
		case "validateMessage":
			out["x-aggrega-validateMessage"] = decodeScalar(v)
		case "when":
			out["x-aggrega-when"] = decodeScalar(v)
		case "then":
			child, err := compileNode(v, pathJoin(path, "then"))
			if err != nil {
				return nil, err
			}
			out["then"] = child
		case "else":
			child, err := compileNode(v, pathJoin(path, "else"))
			if err != nil {
				return nil, err
			}
			out["else"] = child
		case "$ref":
			out["$ref"] = decodeScalar(v)
		case "items":
			// items: recurse — element can be compact form or JSON Schema
			child, err := compileNode(v, pathJoin(path, "items"))
			if err != nil {
				return nil, err
			}
			out["items"] = child
		case "properties":
			// `properties:` value is a map of property-name -> schema;
			// each entry is compiled as a schema but the outer container
			// is a flat map, not an implicit-object scope.
			child, err := emitSchemaMap(v, pathJoin(path, "properties"))
			if err != nil {
				return nil, err
			}
			out["properties"] = child
		case "patternProperties":
			child, err := emitSchemaMap(v, pathJoin(path, "patternProperties"))
			if err != nil {
				return nil, err
			}
			out["patternProperties"] = child
		case "$defs", "definitions", "dependentSchemas":
			child, err := emitSchemaMap(v, pathJoin(path, k))
			if err != nil {
				return nil, err
			}
			out[k] = child
		case "additionalProperties":
			child, err := compileNode(v, pathJoin(path, "additionalProperties"))
			if err != nil {
				return nil, err
			}
			out["additionalProperties"] = child
		default:
			if !isKnownKeyword(k) {
				return nil, &CompileError{
					Path:    path,
					Line:    nodeLine(node),
					Keyword: k,
					Message: "unknown keyword",
					Err:     ErrUnknownKeyword,
				}
			}
			// Generic passthrough for known JSON Schema keywords
			// (enum / pattern / format / default / description / etc.)
			// and for composition keywords inside a leaf.
			child, err := compileNode(v, pathJoin(path, k))
			if err != nil {
				return nil, err
			}
			out[k] = child
		}
	}

	injectWhenWrapper(out)
	return out, nil
}

// emitSchemaMap compiles the value of a JSON Schema reserved keyword
// whose content is a flat map of name -> schema (`properties:`,
// `patternProperties:`, `$defs:`, `definitions:`, `dependentSchemas:`).
// Each entry's value is dispatched through compileNode and stored at
// the entry's key in the output map. The container itself is a plain
// map — it is not wrapped in an implicit-object schema, since the
// enclosing keyword's semantics already say "this is a map of schemas".
func emitSchemaMap(node *yaml.Node, path string) (Schema, error) {
	out := Schema{}
	if node == nil || node.Kind != yaml.MappingNode {
		return out, nil
	}
	keys := mappingKeyOrder(node)
	for _, k := range keys {
		v := mappingValue(node, k)
		child, err := compileNode(v, pathJoin(path, k))
		if err != nil {
			return nil, err
		}
		out[k] = child
	}
	return out, nil
}

// injectWhenWrapper adds `"if": true` to a schema that pairs
// `x-aggrega-when:` with `then:` and/or `else:` clauses. The boolean
// `if: true` makes standard JSON Schema 2020-12 consumers (those without
// the Aggrega validator adapter) apply the `then:` constraints
// unconditionally; an Aggrega adapter that recognises `x-aggrega-when:`
// is expected to evaluate the template predicate and rewrite the
// resolved schema accordingly. Without `if:`, `then:` and `else:` are
// inert under standard JSON Schema semantics, which silently drops the
// gated constraints.
//
// The helper is a no-op when `x-aggrega-when:` is absent (so a node
// carrying only `then:` / `else:` from explicit JSON Schema YAML
// authoring is not touched) and when neither `then:` nor `else:` is
// present (so the standalone `when:` form keeps its prior semantic of
// gating the surrounding node's constraints rather than introducing
// an if/then/else wrapper).
func injectWhenWrapper(out Schema) {
	if _, hasWhen := out["x-aggrega-when"]; !hasWhen {
		return
	}
	if _, hasIf := out["if"]; hasIf {
		// Explicit `if:` from authored content takes precedence; the
		// wrapper is only synthesised when the author did not specify
		// an `if:` themselves.
		return
	}
	_, hasThen := out["then"]
	_, hasElse := out["else"]
	if !hasThen && !hasElse {
		return
	}
	out["if"] = true
}

// minMaxKeyForType resolves the JSON Schema keyword to emit for a
// compact-form `min` / `max` based on the field's declared `type:`.
// Numeric -> minimum / maximum; string -> minLength / maxLength;
// array -> minItems / maxItems. Other types reject with
// ErrConstraintTypeMismatch.
func minMaxKeyForType(typ string, isMin bool, path, keyword string, line int) (string, error) {
	switch typ {
	case "integer", "number":
		if isMin {
			return "minimum", nil
		}
		return "maximum", nil
	case "string":
		if isMin {
			return "minLength", nil
		}
		return "maxLength", nil
	case "array":
		if isMin {
			return "minItems", nil
		}
		return "maxItems", nil
	default:
		return "", &CompileError{
			Path:    path,
			Line:    line,
			Keyword: keyword,
			Message: "constraint applies to integer/number/string/array; declared type is `" + typ + "`",
			Err:     ErrConstraintTypeMismatch,
		}
	}
}

// emitImplicitObject emits a compact-form object scope as a JSON Schema
// `type: object, properties: { ... }, additionalProperties: false`
// document. The implicit-object scope is the typical compact-form
// authoring shape: an object where every YAML map entry is a
// property-name -> child-schema pair. Per-leaf `required: true` markers
// in children accumulate into the parent's `required:` array.
//
// Top-level JSON Schema meta-keywords authored alongside property
// declarations (`$defs:`, `definitions:`) emit at the schema root
// rather than inside the `properties:` map — they describe reusable
// schemas, not properties of the current object.
func emitImplicitObject(node *yaml.Node, path string) (Schema, error) {
	keys := mappingKeyOrder(node)
	properties := Schema{}
	required := []string{}
	rootMeta := Schema{}

	for _, k := range keys {
		v := mappingValue(node, k)

		// JSON Schema meta-keywords authored at the implicit-object
		// scope root: emit at the schema root, not inside properties.
		if k == "$defs" || k == "definitions" {
			child, err := emitSchemaMap(v, pathJoin(path, k))
			if err != nil {
				return nil, err
			}
			rootMeta[k] = child
			continue
		}

		// Recurse the child for its own schema emission.
		childSchema, err := compileNode(v, pathJoin(path, k))
		if err != nil {
			return nil, err
		}

		// Detect `required: true` on the child and accumulate to the
		// parent's required array. Only inspect direct compact-form
		// leaves — JSON Schema children carry their own required: array.
		if v != nil && v.Kind == yaml.MappingNode {
			if isRequiredLeaf(v) {
				required = append(required, k)
			}
		}

		properties[k] = childSchema
	}

	out := Schema{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false, // close objects by default; opt-in via additionalProperties: true
	}
	if len(required) > 0 {
		out["required"] = required
	}
	for k, v := range rootMeta {
		out[k] = v
	}
	return out, nil
}

// isRequiredLeaf reports whether a YAML mapping node carries a leaf
// `required: true` marker (the compact-form per-leaf required hint).
// The presence of this marker accumulates to the parent's required
// array in emitImplicitObject.
func isRequiredLeaf(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.Value == "required" {
			if v.Kind == yaml.ScalarNode && v.Value == "true" {
				return true
			}
		}
	}
	return false
}

// emitJSONSchemaPassthrough emits a node containing JSON Schema-reserved
// structural keywords (properties, items, additionalProperties, if/then/
// else, not, etc.) unchanged: the YAML structure maps directly to JSON
// Schema, with per-child recursion for nested mappings. Keywords whose
// value is a map-of-schemas (`properties:`, `patternProperties:`,
// `$defs:`, `definitions:`, `dependentSchemas:`) route through
// emitSchemaMap so the container is a flat map rather than an implicit-
// object schema.
func emitJSONSchemaPassthrough(node *yaml.Node, path string) (Schema, error) {
	out := Schema{}
	keys := mappingKeyOrder(node)

	for _, k := range keys {
		v := mappingValue(node, k)
		if k == "$mode" {
			continue
		}
		if !isKnownKeyword(k) {
			return nil, &CompileError{
				Path:    path,
				Line:    nodeLine(node),
				Keyword: k,
				Message: "unknown keyword",
				Err:     ErrUnknownKeyword,
			}
		}
		switch k {
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			child, err := emitSchemaMap(v, pathJoin(path, k))
			if err != nil {
				return nil, err
			}
			out[k] = child
		default:
			child, err := compileNode(v, pathJoin(path, k))
			if err != nil {
				return nil, err
			}
			out[k] = child
		}
	}
	return out, nil
}

// emitEscape embeds the `$jsonschema:` block content verbatim at this
// node's position. The escape body is parsed as JSON Schema YAML; the
// compiler does not validate the body's vocabulary beyond YAML parsing.
func emitEscape(node *yaml.Node, path string) (Schema, error) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.Value != "$jsonschema" {
			continue
		}

		// The escape value can be either a YAML block-scalar string
		// containing nested YAML (which we parse) or a directly-nested
		// YAML mapping (which we emit through the JSON Schema
		// passthrough).
		if v.Kind == yaml.ScalarNode {
			// Block-scalar string: parse as YAML.
			var inner yaml.Node
			if err := yaml.Unmarshal([]byte(v.Value), &inner); err != nil {
				return nil, &CompileError{
					Path:    path,
					Line:    nodeLine(v),
					Keyword: "$jsonschema",
					Message: "escape content does not parse as YAML: " + err.Error(),
					Err:     ErrEscapeContentInvalid,
				}
			}
			if inner.Kind == yaml.DocumentNode && len(inner.Content) > 0 {
				inner = *inner.Content[0]
			}
			child, err := compileNode(&inner, pathJoin(path, "$jsonschema"))
			if err != nil {
				return nil, err
			}
			if m, ok := child.(Schema); ok {
				return m, nil
			}
			return Schema{"value": child}, nil
		}
		// Directly-nested mapping.
		if v.Kind == yaml.MappingNode {
			return emitJSONSchemaPassthrough(v, pathJoin(path, "$jsonschema"))
		}
		return nil, &CompileError{
			Path:    path,
			Line:    nodeLine(v),
			Keyword: "$jsonschema",
			Message: "expected block-scalar string or nested mapping",
			Err:     ErrEscapeContentInvalid,
		}
	}
	return Schema{}, nil
}

// emitReference emits a `$ref:` passthrough. The compactform compiler
// validates the URL shape but does not resolve the reference — the
// validator pipeline at pkg/validator/merger/ + pkg/validator/jsonschema/
// resolves references at the appropriate pipeline stage.
func emitReference(node *yaml.Node, path string) (Schema, error) {
	out := Schema{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if k.Value == "$mode" {
			continue
		}
		if k.Value == "$ref" {
			if v.Kind != yaml.ScalarNode || v.Value == "" {
				return nil, &CompileError{
					Path:    path,
					Line:    nodeLine(v),
					Keyword: "$ref",
					Message: "expected a non-empty reference URL",
					Err:     ErrReferenceUnresolvable,
				}
			}
			out["$ref"] = v.Value
			continue
		}
		// Adjacent keys at a $ref node are composed alongside the
		// reference (e.g. `secondary: { $ref: ..., when: ... }`).
		child, err := compileNode(v, pathJoin(path, k.Value))
		if err != nil {
			return nil, err
		}
		if k.Value == "validate" {
			out["x-aggrega-validate"] = child
		} else if k.Value == "validateMessage" {
			out["x-aggrega-validateMessage"] = child
		} else if k.Value == "when" {
			out["x-aggrega-when"] = child
		} else {
			out[k.Value] = child
		}
	}
	injectWhenWrapper(out)
	return out, nil
}

// emitComposition recurses each entry of an `allOf` / `anyOf` / `oneOf`
// list, dispatching each composing entry independently. Other keys on
// the node (siblings of the composition keyword) are emitted alongside.
func emitComposition(node *yaml.Node, compKey, path string) (Schema, error) {
	out := Schema{}
	keys := mappingKeyOrder(node)

	for _, k := range keys {
		v := mappingValue(node, k)
		if k == "$mode" {
			continue
		}
		if k == compKey {
			if v == nil || v.Kind != yaml.SequenceNode {
				return nil, &CompileError{
					Path:    path,
					Line:    nodeLine(v),
					Keyword: compKey,
					Message: "expected a sequence of schemas",
					Err:     ErrEscapeContentInvalid,
				}
			}
			entries := make([]any, 0, len(v.Content))
			for i, e := range v.Content {
				entry, err := compileNode(e, pathJoin(path, compKey+indexSegment(i)))
				if err != nil {
					return nil, err
				}
				entries = append(entries, entry)
			}
			out[compKey] = entries
			continue
		}
		// Sibling keys at the composition node: emit through the
		// constraint-leaf pathway so per-keyword handling (validate ->
		// x-aggrega-validate, when -> x-aggrega-when, $ref passthrough,
		// type/min/max/etc.) applies consistently.
		if !isKnownKeyword(k) {
			return nil, &CompileError{
				Path:    path,
				Line:    nodeLine(node),
				Keyword: k,
				Message: "unknown keyword",
				Err:     ErrUnknownKeyword,
			}
		}
		child, err := compileNode(v, pathJoin(path, k))
		if err != nil {
			return nil, err
		}
		switch k {
		case "validate":
			out["x-aggrega-validate"] = child
		case "validateMessage":
			out["x-aggrega-validateMessage"] = child
		case "when":
			out["x-aggrega-when"] = child
		default:
			out[k] = child
		}
	}
	injectWhenWrapper(out)
	return out, nil
}

// mappingKeyOrder returns the keys of a YAML mapping node in author
// order. Author order preservation is load-bearing for the
// deterministic-output invariant.
func mappingKeyOrder(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]string, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		if k.Kind == yaml.ScalarNode {
			out = append(out, k.Value)
		}
	}
	return out
}

// mappingValue returns the value node paired with the given key in a
// YAML mapping node, or nil when the key is absent.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return v
		}
	}
	return nil
}

// decodeScalar decodes a yaml.Node scalar into its native Go type
// (string, int, float, bool). Non-scalar nodes return nil.
func decodeScalar(node *yaml.Node) any {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return nil
	}
	var v any
	if err := node.Decode(&v); err == nil {
		return v
	}
	return node.Value
}
