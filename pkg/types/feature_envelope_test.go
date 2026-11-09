// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	libschema "github.com/google/jsonschema-go/jsonschema"

	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// featureEnvelopeValidator constructs a Validator over the shipped Feature
// JSON Schema. The schema is compiled into this test binary from a
// referenced variable, so a schema edit invalidates this package's cached
// result rather than serving a stale one.
func featureEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemas.FeatureV1beta1), schema); err != nil {
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

	src := `apiVersion: aggrega.ai/v1beta1
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

	src := `apiVersion: aggrega.ai/v1beta1
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
