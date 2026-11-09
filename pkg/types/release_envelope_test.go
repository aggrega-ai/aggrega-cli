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

// releaseEnvelopeValidator constructs a Validator over the shipped Release
// JSON Schema. The schema is compiled into this test binary from a
// referenced variable, so a schema edit invalidates this package's cached
// result rather than serving a stale one.
func releaseEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemas.ReleaseV1beta1), schema); err != nil {
		t.Fatalf("unmarshal envelope schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator: %v", err)
	}
	return v
}

// TestReleaseEnvelope_AcceptsParametersInline pins the positive path for
// mode 1: a Release with inline parameters object passes envelope load.
func TestReleaseEnvelope_AcceptsParametersInline(t *testing.T) {
	t.Parallel()
	v := releaseEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: inline-params
  environment: prod
  version: 1.0.0
spec:
  aggregate: my-app
  manifest: my-manifest
  parameters:
    api_port: "8080"
    db:
      host: prod-db.internal
  boundaries:
    prod:
      directory: ./output
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected envelope acceptance of inline parameters; got %d findings: %v", len(rpt.Findings), rpt.Findings)
	}
}

// TestReleaseEnvelope_AcceptsParametersImport pins the positive path for
// mode 2: parameters: {$import: <path>}.
func TestReleaseEnvelope_AcceptsParametersImport(t *testing.T) {
	t.Parallel()
	v := releaseEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: import-params
  environment: prod
  version: 1.0.0
spec:
  aggregate: my-app
  manifest: my-manifest
  parameters:
    $import: ./prod-values.yaml
  boundaries:
    prod:
      directory: ./output
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected envelope acceptance of parameters $import; got %d findings: %v", len(rpt.Findings), rpt.Findings)
	}
}
