// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"errors"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestDispatchEscape verifies the $jsonschema: escape hatch embeds
// JSON Schema content verbatim at the node's position.
func TestDispatchEscape(t *testing.T) {
	t.Parallel()

	src := `
field:
  $jsonschema: |
    type: object
    unevaluatedProperties: false
    properties:
      key: { type: string }
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	field, ok := out["properties"].(Schema)["field"].(Schema)
	if !ok {
		t.Fatalf("field is not a Schema; got %T", out["properties"].(Schema)["field"])
	}
	if field["type"] != "object" {
		t.Errorf("expected type=object in escape; got %v", field["type"])
	}
	if field["unevaluatedProperties"] != false {
		t.Errorf("expected unevaluatedProperties=false; got %v", field["unevaluatedProperties"])
	}
}

// TestDispatchReference verifies $ref: emits as a passthrough.
func TestDispatchReference(t *testing.T) {
	t.Parallel()

	src := `resources: { $ref: "#/$defs/PodResources" }`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	resources := out["properties"].(Schema)["resources"].(Schema)
	if resources["$ref"] != "#/$defs/PodResources" {
		t.Errorf("expected $ref to round-trip; got %v", resources["$ref"])
	}
}

// TestDispatchEmptyRef verifies an empty $ref: surfaces as
// ErrReferenceUnresolvable.
func TestDispatchEmptyRef(t *testing.T) {
	t.Parallel()

	src := `bad: { $ref: "" }`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrReferenceUnresolvable; got nil")
	}
	if !errors.Is(err, ErrReferenceUnresolvable) {
		t.Fatalf("expected ErrReferenceUnresolvable; got %v", err)
	}
}

// TestDispatchModeOverride verifies the $mode: annotation overrides
// the content-shape heuristic.
func TestDispatchModeOverride(t *testing.T) {
	t.Parallel()

	src := `
container:
  $mode: jsonschema
  type: object
  properties:
    key: { type: string }
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	container := out["properties"].(Schema)["container"].(Schema)
	if container["type"] != "object" {
		t.Errorf("expected type=object via $mode: jsonschema; got %v", container["type"])
	}
}

// TestDispatchModeInvalid verifies an invalid $mode value surfaces as
// ErrAmbiguousDispatch.
func TestDispatchModeInvalid(t *testing.T) {
	t.Parallel()

	src := `field: { $mode: bogus, type: string }`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrAmbiguousDispatch; got nil")
	}
	if !errors.Is(err, ErrAmbiguousDispatch) {
		t.Fatalf("expected ErrAmbiguousDispatch; got %v", err)
	}
}

// TestDispatchComposition verifies allOf / anyOf / oneOf recurse into
// each composing entry independently.
func TestDispatchComposition(t *testing.T) {
	t.Parallel()

	src := `
secondary:
  allOf:
    - { $ref: "#/$defs/NodePool" }
    - replicaCount: { type: integer, min: 2 }
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	secondary := out["properties"].(Schema)["secondary"].(Schema)
	entries, ok := secondary["allOf"].([]any)
	if !ok {
		t.Fatalf("allOf is not a slice; got %T", secondary["allOf"])
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 allOf entries; got %d", len(entries))
	}
	first := entries[0].(Schema)
	if first["$ref"] != "#/$defs/NodePool" {
		t.Errorf("first allOf entry should carry $ref; got %v", first)
	}
}

// TestDispatchCompositionAnyOf verifies anyOf composition with
// sibling validate: + when: + then: keywords (exercises the
// emitComposition path's sibling handling).
func TestDispatchCompositionAnyOf(t *testing.T) {
	t.Parallel()

	src := `
shape:
  anyOf:
    - { type: string, minLength: 1 }
    - { type: integer, min: 0 }
  validate: '{{ ne . nil }}'
  validateMessage: "shape must be non-nil"
  when: '{{ .shapeRequired }}'
  then:
    description: { type: string, required: true }
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	shape := out["properties"].(Schema)["shape"].(Schema)
	if _, ok := shape["anyOf"].([]any); !ok {
		t.Errorf("anyOf should be a slice; got %T", shape["anyOf"])
	}
	if _, ok := shape["x-aggrega-validate"]; !ok {
		t.Errorf("validate: sibling should emit x-aggrega-validate")
	}
	if shape["x-aggrega-validateMessage"] != "shape must be non-nil" {
		t.Errorf("validateMessage sibling missing or wrong; got %v", shape["x-aggrega-validateMessage"])
	}
	if shape["if"] != true {
		t.Errorf("when:+then: should inject if=true; got %v", shape["if"])
	}
}

