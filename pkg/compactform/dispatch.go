// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"gopkg.in/yaml.v3"
)

// dispatchMapping classifies a YAML mapping node into one of five
// modes based on content shape, then routes to the appropriate
// emission pathway:
//
//   - escape: contains `$jsonschema:` -> embed the escape content
//     verbatim into the output JSON Schema at this position.
//   - reference: contains `$ref:` -> emit a reference passthrough.
//   - composition: contains `allOf` / `anyOf` / `oneOf` -> recurse into
//     each composing entry independently.
//   - compactConstraint: contains compact-form constraint keywords
//     (type / min / max / default / etc.) -> emit a JSON Schema leaf.
//   - schemaContainer: contains JSON Schema-reserved keywords (properties /
//     items / additionalProperties / etc.) -> emit JSON Schema unchanged
//     (the YAML structure already maps directly).
//   - implicitObject: contains only field-name -> child-schema entries
//     -> treat as a compact-form object scope; recurse children.
//
// The `$mode:` annotation, when present, overrides the heuristic and
// forces dispatch to either `compact` or `jsonschema`.
func dispatchMapping(node *yaml.Node, path string) (any, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, &CompileError{
			Path:    path,
			Line:    nodeLine(node),
			Message: "expected mapping node",
			Err:     ErrEscapeContentInvalid,
		}
	}

	keys := mappingKeys(node)
	mode := keys["$mode"]

	// Validate unknown keywords up-front: every key on a compact-form
	// node must be a known keyword (compact or JSON-Schema-reserved or
	// reference) OR must be a property name in a compact-form-implicit-
	// object scope. We defer the property-vs-keyword distinction to the
	// emission paths; here, only the explicit `$mode:` override is
	// pre-validated.
	if mode != "" {
		switch mode {
		case "compact":
			return emitCompactConstraint(node, path)
		case "jsonschema":
			return emitJSONSchemaPassthrough(node, path)
		default:
			return nil, &CompileError{
				Path:    path,
				Line:    nodeLine(node),
				Keyword: "$mode",
				Message: "value must be `compact` or `jsonschema`",
				Err:     ErrAmbiguousDispatch,
			}
		}
	}

	// Escape: $jsonschema present.
	if _, ok := keys["$jsonschema"]; ok {
		return emitEscape(node, path)
	}

	// Reference: $ref present.
	if _, ok := keys["$ref"]; ok {
		return emitReference(node, path)
	}

	// Composition: allOf / anyOf / oneOf present at the top of this
	// node. Recurse into each composing entry.
	if _, ok := keys["allOf"]; ok {
		return emitComposition(node, "allOf", path)
	}
	if _, ok := keys["anyOf"]; ok {
		return emitComposition(node, "anyOf", path)
	}
	if _, ok := keys["oneOf"]; ok {
		return emitComposition(node, "oneOf", path)
	}

	// Compact constraint: the node has a `type:` or another compact-
	// form constraint keyword as a leaf-level constraint. The presence
	// of `type:` is the canonical signal — except for the ambiguous
	// case where the schema has a property literally named `type` (in
	// which case `$mode:` is required, handled above).
	if hasCompactConstraintShape(keys) {
		return emitCompactConstraint(node, path)
	}

	// Schema container: JSON Schema-reserved structural keywords
	// (properties, items, additionalProperties, if, etc.) present.
	if hasSchemaContainerShape(keys) {
		return emitJSONSchemaPassthrough(node, path)
	}

	// Implicit object: every key is a property name (no compact-form
	// or JSON-Schema-reserved keywords). Emit as a JSON Schema object
	// with properties: { ... } populated by recursing each child.
	return emitImplicitObject(node, path)
}

// hasCompactConstraintShape reports whether the node looks like a
// compact-form constraint leaf — a `type:` is present, or a per-leaf
// compact constraint keyword (`min`, `max`, `enum`, `pattern`, etc.)
// is present without JSON Schema structural keywords. The presence of
// `properties:` or `items:` at the same level steers toward the schema-
// container path instead.
func hasCompactConstraintShape(keys map[string]string) bool {
	// Direct compact-constraint signals.
	for k := range keys {
		if !isCompactKeyword(k) {
			continue
		}
		// Excludes the structural compact-form keys that nest other
		// content rather than acting as a leaf constraint.
		if k == "$jsonschema" || k == "$mode" {
			continue
		}
		return true
	}
	return false
}

// hasSchemaContainerShape reports whether the node carries JSON Schema
// 2020-12 structural keywords (properties, items, additionalProperties,
// if/then/else, etc.) that signal "this is a JSON Schema YAML node,
// emit unchanged."
func hasSchemaContainerShape(keys map[string]string) bool {
	structural := []string{
		"properties", "items", "additionalProperties",
		"if", "then", "else", "not",
		"patternProperties", "propertyNames",
		"prefixItems", "contains", "minContains", "maxContains",
		"dependentRequired", "dependentSchemas",
		"unevaluatedItems", "unevaluatedProperties",
	}
	for _, k := range structural {
		if _, ok := keys[k]; ok {
			return true
		}
	}
	return false
}

// mappingKeys returns the keys of a YAML mapping node as a map for
// fast presence-checking. Values are the scalar string form of the
// value node where the value is a scalar; non-scalar values yield an
// empty string (presence is what matters for dispatch).
func mappingKeys(node *yaml.Node) map[string]string {
	out := make(map[string]string, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if v.Kind == yaml.ScalarNode {
			out[k.Value] = v.Value
		} else {
			out[k.Value] = ""
		}
	}
	return out
}

// nodeLine returns the line of a yaml.Node, or zero if the node is nil.
// Provenance use only — surfaces in CompileError.Line.
func nodeLine(node *yaml.Node) int {
	if node == nil {
		return 0
	}
	return node.Line
}
