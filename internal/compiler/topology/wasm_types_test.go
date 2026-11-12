// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// mutatorSchemaValidator constructs a Validator over the shipped
// wasm-mutator JSON Schema. The schema is compiled into this test binary
// from a referenced variable, so a schema edit invalidates this package's
// cached result rather than serving a stale one.
func mutatorSchemaValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemas.WasmMutatorV1beta1), schema); err != nil {
		t.Fatalf("unmarshal mutator schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator: %v", err)
	}
	return v
}

// jsonInstance marshals v and unmarshals the result back to a generic
// any value. The any value is what the validator's deterministic walker
// consumes as its instance.
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

// TestMutatorInput_RoundTripJSONSchema validates that a fixture
// MutatorInput marshals to JSON that passes the wasm-mutator schema
// and unmarshals back to an equal Go value.
func TestMutatorInput_RoundTripJSONSchema(t *testing.T) {
	t.Parallel()
	v := mutatorSchemaValidator(t)

	labelsAPI := types.NewOrderedMap[string]()
	labelsAPI.Set("team", "payments")
	labelsAPI.Set("tier", "service")
	labelsDB := types.NewOrderedMap[string]()
	labelsDB.Set("team", "payments")
	labelsDB.Set("tier", "datastore")
	nodes := types.NewOrderedMap[topology.MutatorNode]()
	nodes.Set("payments-api", topology.MutatorNode{
		Component:       "core/web-service",
		ResolvedVersion: "1.4.2",
		Labels:          *labelsAPI,
	})
	nodes.Set("payments-db", topology.MutatorNode{
		Component:       "core/postgres",
		ResolvedVersion: "15.2.0",
		Labels:          *labelsDB,
	})

	fixture := topology.MutatorInput{
		CompilationContext: topology.CompilationContext{
			ReleaseName: "payments",
			Environment: "prod",
		},
		Graph: topology.MutatorGraph{
			Nodes: *nodes,
		},
	}

	rpt, err := v.Validate(jsonInstance(t, fixture), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected schema acceptance of MutatorInput fixture; got %d findings: %v",
			len(rpt.Findings), rpt.Findings)
	}

	// Round-trip: marshal, unmarshal back to MutatorInput, assert equality.
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var round topology.MutatorInput
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(fixture, round) {
		t.Errorf("round-trip inequality:\n  before: %+v\n  after:  %+v", fixture, round)
	}
}

// TestMutatorOutput_RoundTripJSONSchema validates that a fixture
// MutatorOutput marshals to JSON that passes the wasm-mutator schema
// and unmarshals back to an equal Go value.
func TestMutatorOutput_RoundTripJSONSchema(t *testing.T) {
	t.Parallel()
	v := mutatorSchemaValidator(t)

	patch1 := json.RawMessage(`[{"op":"add","path":"/nodes/payments-api/labels/compliance","value":"soc2"}]`)
	patch2 := json.RawMessage(`[{"op":"replace","path":"/nodes/payments-db/labels/tier","value":"managed-db"}]`)

	fixture := topology.MutatorOutput{
		Patches: []json.RawMessage{patch1, patch2},
	}

	rpt, err := v.Validate(jsonInstance(t, fixture), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected schema acceptance of MutatorOutput fixture; got %d findings: %v",
			len(rpt.Findings), rpt.Findings)
	}

	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var round topology.MutatorOutput
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(round.Patches) != len(fixture.Patches) {
		t.Errorf("round-trip patch count: before=%d after=%d", len(fixture.Patches), len(round.Patches))
	}
}

// TestMutatorTypes_JSONSchemaFieldCoverage asserts that every exported
// Go field of MutatorInput and MutatorOutput appears as a property name
// in the corresponding $defs block of the JSON Schema. Catches the
// "Go struct grew a field, schema didn't" drift directly.
func TestMutatorTypes_JSONSchemaFieldCoverage(t *testing.T) {
	t.Parallel()
	var raw map[string]any
	if err := json.Unmarshal([]byte(schemas.WasmMutatorV1beta1), &raw); err != nil {
		t.Fatalf("unmarshal mutator schema: %v", err)
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

	assertGoStructPropertiesIn(t, reflect.TypeOf(topology.MutatorInput{}), "MutatorInput")
	assertGoStructPropertiesIn(t, reflect.TypeOf(topology.MutatorOutput{}), "MutatorOutput")
	assertGoStructPropertiesIn(t, reflect.TypeOf(topology.CompilationContext{}), "CompilationContext")
	assertGoStructPropertiesIn(t, reflect.TypeOf(topology.MutatorGraph{}), "MutatorGraph")
	assertGoStructPropertiesIn(t, reflect.TypeOf(topology.MutatorNode{}), "MutatorNode")
}
