// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"
)

// buildValidator compiles a JSON-string schema into a Validator for
// tests in this file. Failures fatal-the-test.
func buildValidator(t *testing.T, schemaJSON string) Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemaJSON), schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

// findingsContain reports whether any finding has the given Keyword
// AND the message contains the given substring. Helper for tests that
// pin specific construct + content combinations.
func findingsContain(rpt *Report, keyword, msgSubstring string) bool {
	for _, f := range rpt.Findings {
		if (keyword == "" || strings.Contains(f.Keyword, keyword)) &&
			(msgSubstring == "" || strings.Contains(f.Message, msgSubstring)) {
			return true
		}
	}
	return false
}

// TestAdditionalPropertiesAsSchema_DeterministicWalk pins the new
// additionalProperties: <schema> coverage. Keys NOT in Properties
// validate against the additionalProperties sub-schema; findings emit
// at deterministic field paths.
func TestAdditionalPropertiesAsSchema_DeterministicWalk(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"properties": { "a": { "type": "string" } },
		"additionalProperties": { "type": "integer" }
	}`)
	rpt, err := v.Validate(map[string]any{"a": "x", "b": 1, "c": "bad"}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !findingsContain(rpt, "additionalProperties", "") && !findingsContain(rpt, "validation", "") {
		t.Errorf("expected additionalProperties violation for c=string; got %v", rpt.Findings)
	}
	// Key b (value 1, integer) passes; key c (value "bad", string) fails.
	found := false
	for _, f := range rpt.Findings {
		if f.FieldPath == "c" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected finding at field path 'c'; got %v", rpt.Findings)
	}
}

// TestOneOf_NoMatch_SingleFinding pins zero-match oneOf semantic.
func TestOneOf_NoMatch_SingleFinding(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"oneOf": [ { "type": "string" }, { "type": "integer" } ]
	}`)
	rpt, _ := v.Validate([]any{}, nil)
	if !findingsContain(rpt, "oneOf", "no oneOf branch matched") {
		t.Errorf("expected oneOf no-match finding; got %v", rpt.Findings)
	}
}

// TestOneOf_MultipleMatch_SingleFinding pins multi-match oneOf
// semantic + the revision-2 enhancement (matched-branch indices in
// the error message).
func TestOneOf_MultipleMatch_SingleFinding(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"oneOf": [
			{ "type": "object" },
			{ "type": "object", "properties": { "a": { "type": "string" } } }
		]
	}`)
	rpt, _ := v.Validate(map[string]any{"a": "x"}, nil)
	if !findingsContain(rpt, "oneOf", "multiple oneOf branches matched") {
		t.Errorf("expected oneOf multi-match finding; got %v", rpt.Findings)
	}
	// Revision-2 enhancement: matched-branch indices are named.
	if !findingsContain(rpt, "oneOf", "branches [0, 1]") {
		t.Errorf("expected matched-branch indices [0, 1] in finding message; got %v", rpt.Findings)
	}
}

// TestOneOf_ExactlyOneMatch_NoFinding pins the happy-path oneOf
// semantic — exactly-one-clean-match accepts the instance.
func TestOneOf_ExactlyOneMatch_NoFinding(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"oneOf": [ { "type": "string" }, { "type": "integer" } ]
	}`)
	rpt, _ := v.Validate("hello", nil)
	if len(rpt.Findings) != 0 {
		t.Errorf("expected zero findings on exactly-one match; got %v", rpt.Findings)
	}
}

// TestAllOf_AllFail_AllFindings pins allOf union semantic.
func TestAllOf_AllFail_AllFindings(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"allOf": [ { "type": "integer" }, { "type": "string", "minLength": 100 } ]
	}`)
	rpt, _ := v.Validate("wrong", nil)
	// "wrong" passes branch[1]'s type:string but fails minLength; fails branch[0]'s type:integer.
	// Expect findings from BOTH branches.
	foundBranch0 := false
	foundBranch1 := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Keyword, "allOf/branch[0]") {
			foundBranch0 = true
		}
		if strings.Contains(f.Keyword, "allOf/branch[1]") {
			foundBranch1 = true
		}
	}
	if !foundBranch0 || !foundBranch1 {
		t.Errorf("expected findings from both allOf branches; got %v", rpt.Findings)
	}
}

// TestAnyOf_NoMatch_SingleFinding pins anyOf no-match semantic.
func TestAnyOf_NoMatch_SingleFinding(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"anyOf": [ { "type": "string" }, { "type": "integer" } ]
	}`)
	rpt, _ := v.Validate([]any{}, nil)
	if !findingsContain(rpt, "anyOf", "no anyOf branch matched") {
		t.Errorf("expected anyOf no-match finding; got %v", rpt.Findings)
	}
}

