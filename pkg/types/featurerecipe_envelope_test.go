// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"testing"
)

// featureRecipeEnvelopeValidator is declared in recipe_envelope_test.go.
// This file extends FeatureRecipe envelope coverage with assertions-shape
// acceptance / rejection arms.

func TestFeatureRecipeEnvelope_AcceptsAssertions(t *testing.T) {
	t.Parallel()
	v := featureRecipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: ops/audit-bitnami
  version: "1.0.0"
spec:
  capability: helm
  implements: ops/audit:^1.0.0
  assertions:
    chart-version-supported: |
      {{- if eq .values.level "off" -}}{{- fail "Bitnami audit chart 20.x does not support off" -}}{{- end -}}
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Errorf("expected acceptance; got %d findings: %v", len(rpt.Findings), rpt.Findings)
	}
}

func TestFeatureRecipeEnvelope_RejectsNonStringAssertionValue(t *testing.T) {
	t.Parallel()
	v := featureRecipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: ops/audit-bitnami
  version: "1.0.0"
spec:
  capability: helm
  implements: ops/audit:^1.0.0
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

// TestFeatureRecipeEnvelope_AcceptsArtifactsBareNameLibraryImport pins the
// artifacts-site relax at the FeatureRecipe surface: a bare-name ArtifactsLibrary
// reference `<Library>#/artifacts/<entry>` is admitted (the entry names a bundle).
func TestFeatureRecipeEnvelope_AcceptsArtifactsBareNameLibraryImport(t *testing.T) {
	t.Parallel()
	v := featureRecipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: ops/audit-bitnami
  version: "1.0.0"
spec:
  capability: helm
  implements: ops/audit:^1.0.0
  artifacts:
    bundle:
      $import: SharedCRDs#/artifacts/Default
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) != 0 {
		t.Fatalf("expected a bare-name library import at artifacts to be accepted; got findings: %v", rpt.Findings)
	}
}

// TestFeatureRecipeEnvelope_RejectsArtifactsOCIImport pins that the host-bearing
// `oci://` form stays rejected at the FeatureRecipe artifacts site — a library is
// addressed by identity, never by registry location.
func TestFeatureRecipeEnvelope_RejectsArtifactsOCIImport(t *testing.T) {
	t.Parallel()
	v := featureRecipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: ops/audit-bitnami
  version: "1.0.0"
spec:
  capability: helm
  implements: ops/audit:^1.0.0
  artifacts:
    bundle:
      $import: oci://hub.aggrega.ai/aggrega/crds@1.0.0#/artifacts/Default
`
	rpt, err := v.Validate(yamlInstance(t, src), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rpt.Findings) == 0 {
		t.Fatalf("expected envelope rejection on oci:// URL at FeatureRecipe artifacts entry; got no findings")
	}
}
