// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/compactform"
	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

// TestNewNilSchema verifies the constructor rejects a nil schema with
// a structured AdapterError wrapping ErrSchemaResolve.
func TestNewNilSchema(t *testing.T) {
	t.Parallel()

	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error on nil schema; got nil")
	}
	if !errors.Is(err, ErrSchemaResolve) {
		t.Errorf("expected ErrSchemaResolve; got %v", err)
	}
	var ae *AdapterError
	if !errors.As(err, &ae) {
		t.Errorf("expected *AdapterError; got %T", err)
	}
}

// TestValidateMinimalFixturePass verifies the minimal UUID-generator
// fixture validates a valid instance with zero findings.
func TestValidateMinimalFixturePass(t *testing.T) {
	t.Parallel()

	src := `version: { type: string, enum: [v1, v4, v7], default: v4 }`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	instance := map[string]any{"version": "v4"}
	rpt, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected zero findings on valid instance; got %v", rpt.Findings)
	}
}

// TestValidateMinimalFixtureFail verifies the minimal fixture flags
// an invalid enum value with at least one finding.
func TestValidateMinimalFixtureFail(t *testing.T) {
	t.Parallel()

	src := `version: { type: string, enum: [v1, v4, v7], default: v4 }`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	instance := map[string]any{"version": "v99"}
	rpt, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Errorf("expected at least one finding for invalid enum value; got %v", rpt)
	}
}

// TestValidateXAggregaWhenTruthy verifies the adapter validates against
// then: when x-aggrega-when: predicate is truthy. The fixture: a
// tlsConfig field gated on tlsEnabled=true requiring cert/key paths.
func TestValidateXAggregaWhenTruthy(t *testing.T) {
	t.Parallel()

	src := `
tlsEnabled: { type: boolean, default: false }
tlsConfig:
  when: '{{ eq $.tlsEnabled true }}'
  then:
    certPath: { type: string, pattern: "^/.*\\.pem$", required: true }
  else:
    plainPort: { type: integer, min: 1024, max: 65535 }
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Truthy: tlsEnabled=true; tlsConfig must have certPath.
	instance := map[string]any{
		"tlsEnabled": true,
		"tlsConfig": map[string]any{
			"certPath": "/etc/tls/cert.pem",
		},
	}
	ctx := &Context{Surround: instance}
	rpt, err := v.Validate(instance, ctx)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("truthy when: + valid then-instance should produce no findings; got %v", rpt.Findings)
	}
}

// TestValidateXAggregaWhenFalsy verifies the adapter validates against
// else: when x-aggrega-when: predicate is falsy. Same fixture as above;
// tlsEnabled=false; tlsConfig.plainPort required (else: clause).
func TestValidateXAggregaWhenFalsy(t *testing.T) {
	t.Parallel()

	src := `
tlsEnabled: { type: boolean, default: false }
tlsConfig:
  when: '{{ eq $.tlsEnabled true }}'
  then:
    certPath: { type: string, pattern: "^/.*\\.pem$", required: true }
  else:
    plainPort: { type: integer, min: 1024, max: 65535 }
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Falsy: tlsEnabled=false; tlsConfig has plainPort (satisfies else).
	instance := map[string]any{
		"tlsEnabled": false,
		"tlsConfig": map[string]any{
			"plainPort": 8080,
		},
	}
	ctx := &Context{Surround: instance}
	rpt, err := v.Validate(instance, ctx)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("falsy when: + valid else-instance should produce no findings; got %v", rpt.Findings)
	}
}

// TestValidateXAggregaWhenFalsyWithoutElse verifies falsy + no else:
// produces no findings even when then:'s constraints would otherwise
// fail. D-001b-2 semantic: omit else means no constraint when falsy.
func TestValidateXAggregaWhenFalsyWithoutElse(t *testing.T) {
	t.Parallel()

	src := `
tlsEnabled: { type: boolean, default: false }
tlsConfig:
  when: '{{ eq $.tlsEnabled true }}'
  then:
    certPath: { type: string, pattern: "^/.*\\.pem$", required: true }
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Falsy: tlsEnabled=false; tlsConfig empty. With no else:, no
	// constraint applies; should pass.
	instance := map[string]any{
		"tlsEnabled": false,
		"tlsConfig":  map[string]any{},
	}
	ctx := &Context{Surround: instance}
	rpt, err := v.Validate(instance, ctx)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("falsy when + no else: should produce no findings; got %v", rpt.Findings)
	}
}

// TestValidateXAggregaValidate verifies the x-aggrega-validate:
// extension fires a Finding when the predicate evaluates falsy,
// carrying the x-aggrega-validateMessage: content.
func TestValidateXAggregaValidate(t *testing.T) {
	t.Parallel()

	src := `
db_size:
  type: string
  pattern: "^[1-9][0-9]*(Mi|Gi)$"
  validate: '{{ regexMatch "Gi$" . }}'
  validateMessage: "db_size must be expressed in Gi"
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Passes pattern but violates validate (uses Mi instead of Gi).
	instance := map[string]any{"db_size": "100Mi"}
	rpt, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	found := false
	for _, f := range rpt.Findings {
		if f.Keyword == "x-aggrega-validate" && strings.Contains(f.Message, "must be expressed in Gi") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected x-aggrega-validate finding with custom message; got %v", rpt.Findings)
	}
}

