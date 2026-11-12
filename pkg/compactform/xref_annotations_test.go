// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Unit tests for the source-map annotation helpers. The annotation
// pipeline has two roles: injection (annotateLibrarySource decorates a
// resolved library schema with x-aggrega-source values at every level
// of the schema tree) and extraction (LibrarySourceAnnotation reads
// the annotation from a schema, for consumers like the validator
// error-enrichment formatter). The xref_expander_test.go integration
// tests exercise both helpers via Compile; these unit tests pin the
// helpers' behaviour in isolation for edge cases that don't surface
// naturally through Compile (primitive-entry annotation, deeply-nested
// array items, schema-with-no-properties).

package compactform

import "testing"

// TestAnnotateLibrarySource_ObjectProperties confirms a Schema with a
// properties map gets the annotation on each property, not at the root.
// The annotation lands per-property because downstream tooling resolves
// the source of a single property without traversing back to the parent.
func TestAnnotateLibrarySource_ObjectProperties(t *testing.T) {
	t.Parallel()

	schema := Schema{
		"type": "object",
		"properties": Schema{
			"a": Schema{"type": "string"},
			"b": Schema{"type": "integer"},
		},
	}
	annotateLibrarySource(schema, "Lib", "Entry")

	props, _ := schema["properties"].(Schema)
	a, _ := props["a"].(Schema)
	b, _ := props["b"].(Schema)
	want := "Lib#/schemas/Entry"
	if got := LibrarySourceAnnotation(a); got != want {
		t.Errorf("property a annotation = %q; want %q", got, want)
	}
	if got := LibrarySourceAnnotation(b); got != want {
		t.Errorf("property b annotation = %q; want %q", got, want)
	}
	// Root schema does not carry the annotation when properties are
	// present — attribution lands per-property, not at the container.
	if got := LibrarySourceAnnotation(schema); got != "" {
		t.Errorf("root with properties should not carry annotation; got %q", got)
	}
}

// TestAnnotateLibrarySource_PrimitiveSchema confirms a Schema with no
// `properties:` map (a primitive-typed entry like `URL: { type: string }`)
// gets the annotation at the root. The whole schema is library-sourced,
// so the annotation has nowhere else to land.
func TestAnnotateLibrarySource_PrimitiveSchema(t *testing.T) {
	t.Parallel()

	schema := Schema{
		"type":    "string",
		"pattern": "^https?://",
	}
	annotateLibrarySource(schema, "WebPrimitives", "URL")

	want := "WebPrimitives#/schemas/URL"
	if got := LibrarySourceAnnotation(schema); got != want {
		t.Errorf("primitive schema annotation = %q; want %q", got, want)
	}
}

// TestAnnotateLibrarySource_NestedObjects confirms the annotation
// recurses into nested object properties so deeply-nested fields stay
// attributed to their originating library entry.
func TestAnnotateLibrarySource_NestedObjects(t *testing.T) {
	t.Parallel()

	schema := Schema{
		"type": "object",
		"properties": Schema{
			"outer": Schema{
				"type": "object",
				"properties": Schema{
					"inner": Schema{"type": "string"},
				},
			},
		},
	}
	annotateLibrarySource(schema, "Lib", "Entry")

	outer, _ := schema["properties"].(Schema)["outer"].(Schema)
	inner, _ := outer["properties"].(Schema)["inner"].(Schema)
	want := "Lib#/schemas/Entry"
	if got := LibrarySourceAnnotation(outer); got != want {
		t.Errorf("outer annotation = %q; want %q", got, want)
	}
	if got := LibrarySourceAnnotation(inner); got != want {
		t.Errorf("inner annotation = %q; want %q", got, want)
	}
}

// TestAnnotateLibrarySource_ArrayItems confirms the annotation recurses
// into items: schema for array-typed properties — a library entry
// declaring an array of objects keeps attribution on the item shape.
func TestAnnotateLibrarySource_ArrayItems(t *testing.T) {
	t.Parallel()

	schema := Schema{
		"type": "object",
		"properties": Schema{
			"list": Schema{
				"type": "array",
				"items": Schema{
					"type": "object",
					"properties": Schema{
						"field": Schema{"type": "string"},
					},
				},
			},
		},
	}
	annotateLibrarySource(schema, "Lib", "Entry")

	list, _ := schema["properties"].(Schema)["list"].(Schema)
	items, _ := list["items"].(Schema)
	itemField, _ := items["properties"].(Schema)["field"].(Schema)
	want := "Lib#/schemas/Entry"
	if got := LibrarySourceAnnotation(list); got != want {
		t.Errorf("list annotation = %q; want %q", got, want)
	}
	if got := LibrarySourceAnnotation(items); got != want {
		t.Errorf("items annotation = %q; want %q", got, want)
	}
	if got := LibrarySourceAnnotation(itemField); got != want {
		t.Errorf("items.field annotation = %q; want %q", got, want)
	}
}

// TestAnnotateLibrarySource_NilSchema confirms the helper tolerates a
// nil schema (no panic, no work) — defensive contract matching the
// rest of the compactform package's nil-tolerant pattern.
func TestAnnotateLibrarySource_NilSchema(t *testing.T) {
	t.Parallel()
	annotateLibrarySource(nil, "Lib", "Entry")
}

// TestAnnotateLibrarySource_EmptyProperties confirms a Schema with an
// empty properties map gets the annotation at the root rather than on
// each property (because there are no properties).
func TestAnnotateLibrarySource_EmptyProperties(t *testing.T) {
	t.Parallel()

	schema := Schema{
		"type":       "object",
		"properties": Schema{},
	}
	annotateLibrarySource(schema, "Lib", "Entry")

	want := "Lib#/schemas/Entry"
	if got := LibrarySourceAnnotation(schema); got != want {
		t.Errorf("schema with empty properties should carry annotation at root; got %q want %q", got, want)
	}
}

// TestLibrarySourceAnnotation_Absent confirms an unannotated schema
// returns the empty string — callers (the validator error formatter)
// distinguish present-but-empty from missing via this contract.
func TestLibrarySourceAnnotation_Absent(t *testing.T) {
	t.Parallel()

	schema := Schema{"type": "string"}
	if got := LibrarySourceAnnotation(schema); got != "" {
		t.Errorf("unannotated schema = %q; want empty string", got)
	}
}

// TestLibrarySourceAnnotation_NilSchema confirms nil-tolerance — the
// validator error formatter walks a schema path that may bottom out at
// a nil sub-schema; the helper returns the empty string rather than
// panicking.
func TestLibrarySourceAnnotation_NilSchema(t *testing.T) {
	t.Parallel()

	if got := LibrarySourceAnnotation(nil); got != "" {
		t.Errorf("nil schema = %q; want empty string", got)
	}
}

// TestLibrarySourceAnnotation_NonStringValue confirms the helper does
// not panic when x-aggrega-source is present with a non-string value
// (defensive against a future authoring shape that authors a structured
// annotation; today the contract is string-only).
func TestLibrarySourceAnnotation_NonStringValue(t *testing.T) {
	t.Parallel()

	schema := Schema{xAggregaSourceKey: 42}
	if got := LibrarySourceAnnotation(schema); got != "" {
		t.Errorf("non-string annotation should return empty string; got %q", got)
	}
}
