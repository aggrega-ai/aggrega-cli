// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// AnnotationBinding load tests: verify the Manifest loader parses
// `mappings.features.annotations` from the flat Manifest shape into the
// `*OrderedMap[AnnotationBinding]` container declared on MappingTier.
// Tests pin the load-bearing nil-vs-empty distinction on the Annotations
// pointer and the inner-key ordering on Infra/Values OrderedMaps.

func TestManifest_AnnotationBinding_SingleBindingRoundTrip(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      type:
        core/rewrite-host: rewrite-host-helm:1.0.0
      annotations:
        feature.myco/rewrite-host:
          feature: core/rewrite-host:1.0.0
          infra:
            zulu_field:
              type: string
            alpha_field:
              type: boolean
              default: true
          values:
            mike_field:
              type: integer
            bravo_field:
              type: string
`)

	var m Manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if m.Spec.Mappings.Features == nil {
		t.Fatalf("Features tier nil; expected populated")
	}
	if m.Spec.Mappings.Features.Annotations == nil {
		t.Fatalf("Features.Annotations nil; expected populated")
	}
	binding, ok := m.Spec.Mappings.Features.Annotations.Get("feature.myco/rewrite-host")
	if !ok {
		t.Fatalf("binding feature.myco/rewrite-host missing")
	}
	if binding.Feature != "core/rewrite-host:1.0.0" {
		t.Errorf("Feature = %q, want core/rewrite-host:1.0.0", binding.Feature)
	}

	gotInfra := binding.Infra.Keys()
	wantInfra := []string{"zulu_field", "alpha_field"}
	if !slices.Equal(gotInfra, wantInfra) {
		t.Errorf("Infra keys = %v, want %v (author order)", gotInfra, wantInfra)
	}

	gotValues := binding.Values.Keys()
	wantValues := []string{"mike_field", "bravo_field"}
	if !slices.Equal(gotValues, wantValues) {
		t.Errorf("Values keys = %v, want %v (author order)", gotValues, wantValues)
	}
}

func TestManifest_AnnotationBinding_MultipleBindingsAuthorOrder(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        zulu.example/three:
          feature: core/three:1.0.0
        alpha.example/one:
          feature: core/one:1.0.0
        mike.example/two:
          feature: core/two:1.0.0
`)

	var m Manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if m.Spec.Mappings.Features == nil || m.Spec.Mappings.Features.Annotations == nil {
		t.Fatalf("Features.Annotations nil; expected populated")
	}
	got := m.Spec.Mappings.Features.Annotations.Keys()
	want := []string{"zulu.example/three", "alpha.example/one", "mike.example/two"}
	if !slices.Equal(got, want) {
		t.Errorf("Annotations keys = %v, want %v (author order)", got, want)
	}
}

func TestManifest_AnnotationBinding_AbsentSectionYieldsNilPointer(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      type:
        core/server: server-helm:1.0.0
`)

	var m Manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Spec.Mappings.Features == nil {
		t.Fatalf("Features tier nil; expected populated by `type:`")
	}
	if m.Spec.Mappings.Features.Annotations != nil {
		t.Errorf("Features.Annotations = %v, want nil (absent section preserves nil pointer)", m.Spec.Mappings.Features.Annotations)
	}
}

func TestManifest_AnnotationBinding_EmptyMappingYieldsNonNilEmpty(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations: {}
`)

	var m Manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Spec.Mappings.Features == nil {
		t.Fatalf("Features tier nil; expected populated")
	}
	if m.Spec.Mappings.Features.Annotations == nil {
		t.Fatalf("Features.Annotations nil; expected non-nil empty OrderedMap from explicit `annotations: {}`")
	}
	if got := m.Spec.Mappings.Features.Annotations.Len(); got != 0 {
		t.Errorf("Features.Annotations.Len() = %d, want 0", got)
	}
}

func TestManifest_AnnotationBinding_LegacyTierStillParses(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    helm:
      features:
        type:
          core/rewrite-host: rewrite-host-helm:1.0.0
        annotations:
          feature.myco/rewrite-host:
            feature: core/rewrite-host:1.0.0
            infra:
              min_version:
                type: string
`)

	var m Manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	pm, ok := m.Spec.Mappings.Legacy.Get("helm")
	if !ok {
		t.Fatalf("legacy provider tier `helm` missing")
	}
	if pm.Features == nil || pm.Features.Annotations == nil {
		t.Fatalf("legacy helm.features.annotations nil; expected populated")
	}
	binding, ok := pm.Features.Annotations.Get("feature.myco/rewrite-host")
	if !ok {
		t.Fatalf("legacy binding feature.myco/rewrite-host missing")
	}
	if binding.Feature != "core/rewrite-host:1.0.0" {
		t.Errorf("legacy binding.Feature = %q, want core/rewrite-host:1.0.0", binding.Feature)
	}
	if got := binding.Infra.Keys(); !slices.Equal(got, []string{"min_version"}) {
		t.Errorf("legacy binding.Infra keys = %v, want [min_version]", got)
	}
}