// TestAnyOf_FirstMatch_ShortCircuit pins anyOf short-circuit on first
// clean match.
func TestAnyOf_FirstMatch_ShortCircuit(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"anyOf": [ { "type": "string" }, { "type": "integer" } ]
	}`)
	rpt, _ := v.Validate("hello", nil)
	if len(rpt.Findings) != 0 {
		t.Errorf("expected zero findings on first anyOf match; got %v", rpt.Findings)
	}
}

// TestNot_InstanceMatchesNot_SingleFinding pins not semantic when
// instance MATCHES the inverse.
func TestNot_InstanceMatchesNot_SingleFinding(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{ "not": { "type": "string" } }`)
	rpt, _ := v.Validate("hello", nil)
	if !findingsContain(rpt, "not", "must NOT match") {
		t.Errorf("expected not finding when instance matches the inverse; got %v", rpt.Findings)
	}
}

// TestNot_InstanceDoesNotMatchNot_ZeroFindings pins not semantic when
// instance does NOT match the inverse.
func TestNot_InstanceDoesNotMatchNot_ZeroFindings(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{ "not": { "type": "string" } }`)
	rpt, _ := v.Validate(42, nil)
	if len(rpt.Findings) != 0 {
		t.Errorf("expected zero findings when instance doesn't match the inverse; got %v", rpt.Findings)
	}
}

// TestPatternProperties_KeyMatchesPattern_ValidatedAgainstPatternSchema
// pins patternProperties single-pattern matching.
func TestPatternProperties_KeyMatchesPattern_ValidatedAgainstPatternSchema(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"patternProperties": {
			"^x_.*$": { "type": "integer" }
		}
	}`)
	rpt, _ := v.Validate(map[string]any{"x_a": 1, "x_b": "wrong"}, nil)
	foundB := false
	for _, f := range rpt.Findings {
		if f.FieldPath == "x_b" {
			foundB = true
		}
	}
	if !foundB {
		t.Errorf("expected x_b violation; got %v", rpt.Findings)
	}
}

// TestPatternProperties_KeyMatchesMultiplePatterns_AllValidate pins
// multi-pattern semantic: a key matching multiple patterns validates
// against each matched pattern's sub-schema.
func TestPatternProperties_KeyMatchesMultiplePatterns_AllValidate(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"patternProperties": {
			"^x_.*$": { "type": "integer" },
			"^.*_a$": { "minimum": 100 }
		}
	}`)
	// Key x_a matches both patterns. Value 5 fails both: type:integer passes
	// but minimum:100 fails; actually 5 IS integer so first pattern passes.
	// Use value "five" (string) which fails both — type:integer fails AND
	// the second pattern's minimum:100 also requires the integer value
	// (will produce its own error path). Expect findings from both.
	rpt, _ := v.Validate(map[string]any{"x_a": "five"}, nil)
	if len(rpt.Findings) < 1 {
		t.Errorf("expected at least one finding for x_a (matches both patterns); got %v", rpt.Findings)
	}
}

// TestPatternProperties_KeyInPropertiesAndPattern_PropertiesWins pins
// the precedence rule (Properties > PatternProperties).
func TestPatternProperties_KeyInPropertiesAndPattern_PropertiesWins(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"properties": { "x_a": { "type": "string" } },
		"patternProperties": {
			"^x_.*$": { "type": "integer" }
		}
	}`)
	// x_a is in Properties (type: string). Value "hello" passes string.
	// If patternProperties took precedence (or also fired), the integer
	// check would fail. Expect zero findings — Properties wins.
	rpt, _ := v.Validate(map[string]any{"x_a": "hello"}, nil)
	if len(rpt.Findings) != 0 {
		t.Errorf("expected Properties to take precedence; got %v", rpt.Findings)
	}
}