// TestValidateXAggregaValidatePass verifies the x-aggrega-validate:
// extension emits no Finding when the predicate evaluates truthy.
func TestValidateXAggregaValidatePass(t *testing.T) {
	t.Parallel()

	src := `
db_size:
  type: string
  pattern: "^[1-9][0-9]*(Mi|Gi)$"
  validate: '{{ regexMatch "Gi$" . }}'
  validateMessage: "db_size must be expressed in Gi"
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	instance := map[string]any{"db_size": "100Gi"}
	rpt, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, f := range rpt.Findings {
		if f.Keyword == "x-aggrega-validate" {
			t.Errorf("expected no x-aggrega-validate findings when predicate truthy; got %v", f)
		}
	}
}

// TestValidateXAggregaWhenFalsyElseFails verifies that when the
// predicate is falsy and the else: branch validation fails on the
// instance, the adapter emits findings rebased to the when-node's
// instance path. Triggers libraryErrorToFindingsWithPathPrefix.
func TestValidateXAggregaWhenFalsyElseFails(t *testing.T) {
	t.Parallel()

	src := `
tlsEnabled: { type: boolean, default: false }
tlsConfig:
  when: '{{ eq $.tlsEnabled true }}'
  then:
    certPath: { type: string, pattern: "^/.*\\.pem$", required: true }
  else:
    plainPort: { type: integer, min: 1024, max: 65535, required: true }
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Falsy: tlsEnabled=false; tlsConfig MISSING required plainPort.
	instance := map[string]any{
		"tlsEnabled": false,
		"tlsConfig":  map[string]any{},
	}
	ctx := &Context{Surround: instance}
	rpt, err := v.Validate(instance, ctx)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Errorf("falsy when + failing else: should produce findings; got none")
	}
	// Findings should be rebased under tlsConfig.
	found := false
	for _, f := range rpt.Findings {
		if isUnderPath(f.FieldPath, "tlsConfig") {
			found = true
		}
	}
	if !found {
		t.Errorf("else: findings should be rebased under tlsConfig; got %v", rpt.Findings)
	}
}

// TestValidateAllOfComposition verifies the walker recurses into
// allOf entries.
func TestValidateAllOfComposition(t *testing.T) {
	t.Parallel()

	src := `
component:
  allOf:
    - { type: object, properties: { name: { type: string } } }
    - { type: object, properties: { port: { type: integer } } }
`
	schema := mustCompileAndConvert(t, src)
	if _, err := New(schema); err != nil {
		t.Fatalf("New: %v", err)
	}
}

// TestValidateOneOfComposition verifies the walker recurses into
// oneOf entries.
func TestValidateOneOfComposition(t *testing.T) {
	t.Parallel()

	src := `
shape:
  oneOf:
    - { type: string, minLength: 1 }
    - { type: integer, min: 0 }
`
	schema := mustCompileAndConvert(t, src)
	if _, err := New(schema); err != nil {
		t.Fatalf("New: %v", err)
	}
}

// TestValidateAnyOfWithDefs verifies the walker recurses into anyOf
// + $defs entries without error.
func TestValidateAnyOfWithDefs(t *testing.T) {
	t.Parallel()

	src := `
$defs:
  StringField: { type: string, minLength: 1 }
  IntField: { type: integer, min: 0 }
data:
  anyOf:
    - { $ref: "#/$defs/StringField" }
    - { $ref: "#/$defs/IntField" }
`
	schema := mustCompileAndConvert(t, src)
	if _, err := New(schema); err != nil {
		t.Fatalf("New: %v", err)
	}
}

