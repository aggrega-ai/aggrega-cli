// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonschema

import (
	"strings"
)

// xAggregaSourceKey is the JSON Schema extension annotation key the
// compactform compiler injects on properties contributed by cross-
// document $ref expansion. The same constant lives in
// pkg/compactform/xref_annotations.go; both sides use a string literal
// to avoid the pkg/validator/jsonschema → pkg/compactform import edge
// that would otherwise emerge.
const xAggregaSourceKey = "x-aggrega-source"

// EnrichFinding decorates a validation finding with library-source
// attribution by walking the supplied schema to the finding's FieldPath
// and reading the x-aggrega-source annotation on the schema at that
// location (or the nearest annotated ancestor).
//
// The walk treats FieldPath as a dot-delimited property navigation
// against the schema's properties map (the convention the validator's
// own walker uses for instance paths). When the path resolves to a
// schema carrying x-aggrega-source, finding.LibrarySource is populated;
// finding.ConsumerFile is always set to consumerFile when non-empty so
// formatters can name both endpoints regardless of whether the failing
// constraint was library- or locally-authored.
//
// The function mutates the finding in place and returns it for chaining.
// A nil finding or nil schema is tolerated (returns the finding
// unmodified) so callers can compose enrichment defensively.
func EnrichFinding(f *Finding, schema map[string]any, consumerFile string) *Finding {
	if f == nil {
		return f
	}
	if consumerFile != "" {
		f.ConsumerFile = consumerFile
	}
	if schema == nil {
		return f
	}
	f.LibrarySource = walkAndReadSource(schema, f.FieldPath)
	return f
}

// EnrichReport applies EnrichFinding to every finding in a Report. The
// helper is the typical entry point: validator callers invoke Validate,
// then pass the resulting Report through EnrichReport to attach
// library-source attribution before formatting or returning to the user.
func EnrichReport(r *Report, schema map[string]any, consumerFile string) *Report {
	if r == nil {
		return r
	}
	for i := range r.Findings {
		EnrichFinding(&r.Findings[i], schema, consumerFile)
	}
	return r
}

// FormatEnriched renders a finding's user-facing message with library-
// source attribution when the LibrarySource and ConsumerFile fields are
// populated; otherwise returns the bare Message. The enriched format
// is the following:
//
//	validation failed at /spec/values/displayName
//	  resolved from: BaseComponents#/schemas/BaseComponentParameter
//	  referenced from: aggrega-hub/aggrega-catalog/component/core/datastore/base.yaml
//
// Callers wiring the validator's output into user-facing diagnostics
// invoke FormatEnriched per finding. Callers consuming Findings
// programmatically (the lint pipeline, the future aggrega explain CLI)
// inspect the structured fields directly without invoking the formatter.
func FormatEnriched(f *Finding) string {
	if f == nil {
		return ""
	}
	header := "validation failed"
	if f.FieldPath != "" {
		header += " at " + f.FieldPath
	}
	if f.Keyword != "" {
		header += " (" + f.Keyword + ")"
	}
	if f.Message != "" {
		header += ": " + f.Message
	}
	if f.LibrarySource == "" && f.ConsumerFile == "" {
		return header
	}
	var b strings.Builder
	b.WriteString(header)
	if f.LibrarySource != "" {
		b.WriteString("\n  resolved from: ")
		b.WriteString(f.LibrarySource)
	}
	if f.ConsumerFile != "" {
		b.WriteString("\n  referenced from: ")
		b.WriteString(f.ConsumerFile)
	}
	return b.String()
}

// walkAndReadSource navigates from the schema root to the schema at the
// given dot-delimited property path, then reads the x-aggrega-source
// annotation from that schema. If the leaf schema does not carry the
// annotation, the walk reports the empty string — callers treat that as
// "locally authored" rather than recursing back up the tree.
//
// The dot-delimited convention matches the validator walker's FieldPath
// emission. Bracket-form array indices (e.g. "items[0].port") are
// handled by stripping the index and descending into the property's
// `items:` schema. So a finding at "items[0].port" walks
// schema.properties.items.items.properties.port and reads the
// annotation there — library-sourced properties inside array items
// retain attribution through validator-error formatting.
func walkAndReadSource(schema map[string]any, fieldPath string) string {
	if schema == nil {
		return ""
	}
	if fieldPath == "" {
		return readSourceAnnotation(schema)
	}
	cur := schema
	segments := strings.Split(fieldPath, ".")
	for _, seg := range segments {
		if cur == nil {
			return ""
		}
		propName, hasIndex := splitArrayIndex(seg)
		props, ok := cur["properties"].(map[string]any)
		if !ok {
			return ""
		}
		next, ok := props[propName].(map[string]any)
		if !ok {
			return ""
		}
		cur = next
		// Bracket-form index: descend into the property's items: schema
		// so a finding at items[0] (or items[0].port via subsequent
		// segments) maps to the item-element schema rather than the
		// array container.
		if hasIndex {
			items, ok := cur["items"].(map[string]any)
			if !ok {
				return ""
			}
			cur = items
		}
	}
	return readSourceAnnotation(cur)
}

// splitArrayIndex separates a "name[N]" segment into its property name
// and a flag indicating whether the segment carried a bracket index.
// "name[0]" returns ("name", true); "name" returns ("name", false). The
// numeric index value itself is discarded — JSON Schema's items:
// schema applies uniformly across the array, so we only need to know
// "descend into items:" not "which item."
//
// The bracket contents are not validated as numeric — any non-empty
// segment ending in "]" with a matching "[" is treated as indexed.
// Permissive by design: the upstream FieldPath emitter always produces
// numeric indices, and items: applies uniformly regardless of index
// content, so the semantic ("descend into items:") is correct for any
// bracket-delimited segment.
func splitArrayIndex(segment string) (name string, hasIndex bool) {
	open := strings.IndexByte(segment, '[')
	if open < 0 {
		return segment, false
	}
	if !strings.HasSuffix(segment, "]") {
		return segment, false
	}
	return segment[:open], true
}

// readSourceAnnotation extracts the x-aggrega-source annotation from a
// schema map, returning the empty string when the annotation is absent
// or carries a non-string value.
func readSourceAnnotation(schema map[string]any) string {
	if schema == nil {
		return ""
	}
	if v, ok := schema[xAggregaSourceKey].(string); ok {
		return v
	}
	return ""
}
