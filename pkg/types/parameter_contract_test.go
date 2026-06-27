// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestParameterContract_RoundTripTyped pins typed-contract YAML decode for
// the compact-form vocabulary subset.
func TestParameterContract_RoundTripTyped(t *testing.T) {
	t.Parallel()
	src := `type: integer
description: API port
default: 8080
min: 1024
max: 65535
required: true
`
	var pc ParameterContract
	if err := yaml.Unmarshal([]byte(src), &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pc.Type != "integer" {
		t.Errorf("Type = %q; want integer", pc.Type)
	}
	if pc.Description != "API port" {
		t.Errorf("Description = %q", pc.Description)
	}
	if pc.Default != 8080 {
		t.Errorf("Default = %v (%T); want 8080", pc.Default, pc.Default)
	}
	if !pc.Required {
		t.Errorf("Required = false; want true")
	}
}

// TestParameterContract_RoundTripStringConstraints pins string-shape
// constraint fields (minLength, maxLength, pattern, format).
func TestParameterContract_RoundTripStringConstraints(t *testing.T) {
	t.Parallel()
	src := `type: string
description: Hostname
minLength: 3
maxLength: 63
pattern: "^[a-z][a-z0-9-]*$"
format: hostname
`
	var pc ParameterContract
	if err := yaml.Unmarshal([]byte(src), &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pc.Type != "string" {
		t.Errorf("Type = %q", pc.Type)
	}
	if pc.MinLength == nil || *pc.MinLength != 3 {
		t.Errorf("MinLength = %v; want *3", pc.MinLength)
	}
	if pc.MaxLength == nil || *pc.MaxLength != 63 {
		t.Errorf("MaxLength = %v; want *63", pc.MaxLength)
	}
	if pc.Pattern != "^[a-z][a-z0-9-]*$" {
		t.Errorf("Pattern = %q", pc.Pattern)
	}
	if pc.Format != "hostname" {
		t.Errorf("Format = %q", pc.Format)
	}
}

// TestParameterContract_RoundTripEnum pins enum-shape constraint decode.
func TestParameterContract_RoundTripEnum(t *testing.T) {
	t.Parallel()
	src := `type: string
description: Environment
enum:
  - dev
  - staging
  - prod
default: dev
`
	var pc ParameterContract
	if err := yaml.Unmarshal([]byte(src), &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pc.Enum) != 3 {
		t.Fatalf("Enum length = %d; want 3", len(pc.Enum))
	}
	if pc.Enum[0] != "dev" || pc.Enum[2] != "prod" {
		t.Errorf("Enum = %v", pc.Enum)
	}
}

// TestParameterContract_RoundTripValidate pins the validate-predicate fields
// (validate + validateMessage) — the substantive new authoring affordance
// for cross-field-aware constraints.
func TestParameterContract_RoundTripValidate(t *testing.T) {
	t.Parallel()
	src := `type: integer
description: Replica count
default: 3
validate: '{{ gt .value 0 }}'
validateMessage: replicas must be positive
`
	var pc ParameterContract
	if err := yaml.Unmarshal([]byte(src), &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pc.Validate != "{{ gt .value 0 }}" {
		t.Errorf("Validate = %q", pc.Validate)
	}
	if pc.ValidateMessage != "replicas must be positive" {
		t.Errorf("ValidateMessage = %q", pc.ValidateMessage)
	}
}

// TestDecodeParameterContract_RejectsShorthand pins the post-shorthand-drop
// behavior: bare-scalar entries return a directed error pointing the author
// at the typed-contract syntax.
func TestDecodeParameterContract_RejectsShorthand(t *testing.T) {
	t.Parallel()
	scalars := []any{8080, "string-value", true, false, 1.5, int64(1024), nil}
	for _, s := range scalars {
		_, err := DecodeParameterContract(s)
		if err == nil {
			t.Errorf("DecodeParameterContract(%v) returned no error; expected shorthand-rejection", s)
			continue
		}
		if !strings.Contains(err.Error(), "typed-contract form") {
			t.Errorf("DecodeParameterContract(%v) error should name 'typed-contract form'; got: %v", s, err)
		}
	}
}

// TestDecodeParameterContract_AcceptsTypedForm pins the surviving path: a
// typed-contract map decodes correctly into a ParameterContract.
func TestDecodeParameterContract_AcceptsTypedForm(t *testing.T) {
	t.Parallel()
	typedMap := map[string]any{
		"type":        "string",
		"description": "Hostname",
		"pattern":     "^[a-z]+$",
		"required":    true,
	}
	typedPC, err := DecodeParameterContract(typedMap)
	if err != nil {
		t.Fatalf("DecodeParameterContract(map): %v", err)
	}
	if typedPC.Type != "string" || typedPC.Pattern != "^[a-z]+$" || !typedPC.Required {
		t.Errorf("typed dispatch: Type=%q Pattern=%q Required=%v",
			typedPC.Type, typedPC.Pattern, typedPC.Required)
	}
}

// TestParameterContract_NestedProperties pins typed-form nested object
// shape: a parameter with type: object carries Properties as an
// OrderedMap[any] of child entries (each itself simple OR typed). Pins
// OrderedMap key-ordering fidelity for the typed-contract decoder.
func TestParameterContract_NestedProperties(t *testing.T) {
	t.Parallel()
	src := `type: object
description: Database connection settings
properties:
  host:
    type: string
    default: localhost
  port:
    type: integer
    min: 1
    max: 65535
    default: 5432
`
	var pc ParameterContract
	if err := yaml.Unmarshal([]byte(src), &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pc.Type != "object" {
		t.Errorf("Type = %q", pc.Type)
	}
	if pc.Properties.Len() != 2 {
		t.Fatalf("Properties len = %d; want 2", pc.Properties.Len())
	}
	keys := pc.Properties.Keys()
	if keys[0] != "host" || keys[1] != "port" {
		t.Errorf("Properties keys = %v; want [host port]", keys)
	}
	// Child entry round-trip — host should decode as typed-string with default.
	hostEntry, _ := pc.Properties.Get("host")
	hostPC, err := DecodeParameterContract(hostEntry)
	if err != nil {
		t.Fatalf("decode child: %v", err)
	}
	if hostPC.Type != "string" || hostPC.Default != "localhost" {
		t.Errorf("host child: Type=%q Default=%v", hostPC.Type, hostPC.Default)
	}
}
