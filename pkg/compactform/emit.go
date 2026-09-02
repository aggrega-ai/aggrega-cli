// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/anchorcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"go.yaml.in/yaml/v3"
)

// emitCompactConstraint emits a compact-form leaf constraint as a JSON
// Schema 2020-12 object. The leaf carries a `type:` (or another
// compact-form keyword) plus zero or more constraint keywords. The
// emitter dispatches `min` / `max` to the appropriate JSON Schema
// keyword (`minimum` / `maximum` for numeric, `minLength` / `maxLength`
// for string, `minItems` / `maxItems` for array) based on `type:`.
func emitCompactConstraint(node *yaml.Node, path string, ctx *compileCtx) (Schema, error) {
	out := Schema{}
	keys := mappingKeyOrder(node)
	typ := ""

	// First pass: collect `type:` so subsequent dispatch knows the
	// constraint-keyword mapping target.
	//
	// `type:` accepts both scalar (the common case: `type: string`) and
	// sequence (JSON Schema 2020-12 array-of-types: `type: [string, integer,
	// boolean]`). For sequence-valued `type:`, the array is emitted as-is
	// into the compiled schema; `typ` stays empty because there is no
	// single resolved type for `min:`/`max:` polymorphism. If a later
	// constraint keyword needs polymorphic dispatch (`min:` / `max:`) and
	// the type is unresolved-due-to-union, minMaxKeyForType returns
	// ErrConstraintTypeMismatch, which is the right loud halt — the
	// author can't compose a numeric range constraint against a union
	// type that might be string-valued.
	for _, k := range keys {
		v := mappingValue(node, k)
		if k != "type" || v == nil {
			continue
		}
		switch v.Kind {
		case yaml.ScalarNode:
			typ = v.Value
			out["type"] = v.Value
		case yaml.SequenceNode:
			child, err := emitSequence(v, pathJoin(path, "type"), ctx)
			if err != nil {
				return nil, err
			}
			out["type"] = child
		default:
			return nil, &CompileError{
				Path:    path,
				Line:    nodeLine(v),
				Keyword: "type",
				Message: "expected a scalar type name or a sequence of type names (JSON Schema 2020-12 array-of-types); got unsupported node kind",
				Err:     ErrConstraintTypeMismatch(),
			}
		}
	}

	// Second pass: emit each keyword in author order.
	for _, k := range keys {
		v := mappingValue(node, k)
		if k == "type" {
			continue // already emitted above
		}
		if k == "required" {
			// Two shapes carry meaning here:
			//   - Compact-form per-leaf marker (`required: true`): accumulated
			//     by the parent's implicit-object emission, ignored here.
			//   - JSON Schema array (`required: [name1, name2]`): a top-level
			//     keyword the author wrote alongside type/properties; passes
			//     through as a JSON Schema 2020-12 required array.
			if v != nil && v.Kind == yaml.SequenceNode {
				child, err := emitSequence(v, pathJoin(path, "required"), ctx)
				if err != nil {
					return nil, err
				}
				out["required"] = child
			}
			continue
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
					Err:     ErrConstraintTypeMismatch(),
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
		case "sensitive":
			// sensitive: true -> x-aggrega-sensitive marks the leaf
			out["x-aggrega-sensitive"] = decodeScalar(v)
		case "then":
			child, err := compileNode(v, pathJoin(path, "then"), ctx)
			if err != nil {
				return nil, err
			}
			out["then"] = child
		case "else":
			child, err := compileNode(v, pathJoin(path, "else"), ctx)
			if err != nil {
				return nil, err
			}
			out["else"] = child
		case "$ref":
			out["$ref"] = decodeScalar(v)
		case "items":
			// items: recurse — element can be compact form or JSON Schema
			child, err := compileNode(v, pathJoin(path, "items"), ctx)
			if err != nil {
				return nil, err
			}
			out["items"] = child
		case "properties":
			// `properties:` value is a map of property-name -> schema;
			// each entry is compiled as a schema but the outer container
			// is a flat map, not an implicit-object scope.
			child, err := emitSchemaMap(v, pathJoin(path, "properties"), ctx)
			if err != nil {
				return nil, err
			}
			out["properties"] = child
		case "patternProperties":
			child, err := emitSchemaMap(v, pathJoin(path, "patternProperties"), ctx)
			if err != nil {
				return nil, err
			}
			out["patternProperties"] = child
		case "$defs", "definitions", "dependentSchemas":
			child, err := emitSchemaMap(v, pathJoin(path, k), ctx)
			if err != nil {
				return nil, err
			}
			out[k] = child
		case "additionalProperties":
			child, err := compileNode(v, pathJoin(path, "additionalProperties"), ctx)
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
					Err:     ErrUnknownKeyword(),
				}
			}
			// Generic passthrough for known JSON Schema keywords
			// (enum / pattern / format / default / description / etc.)
			// and for composition keywords inside a leaf.
			child, err := compileNode(v, pathJoin(path, k), ctx)
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
func emitSchemaMap(node *yaml.Node, path string, ctx *compileCtx) (Schema, error) {
	out := Schema{}
	if node == nil || node.Kind != yaml.MappingNode {
		return out, nil
	}
	keys := mappingKeyOrder(node)
	for _, k := range keys {
		v := mappingValue(node, k)
		child, err := compileNode(v, pathJoin(path, k), ctx)
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
		// wrapper is only synthesized when the author did not specify
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
//
// The empty `typ` case arises when `type:` was authored as a JSON
// Schema 2020-12 array-of-types (union) — `type: [string, integer,
// boolean]`. No single resolved type means `min:`/`max:` polymorphic
// dispatch cannot pick `minimum` vs `minLength` vs `minItems`; the
// loud halt names the union origin so the author can either narrow the
// `type:` to a single value or remove the `min:`/`max:` constraint.
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
	case "":
		return "", &CompileError{
			Path:    path,
			Line:    line,
			Keyword: keyword,
			Message: "constraint applies to a single declared type (integer/number/string/array); `type:` is unresolved (union or absent) — narrow `type:` to a single value or remove this constraint",
			Err:     ErrConstraintTypeMismatch(),
		}
	default:
		return "", &CompileError{
			Path:    path,
			Line:    line,
			Keyword: keyword,
			Message: "constraint applies to integer/number/string/array; declared type is `" + typ + "`",
			Err:     ErrConstraintTypeMismatch(),
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
func emitImplicitObject(node *yaml.Node, path string, ctx *compileCtx) (Schema, error) {
	keys := mappingKeyOrder(node)
	properties := Schema{}
	required := []string{}
	rootMeta := Schema{}

	for _, k := range keys {
		v := mappingValue(node, k)

		// JSON Schema meta-keywords authored at the implicit-object
		// scope root: emit at the schema root, not inside properties.
		if k == "$defs" || k == "definitions" {
			child, err := emitSchemaMap(v, pathJoin(path, k), ctx)
			if err != nil {
				return nil, err
			}
			rootMeta[k] = child
			continue
		}

		// Recurse the child for its own schema emission.
		childSchema, err := compileNode(v, pathJoin(path, k), ctx)
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
func emitJSONSchemaPassthrough(node *yaml.Node, path string, ctx *compileCtx) (Schema, error) {
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
				Err:     ErrUnknownKeyword(),
			}
		}
		switch k {
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			child, err := emitSchemaMap(v, pathJoin(path, k), ctx)
			if err != nil {
				return nil, err
			}
			out[k] = child
		default:
			child, err := compileNode(v, pathJoin(path, k), ctx)
			if err != nil {
				return nil, err
			}
			out[k] = child
		}
	}
	return out, nil
}

// emitEscape embeds the `$jsonschema:` block content at this node's
// position. The body is JSON Schema YAML and its structure is carried
// through unchanged.
//
// "Verbatim" describes the STRUCTURE and not the vocabulary: every key in
// the body is checked against the known-keyword set, on both of the two
// routes below — a block-scalar body is parsed and run through compileNode,
// a directly-nested mapping goes to emitJSONSchemaPassthrough, and each
// rejects an unrecognised key with ErrUnknownKeyword. So the escape widens
// what a node may CONTAIN, never which keywords it may name, and an
// authored key outside the vocabulary is refused here rather than passed
// through into the compiled schema.
func emitEscape(node *yaml.Node, path string, ctx *compileCtx) (Schema, error) {
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
					Err:     ErrEscapeContentInvalid(),
				}
			}
			// A fresh tree the entry check never saw: it was a string when
			// Compile inspected its input, and its anchors only become anchors
			// on the parse above. Recursion in it is therefore refused here
			// rather than there.
			if anchor, found := anchorcycle.Find(&inner); found {
				return nil, &CompileError{
					Path:    path,
					Line:    nodeLine(v),
					Keyword: "$jsonschema",
					Message: anchorcycle.Explain(anchor),
					Err:     ErrCircularRef(),
				}
			}
			if inner.Kind == yaml.DocumentNode && len(inner.Content) > 0 {
				inner = *inner.Content[0]
			}
			child, err := compileNode(&inner, pathJoin(path, "$jsonschema"), ctx)
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
			return emitJSONSchemaPassthrough(v, pathJoin(path, "$jsonschema"), ctx)
		}
		return nil, &CompileError{
			Path:    path,
			Line:    nodeLine(v),
			Keyword: "$jsonschema",
			Message: "expected block-scalar string or nested mapping",
			Err:     ErrEscapeContentInvalid(),
		}
	}
	return Schema{}, nil
}