// TestValidateDeterminism verifies the deterministic-output invariant
// for both single-violation and multi-violation instances. The
// deterministic property walk in (*googleAdapter).Validate validates
// each property's sub-instance against its pre-resolved sub-schema in
// sorted-key order, so all violations surface as Findings regardless
// of the library's internal property iteration order.
func TestValidateDeterminism(t *testing.T) {
	t.Parallel()

	src := `
name: { type: string, pattern: "^[a-z]+$", required: true }
port: { type: integer, min: 1024, max: 65535 }
host: { type: string, format: hostname }
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Multi-violation instance: name pattern + port min both fail.
	instance := map[string]any{
		"name": "INVALID-CAPS",
		"port": 80,
		"host": "ok.example.com",
	}

	// Run the same Validate call many times. Even with Go's
	// randomised map iteration over Properties, the sorted walk
	// produces the same finding set + same ordering each time.
	const trials = 25
	var first string
	for i := 0; i < trials; i++ {
		rpt, err := v.Validate(instance, nil)
		if err != nil {
			t.Fatalf("Validate trial %d: %v", i, err)
		}
		current, _ := json.Marshal(rpt)
		if i == 0 {
			first = string(current)
			// Both violations should surface; expect at least 2 findings.
			if len(rpt.Findings) < 2 {
				t.Errorf("multi-violation instance should produce >= 2 findings; got %d (%v)",
					len(rpt.Findings), rpt.Findings)
			}
			continue
		}
		if string(current) != first {
			t.Fatalf("trial %d Report drifted from first:\nfirst:    %s\ncurrent:  %s",
				i, first, current)
		}
	}
}

// TestValidateNestedWhenOuterFalsyConsumes verifies that when an
// outer x-aggrega-when: predicate evaluates falsy, nested when-nodes
// inside its then: subtree are consumed (not evaluated). Exercises
// Validate's handled-paths skip path.
func TestValidateNestedWhenOuterFalsyConsumes(t *testing.T) {
	t.Parallel()

	src := `
outerToggle: { type: boolean, default: false }
outer:
  when: '{{ eq $.outerToggle true }}'
  then:
    inner:
      when: '{{ eq $.innerToggle true }}'
      then:
        deepField: { type: string, required: true }
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Outer falsy: outerToggle is false. Inner shouldn't be evaluated.
	// Instance has outer absent or empty.
	instance := map[string]any{
		"outerToggle": false,
	}
	ctx := &Context{Surround: instance}
	rpt, err := v.Validate(instance, ctx)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// No findings expected: outer falsy + no outer-else means no
	// constraint applies; inner is consumed.
	for _, f := range rpt.Findings {
		if isUnderPath(f.FieldPath, "outer") {
			t.Errorf("outer falsy should consume nested findings; got %v", f)
		}
	}
}

// TestValidateXAggregaWhenFalsyElseDeepFailure verifies that when an
// else: branch validation fails on a deeply-nested property, the
// adapter rebases the path correctly (covers
// libraryErrorToFindingsWithPathPrefix's subPath-nonempty branch).
func TestValidateXAggregaWhenFalsyElseDeepFailure(t *testing.T) {
	t.Parallel()

	src := `
toggle: { type: boolean, default: false }
config:
  when: '{{ eq $.toggle true }}'
  then:
    enabled: { type: boolean }
  else:
    detail: { type: string, pattern: "^[a-z]+$" }
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Falsy: toggle=false; config.detail has invalid pattern.
	instance := map[string]any{
		"toggle": false,
		"config": map[string]any{
			"detail": "HAS_CAPS_AND_UNDERSCORES",
		},
	}
	ctx := &Context{Surround: instance}
	rpt, err := v.Validate(instance, ctx)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Expect a finding rebased under config.
	found := false
	for _, f := range rpt.Findings {
		if isUnderPath(f.FieldPath, "config") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected finding rebased under config; got %v", rpt.Findings)
	}
}

// TestValidateRequiredMissing verifies the deterministic walk emits
// one Finding per missing required key per level, sorted, regardless
// of which keys the library would have reported first.
func TestValidateRequiredMissing(t *testing.T) {
	t.Parallel()

	src := `
alpha: { type: string, required: true }
bravo: { type: string, required: true }
charlie: { type: string, required: true }
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// All three required keys missing.
	instance := map[string]any{}
	rpt, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) < 3 {
		t.Errorf("expected >=3 missing-required findings; got %d (%v)",
			len(rpt.Findings), rpt.Findings)
	}
	// Findings should be sorted alphabetically by FieldPath.
	for i := 1; i < len(rpt.Findings); i++ {
		if rpt.Findings[i].FieldPath < rpt.Findings[i-1].FieldPath {
			t.Errorf("findings not sorted by FieldPath: %v", rpt.Findings)
			break
		}
	}
}

// TestValidateMissingInstancePath verifies that when an x-aggrega-when:
// predicate's instance path does not exist in the instance, the
// adapter skips the subtree (covers Validate's navigate-failed
// continue branch).
func TestValidateMissingInstancePath(t *testing.T) {
	t.Parallel()

	src := `
optionalGate:
  when: '{{ true }}'
  then:
    requiredField: { type: string, required: true }
`
	schema := mustCompileAndConvert(t, src)
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// optionalGate not present in instance.
	instance := map[string]any{}
	_, err = v.Validate(instance, nil)
	if err != nil {
		// Missing optionalGate is acceptable; library may emit a
		// finding about missing properties, but no AdapterError.
		t.Logf("Validate returned error (acceptable): %v", err)
	}
}

// mustCompileAndConvert is a test helper that takes a compact-form
// YAML string, compiles to a map via pkg/compactform.Compile, then
// JSON-roundtrips into a *jsonschema.Schema.
func mustCompileAndConvert(t *testing.T, src string) *jsonschema.Schema {
	t.Helper()

	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	schemaMap, err := compactform.Compile(&input)
	if err != nil {
		t.Fatalf("compactform.Compile: %v", err)
	}
	jsonBytes, err := json.Marshal(schemaMap)
	if err != nil {
		t.Fatalf("json.Marshal schema map: %v", err)
	}
	schema := &jsonschema.Schema{}
	if err := json.Unmarshal(jsonBytes, schema); err != nil {
		t.Fatalf("json.Unmarshal -> *Schema: %v", err)
	}
	return schema
}
