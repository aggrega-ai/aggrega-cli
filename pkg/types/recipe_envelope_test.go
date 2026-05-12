// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"

	jsonvalidator "github.com/cognitio/aggrega-cli/pkg/validator/jsonschema"
)

// recipeEnvelopeValidator loads the shipped Recipe JSON Schema from the
// sibling aggrega-schemas repo and constructs a Validator over it.
// Tests use the validator to assert envelope-level rejection of the
// retired triple-collapse fields (valuesMappingFile, valuesMappingJsonnet)
// at workspace-load time. The triple's retirement at the renderer surface
// is covered by tests in pkg/validator and internal/compiler/renderer;
// the envelope-load surface is the JSON Schema's responsibility and
// gets pinned here so a future regression that reintroduces the fields
// surfaces immediately as an envelope-validation failure rather than
// flowing through to render time.
//
// Path is stable within the umbrella workspace: from
// aggrega-cli/pkg/types/, the schema sits at
// ../../../aggrega-schemas/json-schemas/recipe-v1alpha1.schema.json.
// The helper t.Skipf's gracefully if the schema is unreachable.
func recipeEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schemaPath := filepath.Join("..", "..", "..", "aggrega-schemas", "json-schemas", "recipe-v1alpha1.schema.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("Recipe envelope schema not reachable at %s; envelope-validation tests skipped in this environment: %v", schemaPath, err)
	}
	schema := &libschema.Schema{}
	if err := json.Unmarshal(schemaBytes, schema); err != nil {
		t.Fatalf("unmarshal envelope schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build validator for envelope schema: %v", err)
	}
	return v
}

func featureRecipeEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schemaPath := filepath.Join("..", "..", "..", "aggrega-schemas", "json-schemas", "featurerecipe-v1alpha1.schema.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Skipf("FeatureRecipe envelope schema not reachable at %s: %v", schemaPath, err)
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

func yamlInstance(t *testing.T, src string) any {
	t.Helper()
	var raw any
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return normaliseYAML(raw)
}

func normaliseYAML(v any) any {
	switch val := v.(type) {
	case map[any]any:
		out := map[string]any{}
		for k, vv := range val {
			if ks, ok := k.(string); ok {
				out[ks] = normaliseYAML(vv)
			}
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, vv := range val {
			out[k] = normaliseYAML(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = normaliseYAML(vv)
		}
		return out
	default:
		return v
	}
}

// TestRecipeEnvelope_RejectsValuesMappingJsonnet pins the envelope-load
// rejection of the retired valuesMappingJsonnet: field. A Recipe
// authored with the legacy field fails envelope validation before
// reaching the renderer; the rejection home migrated from renderer-time
// (pre-WI: ErrJsonnetNotSupported sentinel) to envelope-load time (this
// WI's collapse of the triple to a single valuesMapping: field with
// additionalProperties: false on spec).
func TestRecipeEnvelope_RejectsValuesMappingJsonnet(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: legacy-jsonnet
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  source: ./chart
  valuesMappingJsonnet: |
    { x: 1 }
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on retired valuesMappingJsonnet: field; got no findings")
	}
}

// TestRecipeEnvelope_RejectsValuesMappingFile pins the envelope-load
// rejection of the retired valuesMappingFile: field. The file-import
// capability relocates into valuesMapping: { $import: ... } (mode 2);
// the legacy sibling key has no coherent existence post-collapse.
func TestRecipeEnvelope_RejectsValuesMappingFile(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: legacy-file
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  source: ./chart
  valuesMappingFile: ./mappings/values.tmpl
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on retired valuesMappingFile: field; got no findings")
	}
}

// TestRecipeEnvelope_AcceptsInlineValuesMapping verifies the positive
// path: a Recipe with mode-1 inline valuesMapping passes envelope
// validation.
func TestRecipeEnvelope_AcceptsInlineValuesMapping(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: inline-recipe
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  source: ./chart
  valuesMapping: |
    image:
      tag: latest
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected envelope acceptance of inline valuesMapping; got %d findings:\n  %v", len(rpt.Findings), rpt.Findings)
	}
}

// TestRecipeEnvelope_AcceptsImportValuesMapping verifies the positive
// path for mode-2 file import: a Recipe with valuesMapping: { $import:
// <path> } passes envelope validation.
func TestRecipeEnvelope_AcceptsImportValuesMapping(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: import-recipe
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  source: ./chart
  valuesMapping:
    $import: ./mappings/values.tmpl
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected envelope acceptance of $import valuesMapping; got %d findings:\n  %v", len(rpt.Findings), rpt.Findings)
	}
}

// TestFeatureRecipeEnvelope_RejectsValuesMappingJsonnet pins the
// FeatureRecipe-side envelope rejection of the retired Jsonnet field.
func TestFeatureRecipeEnvelope_RejectsValuesMappingJsonnet(t *testing.T) {
	t.Parallel()
	v := featureRecipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: legacy-fr-jsonnet
  version: 1.0.0
spec:
  provider: helm
  implements: core/feature
  valuesMappingJsonnet: |
    { x: 1 }
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on retired valuesMappingJsonnet: field; got no findings")
	}
}

