// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// helper builds a contract OrderedMap from a series of (key, value) pairs.
func buildContract(t *testing.T, entries ...any) types.OrderedMap[any] {
	t.Helper()
	if len(entries)%2 != 0 {
		t.Fatal("buildContract requires key/value pairs")
	}
	m := types.NewOrderedMap[any]()
	for i := 0; i < len(entries); i += 2 {
		key, ok := entries[i].(string)
		if !ok {
			t.Fatalf("expected string key, got %T", entries[i])
		}
		m.Set(key, entries[i+1])
	}
	return *m
}

// TestStep1a_PassesOnValidTypedContract pins the happy path: typed contract
// + Release values matching every constraint produces no errors.
func TestStep1a_PassesOnValidTypedContract(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"port", map[string]any{
			"type":    "integer",
			"default": 8080,
			"min":     1024,
			"max":     65535,
		},
		"host", map[string]any{
			"type":    "string",
			"pattern": "^[a-z][a-z0-9.-]*$",
		},
	)
	params := map[string]any{
		"port": 9090,
		"host": "db.internal",
	}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 0 {
		t.Errorf("expected no errors; got %d: %v", len(errs), errs)
	}
}

// TestStep1a_FailsOnTypeMismatch pins type-kind dispatch rejection.
func TestStep1a_FailsOnTypeMismatch(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"port", map[string]any{"type": "integer", "default": 8080},
	)
	params := map[string]any{"port": "not-a-number"}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 1 {
		t.Fatalf("expected 1 error; got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrParameterContractViolation) {
		t.Errorf("expected ErrParameterContractViolation; got %v", errs[0])
	}
	if !strings.Contains(errs[0].Message, "expected type integer") {
		t.Errorf("error message missing type expectation: %q", errs[0].Message)
	}
}

// TestStep1a_FailsOnEnumMiss pins enum-membership rejection.
func TestStep1a_FailsOnEnumMiss(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"env", map[string]any{
			"type": "string",
			"enum": []any{"dev", "staging", "prod"},
		},
	)
	params := map[string]any{"env": "production"}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterContractViolation) {
		t.Fatalf("expected one ErrParameterContractViolation; got %v", errs)
	}
}

// TestStep1a_FailsOnPatternMiss pins regex-pattern rejection.
func TestStep1a_FailsOnPatternMiss(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"host", map[string]any{
			"type":    "string",
			"pattern": "^[a-z]+$",
		},
	)
	params := map[string]any{"host": "DB-01"}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 1 {
		t.Fatalf("expected 1 error; got %d", len(errs))
	}
	if !strings.Contains(errs[0].Message, "does not match pattern") {
		t.Errorf("error message missing pattern detail: %q", errs[0].Message)
	}
}

// TestStep1a_FailsOnMinMaxOutOfRange pins integer range rejection.
func TestStep1a_FailsOnMinMaxOutOfRange(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"port", map[string]any{"type": "integer", "min": 1024, "max": 65535},
	)
	belowMin := map[string]any{"port": 80}
	errs := ValidateParametersAgainstContract(contract, belowMin, "test.yaml")
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "below min") {
		t.Errorf("below-min: expected one below-min error; got %v", errs)
	}
	aboveMax := map[string]any{"port": 99999}
	errs = ValidateParametersAgainstContract(contract, aboveMax, "test.yaml")
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "above max") {
		t.Errorf("above-max: expected one above-max error; got %v", errs)
	}
}

// TestStep1a_FailsOnMissingRequired pins required-marker enforcement.
func TestStep1a_FailsOnMissingRequired(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"db_host", map[string]any{"type": "string", "required": true},
	)
	params := map[string]any{}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterMissing) {
		t.Fatalf("expected ErrParameterMissing; got %v", errs)
	}
}

// TestStep1a_DefaultsSatisfyRequired pins that an Aggregate-side default
// satisfies the required marker even when the Release omits the parameter.
func TestStep1a_DefaultsSatisfyRequired(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"db_host", map[string]any{
			"type":     "string",
			"required": true,
			"default":  "localhost",
		},
	)
	params := map[string]any{}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 0 {
		t.Errorf("expected no errors when default present; got %v", errs)
	}
}

// TestStep1a_SimpleFormPreserved pins Q2a simple-shorthand acceptance.
// A bare-scalar Aggregate parameter accepts a matching Release value.
func TestStep1a_SimpleFormPreserved(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"api_port", 8080,
		"db_host", "localhost",
	)
	params := map[string]any{
		"api_port": 9090,
		"db_host":  "remote-db",
	}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 0 {
		t.Errorf("expected no errors; got %v", errs)
	}
}

// TestStep1a_SimpleFormRejectsTypeMismatch pins that simple-shorthand
// inferred-type still enforces type compatibility — string-quoted "8080"
// against integer-shorthand 8080 is a type mismatch.
func TestStep1a_SimpleFormRejectsTypeMismatch(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"api_port", 8080, // integer simple shorthand
	)
	params := map[string]any{"api_port": "9090"} // string supplied
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterContractViolation) {
		t.Errorf("expected type-mismatch error; got %v", errs)
	}
}

