// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
	if !errors.Is(errs[0], ErrParameterContractViolation()) {
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
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterContractViolation()) {
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
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterMissing()) {
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

// TestStep1a_ShorthandRejected pins the post-shorthand-drop behavior:
// bare-scalar Aggregate parameter entries produce
// ErrParameterContractViolation with a directed message at parse time.
func TestStep1a_ShorthandRejected(t *testing.T) {
	t.Parallel()
	scalars := map[string]any{
		"int_shorthand":    8080,
		"string_shorthand": "localhost",
		"bool_shorthand":   true,
	}
	for name, scalar := range scalars {
		t.Run(name, func(t *testing.T) {
			contract := buildContract(t, "field", scalar)
			params := map[string]any{"field": scalar}
			errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
			if len(errs) == 0 {
				t.Fatalf("expected shorthand-rejection error; got none")
			}
			if !errors.Is(errs[0], ErrParameterContractViolation()) {
				t.Errorf("expected ErrParameterContractViolation; got %v", errs[0])
			}
			if !strings.Contains(errs[0].Message, "typed-contract form") {
				t.Errorf("expected 'typed-contract form' in error message; got: %s", errs[0].Message)
			}
		})
	}
}

// TestStep1a_MixedFormRejectedWhenShorthandPresent pins that mixing
// shorthand and typed-form entries surfaces the shorthand-rejection
// even when other entries decode cleanly. Per-entry dispatch isolates
// the failure to the shorthand slot.
func TestStep1a_MixedFormRejectedWhenShorthandPresent(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"shorthand_port", 8080, // shorthand — rejected
		"typed_host", map[string]any{ // typed — accepted
			"type":    "string",
			"pattern": "^[a-z]+$",
		},
	)
	params := map[string]any{
		"shorthand_port": 9090,
		"typed_host":     "localhost",
	}
	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) == 0 {
		t.Fatalf("expected shorthand-rejection error for shorthand_port; got none")
	}
	// The shorthand slot must be named in the failure path.
	foundShorthand := false
	for _, e := range errs {
		if strings.Contains(e.Field, "shorthand_port") {
			foundShorthand = true
		}
	}
	if !foundShorthand {
		t.Errorf("expected one error to name shorthand_port; got %v", errs)
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
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterContractViolation()) {
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
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterMissing()) {
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
    host:
      type: string
      default: localhost
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

// TestAggregateParameterViolations_MultiViolation pins the multi-violation
// reporting contract per the aggregateParameterViolations helper. When two
// parameters violate their contracts simultaneously, the returned error is
// a *MultiResolverError extractable via errors.As, with Field
// ".spec.parameters", a multi-line Summary naming both violation paths,
// and a Violations slice preserving every per-violation Field locator for
// downstream consumers (the daemon's Tier-4 position-recovery walks).
func TestAggregateParameterViolations_MultiViolation(t *testing.T) {
	t.Parallel()

	contract := buildContract(t,
		"port", map[string]any{"type": "integer", "min": 1024},
		"host", map[string]any{"type": "string"},
	)
	params := map[string]any{
		"port": "not-an-integer",
		"host": 42,
	}

	verrs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(verrs) < 2 {
		t.Fatalf("expected >=2 violations; got %d: %v", len(verrs), verrs)
	}

	err := aggregateParameterViolations(verrs)
	if err == nil {
		t.Fatalf("expected aggregated error, got nil")
	}

	var multi *MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("expected *MultiResolverError extractable via errors.As; got %T: %v", err, err)
	}
	if multi.Field != ".spec.parameters" {
		t.Errorf("MultiResolverError Field = %q; want %q", multi.Field, ".spec.parameters")
	}
	if !errors.Is(err, ErrParameterContractViolation()) {
		t.Errorf("aggregated error not ErrParameterContractViolation: %v", err)
	}

	// Per-violation slice preserves both Field locators.
	if len(multi.Violations) < 2 {
		t.Errorf("expected >=2 preserved violations; got %d", len(multi.Violations))
	}
	gotPaths := make(map[string]bool, len(multi.Violations))
	for _, v := range multi.Violations {
		gotPaths[v.Field] = true
	}
	if !gotPaths[".spec.parameters.port"] {
		t.Errorf("Violations missing port locator; got %v", gotPaths)
	}
	if !gotPaths[".spec.parameters.host"] {
		t.Errorf("Violations missing host locator; got %v", gotPaths)
	}

	// Multi-line Summary names both paths (human-readable shape preserved).
	if !strings.Contains(multi.Summary, ".spec.parameters.port") {
		t.Errorf("aggregated Summary missing port violation: %s", multi.Summary)
	}
	if !strings.Contains(multi.Summary, ".spec.parameters.host") {
		t.Errorf("aggregated Summary missing host violation: %s", multi.Summary)
	}

	// Single-violation case: aggregator returns the original *ResolverError
	// unchanged (no MultiResolverError wrapping).
	singleVerr := []*ResolverError{verrs[0]}
	single := aggregateParameterViolations(singleVerr)
	var singleAsResolver *ResolverError
	if !errors.As(single, &singleAsResolver) || singleAsResolver != verrs[0] {
		t.Errorf("single-violation case should pass through unchanged; got %T %v", single, single)
	}
}