// emitReference dispatches a `$ref:` node between two paths based on
// the URI shape:
//
//   - In-document refs (URI starts with `#/`): emitted as a literal $ref
//     passthrough preserving today's behaviour. The compactform compiler
//     validates the URL shape; the validator pipeline at
//     pkg/validator/jsonschema/ resolves the reference at validation time
//     against the same document's $defs.
//
//   - Cross-document refs (URI has a non-empty document portion before
//     `#`): resolved at compile time via expandSchemaLibraryRef when the
//     compile context carries a SchemaLibraries map. The library entry's
//     content is compiled recursively (with the current refURI pushed onto
//     the resolution stack for cycle detection), then merged into the
//     consumer's schema via composeXRefSchema. Absent a SchemaLibraries
//     map (ctx==nil or ctx.libraries==nil), a cross-document URI surfaces
//     as ErrUnresolvedSchemaLibraryRef rather than passing through as an
//     unresolvable literal.
//
// Adjacent keys at a $ref node ("siblings" of $ref) are merged into the
// emitted schema. Under JSON Schema 2019-09 sibling-of-$ref semantics
// implemented by google/jsonschema-go, siblings of an in-document $ref
// are ignored at validation; the in-document path emits them anyway for
// authoring-fidelity and downstream-tool consumption. The cross-document
// path applies a sibling allowlist + composition merge per the
// emitWithXRefExpand function.
func emitReference(node *yaml.Node, path string, ctx *compileCtx) (Schema, error) {
	refValue := refURIFromNode(node)
	if refValue != "" && !isInDocumentRef(refValue) {
		return emitWithXRefExpand(node, refValue, path, ctx)
	}
	return emitInDocumentReference(node, path, ctx)
}

