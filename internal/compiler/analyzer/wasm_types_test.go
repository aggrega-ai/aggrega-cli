// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// policyValidatorSchemaValidator loads the shipped wasm-policy-validator
// JSON Schema from the sibling aggrega-schemas repo and constructs a
// Validator over it. Skips when the schema file is unreachable so the
// test suite stays clean on consumer checkouts that do not vendor the
// schemas submodule.
func policyValidatorSchemaValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schemaPath := filepath.Join("..", "..", "..", "..", "aggrega-schemas", "json-schemas", "wasm-policy-validator-v1alpha1.schema.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("wasm-policy-validator schema not reachable at %s: %v", schemaPath, err)
	}
	schema := &libschema.Schema{}
	if err := json.Unmarshal(schemaBytes, schema); err != nil {
		t.Fatalf("unmarshal policy-validator schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator: %v", err)
	}
	return v
}

// jsonInstance marshals v and unmarshals the result back to a generic
// any value the validator's deterministic walker consumes.
func jsonInstance(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var inst any
	if err := json.Unmarshal(raw, &inst); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return inst
}

// TestPolicyValidatorInput_RoundTripJSONSchema validates that a fixture
// PolicyValidatorInput marshals to JSON that passes the schema and
// unmarshals back to an equivalent Go value.
func TestPolicyValidatorInput_RoundTripJSONSchema(t *testing.T) {
	t.Parallel()
	v := policyValidatorSchemaValidator(t)

	labelsAPI := types.NewOrderedMap[string]()
	labelsAPI.Set("team", "payments")
	labelsAPI.Set("tier", "service")
	labelsDB := types.NewOrderedMap[string]()
	labelsDB.Set("team", "payments")
	labelsDB.Set("tier", "datastore")

	fixture := analyzer.PolicyValidatorInput{
		Graph: analyzer.PolicyGraph{
			Nodes: map[string]analyzer.PolicyNode{
				"payments-api": {
					Component:       "core/web-service",
					ResolvedVersion: "1.4.2",
					Labels:          *labelsAPI,
				},
				"payments-db": {
					Component:       "core/postgres",
					ResolvedVersion: "15.2.0",
					Labels:          *labelsDB,
				},
			},
			Edges: []analyzer.PolicyEdge{
				{
					Source: "payments-api",
					Target: "payments-db",
					Socket: "databases",
				},
			},
		},
		PrecalculatedPaths: []analyzer.PrecalculatedPath{
			{
				PathID:           "path-0001",
				Nodes:            []string{"payments-api", "payments-db"},
				AttachedFeatures: []string{},
			},
		},
	}

	rpt, err := v.Validate(jsonInstance(t, fixture), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected schema acceptance of PolicyValidatorInput fixture; got %d findings: %v",
			len(rpt.Findings), rpt.Findings)
	}

	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var round analyzer.PolicyValidatorInput
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	// Field-by-field comparison: OrderedMap's iteration order survives
	// the round-trip via its Marshal/Unmarshal pair; reflect.DeepEqual
	// suffices for the scalar fields and the precalculated-paths slice.
	if !reflect.DeepEqual(fixture.PrecalculatedPaths, round.PrecalculatedPaths) {
		t.Errorf("PrecalculatedPaths round-trip inequality:\n  before: %+v\n  after:  %+v",
			fixture.PrecalculatedPaths, round.PrecalculatedPaths)
	}
	if !reflect.DeepEqual(fixture.Graph.Edges, round.Graph.Edges) {
		t.Errorf("Graph.Edges round-trip inequality:\n  before: %+v\n  after:  %+v",
			fixture.Graph.Edges, round.Graph.Edges)
	}
	if len(fixture.Graph.Nodes) != len(round.Graph.Nodes) {
		t.Errorf("Graph.Nodes count mismatch: before=%d after=%d",
			len(fixture.Graph.Nodes), len(round.Graph.Nodes))
	}
}

// TestPolicyValidatorOutput_RoundTripJSONSchema validates that a fixture
// PolicyValidatorOutput marshals to JSON that passes the schema and
// unmarshals back to an equal Go value.
func TestPolicyValidatorOutput_RoundTripJSONSchema(t *testing.T) {
	t.Parallel()
	v := policyValidatorSchemaValidator(t)

	fixture := analyzer.PolicyValidatorOutput{
		Results: []analyzer.PolicyResult{
			{Pass: true, Message: "soc2-required-labels: payments tier carries soc2 annotation"},
			{Pass: false, Message: "version-floor: payments-db must run resolved_version >= 15.0.0; got 14.9.0"},
		},
	}

	rpt, err := v.Validate(jsonInstance(t, fixture), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected schema acceptance of PolicyValidatorOutput fixture; got %d findings: %v",
			len(rpt.Findings), rpt.Findings)
	}

	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var round analyzer.PolicyValidatorOutput
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(fixture, round) {
		t.Errorf("round-trip inequality:\n  before: %+v\n  after:  %+v", fixture, round)
	}
}

// TestPolicyValidatorTypes_JSONSchemaFieldCoverage asserts every
// exported Go field of the policy-validator socket types appears as a
// property in the corresponding $defs block of the JSON Schema. Catches
// "Go struct grew a field, schema didn't" drift directly.
func TestPolicyValidatorTypes_JSONSchemaFieldCoverage(t *testing.T) {
	t.Parallel()
	schemaPath := filepath.Join("..", "..", "..", "..", "aggrega-schemas", "json-schemas", "wasm-policy-validator-v1alpha1.schema.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("wasm-policy-validator schema not reachable at %s: %v", schemaPath, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(schemaBytes, &raw); err != nil {
		t.Fatalf("unmarshal policy-validator schema: %v", err)
	}
	defs, ok := raw["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("schema $defs missing or not an object")
	}

	assertGoStructPropertiesIn := func(t *testing.T, goType reflect.Type, defName string) {
		t.Helper()
		def, ok := defs[defName].(map[string]any)
		if !ok {
			t.Fatalf("schema $defs.%s missing or not an object", defName)
			return
		}
		props, ok := def["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema $defs.%s.properties missing or not an object", defName)
			return
		}
		for i := 0; i < goType.NumField(); i++ {
			f := goType.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "" {
				name = f.Name
			}
			if _, present := props[name]; !present {
				t.Errorf("schema $defs.%s.properties missing %q (Go field %s.%s)",
					defName, name, goType.Name(), f.Name)
			}
		}
	}

	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.PolicyValidatorInput{}), "PolicyValidatorInput")
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.PolicyValidatorOutput{}), "PolicyValidatorOutput")
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.PolicyGraph{}), "PolicyGraph")
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.PolicyNode{}), "PolicyNode")
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.PolicyEdge{}), "PolicyEdge")
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.PrecalculatedPath{}), "PrecalculatedPath")
	assertGoStructPropertiesIn(t, reflect.TypeOf(analyzer.PolicyResult{}), "PolicyResult")
}
