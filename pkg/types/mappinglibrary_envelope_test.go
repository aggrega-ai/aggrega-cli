// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	libschema "github.com/google/jsonschema-go/jsonschema"
	"go.yaml.in/yaml/v3"

	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// mappingLibraryEnvelopeValidator constructs a Validator over the shipped
// MappingLibrary JSON Schema. Tests use the validator to assert
// envelope-level constraints (PascalCase patterns, additionalProperties:
// false on spec) actually fire on malformed input.
//
// The schema is compiled into this test binary from a referenced variable,
// so a schema edit invalidates this package's cached result rather than
// serving a stale one.
func mappingLibraryEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemas.MappingLibraryV1alpha1), schema); err != nil {
		t.Fatalf("unmarshal envelope schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator for envelope schema: %v", err)
	}
	return v
}

// yamlInstanceForMappingLibrary parses a YAML document into the any-typed
// instance shape the validator expects, normalising YAML's
// map[interface{}]interface{} into map[string]any.
func yamlInstanceForMappingLibrary(t *testing.T, src string) any {
	t.Helper()
	var raw any
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return normaliseMappingYAML(raw)
}

func normaliseMappingYAML(v any) any {
	switch val := v.(type) {
	case map[any]any:
		out := map[string]any{}
		for k, vv := range val {
			if ks, ok := k.(string); ok {
				out[ks] = normaliseMappingYAML(vv)
			}
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, vv := range val {
			out[k] = normaliseMappingYAML(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = normaliseMappingYAML(vv)
		}
		return out
	default:
		return v
	}
}

// TestMappingLibrary_MappingKeysPascalCase verifies the propertyNames
// pattern on spec.mappings. A non-PascalCase key fails envelope
// validation; the canonical reference URL derivation depends on this
// discipline.
func TestMappingLibrary_MappingKeysPascalCase(t *testing.T) {
	t.Parallel()
	v := mappingLibraryEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: LowercaseKey
  version: 1.0.0
spec:
  mappings:
    lowercase_thing: 'value: x'
`
	rpt, err := v.Validate(yamlInstanceForMappingLibrary(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on lowercase mapping key; got no findings")
	}
}

// TestMappingLibrary_SpecRejectsAdditionalProperties verifies the
// envelope-level rejection of unknown sibling fields on spec.
// MappingLibrary ships single-authoring-mode; the envelope schema
// rejects any sibling field (mappingsJsonnet as the canonical example).
func TestMappingLibrary_SpecRejectsAdditionalProperties(t *testing.T) {
	t.Parallel()
	v := mappingLibraryEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: WithJsonnet
  version: 1.0.0
spec:
  mappings:
    Valid: 'x: y'
  mappingsJsonnet:
    Foo: |
      { value: 'x' }
`
	rpt, err := v.Validate(yamlInstanceForMappingLibrary(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on mappingsJsonnet sibling field; got no findings")
	}
}

// TestMappingLibrary_EnvelopeAcceptsValidInstance is the positive control
// for the rejection tests: a clean MappingLibrary instance with
// PascalCase keys, a non-empty mappings map, and no unknown spec
// siblings validates with zero findings.
func TestMappingLibrary_EnvelopeAcceptsValidInstance(t *testing.T) {
	t.Parallel()
	v := mappingLibraryEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.image }}'
    Replicas: 'replicas: {{ .Values.replicas | default 1 }}'
`
	rpt, err := v.Validate(yamlInstanceForMappingLibrary(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		var unexpected []string
		for _, f := range rpt.Findings {
			unexpected = append(unexpected, f.FieldPath+": "+f.Message)
		}
		t.Errorf("expected valid envelope; got %d findings:\n  %v", len(rpt.Findings), unexpected)
	}
}