// TestFeatureRecipeEnvelope_RejectsValuesMappingFile pins the
// FeatureRecipe-side envelope rejection of the retired File field.
func TestFeatureRecipeEnvelope_RejectsValuesMappingFile(t *testing.T) {
	t.Parallel()
	v := featureRecipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: legacy-fr-file
  version: 1.0.0
spec:
  provider: helm
  implements: core/feature
  valuesMappingFile: ./mappings/v.tmpl
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on retired valuesMappingFile: field; got no findings")
	}
}

// TestRecipeEnvelope_RejectsWorkloadFrom pins the envelope-load rejection
// of the retired workloadFrom: field. The file-import capability
// relocates into workload: { $import: ... } (mode 2 of the
// WorkloadTemplate discriminator).
func TestRecipeEnvelope_RejectsWorkloadFrom(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: legacy-workload-from
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  workloadFrom: ./workload.yaml
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on retired workloadFrom: field; got no findings")
	}
}

// TestRecipeEnvelope_RejectsArtifactsFrom pins the envelope-load rejection
// of the retired artifactsFrom: field. The list-form capability migrates
// to per-entry $import: under the artifacts: map.
func TestRecipeEnvelope_RejectsArtifactsFrom(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: legacy-artifacts-from
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  artifactsFrom:
    - ./extras/policy.yaml
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on retired artifactsFrom: field; got no findings")
	}
}

// TestRecipeEnvelope_AcceptsWorkloadImport pins the positive path for
// the WorkloadTemplate mode 2 (file $import:).
func TestRecipeEnvelope_AcceptsWorkloadImport(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: workload-import
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  workload:
    $import: ./workload.yaml
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected envelope acceptance of workload: {$import:}; got %d findings: %v", len(rpt.Findings), rpt.Findings)
	}
}

// TestRecipeEnvelope_RejectsWorkloadOCIImport pins the operator-decided
// Mode-3 envelope rejection: oci:// URLs are not supported at workload:.
// The schema's `not: pattern` constraint on the $import: string value
// surfaces the rejection at envelope load with a specific error
// rather than a generic file-not-found at consumption time.
func TestRecipeEnvelope_RejectsWorkloadOCIImport(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: workload-oci-import
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  workload:
    $import: oci://hub.aggrega.ai/aggrega/helpers@1.0.0#/workloads/Default
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on oci:// URL at workload: site; got no findings")
	}
}

// TestRecipeEnvelope_RejectsArtifactsOCIImport pins the parallel Mode-3
// rejection for artifacts entries.
func TestRecipeEnvelope_RejectsArtifactsOCIImport(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: artifacts-oci-import
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  source: ./chart
  valuesMapping: |
    {}
  artifacts:
    policy.yaml:
      $import: oci://hub.aggrega.ai/aggrega/policies@1.0.0#/artifacts/Default
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on oci:// URL at artifacts entry; got no findings")
	}
}

// TestRecipeEnvelope_AcceptsAssertions pins the new spec.assertions:
// field — an OrderedMap of named Go template strings carrying Recipe-
// level implementation-invariant rules. Evaluated at Step 5b.
func TestRecipeEnvelope_AcceptsAssertions(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: bitnami/datastore/mariadb/k8s
  version: "1.0.0"
spec:
  provider: helm
  implements: core/datastore/mariadb:^1.0.0
  source:
    chart: oci://registry-1.docker.io/bitnamicharts/mariadb
    version: "20.0.0"
  valuesMapping: |
    {}
  locals: {}
  assertions:
    chart-replication-arm-incompatible: |
      {{- if and (eq .values.architecture "replication") (contains "arm" .infra.architecture) -}}
        {{- fail "Bitnami chart 20.x is incompatible with ARM replication" -}}
      {{- end -}}
    metrics-namespace-required: |
      {{- if and (eq .values.architecture "replication") .values.metrics.serviceMonitor.enabled -}}
        {{- if not .values.metrics.serviceMonitor.namespace -}}
          {{- fail "metrics.serviceMonitor.namespace required" -}}
        {{- end -}}
      {{- end -}}
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

// TestRecipeEnvelope_RejectsNonStringAssertionValue pins the
// additionalProperties: { type: string } constraint on Recipe assertions.
func TestRecipeEnvelope_RejectsNonStringAssertionValue(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: bitnami/datastore/mariadb/k8s
  version: "1.0.0"
spec:
  provider: helm
  implements: core/datastore/mariadb:^1.0.0
  source:
    chart: oci://registry-1.docker.io/bitnamicharts/mariadb
    version: "20.0.0"
  valuesMapping: |
    {}
  locals: {}
  assertions:
    bad-rule:
      nested: object
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on non-string assertion value; got no findings")
	}
}