// TestDispatchCompositionOneOf verifies oneOf composition with a $ref
// sibling key, exercising emitComposition's default-case branch.
func TestDispatchCompositionOneOf(t *testing.T) {
	t.Parallel()

	src := `
target:
  oneOf:
    - { $ref: "#/$defs/A" }
    - { $ref: "#/$defs/B" }
  $ref: "#/$defs/Base"
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	target := out["properties"].(Schema)["target"].(Schema)
	if target["$ref"] != "#/$defs/Base" {
		t.Errorf("sibling $ref should round-trip; got %v", target["$ref"])
	}
	entries, ok := target["oneOf"].([]any)
	if !ok || len(entries) != 2 {
		t.Errorf("oneOf should carry 2 entries; got %v", target["oneOf"])
	}
}

// TestDispatchCompositionEntryUnknown verifies the composition path's
// unknown-keyword rejection on a sibling key.
func TestDispatchCompositionEntryUnknown(t *testing.T) {
	t.Parallel()

	src := `
bad:
  anyOf:
    - { type: string }
  bogus: 42
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrUnknownKeyword; got nil")
	}
	if !errors.Is(err, ErrUnknownKeyword) {
		t.Errorf("expected ErrUnknownKeyword; got %v", err)
	}
}

// TestDispatchCompositionWrongShape verifies that allOf with a scalar
// value (not a sequence) rejects with ErrEscapeContentInvalid.
func TestDispatchCompositionWrongShape(t *testing.T) {
	t.Parallel()

	src := `
bad:
  allOf: "not a sequence"
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrEscapeContentInvalid; got nil")
	}
	if !errors.Is(err, ErrEscapeContentInvalid) {
		t.Errorf("expected ErrEscapeContentInvalid; got %v", err)
	}
}

// TestDispatchEscapeMalformedYAML verifies a $jsonschema: block-scalar
// carrying malformed YAML rejects with ErrEscapeContentInvalid.
func TestDispatchEscapeMalformedYAML(t *testing.T) {
	t.Parallel()

	src := "field:\n  $jsonschema: \"unclosed_string\nthis: is broken\""
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		// If the outer YAML fails to parse, the test setup is wrong;
		// retry with a well-formed outer and an inner that yaml.v3 will
		// reject.
		_ = err
	}
	src2 := `
field:
  $jsonschema: |
    type: object
    properties:
      a: { type: "broken
`
	if err := yaml.Unmarshal([]byte(src2), &input); err != nil {
		t.Fatalf("outer unmarshal should succeed; got %v", err)
	}
	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrEscapeContentInvalid; got nil")
	}
	if !errors.Is(err, ErrEscapeContentInvalid) {
		t.Errorf("expected ErrEscapeContentInvalid; got %v", err)
	}
}

// TestDispatchEscapeWrongShape verifies a $jsonschema: with neither
// scalar nor mapping value rejects.
func TestDispatchEscapeWrongShape(t *testing.T) {
	t.Parallel()

	src := `
field:
  $jsonschema: [one, two, three]
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrEscapeContentInvalid; got nil")
	}
	if !errors.Is(err, ErrEscapeContentInvalid) {
		t.Errorf("expected ErrEscapeContentInvalid; got %v", err)
	}
}

// TestDispatchJSONSchemaPassthroughUnknownKey verifies the unknown-
// keyword error path through emitJSONSchemaPassthrough.
func TestDispatchJSONSchemaPassthroughUnknownKey(t *testing.T) {
	t.Parallel()

	src := `
field:
  $mode: jsonschema
  bogus_keyword_xyz: "value"
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrUnknownKeyword; got nil")
	}
	if !errors.Is(err, ErrUnknownKeyword) {
		t.Errorf("expected ErrUnknownKeyword; got %v", err)
	}
}

// TestDispatchSequenceAtRoot verifies a YAML sequence at the root
// compiles without error (exercises emitSequence path).
func TestDispatchSequenceAtRoot(t *testing.T) {
	t.Parallel()

	src := `
listed:
  type: array
  items:
    type: array
    items: { type: string }
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	listed := out["properties"].(Schema)["listed"].(Schema)
	inner := listed["items"].(Schema)
	if inner["type"] != "array" {
		t.Errorf("nested array items: should compile; got %v", inner)
	}
}
