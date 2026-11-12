// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types_test

import (
	"strings"
	"testing"
)

// featureRecipeEnvelopeValidator is declared in recipe_envelope_test.go.
// This file extends FeatureRecipe envelope coverage with assertions-shape
// acceptance / rejection arms.

func TestFeatureRecipeEnvelope_AcceptsAssertions(t *testing.T) {
	t.Parallel()
	v := featureRecipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
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

	src := `apiVersion: aggrega.ai/v1beta1
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
// artifacts-site relax at the FeatureRecipe surface: a bare-name ArtifactLibrary
// reference `<Library>#/artifacts/<entry>` is admitted (the entry names a bundle).
func TestFeatureRecipeEnvelope_AcceptsArtifactsBareNameLibraryImport(t *testing.T) {
	t.Parallel()
	v := featureRecipeEnvelopeValidator(t)

	src := `apiVersion: aggrega.ai/v1beta1
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

	src := `apiVersion: aggrega.ai/v1beta1
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

// TestFeatureRecipeEnvelope_RejectsArtifactKeyOutsideFilenameAlphabet pins the
// propertyNames constraint at the FeatureRecipe artifacts site. The key names
// the entry's output, so it is bounded to a single filename: a path separator
// would make one key name a path rather than a file, and a leading dot would
// admit the parent-directory segment, which names a location outside the
// output directory.
//
// The assertion names the keyword and the offending key rather than counting
// findings, because a document can fail envelope validation for reasons that
// have nothing to do with its artifact keys.
func TestFeatureRecipeEnvelope_RejectsArtifactKeyOutsideFilenameAlphabet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		key  string
	}{
		{"path separator", "nested/policy.yaml"},
		{"leading dot", ".hidden.yaml"},
		{"parent directory", ".."},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := featureRecipeEnvelopeValidator(t)

			src := `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: ops/audit-bitnami
  version: "1.0.0"
spec:
  capability: helm
  implements: ops/audit:^1.0.0
  artifacts:
    "` + tc.key + `": |
      apiVersion: v1
      kind: ConfigMap
`
			rpt, err := v.Validate(yamlInstance(t, src), nil)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			var named bool
			for _, f := range rpt.Findings {
				if f.Keyword == "propertyNames" && strings.Contains(f.Message, tc.key) {
					named = true
				}
			}
			if !named {
				t.Fatalf("expected a propertyNames finding naming the artifact key %q; got %d findings: %v",
					tc.key, len(rpt.Findings), rpt.Findings)
			}
		})
	}
}

// TestFeatureRecipeEnvelope_AcceptsArtifactKeyInFilenameAlphabet is the
// acceptance half of the constraint above. A rejection-only test is satisfied
// by a pattern that matches nothing, so a pattern that over-constrained the
// alphabet would pass the rejection arm and be caught only here.
func TestFeatureRecipeEnvelope_AcceptsArtifactKeyInFilenameAlphabet(t *testing.T) {
	t.Parallel()

	keys := []string{
		"main.tf",
		"template.yaml",
		"serviceaccount.yaml",
		"a",
		"with-hyphen.yaml",
		"with_underscore.yaml",
	}

	for _, key := range keys {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			v := featureRecipeEnvelopeValidator(t)

			src := `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: ops/audit-bitnami
  version: "1.0.0"
spec:
  capability: helm
  implements: ops/audit:^1.0.0
  artifacts:
    "` + key + `": |
      apiVersion: v1
      kind: ConfigMap
`
			rpt, err := v.Validate(yamlInstance(t, src), nil)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if len(rpt.Findings) != 0 {
				t.Fatalf("expected the artifact key %q to be admitted; got %d findings: %v",
					key, len(rpt.Findings), rpt.Findings)
			}
		})
	}
}