// TestValidateParametersAgainstContract_ArrayItems_TypeMismatch pins
// element-type enforcement at the items: layer. A contract declaring
// array-of-string Release-supplied as integers produces one violation
// per non-conforming element, with dot-notation paths.
func TestValidateParametersAgainstContract_ArrayItems_TypeMismatch(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"hosts", map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	)
	params := map[string]any{"hosts": []any{1, 2, 3}}

	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 3 {
		t.Fatalf("expected 3 element violations, got %d: %v", len(errs), errs)
	}
	for i, e := range errs {
		want := "hosts." + string(rune('0'+i))
		if !strings.Contains(e.Field, want) {
			t.Errorf("error[%d].Field = %q; want to contain %q", i, e.Field, want)
		}
	}
}

// TestValidateParametersAgainstContract_ArrayItems_ConstraintViolation pins
// per-element constraint enforcement: items contract declares minLength
// and only elements failing minLength produce errors.
func TestValidateParametersAgainstContract_ArrayItems_ConstraintViolation(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"tags", map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string", "minLength": 3},
		},
	)
	params := map[string]any{"tags": []any{"a", "ab", "abc"}}

	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 2 {
		t.Fatalf("expected 2 short-element violations, got %d: %v", len(errs), errs)
	}
	// Element 0 ("a") and element 1 ("ab") should fail; element 2 ("abc") passes.
	if !strings.Contains(errs[0].Field, "tags.0") {
		t.Errorf("expected tags.0 violation; got %q", errs[0].Field)
	}
	if !strings.Contains(errs[1].Field, "tags.1") {
		t.Errorf("expected tags.1 violation; got %q", errs[1].Field)
	}
}

// TestValidateParametersAgainstContract_ValuesType_TypeMismatch pins the
// typed-value-map validation: a contract declaring ValuesType (no
// Properties) is exercised end-to-end. The early-return gate must NOT
// short-circuit when Properties is empty but ValuesType is set.
func TestValidateParametersAgainstContract_ValuesType_TypeMismatch(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"limits", map[string]any{
			"type":       "object",
			"valuesType": map[string]any{"type": "integer"},
		},
	)
	params := map[string]any{"limits": map[string]any{"a": 1, "b": "two"}}

	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 1 {
		t.Fatalf("expected 1 ValuesType violation, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Field, "limits.b") {
		t.Errorf("expected limits.b violation; got %q", errs[0].Field)
	}
}

// TestValidateParametersAgainstContract_ValuesType_PlusProperties_Precedence
// pins the Properties-takes-precedence semantic when both Properties and
// ValuesType are declared.
func TestValidateParametersAgainstContract_ValuesType_PlusProperties_Precedence(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"config", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "integer"},
			},
			"valuesType": map[string]any{"type": "string"},
		},
	)

	// Case 1: a matches Properties, b matches ValuesType → no errors.
	clean := map[string]any{"config": map[string]any{"a": 1, "b": "x"}}
	errs := ValidateParametersAgainstContract(contract, clean, "test.yaml")
	if len(errs) != 0 {
		t.Errorf("clean case: expected no errors, got %d: %v", len(errs), errs)
	}

	// Case 2: a fails Properties (Properties takes precedence; "wrong" not integer).
	badA := map[string]any{"config": map[string]any{"a": "wrong", "b": "x"}}
	errs = ValidateParametersAgainstContract(contract, badA, "test.yaml")
	if len(errs) != 1 {
		t.Fatalf("badA: expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Field, "config.a") {
		t.Errorf("badA: expected config.a violation, got %q", errs[0].Field)
	}

	// Case 3: b fails ValuesType (42 not a string).
	badB := map[string]any{"config": map[string]any{"a": 1, "b": 42}}
	errs = ValidateParametersAgainstContract(contract, badB, "test.yaml")
	if len(errs) != 1 {
		t.Fatalf("badB: expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Field, "config.b") {
		t.Errorf("badB: expected config.b violation, got %q", errs[0].Field)
	}
}

// TestValidateParametersAgainstContract_ValuesType_OpenObject_NoUnknownKeyRejection
// pins the closed-object-semantic adjustment: when ValuesType is declared,
// keys absent from Properties are NOT rejected as unknown — they're the
// typed-value-map's open extension surface.
func TestValidateParametersAgainstContract_ValuesType_OpenObject_NoUnknownKeyRejection(t *testing.T) {
	t.Parallel()
	contract := buildContract(t,
		"tags", map[string]any{
			"type":       "object",
			"valuesType": map[string]any{"type": "integer"},
		},
	)
	params := map[string]any{"tags": map[string]any{"x": 1, "y": 2, "z": 3}}

	errs := ValidateParametersAgainstContract(contract, params, "test.yaml")
	if len(errs) != 0 {
		t.Errorf("expected no unknown-key rejection; got %d errors: %v", len(errs), errs)
	}
}