// refURIFromNode extracts the `$ref:` URI scalar from a mapping node, or
// returns the empty string when the value is missing or non-scalar. The
// caller distinguishes in-document refs from cross-document refs based on
// the returned string; an empty return defers shape validation to the
// in-document emit path so the existing ErrReferenceUnresolvable error
// surfaces unchanged.
func refURIFromNode(node *yaml.Node) string {
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.Value == "$ref" && v.Kind == yaml.ScalarNode {
			return v.Value
		}
	}
	return ""
}

// isInDocumentRef reports whether a $ref URI targets a location inside
// the same document (the JSON Pointer fragment-only form starting with
// `#`). Cross-document refs have a non-empty document portion before the
// `#`. OCI URIs are not in-document; the cross-document dispatch handles
// them via expandSchemaLibraryRef, which rejects a host-bearing reference —
// libraries are addressed by identity, not by registry host.
func isInDocumentRef(refURI string) bool {
	return refURI != "" && refURI[0] == '#'
}

// emitInDocumentReference preserves today's $ref passthrough behaviour
// verbatim: emit the literal $ref string into the output schema; emit
// adjacent sibling keys alongside (under JSON Schema 2019-09 sibling
// semantics the validator ignores them; authoring intent is preserved
// for downstream tools).
func emitInDocumentReference(node *yaml.Node, path string, ctx *compileCtx) (Schema, error) {
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
					Err:     ErrReferenceUnresolvable(),
				}
			}
			out["$ref"] = v.Value
			continue
		}
		// Adjacent keys at a $ref node are composed alongside the
		// reference (e.g. `secondary: { $ref: ..., when: ... }`).
		child, err := compileNode(v, pathJoin(path, k.Value), ctx)
		if err != nil {
			return nil, err
		}
		if k.Value == "validate" {
			out["x-aggrega-validate"] = child
		} else if k.Value == "validateMessage" {
			out["x-aggrega-validateMessage"] = child
		} else if k.Value == "when" {
			out["x-aggrega-when"] = child
		} else if k.Value == "sensitive" {
			out["x-aggrega-sensitive"] = child
		} else {
			out[k.Value] = child
		}
	}
	injectWhenWrapper(out)
	return out, nil
}

