// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// AnnotationBinding load tests: verify the Manifest loader parses
// `mappings.features.annotations` from the flat Manifest shape into the
// `*OrderedMap[AnnotationBinding]` container declared on MappingTier.
// Tests pin the load-bearing nil-vs-empty distinction on the Annotations
// pointer and the pillar-grouped dotted-path routing on the Maps struct.

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
          maps:
            infra:
              - zulu_field
              - alpha_field
            values:
              - mike_field
              - bravo_field
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

	wantInfra := []string{"zulu_field", "alpha_field"}
	if !slices.Equal(binding.Maps.Infra, wantInfra) {
		t.Errorf("Maps.Infra = %v, want %v (author order)", binding.Maps.Infra, wantInfra)
	}

	wantValues := []string{"mike_field", "bravo_field"}
	if !slices.Equal(binding.Maps.Values, wantValues) {
		t.Errorf("Maps.Values = %v, want %v (author order)", binding.Maps.Values, wantValues)
	}
}

func TestManifest_AnnotationBinding_NestedPathRoundTrips(t *testing.T) {
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
        myco/db-pool: db-pool-helm:1.0.0
      annotations:
        feature.myco/db-pool:
          feature: myco/db-pool:1.0.0
          maps:
            values:
              - db_name
              - ingress.enabled
              - ingress.host
            infra:
              - pool_size
              - max_connections
`)

	var m Manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	binding, ok := m.Spec.Mappings.Features.Annotations.Get("feature.myco/db-pool")
	if !ok {
		t.Fatalf("binding feature.myco/db-pool missing")
	}
	wantValues := []string{"db_name", "ingress.enabled", "ingress.host"}
	if !slices.Equal(binding.Maps.Values, wantValues) {
		t.Errorf("Maps.Values = %v, want %v (dotted paths round-trip unchanged)", binding.Maps.Values, wantValues)
	}
	wantInfra := []string{"pool_size", "max_connections"}
	if !slices.Equal(binding.Maps.Infra, wantInfra) {
		t.Errorf("Maps.Infra = %v, want %v", binding.Maps.Infra, wantInfra)
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

// TestManifestMappings_UnknownKeyIsRefused pins the codec half of the mapping
// key set: `components` and `features` are the whole surface, and any other key
// under `mappings:` is refused by name rather than dropped.
//
// The refusal matters at this layer specifically. A document read through the
// resolver is schema-validated first, so the schema is what an author meets; a
// caller decoding these bytes straight into the type passes no schema gate at
// all, and yaml.v3's default for an unrecognised key is to drop it silently. A
// dropped mapping is one the author wrote and the build will not honour, so the
// key set is enforced where the decode happens too.
//
// The message is asserted to name both the offending key and what to write
// instead, because a refusal that only says "no" leaves the author to guess.
func TestManifestMappings_UnknownKeyIsRefused(t *testing.T) {
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
`)

	var m Manifest
	err := yaml.Unmarshal(src, &m)
	if err == nil {
		t.Fatalf("Unmarshal accepted an unknown mappings key; want a refusal")
	}
	got := err.Error()
	for _, want := range []string{`"helm"`, "components", "features"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal does not name %s; got %q", want, got)
		}
	}
}

// TestManifestMappings_KnownKeysStillDecode is the other half of the pair
// above: closing the key set must not close the two keys that are the surface.
func TestManifestMappings_KnownKeysStillDecode(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/api-server: api-server-helm:1.0.0
    features:
      type:
        core/rewrite-host: rewrite-host-helm:1.0.0
`)

	var m Manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Spec.Mappings.Components == nil {
		t.Fatalf("Components nil; expected populated")
	}
	if got, ok := m.Spec.Mappings.Components.Type.Get("core/api-server"); !ok || got != "api-server-helm:1.0.0" {
		t.Errorf("components.type[core/api-server] = %q, %v; want api-server-helm:1.0.0, true", got, ok)
	}
	if m.Spec.Mappings.Features == nil {
		t.Fatalf("Features nil; expected populated")
	}
	if got, ok := m.Spec.Mappings.Features.Type.Get("core/rewrite-host"); !ok || got != "rewrite-host-helm:1.0.0" {
		t.Errorf("features.type[core/rewrite-host] = %q, %v; want rewrite-host-helm:1.0.0, true", got, ok)
	}
}
