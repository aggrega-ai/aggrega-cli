// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types_test

import (
	"encoding/json"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	libschema "github.com/google/jsonschema-go/jsonschema"

	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// componentEnvelopeValidator constructs a Validator over the shipped
// Component JSON Schema. The schema is compiled into this test binary from a
// referenced variable, so a schema edit invalidates this package's cached
// result rather than serving a stale one.
func componentEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemas.ComponentV1beta1), schema); err != nil {
		t.Fatalf("unmarshal envelope schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator: %v", err)
	}
	return v
}

// TestComponentEnvelope_AcceptsAssertions pins the new field's acceptance:
// spec.assertions: an OrderedMap of named Go template strings.
func TestComponentEnvelope_AcceptsAssertions(t *testing.T) {
	t.Parallel()
	v := componentEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  assertions:
    replication-min-secondary: |
      {{- if eq .values.architecture "replication" -}}
        {{- if lt .values.secondary.replicaCount 2 -}}
          {{- fail "replication requires >= 2 secondary" -}}
        {{- end -}}
      {{- end -}}
    total-node-limit: |
      {{- $total := add .values.primary.replicaCount .values.secondary.replicaCount -}}
      {{- if gt $total 9 -}}{{- fail "total > 9" -}}{{- end -}}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected acceptance of named assertions; got %d findings: %v",
			len(rpt.Findings), rpt.Findings)
	}
}

// TestComponentEnvelope_RejectsNonStringAssertionValue pins the
// additionalProperties: { type: string } constraint — values must be
// template strings, not nested objects or other shapes.
func TestComponentEnvelope_RejectsNonStringAssertionValue(t *testing.T) {
	t.Parallel()
	v := componentEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  assertions:
    bad-rule:
      nested: object
      not: a-string
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatal("expected envelope rejection of non-string assertion value; got none")
	}
}
