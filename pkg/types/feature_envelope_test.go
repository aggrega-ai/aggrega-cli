// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"

	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

func featureEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schemaPath := filepath.Join("..", "..", "..", "aggrega-schemas", "json-schemas", "feature-v1alpha1.schema.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("Feature envelope schema not reachable at %s: %v", schemaPath, err)
	}
	schema := &libschema.Schema{}
	if err := json.Unmarshal(schemaBytes, schema); err != nil {
		t.Fatalf("unmarshal envelope schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator: %v", err)
	}
	return v
}

func TestFeatureEnvelope_AcceptsAssertions(t *testing.T) {
	t.Parallel()
	v := featureEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: ops/audit
  version: "1.0.0"
spec:
  assertions:
    audit-level-required: |
      {{- if eq .values.level "" -}}{{- fail "level required" -}}{{- end -}}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected acceptance; got %d findings: %v", len(rpt.Findings), rpt.Findings)
	}
}

func TestFeatureEnvelope_RejectsNonStringAssertionValue(t *testing.T) {
	t.Parallel()
	v := featureEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: ops/audit
  version: "1.0.0"
spec:
  assertions:
    bad-rule:
      nested: object
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatal("expected envelope rejection; got none")
	}
}
