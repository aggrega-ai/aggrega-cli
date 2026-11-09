// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/stringformat"
)

// TestLoadAggregateParameterContract_EnvelopeRejection_BadType pins
// envelope-validation: a contract entry declaring an invalid type
// surfaces at workspace load (LoadAggregateParameterContract) rather
// than as a contract-violation at Step 1a.
func TestLoadAggregateParameterContract_EnvelopeRejection_BadType(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	src := `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: test
  version: 1.0.0
spec:
  parameters:
    bad:
      type: invalid
`
	path := filepath.Join(tmp, "aggregate.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadAggregateParameterContract(path)
	if err == nil {
		t.Fatalf("expected envelope-validation error for invalid type; got nil")
	}
	var re *ResolverError
	if !errors.As(err, &re) {
		t.Fatalf("expected *ResolverError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrYAMLLoad()) {
		t.Errorf("expected ErrYAMLLoad sentinel; got %v", err)
	}
}

// TestLoadAggregateParameterContract_EnvelopeRejection_BadMinLength pins
// envelope-validation for the minLength field type mismatch (the schema
// requires integer; a string fails).
func TestLoadAggregateParameterContract_EnvelopeRejection_BadMinLength(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	src := `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: test
  version: 1.0.0
spec:
  parameters:
    name:
      type: string
      minLength: not-a-number
`
	path := filepath.Join(tmp, "aggregate.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadAggregateParameterContract(path)
	if err == nil {
		t.Fatalf("expected envelope-validation error for non-integer minLength; got nil")
	}
	if !errors.Is(err, ErrYAMLLoad()) {
		t.Errorf("expected ErrYAMLLoad sentinel; got %v", err)
	}
}

// TestLoadAggregateParameterContract_EnvelopeAccepts_ValidContract pins
// the happy path: a fully-valid typed-form contract passes envelope
// validation cleanly.
func TestLoadAggregateParameterContract_EnvelopeAccepts_ValidContract(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	src := `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: test
  version: 1.0.0
spec:
  parameters:
    port:
      type: integer
      min: 1024
      max: 65535
      default: 8080
    host:
      type: string
      pattern: "^[a-z0-9.-]+$"
      required: true
    display_name:
      type: string
      default: "literal-default"
`
	path := filepath.Join(tmp, "aggregate.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	contract, err := LoadAggregateParameterContract(path)
	if err != nil {
		t.Fatalf("expected no envelope error on valid contract; got %v", err)
	}
	if contract.Len() != 3 {
		t.Errorf("expected 3 contract entries; got %d", contract.Len())
	}
}

// TestParameterContractSchema_NoDrift is the maintenance-contract guard for
// the hardcoded parameterContractSchemaJSON literal. It reads the canonical
// Aggregate schema, extracts the `properties.spec.properties.parameters`
// subtree, and asserts byte-equivalent content (after canonical JSON
// formatting) against the hardcoded literal. Drift in either direction fails
// the test with a directed message naming the canonical source.
//
// The canonical schema is compiled into this test binary from a referenced
// variable, so a schema edit invalidates this package's cached result rather
// than serving a stale one.
//
// MAINTENANCE — when the Aggregate schema legitimately changes its
// `properties.spec.properties.parameters` subtree, update the hardcoded
// literal at parameterContractSchemaJSON in lockstep.
func TestParameterContractSchema_NoDrift(t *testing.T) {
	t.Parallel()

	const canonicalSource = "aggregate-v1beta1.schema.json"

	var root map[string]any
	if err := json.Unmarshal([]byte(schemas.AggregateV1beta1), &root); err != nil {
		t.Fatalf("unmarshal canonical schema: %v", err)
	}
	props, _ := root["properties"].(map[string]any)
	spec, _ := props["spec"].(map[string]any)
	specProps, _ := spec["properties"].(map[string]any)
	parametersSubtree, ok := specProps["parameters"]
	if !ok {
		t.Fatalf("canonical schema missing properties.spec.properties.parameters")
	}

	// Canonical formatting: re-marshal both sides via the SAME path so
	// whitespace and key-ordering differences don't trigger false drift.
	canonical := canonicalJSON(t, parametersSubtree)

	var hardcoded any
	if err := json.Unmarshal([]byte(parameterContractSchemaJSON), &hardcoded); err != nil {
		t.Fatalf("unmarshal hardcoded sub-schema: %v", err)
	}
	hardcodedCanonical := canonicalJSON(t, hardcoded)

	if canonical != hardcodedCanonical {
		t.Errorf("parameter-contract sub-schema drift detected.\n"+
			"  Canonical source: %s (properties.spec.properties.parameters subtree)\n"+
			"  Hardcoded literal: pkg/resolver/step1a_parameter_schema.go::parameterContractSchemaJSON\n"+
			"  Update the literal in lockstep with the canonical schema.\n\n"+
			"  Canonical:\n%s\n\n  Hardcoded:\n%s\n",
			canonicalSource, canonical, hardcodedCanonical)
	}
}

// TestParameterFormatEnumIsTheSupportedSet holds the Aggregate schema's closed
// list of parameter format names equal to the names the format checkers support.
// The schema refuses an unknown name when the Aggregate loads and the checkers
// judge a value against a known one, so a name on one side only is either refused
// although it could be checked, or admitted with nothing to check it. Each
// difference is named, in both directions, and the order is held too, since the
// schema's list is what an editor offers an author.
func TestParameterFormatEnumIsTheSupportedSet(t *testing.T) {
	t.Parallel()

	var root map[string]any
	if err := json.Unmarshal([]byte(schemas.AggregateV1beta1), &root); err != nil {
		t.Fatalf("unmarshal canonical schema: %v", err)
	}
	node := any(root)
	for _, key := range []string{"properties", "spec", "properties", "parameters",
		"additionalProperties", "properties", "format", "enum"} {
		m, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("aggregate-v1beta1.schema.json: no %q on the path to the parameter format enum", key)
		}
		node = m[key]
	}
	raw, ok := node.([]any)
	if !ok {
		t.Fatalf("aggregate-v1beta1.schema.json: the parameter format carries no enum; got %T", node)
	}
	inSchema := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("parameter format enum member %v is not a string", v)
		}
		inSchema = append(inSchema, s)
	}
	supported := stringformat.Names()

	for _, name := range inSchema {
		if !stringformat.Supported(name) {
			t.Errorf("the Aggregate schema admits parameter format %q, which no checker supports", name)
		}
	}
	listed := make(map[string]bool, len(inSchema))
	for _, name := range inSchema {
		listed[name] = true
	}
	for _, name := range supported {
		if !listed[name] {
			t.Errorf("format %q is supported but the Aggregate schema refuses it on a parameter", name)
		}
	}
	if strings.Join(inSchema, ",") != strings.Join(supported, ",") {
		t.Errorf("parameter format enum order %v; want the supported order %v", inSchema, supported)
	}
}

// canonicalJSON produces a stable JSON representation of an arbitrary
// value for cross-source equivalence comparison. encoding/json's default
// map-key sort + 2-space indent is deterministic; identical inputs
// produce identical outputs.
func canonicalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// TestValidateParameterContractEnvelope_Determinism pins the
// deterministic-output contract for multi-violation envelope errors:
// the pkg/validator/jsonschema wrapper's recursive branch-walking
// extension produces byte-identical content across runs, regardless of
// hash-randomised map iteration in the underlying schema library.
//
// Test runs the envelope validator 10× in the same process on a
// malformed contract producing multiple violations and asserts
// byte-identical error.Error() output. Without the wrapper's
// deterministic walker, the equivalent assertion would fail because
// the library's randomised Properties iteration produces variable
// error-string content across runs.
func TestValidateParameterContractEnvelope_Determinism(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	src := `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: test
  version: 1.0.0
spec:
  parameters:
    bad1:
      type: invalid
    bad2:
      type: string
      minLength: not-a-number
    bad3:
      type: string
      maxLength: also-not-a-number
`
	path := filepath.Join(tmp, "aggregate.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var first string
	for i := 0; i < 10; i++ {
		_, err := LoadAggregateParameterContract(path)
		if err == nil {
			t.Fatalf("run %d: expected envelope-validation error, got nil", i)
		}
		got := err.Error()
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Errorf("run %d: envelope error differs from run 0\n  run 0:  %s\n  run %d: %s",
				i, first, i, got)
		}
	}
}