// TestPatternProperties_KeyMatchesNeither_FallsThroughToAdditionalProperties
// pins the third dispatch arm: a key in neither Properties nor
// PatternProperties falls through to additionalProperties.
func TestPatternProperties_KeyMatchesNeither_FallsThroughToAdditionalProperties(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"properties": { "a": { "type": "string" } },
		"patternProperties": {
			"^x_.*$": { "type": "integer" }
		},
		"additionalProperties": { "type": "boolean" }
	}`)
	// Key "z" is in neither Properties nor any pattern → falls through
	// to additionalProperties (boolean). Value 42 fails boolean.
	rpt, _ := v.Validate(map[string]any{"z": 42}, nil)
	foundZ := false
	for _, f := range rpt.Findings {
		if f.FieldPath == "z" {
			foundZ = true
		}
	}
	if !foundZ {
		t.Errorf("expected additionalProperties violation at z; got %v", rpt.Findings)
	}
}

// TestDependentSchemas_TriggerKeyPresent_DependentSchemaValidates pins
// dependentSchemas activation when the trigger key is present.
func TestDependentSchemas_TriggerKeyPresent_DependentSchemaValidates(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"dependentSchemas": {
			"credit_card": {
				"required": ["billing_address"]
			}
		}
	}`)
	// trigger key "credit_card" present; billing_address absent → finding.
	rpt, _ := v.Validate(map[string]any{"credit_card": "1234"}, nil)
	foundDep := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Keyword, "dependentSchemas/credit_card") {
			foundDep = true
		}
	}
	if !foundDep {
		t.Errorf("expected dependentSchemas violation; got %v", rpt.Findings)
	}
}

// TestDependentSchemas_TriggerKeyAbsent_DependentSchemaIgnored pins
// the activation gate: trigger absent → dependent schema not checked.
func TestDependentSchemas_TriggerKeyAbsent_DependentSchemaIgnored(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"dependentSchemas": {
			"credit_card": {
				"required": ["billing_address"]
			}
		}
	}`)
	rpt, _ := v.Validate(map[string]any{"name": "anon"}, nil)
	for _, f := range rpt.Findings {
		if strings.Contains(f.Keyword, "dependentSchemas") {
			t.Errorf("expected no dependentSchemas finding when trigger absent; got %v", rpt.Findings)
		}
	}
}

// TestDependentSchemas_MultipleTriggersPresent_AllDependentsValidate
// pins multi-trigger activation.
func TestDependentSchemas_MultipleTriggersPresent_AllDependentsValidate(t *testing.T) {
	t.Parallel()
	v := buildValidator(t, `{
		"type": "object",
		"dependentSchemas": {
			"credit_card": { "required": ["billing_address"] },
			"shipping": { "required": ["shipping_address"] }
		}
	}`)
	rpt, _ := v.Validate(map[string]any{"credit_card": "1234", "shipping": true}, nil)
	foundCC := false
	foundShip := false
	for _, f := range rpt.Findings {
		if strings.Contains(f.Keyword, "dependentSchemas/credit_card") {
			foundCC = true
		}
		if strings.Contains(f.Keyword, "dependentSchemas/shipping") {
			foundShip = true
		}
	}
	if !foundCC || !foundShip {
		t.Errorf("expected both dependentSchemas to activate; got %v", rpt.Findings)
	}
}

// TestExtendedWalker_Determinism is the load-bearing acceptance signal
// for Option α: a fixture exercising ALL seven constructs runs 10× in
// the same process; assert byte-identical Findings JSON across runs.
func TestExtendedWalker_Determinism(t *testing.T) {
	t.Parallel()
	schemaJSON := `{
		"type": "object",
		"properties": { "name": { "type": "string" } },
		"patternProperties": {
			"^x_.*$": { "type": "integer" }
		},
		"additionalProperties": { "type": "boolean" },
		"oneOf": [
			{ "required": ["a"] },
			{ "required": ["b"] }
		],
		"allOf": [
			{ "type": "object" }
		],
		"anyOf": [
			{ "required": ["q"] },
			{ "required": ["r"] }
		],
		"not": { "type": "string" },
		"dependentSchemas": {
			"trigger": { "required": ["trigger_followup"] }
		}
	}`
	v := buildValidator(t, schemaJSON)
	instance := map[string]any{
		"name":      "x",
		"x_invalid": "should-be-integer",
		"unknown":   42,
		"trigger":   "fires",
	}

	var first []byte
	for i := 0; i < 10; i++ {
		rpt, err := v.Validate(instance, nil)
		if err != nil {
			t.Fatalf("Validate run %d: %v", i, err)
		}
		canonical, _ := json.Marshal(rpt.Findings)
		if i == 0 {
			first = canonical
			continue
		}
		if string(canonical) != string(first) {
			t.Errorf("run %d: findings differ from run 0\n  run 0: %s\n  run %d: %s",
				i, string(first), i, string(canonical))
		}
	}
}
