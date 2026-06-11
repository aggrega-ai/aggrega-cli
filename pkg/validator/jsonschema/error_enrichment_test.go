// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the validator error-enrichment helper. The helper walks the
// validator's input schema to a finding's FieldPath, reads the
// x-aggrega-source extension annotation injected by the compactform
// cross-document $ref expander, and decorates the finding with library
// + consumer attribution. A validation error in a property pulled from
// a SchemaLibrary surfaces the originating library + consumer file
// location, recovering the debugging clarity that runtime $ref
// resolution would have provided naturally.

package jsonschema

import (
	"strings"
	"testing"
)

func TestEnrichFinding_AnnotationPresent(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"displayName": map[string]any{
				"type":              "string",
				xAggregaSourceKey:   "BaseComponents#/schemas/BaseComponentParameter",
			},
		},
	}
	f := &Finding{
		FieldPath: "displayName",
		Keyword:   "type",
		Message:   "expected string; got integer",
	}
	EnrichFinding(f, schema, "catalog/component/core/datastore/base.yaml")

	if f.LibrarySource != "BaseComponents#/schemas/BaseComponentParameter" {
		t.Errorf("LibrarySource = %q; want BaseComponents#/schemas/BaseComponentParameter", f.LibrarySource)
	}
	if f.ConsumerFile != "catalog/component/core/datastore/base.yaml" {
		t.Errorf("ConsumerFile = %q; want catalog/component/core/datastore/base.yaml", f.ConsumerFile)
	}
}

func TestEnrichFinding_AnnotationAbsent(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"locallyAuthored": map[string]any{
				"type": "string",
			},
		},
	}
	f := &Finding{
		FieldPath: "locallyAuthored",
		Keyword:   "type",
		Message:   "expected string; got integer",
	}
	EnrichFinding(f, schema, "catalog/component/x.yaml")

	if f.LibrarySource != "" {
		t.Errorf("locally-authored finding should not carry LibrarySource; got %q", f.LibrarySource)
	}
	// ConsumerFile populates regardless of annotation presence so
	// formatters can surface the source site even for locally-authored
	// failures.
	if f.ConsumerFile != "catalog/component/x.yaml" {
		t.Errorf("ConsumerFile = %q; want catalog/component/x.yaml", f.ConsumerFile)
	}
}

func TestEnrichFinding_NestedPropertyAnnotation(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"outer": map[string]any{
				"type":              "object",
				xAggregaSourceKey:   "Lib#/schemas/Outer",
				"properties": map[string]any{
					"inner": map[string]any{
						"type":              "string",
						xAggregaSourceKey:   "Lib#/schemas/Outer",
					},
				},
			},
		},
	}
	f := &Finding{
		FieldPath: "outer.inner",
		Keyword:   "type",
		Message:   "expected string; got integer",
	}
	EnrichFinding(f, schema, "consumer.yaml")

	if f.LibrarySource != "Lib#/schemas/Outer" {
		t.Errorf("nested-property LibrarySource = %q; want Lib#/schemas/Outer", f.LibrarySource)
	}
}

func TestEnrichFinding_NilFinding(t *testing.T) {
	t.Parallel()

	// Defensive: a nil finding pointer is tolerated (returns the nil
	// pointer; never panics). Callers compose enrichment defensively in
	// pipelines that may dispatch finding pointers from heterogeneous
	// sources.
	if got := EnrichFinding(nil, nil, ""); got != nil {
		t.Errorf("nil finding should return nil; got %v", got)
	}
}

func TestEnrichFinding_NilSchema(t *testing.T) {
	t.Parallel()

	f := &Finding{FieldPath: "x", Message: "msg"}
	EnrichFinding(f, nil, "consumer.yaml")

	if f.LibrarySource != "" {
		t.Errorf("nil schema should leave LibrarySource empty; got %q", f.LibrarySource)
	}
	// ConsumerFile still populates when schema is nil — the consumer
	// location is available independent of schema-walk results.
	if f.ConsumerFile != "consumer.yaml" {
		t.Errorf("ConsumerFile should populate; got %q", f.ConsumerFile)
	}
}

func TestEnrichFinding_EmptyFieldPath(t *testing.T) {
	t.Parallel()

	// A root-level finding (FieldPath="") reads the annotation from the
	// schema root. Useful for whole-instance validation failures.
	schema := map[string]any{
		"type":              "object",
		xAggregaSourceKey:   "Lib#/schemas/Root",
	}
	f := &Finding{FieldPath: "", Message: "msg"}
	EnrichFinding(f, schema, "")

	if f.LibrarySource != "Lib#/schemas/Root" {
		t.Errorf("root annotation = %q; want Lib#/schemas/Root", f.LibrarySource)
	}
}

