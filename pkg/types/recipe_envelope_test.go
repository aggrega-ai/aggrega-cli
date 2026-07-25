// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"go.yaml.in/yaml/v3"

	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// recipeEnvelopeValidator loads the shipped Recipe JSON Schema from the
// sibling aggrega-schemas repo and constructs a Validator over it.
// Tests use the validator to assert envelope-level acceptance of the
// supported authoring shapes at workspace-load time.
//
// Path is stable within the umbrella workspace: from
// aggrega-cli/pkg/types/, the schema sits at
// ../../../aggrega-schemas/json/recipe-v1alpha1.schema.json.
// The helper t.Skipf's gracefully if the schema is unreachable.
func recipeEnvelopeValidator(t *testing.T) jsonvalidator.Validator {
	t.Helper()
	schemaPath := filepath.Join("..", "..", "..", "aggrega-schemas", "json", "recipe-v1alpha1.schema.json")
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
	schemaPath := filepath.Join("..", "..", "..", "aggrega-schemas", "json", "featurerecipe-v1alpha1.schema.json")
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
  capability: helm
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
  capability: helm
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
  capability: helm
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
  capability: helm
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
  capability: helm
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

// TestRecipeEnvelope_AcceptsWorkloadBareNameLibraryImport pins that the envelope
// admits a bare-name WorkloadLibrary reference at workload: — the supported
// library-import mode `<Library>#/workloads/<entry>`. The host-bearing `oci://`
// form stays rejected (see TestRecipeEnvelope_RejectsWorkloadOCIImport); the
// bare-name identity form is the library surface a Recipe uses to factor a shared
// Workload template.
func TestRecipeEnvelope_AcceptsWorkloadBareNameLibraryImport(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: workload-bare-name-import
  version: 1.0.0
spec:
  capability: helm
  implements: core/server
  workload:
    $import: Helpers#/workloads/Default
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Fatalf("expected a bare-name library import at workload: to be accepted; got findings: %v", rpt.Findings)
	}
}

// TestRecipeEnvelope_AcceptsArtifactsBareNameLibraryImport pins that the envelope
// admits a bare-name ArtifactsLibrary reference at an artifacts entry — the
// supported library-import mode `<Library>#/artifacts/<entry>` (the entry names a
// bundle). The host-bearing `oci://` form stays rejected (see
// TestRecipeEnvelope_RejectsArtifactsOCIImport); the bare-name identity form is the
// library surface a Recipe uses to factor a shared artifact bundle.
func TestRecipeEnvelope_AcceptsArtifactsBareNameLibraryImport(t *testing.T) {
	t.Parallel()
	v := recipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: artifacts-bare-name-import
  version: 1.0.0
spec:
  capability: helm
  implements: core/server
  source: ./chart
  valuesMapping: |
    {}
  artifacts:
    bundle:
      $import: Policies#/artifacts/Default
  locals: {}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Fatalf("expected a bare-name library import at artifacts to be accepted; got findings: %v", rpt.Findings)
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
  capability: helm
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
  capability: helm
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
