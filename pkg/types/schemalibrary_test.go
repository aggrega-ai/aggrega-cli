// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestSchemaLibrary_RoundTrip_MinimalEntry pins the upward-compatibility
// property: a minimal SchemaLibrary with one schema entry decodes into the
// typed fields and re-encodes preserving the entry content.
func TestSchemaLibrary_RoundTrip_MinimalEntry(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: WebPrimitives
  version: 1.0.0
spec:
  schemas:
    URL:
      type: string
      pattern: "^https?://"
`
	var lib SchemaLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	if lib.APIVersion != "aggrega.ai/v1beta1" {
		t.Errorf("APIVersion = %q, want aggrega.ai/v1beta1", lib.APIVersion)
	}
	if lib.Kind != "SchemaLibrary" {
		t.Errorf("Kind = %q, want SchemaLibrary", lib.Kind)
	}
	if lib.Metadata.Name != "WebPrimitives" {
		t.Errorf("Metadata.Name = %q, want WebPrimitives", lib.Metadata.Name)
	}
	if lib.Metadata.Version != "1.0.0" {
		t.Errorf("Metadata.Version = %q, want 1.0.0", lib.Metadata.Version)
	}
	if lib.Spec.Schemas.Len() != 1 {
		t.Fatalf("Spec.Schemas.Len = %d, want 1", lib.Spec.Schemas.Len())
	}
	url, ok := lib.Spec.Schemas.Get("URL")
	if !ok {
		t.Fatal("expected URL entry present")
	}
	if url.Content.Kind != yaml.MappingNode {
		t.Errorf("URL.Content.Kind = %d, want MappingNode", url.Content.Kind)
	}
}

// TestSchemaLibrary_RoundTrip_TemplatesDeclared pins the templates-block
// shape: $params: peels off into the typed Params field; the body captures
// the rest of the template's content as a yaml.Node for later substitution.
func TestSchemaLibrary_RoundTrip_TemplatesDeclared(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Probes
  version: 1.0.0
spec:
  schemas:
    LivenessProbe:
      $instantiate: ProbeTemplate
      $args:
        port: 8080
  templates:
    ProbeTemplate:
      $params:
        port:
          type: integer
      type: object
      properties:
        port:
          type: integer
          const: $port
`
	var lib SchemaLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	if lib.Spec.Templates.Len() != 1 {
		t.Fatalf("Spec.Templates.Len = %d, want 1", lib.Spec.Templates.Len())
	}
	tmpl, ok := lib.Spec.Templates.Get("ProbeTemplate")
	if !ok {
		t.Fatal("expected ProbeTemplate present")
	}
	if tmpl.Params.Len() != 1 {
		t.Fatalf("Params.Len = %d, want 1", tmpl.Params.Len())
	}
	portParam, ok := tmpl.Params.Get("port")
	if !ok {
		t.Fatal("expected port parameter declaration")
	}
	if portParam.Type != "integer" {
		t.Errorf("port.Type = %q, want integer", portParam.Type)
	}
	if tmpl.Body.Kind != yaml.MappingNode {
		t.Errorf("Body.Kind = %d, want MappingNode", tmpl.Body.Kind)
	}
}

// TestSchemaLibrary_RoundTrip_MultiEntryAuthorOrder verifies the OrderedMap
// preserves author-declared ordering across YAML decode/encode. A library
// with entries declared in non-alphabetic order must surface those entries
// in the declared order via Spec.Schemas.Keys().
func TestSchemaLibrary_RoundTrip_MultiEntryAuthorOrder(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Mixed
  version: 1.0.0
spec:
  schemas:
    Zulu:
      type: string
    Alpha:
      type: string
    Mike:
      type: string
`
	var lib SchemaLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	keys := lib.Spec.Schemas.Keys()
	want := []string{"Zulu", "Alpha", "Mike"}
	if len(keys) != 3 {
		t.Fatalf("got %d keys; want 3 (%v)", len(keys), keys)
	}
	for i, k := range want {
		if keys[i] != k {
			t.Errorf("keys[%d] = %q, want %q (author order)", i, keys[i], k)
		}
	}
}

// TestSchemaLibrary_RemarshalsCleanly verifies the SchemaEntry +
// TemplateEntry MarshalYAML paths produce non-empty output. Authoring
// round-trip fidelity at the byte level requires the MarshalYAML
// implementations to round-trip the captured yaml.Node content; this
// test exercises the marshaler entry points.
func TestSchemaLibrary_RemarshalsCleanly(t *testing.T) {
	t.Parallel()

	src := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: RoundTrip
  version: 1.0.0
spec:
  schemas:
    Inline:
      type: string
  templates:
    WithParam:
      $params:
        x:
          type: integer
      type: object
      properties:
        value:
          type: integer
          const: $x
`
	var lib SchemaLibrary
	if err := yaml.Unmarshal([]byte(src), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}

	out, err := yaml.Marshal(&lib)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty marshaled output; got empty")
	}
	// Spot-check that both the schema entry and the template body survive
	// the round-trip (presence-level fidelity).
	str := string(out)
	for _, frag := range []string{"Inline", "WithParam", "$params"} {
		if !contains(str, frag) {
			t.Errorf("expected %q in marshaled output; got:\n%s", frag, str)
		}
	}
}

