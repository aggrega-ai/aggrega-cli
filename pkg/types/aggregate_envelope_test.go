// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	libschema "github.com/google/jsonschema-go/jsonschema"

	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// aggregateEnvelopeValidator constructs a Validator over the shipped
// Aggregate JSON Schema. The schema is compiled into this test binary from a
// referenced variable, so a schema edit invalidates this package's cached
// result rather than serving a stale one.
func aggregateEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemas.AggregateV1alpha1), schema); err != nil {
		t.Fatalf("unmarshal envelope schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator: %v", err)
	}
	return v
}

// TestAggregateEnvelope_RejectsShorthandParameters pins the post-shorthand-drop
// behavior at the envelope: bare-scalar parameter entries are rejected by
// the schema's `additionalProperties` constraint (which requires the
// typed-contract object shape).
func TestAggregateEnvelope_RejectsShorthandParameters(t *testing.T) {
	t.Parallel()
	v := aggregateEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: shorthand-params
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
	if len(rpt.Findings) == 0 {
		t.Fatal("expected envelope rejection of shorthand parameters; got no findings")
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

// TestAggregateEnvelope_RejectsMixedForms pins post-shorthand-drop behavior:
// a parameters block mixing shorthand with typed-form entries fails
// envelope validation on the shorthand slot. Per-entry dispatch isolates
// the failure to the shorthand entry; the typed-form entry passes.
func TestAggregateEnvelope_RejectsMixedForms(t *testing.T) {
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
	if len(rpt.Findings) == 0 {
		t.Fatal("expected envelope rejection on shorthand slot in mixed-form parameters; got no findings")
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