func TestEnrichReport_DecoratesAllFindings(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{
				"type":              "string",
				xAggregaSourceKey:   "Lib#/schemas/Entry",
			},
			"b": map[string]any{
				"type": "string",
			},
		},
	}
	r := &Report{
		Findings: []Finding{
			{FieldPath: "a", Keyword: "type"},
			{FieldPath: "b", Keyword: "type"},
		},
	}
	EnrichReport(r, schema, "consumer.yaml")

	if r.Findings[0].LibrarySource != "Lib#/schemas/Entry" {
		t.Errorf("finding[0] LibrarySource = %q; want Lib#/schemas/Entry", r.Findings[0].LibrarySource)
	}
	if r.Findings[1].LibrarySource != "" {
		t.Errorf("finding[1] (locally authored) LibrarySource = %q; want empty", r.Findings[1].LibrarySource)
	}
	for i := range r.Findings {
		if r.Findings[i].ConsumerFile != "consumer.yaml" {
			t.Errorf("finding[%d] ConsumerFile = %q; want consumer.yaml", i, r.Findings[i].ConsumerFile)
		}
	}
}

func TestFormatEnriched_WithAnnotation(t *testing.T) {
	t.Parallel()

	f := &Finding{
		FieldPath:     "spec.values.port",
		Keyword:       "minimum",
		Message:       "expected >= 1024; got 80",
		LibrarySource: "BaseComponents#/schemas/PortContract",
		ConsumerFile:  "consumer.yaml",
	}
	got := FormatEnriched(f)
	for _, want := range []string{
		"spec.values.port",
		"minimum",
		"expected >= 1024; got 80",
		"resolved from: BaseComponents#/schemas/PortContract",
		"referenced from: consumer.yaml",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output missing %q; got:\n%s", want, got)
		}
	}
}

func TestFormatEnriched_BareMessageWhenUnannotated(t *testing.T) {
	t.Parallel()

	f := &Finding{
		FieldPath: "field",
		Keyword:   "type",
		Message:   "expected string",
	}
	got := FormatEnriched(f)
	if strings.Contains(got, "resolved from") {
		t.Errorf("bare format should not include resolved-from line; got:\n%s", got)
	}
	if strings.Contains(got, "referenced from") {
		t.Errorf("bare format should not include referenced-from line; got:\n%s", got)
	}
}

func TestFormatEnriched_NilFinding(t *testing.T) {
	t.Parallel()

	if got := FormatEnriched(nil); got != "" {
		t.Errorf("nil finding should format to empty string; got %q", got)
	}
}

func TestWalkAndReadSource_ArrayIndexNavigatesItemsSchema(t *testing.T) {
	t.Parallel()

	// Bracket-form array indices in FieldPath descend into the property's
	// `items:` schema — library-sourced properties inside array items
	// retain attribution at validator-error time. A finding at
	// "items[0]" maps to schema.properties.items.items (the item-element
	// schema, not the array container).
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":            "object",
					xAggregaSourceKey: "Lib#/schemas/ItemEntry",
				},
			},
		},
	}
	if got := walkAndReadSource(schema, "items[0]"); got != "Lib#/schemas/ItemEntry" {
		t.Errorf("array-indexed path = %q; want Lib#/schemas/ItemEntry", got)
	}
}

func TestWalkAndReadSource_ArrayIndexThenProperty(t *testing.T) {
	t.Parallel()

	// A finding at "items[0].port" walks
	// schema.properties.items.items.properties.port and reads the
	// annotation on the inner property. Pins the multi-segment + bracket
	// case so library-sourced fields deep inside array items stay
	// attributed.
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"port": map[string]any{
							"type":            "integer",
							xAggregaSourceKey: "Lib#/schemas/Probe",
						},
					},
				},
			},
		},
	}
	if got := walkAndReadSource(schema, "items[0].port"); got != "Lib#/schemas/Probe" {
		t.Errorf("nested array+property path = %q; want Lib#/schemas/Probe", got)
	}
}

func TestSplitArrayIndex(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input        string
		wantName     string
		wantHasIndex bool
	}{
		{"name", "name", false},
		{"name[0]", "name", true},
		{"name[42]", "name", true},
		{"name[", "name[", false},  // unclosed — treat as opaque
		{"[0]", "", true},
		{"", "", false},
	}
	for _, c := range cases {
		name, hasIndex := splitArrayIndex(c.input)
		if name != c.wantName || hasIndex != c.wantHasIndex {
			t.Errorf("splitArrayIndex(%q) = (%q, %v); want (%q, %v)",
				c.input, name, hasIndex, c.wantName, c.wantHasIndex)
		}
	}
}