// emitWithXRefExpand resolves a cross-document $ref at compile time and
// merges the resolved library entry into the consumer's schema.
//
// Steps:
//  1. Resolve the URI via expandSchemaLibraryRef against ctx.libraries.
//     Errors (unresolved, circular, OCI-deferred, malformed) surface to
//     the caller with consumer-file provenance.
//  2. Recursively compile the resolved entry's yaml content with the
//     refURI pushed onto ctx.resolutionStack — this catches library-to-
//     library cycle chains at the recursive expander call.
//  3. Validate the consumer-side sibling keys against the allowlist
//     (properties / required / description). Unsupported siblings on a
//     cross-document $ref are rejected with ErrUnknownKeyword — under
//     JSON Schema 2019-09 they would be silently ignored by the
//     validator, so emit-time rejection is the only place authors get a
//     clear diagnostic.
//  4. Merge: library properties first; consumer sibling properties
//     overwrite library properties on key collision (consumer-takes-
//     precedence); required arrays union (library-then-consumer order,
//     deduplicated); consumer description wins if present.
//  5. Inject `additionalProperties: false` on the merged shape so the
//     inferred-form-mandatory rule survives the architecture change —
//     the closure semantic the compact form establishes is preserved
//     through compile-time expansion.
//  6. Annotate library-sourced properties with x-aggrega-source values
//     identifying the originating library + entry. The annotation lands
//     at Step E (xref_annotations.go); this function calls
//     annotateLibrarySource on each contributed property so the wiring
//     is in place when Step E lands.
func emitWithXRefExpand(node *yaml.Node, refURI, path string, ctx *compileCtx) (Schema, error) {
	if ctx == nil || ctx.libraries == nil {
		// Cross-document URI shape but no library map in scope: surface as
		// unresolved with a clear message. Without this guard, the existing
		// in-document passthrough would silently emit an unresolvable
		// $ref into the compiled schema — a downstream validation
		// failure with no actionable diagnostic at compile time.
		return nil, &CompileError{
			Path:    path,
			Line:    nodeLine(node),
			Keyword: "$ref",
			Message: "cross-document $ref but no SchemaLibrary catalog supplied: " + refURI,
			Err:     ErrUnresolvedSchemaLibraryRef(),
		}
	}

	entryContent, libraryName, entryName, err := expandSchemaLibraryRef(
		refURI,
		ctx.libraries,
		ctx.resolutionStack,
		ctx.consumerFile,
	)
	if err != nil {
		return nil, err
	}

	// Push the current entry's canonical identity (version-stripped, host-less
	// `<Library>#/schemas/<Entry>`) onto the resolution stack for the recursive
	// compile so library-to-library cycles surface as ErrCircularRef at the next
	// expandSchemaLibraryRef call — keyed on identity, not the raw reference, so
	// inconsistent `@version` spellings of one entry cannot evade detection.
	childCtx := &compileCtx{
		libraries:       ctx.libraries,
		consumerFile:    ctx.consumerFile,
		resolutionStack: refcycle.Push(ctx.resolutionStack, canonicalRefKey(libraryName, entryName)),
	}

	resolved, err := compileNode(entryContent, pathJoin(path, "$ref"), childCtx)
	if err != nil {
		return nil, err
	}
	resolvedSchema, _ := resolved.(Schema)
	if resolvedSchema == nil {
		resolvedSchema = Schema{}
	}

	// Annotate every property contributed by the library entry. The
	// annotation helper is supplied by xref_annotations.go; tagging here
	// keeps the source-map information localised to the library-sourced
	// surface.
	annotateLibrarySource(resolvedSchema, libraryName, entryName)

	// Validate consumer-side siblings against the allowlist and prepare
	// the merge inputs.
	consumerProps, consumerRequired, consumerDescription, err := collectConsumerSiblings(node, path)
	if err != nil {
		return nil, err
	}

	// Compile the consumer's local property schemas.
	consumerCompiled := Schema{}
	for name, valueNode := range consumerProps {
		child, err := compileNode(valueNode, pathJoin(pathJoin(path, "properties"), name), ctx)
		if err != nil {
			return nil, err
		}
		consumerCompiled[name] = child
	}

	return composeXRefSchema(resolvedSchema, consumerCompiled, consumerRequired, consumerDescription), nil
}

