// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"

	jsonvalidator "github.com/cognitio/aggrega-cli/pkg/validator/jsonschema"
)

// aggregateEnvelopeValidator loads the shipped Aggregate JSON Schema from
// the sibling aggrega-schemas repo and constructs a Validator over it.
func aggregateEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schemaPath := filepath.Join("..", "..", "..", "aggrega-schemas", "json-schemas", "aggregate-v1alpha1.schema.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("Aggregate envelope schema not reachable at %s: %v", schemaPath, err)
	}
	schema := &libschema.Schema{}
	if err := json.Unmarshal(schemaBytes, schema); err != nil {
		t.Fatalf("unmarshal envelope schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator: %v", err)
	}
	return v
}

// TestAggregateEnvelope_AcceptsSimpleShorthandParameters pins the Q2a
// preservation path: bare-scalar parameter entries (the simple shorthand)
// remain accepted by the envelope.
func TestAggregateEnvelope_AcceptsSimpleShorthandParameters(t *testing.T) {
	t.Parallel()
	v := aggregateEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: simple-params
  version: "1.0.0"
spec:
  parameters:
    api_port: 8080
    db_host: localhost
    enabled: true
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected envelope acceptance of simple-shorthand parameters; got %d findings: %v",
			len(rpt.Findings), rpt.Findings)
	}
}

// TestAggregateEnvelope_AcceptsTypedContractParameters pins the typed-form
// path: a parameter with the compact-form vocabulary subset (type, default,
// min, max, pattern, enum, description) loads cleanly.
func TestAggregateEnvelope_AcceptsTypedContractParameters(t *testing.T) {
	t.Parallel()
	v := aggregateEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: typed-params
  version: "1.0.0"
spec:
  parameters:
    api_port:
      type: integer
      description: API listener port
      default: 8080
      min: 1024
      max: 65535
    env:
      type: string
      enum:
        - dev
        - staging
        - prod
      default: dev
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected envelope acceptance of typed-contract parameters; got %d findings: %v",
			len(rpt.Findings), rpt.Findings)
	}
}

// TestAggregateEnvelope_RejectsUnknownVocabulary pins additionalProperties:
// false on the typed-form object. An unknown keyword (here: weirdthing:)
// fails envelope validation.
func TestAggregateEnvelope_RejectsUnknownVocabulary(t *testing.T) {
	t.Parallel()
	v := aggregateEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: unknown-vocab
  version: "1.0.0"
spec:
  parameters:
    api_port:
      type: integer
      weirdthing: this-is-not-a-real-keyword
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatal("expected envelope rejection on unknown typed-form keyword; got no findings")
	}
}

// TestAggregateEnvelope_AcceptsMixedForms pins per-entry dispatch at the
// envelope level: a single parameters block may carry simple-shorthand
// AND typed-contract entries side by side.
func TestAggregateEnvelope_AcceptsMixedForms(t *testing.T) {
	t.Parallel()
	v := aggregateEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mixed-params
  version: "1.0.0"
spec:
  parameters:
    simple_port: 8080
    typed_host:
      type: string
      pattern: "^[a-z]+$"
      required: true
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected envelope acceptance of mixed-form parameters; got %d findings: %v",
			len(rpt.Findings), rpt.Findings)
	}
}

// TestAggregateEnvelope_AcceptsNestedProperties pins typed: object with
// the nested properties: sub-field — the recursive per-key contract shape
// for nested parameter trees.
func TestAggregateEnvelope_AcceptsNestedProperties(t *testing.T) {
	t.Parallel()
	v := aggregateEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: nested-params
  version: "1.0.0"
spec:
  parameters:
    db:
      type: object
      properties:
        host:
          type: string
          required: true
        port:
          type: integer
          min: 1024
          max: 65535
          default: 5432
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected envelope acceptance of nested typed properties; got %d findings: %v",
			len(rpt.Findings), rpt.Findings)
	}
}
