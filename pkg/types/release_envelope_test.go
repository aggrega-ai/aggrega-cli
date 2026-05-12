// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"

	jsonvalidator "github.com/cognitio/aggrega-cli/pkg/validator/jsonschema"
)

// releaseEnvelopeValidator loads the shipped Release JSON Schema from the
// sibling aggrega-schemas repo and constructs a Validator over it.
func releaseEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schemaPath := filepath.Join("..", "..", "..", "aggrega-schemas", "json-schemas", "release-v1alpha1.schema.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("Release envelope schema not reachable at %s: %v", schemaPath, err)
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

// TestReleaseEnvelope_RejectsParametersFrom pins the envelope-load
// rejection of the retired parametersFrom: field. The list-form
// multi-source layering capability migrates to Release extends: chains;
// the single-source file-import capability moves to parameters: {$import:}.
// Authors writing the legacy field fail envelope validation at workspace
// load. Closes the symmetric coverage gap with the Recipe-side retired
// fields (workloadFrom, artifactsFrom).
func TestReleaseEnvelope_RejectsParametersFrom(t *testing.T) {
	t.Parallel()
	v := releaseEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: legacy-params-from
  environment: prod
  version: 1.0.0
spec:
  aggregate: my-app
  manifest: my-manifest
  parametersFrom:
    - ./base.yaml
    - ./prod-overrides.yaml
  boundaries:
    prod:
      directory: ./output
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on retired parametersFrom: field; got no findings")
	}
}

// TestReleaseEnvelope_AcceptsParametersInline pins the positive path for
// mode 1: a Release with inline parameters object passes envelope load.
func TestReleaseEnvelope_AcceptsParametersInline(t *testing.T) {
	t.Parallel()
	v := releaseEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
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

	src := `apiVersion: aggrega.ai/v1alpha1
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