// collectConsumerSiblings walks the keys of a cross-document $ref node
// and partitions consumer-side siblings into properties / required /
// description. Unsupported siblings (anything other than these three +
// the $ref / $mode tokens) reject with ErrUnknownKeyword carrying the
// allowlist in the message. Returning a map[string]*yaml.Node for
// properties (preserving author order via a slice of names alongside)
// lets the merge step compile each property with its own path
// provenance.
func collectConsumerSiblings(node *yaml.Node, path string) (props map[string]*yaml.Node, required []string, description any, err error) {
	props = map[string]*yaml.Node{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		switch k.Value {
		case "$ref", "$mode":
			continue
		case "properties":
			if v == nil || v.Kind != yaml.MappingNode {
				return nil, nil, nil, &CompileError{
					Path:    path,
					Line:    nodeLine(v),
					Keyword: "properties",
					Message: "expected a mapping of property names to schemas",
					Err:     ErrEscapeContentInvalid(),
				}
			}
			for j := 0; j+1 < len(v.Content); j += 2 {
				pk := v.Content[j]
				pv := v.Content[j+1]
				if pk.Kind == yaml.ScalarNode {
					props[pk.Value] = pv
				}
			}
		case "required":
			if v != nil && v.Kind == yaml.SequenceNode {
				for _, item := range v.Content {
					if item.Kind == yaml.ScalarNode {
						required = append(required, item.Value)
					}
				}
			}
		case "description":
			description = decodeScalar(v)
		default:
			msg := "cross-document $ref sibling not allowed (allowed: properties, required, description)"
			if k.Value == "additionalProperties" {
				// Closure of the merged shape is the compiler's
				// responsibility — composeXRefSchema injects
				// additionalProperties: false on the merged consumer
				// schema regardless of the library entry's setting.
				// Authors that need an open-arbitrary surface compose
				// the open object via the $jsonschema: escape rather
				// than try to override the closure here.
				msg += "; use $jsonschema: to embed an open-object schema if the merged shape must accept arbitrary properties"
			}
			return nil, nil, nil, &CompileError{
				Path:    path,
				Line:    nodeLine(k),
				Keyword: k.Value,
				Message: msg,
				Err:     ErrUnknownKeyword(),
			}
		}
	}
	return props, required, description, nil
}