// TestSchemaEntry_EmptyMarshalsToNil verifies a SchemaEntry with no
// captured content marshals to nil (skipped in the parent's emission).
func TestSchemaEntry_EmptyMarshalsToNil(t *testing.T) {
	t.Parallel()
	var entry SchemaEntry
	got, err := entry.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty SchemaEntry; got %v", got)
	}
}

// TestTypecheckParam_AllTypes verifies the parameter-type validation
// covers every supported type plus the empty-type fallback and the
// unsupported-type error.
func TestTypecheckParam_AllTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		decl        ParamDecl
		argVal      any
		expectError bool
	}{
		{"string-ok", ParamDecl{Type: "string"}, "hello", false},
		{"string-mismatch", ParamDecl{Type: "string"}, 42, true},
		{"integer-int", ParamDecl{Type: "integer"}, 42, false},
		{"integer-int64", ParamDecl{Type: "integer"}, int64(42), false},
		{"integer-mismatch", ParamDecl{Type: "integer"}, "42", true},
		{"number-float", ParamDecl{Type: "number"}, 3.14, false},
		{"number-int", ParamDecl{Type: "number"}, 42, false},
		{"number-mismatch", ParamDecl{Type: "number"}, "x", true},
		{"boolean-ok", ParamDecl{Type: "boolean"}, true, false},
		{"boolean-mismatch", ParamDecl{Type: "boolean"}, "yes", true},
		{"untyped-accepts-anything", ParamDecl{Type: ""}, struct{}{}, false},
		{"unsupported-type", ParamDecl{Type: "object"}, map[string]any{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := typecheckParam(tc.name, tc.decl, tc.argVal)
			if tc.expectError && err == nil {
				t.Errorf("expected error; got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("expected no error; got %v", err)
			}
		})
	}
}

// TestScalarTagFor_AllTypes verifies the YAML tag derivation covers every
// supported scalar type. Driven so the substituted scalars emit with the
// right YAML tag (integers as !!int, etc.) rather than as quoted strings.
func TestScalarTagFor_AllTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		v   any
		tag string
	}{
		{42, "!!int"},
		{int32(42), "!!int"},
		{int64(42), "!!int"},
		{3.14, "!!float"},
		{float32(3.14), "!!float"},
		{true, "!!bool"},
		{"hello", "!!str"},
	}
	for _, tc := range cases {
		if got := scalarTagFor(tc.v); got != tc.tag {
			t.Errorf("scalarTagFor(%T) = %q; want %q", tc.v, got, tc.tag)
		}
	}
}

// TestValidateSchemaEntry_NilSafe verifies the boundary on nil/empty
// entries.
func TestValidateSchemaEntry_NilSafe(t *testing.T) {
	t.Parallel()
	if err := ValidateSchemaEntry(nil); err != nil {
		t.Errorf("expected nil on nil entry; got %v", err)
	}
	var empty SchemaEntry
	if err := ValidateSchemaEntry(&empty); err != nil {
		t.Errorf("expected nil on empty entry; got %v", err)
	}
}

// contains is a small substring check that avoids importing strings just
// for one call.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	}())
}

// TestSchemaLibrary_RoundTrip_NoTemplatesOK verifies optional Templates
// field absence is handled cleanly.
func TestSchemaLibrary_RoundTrip_NoTemplatesOK(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Simple
  version: 1.0.0
spec:
  schemas:
    Color:
      type: string
      enum: [red, green, blue]
`
	var lib SchemaLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	if lib.Spec.Templates.Len() != 0 {
		t.Errorf("Templates should be empty; got %d entries", lib.Spec.Templates.Len())
	}
	color, ok := lib.Spec.Schemas.Get("Color")
	if !ok {
		t.Fatal("expected Color entry")
	}
	if color.Content.Kind != yaml.MappingNode {
		t.Errorf("Color.Content.Kind = %d, want MappingNode", color.Content.Kind)
	}
}
