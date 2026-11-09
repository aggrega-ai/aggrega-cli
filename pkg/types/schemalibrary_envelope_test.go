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

// schemaLibraryEnvelopeValidator constructs a Validator over the shipped
// SchemaLibrary JSON Schema. Tests use the validator to assert
// envelope-level constraints (minProperties, propertyNames patterns)
// actually fire on malformed input.
//
// The schema is compiled into this test binary from a referenced variable,
// so a schema edit invalidates this package's cached result rather than
// serving a stale one.
func schemaLibraryEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemas.SchemaLibraryV1beta1), schema); err != nil {
		t.Fatalf("unmarshal envelope schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator for envelope schema: %v", err)
	}
	return v
}

// yamlToInstance parses a YAML document into the any-typed instance shape
// the validator expects.
func yamlToInstance(t *testing.T, src string) any {
	t.Helper()
	var raw any
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	// Normalise YAML's map[interface{}]interface{} into map[string]any so
	// the JSON-Schema-go validator's instance walker handles it cleanly.
	return normalizeYAMLAnyMaps(raw)
}

func normalizeYAMLAnyMaps(v any) any {
	switch val := v.(type) {
	case map[any]any:
		out := map[string]any{}
		for k, vv := range val {
			if ks, ok := k.(string); ok {
				out[ks] = normalizeYAMLAnyMaps(vv)
			}
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, vv := range val {
			out[k] = normalizeYAMLAnyMaps(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = normalizeYAMLAnyMaps(vv)
		}
		return out
	default:
		return v
	}
}

// TestSchemaLibrary_TemplateBodyRequired verifies the envelope-level
// constraint that template entries carry $params + at least one body
// property (minProperties: 2 on each template entry). A template with
// only $params and no body fails envelope validation.
func TestSchemaLibrary_TemplateBodyRequired(t *testing.T) {
	t.Parallel()
	v := schemaLibraryEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: ParamsOnly
  version: 1.0.0
spec:
  schemas:
    Dummy:
      type: string
  templates:
    EmptyTemplate:
      $params:
        port:
          type: integer
`
	rpt, err := v.Validate(yamlToInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on template-body-missing; got no findings")
	}
}

// TestSchemaLibrary_SchemaKeysPascalCase verifies the propertyNames
// pattern on spec.schemas. A non-PascalCase key fails envelope
// validation; entries stay addressable by the host-less
// `<Library>#/schemas/<Entry>` identity reference, which selects by key.
func TestSchemaLibrary_SchemaKeysPascalCase(t *testing.T) {
	t.Parallel()
	v := schemaLibraryEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: LowercaseKey
  version: 1.0.0
spec:
  schemas:
    lowercase_thing:
      type: string
`
	rpt, err := v.Validate(yamlToInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on lowercase schema key; got no findings")
	}
}

// TestSchemaLibrary_TemplateNamesPascalCase verifies the propertyNames
// pattern on spec.templates. Same envelope-level discipline as schema
// entry keys.
func TestSchemaLibrary_TemplateNamesPascalCase(t *testing.T) {
	t.Parallel()
	v := schemaLibraryEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: LowercaseTemplate
  version: 1.0.0
spec:
  schemas:
    Valid:
      type: string
  templates:
    lowercase_template:
      $params:
        port:
          type: integer
      type: object
`
	rpt, err := v.Validate(yamlToInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on lowercase template name; got no findings")
	}
}

// TestSchemaLibrary_EnvelopeAcceptsValidInstance is the positive control
// for the three rejection tests: a clean SchemaLibrary instance with
// PascalCase keys, a non-empty schemas map, and a template carrying both
// $params and a body validates with zero findings.
func TestSchemaLibrary_EnvelopeAcceptsValidInstance(t *testing.T) {
	t.Parallel()
	v := schemaLibraryEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: WebPrimitives
  version: 1.0.0
spec:
  schemas:
    URL:
      type: string
      pattern: "^https?://"
  templates:
    Probe:
      $params:
        port:
          type: integer
      type: object
      properties:
        port:
          type: integer
          const: $port
`
	rpt, err := v.Validate(yamlToInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		// Allow a small set of expected findings if the validator's
		// walker has known coverage gaps (the validator package's
		// documented walker-completeness limitations); fail only when
		// the rejection is on the constraints this test exercises.
		var unexpected []string
		for _, f := range rpt.Findings {
			unexpected = append(unexpected, f.FieldPath+": "+f.Message)
		}
		t.Errorf("expected valid envelope; got %d findings:\n  %v", len(rpt.Findings), unexpected)
	}
}