// composeXRefSchema merges a resolved library schema with consumer-side
// siblings under the composition semantics:
//
//   - Consumer properties overwrite library properties on key collision.
//     Resulting map ordering follows library-author-order first, then
//     consumer-added keys in consumer-author-order; library-sourced keys
//     overwritten by consumer entries lose their x-aggrega-source
//     annotation (the property is now consumer-authored).
//   - Required arrays union: library entries first (in library author
//     order), then consumer additions (in consumer author order),
//     deduplicated.
//   - additionalProperties: false injected on the merged shape — the
//     inferred-form-mandatory rule applied to the merged-properties
//     schema (the consumer's outer closure wins regardless of the
//     library entry's own additionalProperties setting).
//   - Description: consumer wins if present; otherwise library-sourced
//     description is preserved.
func composeXRefSchema(resolved Schema, consumerProps Schema, consumerRequired []string, consumerDescription any) Schema {
	out := Schema{}
	// Start from the resolved library shape so library author-order
	// applies before consumer additions.
	for k, v := range resolved {
		out[k] = v
	}

	mergedProperties := Schema{}
	// Library-author-order properties (if present on the resolved schema)
	// land first.
	if libProps, ok := resolved["properties"].(Schema); ok {
		for k, v := range libProps {
			mergedProperties[k] = v
		}
	}
	// Consumer properties overwrite on collision.
	for k, v := range consumerProps {
		mergedProperties[k] = v
	}

	// Required: library first, consumer-add second, dedup.
	mergedRequired := []string{}
	seen := map[string]struct{}{}
	if libReq, ok := resolved["required"].([]any); ok {
		for _, item := range libReq {
			if s, ok := item.(string); ok {
				if _, dup := seen[s]; !dup {
					mergedRequired = append(mergedRequired, s)
					seen[s] = struct{}{}
				}
			}
		}
	}
	if libReq, ok := resolved["required"].([]string); ok {
		for _, s := range libReq {
			if _, dup := seen[s]; !dup {
				mergedRequired = append(mergedRequired, s)
				seen[s] = struct{}{}
			}
		}
	}
	for _, s := range consumerRequired {
		if _, dup := seen[s]; !dup {
			mergedRequired = append(mergedRequired, s)
			seen[s] = struct{}{}
		}
	}

	out["type"] = "object"
	out["properties"] = mergedProperties
	out["additionalProperties"] = false
	if len(mergedRequired) > 0 {
		out["required"] = mergedRequired
	} else {
		delete(out, "required")
	}
	if consumerDescription != nil {
		out["description"] = consumerDescription
	}
	return out
}

// emitComposition recurses each entry of an `allOf` / `anyOf` / `oneOf`
// list, dispatching each composing entry independently. Other keys on
// the node (siblings of the composition keyword) are emitted alongside.
func emitComposition(node *yaml.Node, compKey, path string, ctx *compileCtx) (Schema, error) {
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
					Err:     ErrEscapeContentInvalid(),
				}
			}
			entries := make([]any, 0, len(v.Content))
			for i, e := range v.Content {
				entry, err := compileNode(e, pathJoin(path, compKey+indexSegment(i)), ctx)
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
				Err:     ErrUnknownKeyword(),
			}
		}
		child, err := compileNode(v, pathJoin(path, k), ctx)
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
		case "sensitive":
			out["x-aggrega-sensitive"] = child
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