// TestStep1a_MixedFormDispatch pins per-entry dispatch when the Aggregate
// mixes simple-form and typed-form parameters.
func TestStep1a_MixedFormDispatch(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"simple_port", 8080,
		"typed_host", map[string]any{
			"type":    "string",
			"pattern": "^[a-z]+$",
		},
	)
	params := map[string]any{
		"simple_port": 9090,
		"typed_host":  "localhost",
	}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 0 {
		t.Errorf("expected no errors; got %v", errs)
	}
}

// TestStep1a_ClosedContractRejectsUndeclared pins the closed-contract policy:
// a Release-supplied parameter with no Aggregate declaration violates.
func TestStep1a_ClosedContractRejectsUndeclared(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"port", map[string]any{"type": "integer"},
	)
	params := map[string]any{"port": 8080, "rogue": "value"}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterContractViolation) {
		t.Fatalf("expected one ErrParameterContractViolation for undeclared key; got %v", errs)
	}
	if !strings.Contains(errs[0].Message, "rogue") {
		t.Errorf("error message missing undeclared key: %q", errs[0].Message)
	}
}

// TestStep1a_EmptyAggregateRejectsReleaseParams pins the closed-contract
// edge case from clarification B: an Aggregate with no parameters block AND
// a Release supplying parameters produces violations.
func TestStep1a_EmptyAggregateRejectsReleaseParams(t *testing.T) {
	t.Parallel()
	contract := types.OrderedMap[any]{}
	params := map[string]any{"foo": "bar"}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) == 0 {
		t.Fatal("expected at least one error for undeclared Release parameter on empty Aggregate")
	}
}

// TestStep1a_EmptyBothSidesPasses pins that empty-on-both-sides is fine.
func TestStep1a_EmptyBothSidesPasses(t *testing.T) {
	t.Parallel()
	contract := types.OrderedMap[any]{}
	params := map[string]any{}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 0 {
		t.Errorf("expected no errors on both-empty; got %v", errs)
	}
}

// TestStep1a_ValidatePassesOnTruePredicate pins the validate-predicate
// success path: a Go-template predicate returning "true" passes.
func TestStep1a_ValidatePassesOnTruePredicate(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"replicas", map[string]any{
			"type":            "integer",
			"validate":        "{{ gt (.value | int) 0 }}",
			"validateMessage": "replicas must be positive",
		},
	)
	params := map[string]any{"replicas": 3}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 0 {
		t.Errorf("expected no errors; got %v", errs)
	}
}

// TestStep1a_ValidateFailsWithCustomMessage pins the validate-predicate
// failure path: rendering "false" surfaces the validateMessage to the operator.
func TestStep1a_ValidateFailsWithCustomMessage(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"replicas", map[string]any{
			"type":            "integer",
			"validate":        "{{ gt (.value | int) 0 }}",
			"validateMessage": "replicas must be positive",
		},
	)
	params := map[string]any{"replicas": -1}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 1 {
		t.Fatalf("expected 1 error; got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Message, "replicas must be positive") {
		t.Errorf("expected custom message in error; got %q", errs[0].Message)
	}
}

// TestStep1a_NestedPropertiesRecursion pins lazy/recursive descent into
// type: object parameters with Properties. Validates clarification A.
func TestStep1a_NestedPropertiesRecursion(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"db", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"host": map[string]any{
					"type":     "string",
					"required": true,
				},
				"port": map[string]any{
					"type": "integer",
					"min":  1024,
					"max":  65535,
				},
			},
		},
	)
	good := map[string]any{
		"db": map[string]any{
			"host": "db.internal",
			"port": 5432,
		},
	}
	errs := ValidateParametersAgainstContract(contract, good, "test.yaml")
	if len(errs) != 0 {
		t.Errorf("good case: expected no errors; got %v", errs)
	}

	badPort := map[string]any{
		"db": map[string]any{
			"host": "db.internal",
			"port": 80, // below min
		},
	}
	errs = ValidateParametersAgainstContract(contract, badPort, "test.yaml")
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "below min") {
		t.Errorf("bad-port: expected one below-min error; got %v", errs)
	}
	if errs[0].Field != ".spec.parameters.db.port" {
		t.Errorf("nested field path wrong: %q", errs[0].Field)
	}

	missingHost := map[string]any{
		"db": map[string]any{
			"port": 5432,
		},
	}
	errs = ValidateParametersAgainstContract(contract, missingHost, "test.yaml")
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterMissing) {
		t.Errorf("missing-host: expected ErrParameterMissing; got %v", errs)
	}
}

// TestStep1a_LoadAggregateParameterContract_ShallowParse pins the shallow-
// parse extraction: a raw Aggregate YAML (which may carry template
// expressions in other surfaces) yields its spec.parameters: block without
// running template interpolation.
func TestStep1a_LoadAggregateParameterContract_ShallowParse(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: shallow-parse
  version: "1.0.0"
spec:
  parameters:
    port:
      type: integer
      default: 8080
    host: localhost
  components:
    web:
      type: core/server:1.0.0
      values:
        port: "{{ .parameters.port }}"
        host: "{{ .parameters.host }}"
`
	path := tmp + "/aggregate.yaml"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := LoadAggregateParameterContract(path)
	if err != nil {
		t.Fatalf("LoadAggregateParameterContract: %v", err)
	}
	if contract.Len() != 2 {
		t.Fatalf("expected 2 contract entries; got %d", contract.Len())
	}
	keys := contract.Keys()
	if keys[0] != "port" || keys[1] != "host" {
		t.Errorf("contract keys = %v; want [port host]", keys)
	}
}

