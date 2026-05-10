// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
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
