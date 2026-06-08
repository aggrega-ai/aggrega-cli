// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"reflect"
	"testing"
)

// TestParam_ValidateReadsParameter: a scalar-rooted x-aggrega-validate predicate
// reads a parameter via param, gating the field value by the resolved parameter.
func TestParam_ValidateReadsParameter(t *testing.T) {
	t.Parallel()
	src := `
tier:
  type: string
  validate: '{{ eq . (param "allowedTier") }}'
  validateMessage: "tier must equal the allowedTier parameter"
`
	v, err := New(mustCompileAndConvert(t, src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	instance := map[string]any{"tier": "gold"}

	// allowedTier=gold → field matches → no finding.
	rpt, err := v.Validate(instance, &Context{Parameters: map[string]any{"allowedTier": "gold"}})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("tier=gold under allowedTier=gold should pass; got %v", rpt.Findings)
	}

	// allowedTier=silver → field mismatches → one validate finding.
	rpt2, err := v.Validate(instance, &Context{Parameters: map[string]any{"allowedTier": "silver"}})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt2.Findings) != 1 || rpt2.Findings[0].Keyword != "x-aggrega-validate" {
		t.Errorf("tier=gold under allowedTier=silver should fail with one validate finding; got %v", rpt2.Findings)
	}
}

// TestParam_WhenReadsParameter: an x-aggrega-when predicate reads a parameter,
// so the gated then: constraint applies under one parameter set and not another.
func TestParam_WhenReadsParameter(t *testing.T) {
	t.Parallel()
	src := `
secret:
  when: '{{ eq (param "env") "prod" }}'
  then:
    value: { type: string, required: true }
`
	v, err := New(mustCompileAndConvert(t, src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// secret present but missing the then-required value.
	instance := map[string]any{"secret": map[string]any{}}

	// env=prod → when truthy → then requires secret.value → finding.
	rpt, err := v.Validate(instance, &Context{Parameters: map[string]any{"env": "prod"}})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Errorf("env=prod should require secret.value (then: applies); got no findings")
	}

	// env=dev → when falsy → then dropped → no finding.
	rpt2, err := v.Validate(instance, &Context{Parameters: map[string]any{"env": "dev"}})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt2.Findings) != 0 {
		t.Errorf("env=dev should not apply the then: constraint; got %v", rpt2.Findings)
	}
}

// TestParam_ByteIdenticalForNonParameterPredicate is the regression guard: a
// predicate that does not call param/parameters produces a byte-identical Report
// across different parameter sets (and nil) — plumbing parameters did not perturb
// the existing field-value + surround scope.
func TestParam_ByteIdenticalForNonParameterPredicate(t *testing.T) {
	t.Parallel()
	src := `
db_size:
  type: string
  pattern: "^[1-9][0-9]*(Mi|Gi)$"
  validate: '{{ regexMatch "Gi$" . }}'
  validateMessage: "db_size must be expressed in Gi"
`
	v, err := New(mustCompileAndConvert(t, src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	instance := map[string]any{"db_size": "100Mi"} // violates validate

	base, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("Validate(nil ctx): %v", err)
	}
	for _, params := range []map[string]any{
		{},
		{"unrelated": "1"},
		{"unrelated": "2", "another": 99},
	} {
		got, err := v.Validate(instance, &Context{Parameters: params})
		if err != nil {
			t.Fatalf("Validate(params=%v): %v", params, err)
		}
		if !reflect.DeepEqual(got.Findings, base.Findings) {
			t.Errorf("non-parameter predicate must render identically across parameter sets;\n base=%v\n got =%v (params=%v)", base.Findings, got.Findings, params)
		}
	}
}

// TestParam_NilParametersSafe: a predicate that calls param when no parameters
// were supplied evaluates without panicking (param → nil).
func TestParam_NilParametersSafe(t *testing.T) {
	t.Parallel()
	src := `
tier:
  type: string
  validate: '{{ eq . (param "allowedTier") }}'
  validateMessage: "tier must equal the allowedTier parameter"
`
	v, err := New(mustCompileAndConvert(t, src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	instance := map[string]any{"tier": "gold"}
	// nil Context → param returns nil → eq "gold" nil is false → a finding, no panic.
	rpt, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("Validate(nil ctx) must not error; got %v", err)
	}
	if len(rpt.Findings) != 1 {
		t.Errorf("param with no parameters should yield nil and fail the eq; got %v", rpt.Findings)
	}
}
