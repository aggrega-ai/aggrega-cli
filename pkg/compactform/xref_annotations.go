// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compactform

// xAggregaSourceKey is the JSON Schema extension keyword the compactform
// expander injects on properties contributed by cross-document $ref
// expansion. JSON Schema 2020-12 treats x-* keywords as extension
// annotations: spec-compliant validators ignore them; Aggrega-aware
// tooling (the validator error formatter at pkg/validator/jsonschema/,
// the future aggrega explain CLI) consults them to map a property back
// to its originating SchemaLibrary entry.
//
// The annotation value is a string of the form "<LibraryName>#/schemas/
// <EntryName>" — matching the cross-document $ref authoring shape so
// the explain output and the source authoring declaration use one
// vocabulary.
const xAggregaSourceKey = "x-aggrega-source"

// annotateLibrarySource decorates every property in the resolved library
// schema with x-aggrega-source annotations naming the originating
// library + entry. The annotation lands on each property's own schema
// map (not on the enclosing object) so downstream tooling can resolve
// the source of a single property without traversing back to the parent.
//
// The annotation propagates recursively into nested object/array
// properties: a property whose schema is itself an object with its own
// properties: map carries the annotation at every nested level. This
// keeps source attribution intact for deeply-nested library shapes (a
// library entry whose schema is a nested object preserves attribution
// for the inner fields).
//
// The function mutates the schema in place. The caller (emitWithXRefExpand
// in emit.go) passes the resolved schema by reference; mutation is the
// expected semantic.
//
// When the resolved schema has no `properties:` map (e.g. a primitive-
// typed entry like a `URL: { type: string }` library entry), the
// annotation lands at the root of the schema instead — the entire schema
// is library-sourced.
func annotateLibrarySource(schema Schema, libraryName, entryName string) {
	if schema == nil {
		return
	}
	sourceValue := librarySourceValue(libraryName, entryName)

	props, hasProps := schema["properties"].(Schema)
	if !hasProps || len(props) == 0 {
		// Primitive or non-object schema: annotate the schema itself.
		schema[xAggregaSourceKey] = sourceValue
		return
	}

	// Object-typed library entry: annotate each property and recurse
	// into nested object properties so attribution stays attached deep
	// in the schema tree.
	for _, propSchema := range props {
		annotatePropertyRecursive(propSchema, sourceValue)
	}
}

// annotatePropertyRecursive decorates a single property's schema with
// the x-aggrega-source annotation and recurses into nested structures
// (nested object properties, array item schemas) so the attribution
// stays attached across deeply-nested library shapes.
//
// The function tolerates non-Schema property values (e.g. a property
// whose schema is a primitive yaml value rather than a map); those are
// not decorated because there is no place to attach the annotation.
func annotatePropertyRecursive(propSchema any, sourceValue string) {
	s, ok := propSchema.(Schema)
	if !ok {
		return
	}
	s[xAggregaSourceKey] = sourceValue

	// Recurse into nested object properties.
	if nestedProps, ok := s["properties"].(Schema); ok {
		for _, nested := range nestedProps {
			annotatePropertyRecursive(nested, sourceValue)
		}
	}

	// Recurse into items: schema for array-typed properties.
	if items, ok := s["items"].(Schema); ok {
		annotatePropertyRecursive(items, sourceValue)
	}
}

// librarySourceValue formats the x-aggrega-source annotation value for a
// library + entry pair. The shape matches the cross-document $ref
// authoring URI so the explain output and other tooling can compare
// annotation values directly to $ref values.
func librarySourceValue(libraryName, entryName string) string {
	return libraryName + "#/schemas/" + entryName
}

// LibrarySourceAnnotation extracts the x-aggrega-source value from a
// schema, returning the empty string when the annotation is absent. The
// public helper exists so the validator error enrichment path at
// pkg/validator/jsonschema/error_enrichment.go can consume the
// annotation without depending on the internal annotation-key constant.
//
// Callers walk a validator finding's schema path, retrieve the schema at
// the failing location, and pass it here to obtain the originating
// library + entry pair for inclusion in the formatted error message.
func LibrarySourceAnnotation(schema Schema) string {
	if schema == nil {
		return ""
	}
	if v, ok := schema[xAggregaSourceKey].(string); ok {
		return v
	}
	return ""
}
