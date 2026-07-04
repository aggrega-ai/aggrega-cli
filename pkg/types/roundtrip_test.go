// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Round-trip byte-compare tests per entity type.
//
// Each test loads a YAML fixture with deliberately non-alphabetical author
// ordering, marshals once, re-unmarshals the marshalled output, and re-
// marshals — then asserts the two marshalled outputs are byte-identical
// (idempotence) AND that author-order is preserved through the round-trip.
//
// Idempotence is the right shape, not byte-equality with hand-written
// fixtures. yaml.v3's default emit indents differ from hand-fixture
// indenting, and pinning a specific indent pattern would break under
// yaml.v3 version bumps. Idempotence is what determinism actually
// requires.

// assertEntityRoundTrip is the per-entity helper. T is the entity Go type;
// caller supplies the source YAML, the function returning the keys of the
// order-sensitive field, and the expected author-order keys.
//
// Generic T over `any` because Go generics don't allow a method-set
// constraint here cheaply; the unmarshalling target is created via the
// type parameter directly.
func assertEntityRoundTrip[T any](t *testing.T, src []byte, keysAfterRoundTrip func(*T) []string, wantKeys []string) {
	t.Helper()

	var first T
	if err := yaml.Unmarshal(src, &first); err != nil {
		t.Fatalf("first Unmarshal: %v", err)
	}
	out1, err := yaml.Marshal(&first)
	if err != nil {
		t.Fatalf("first Marshal: %v", err)
	}

	var second T
	if err := yaml.Unmarshal(out1, &second); err != nil {
		t.Fatalf("re-Unmarshal: %v", err)
	}
	out2, err := yaml.Marshal(&second)
	if err != nil {
		t.Fatalf("re-Marshal: %v", err)
	}

	if !bytes.Equal(out1, out2) {
		t.Errorf("round-trip not idempotent:\nfirst:\n%s\nsecond:\n%s", out1, out2)
	}

	if got := keysAfterRoundTrip(&second); !slices.Equal(got, wantKeys) {
		t.Errorf("author order lost through round-trip: keys = %v, want %v", got, wantKeys)
	}
}

func TestAggregate_RoundTripIdempotent(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  components:
    zeta:
      type: core/server:1.0.0
    alpha:
      type: core/server:1.0.0
    middle:
      type: core/server:1.0.0
`)
	assertEntityRoundTrip[Aggregate](
		t, src,
		func(a *Aggregate) []string { return a.Spec.Components.Keys() },
		[]string{"zeta", "alpha", "middle"},
	)
}

func TestRelease_RoundTripIdempotent(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  environment: prod
  version: "1.0.0"
spec:
  aggregate: test-aggregate
  manifest: test-manifest
  boundaries:
    zone-z:
      directory: out/z
    zone-a:
      directory: out/a
    zone-m:
      directory: out/m
`)
	assertEntityRoundTrip[Release](
		t, src,
		func(r *Release) []string { return r.Spec.Boundaries.Keys() },
		[]string{"zone-z", "zone-a", "zone-m"},
	)
}

func TestComponent_RoundTripIdempotent(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    z-socket:
      selector: component.aggrega.ai/type=server
    a-socket:
      selector: component.aggrega.ai/type=stateful-server
    m-socket:
      selector: component.aggrega.ai/type=resource
  schema:
    values: {}
`)
	assertEntityRoundTrip[Component](
		t, src,
		func(c *Component) []string { return c.Spec.UsesSchema.Keys() },
		[]string{"z-socket", "a-socket", "m-socket"},
	)
}

func TestFeature_RoundTripIdempotent(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/test-feature
  version: "1.0.0"
spec:
  appliesTo: "tier in (critical, production)"
  appliesToAnyOf:
    - z-key: z-value
      a-key: a-value
      m-key: m-value
  schema:
    values: {}
`)
	assertEntityRoundTrip[Feature](
		t, src,
		func(f *Feature) []string { return f.Spec.AppliesToAnyOf[0].Labels.Keys() },
		[]string{"z-key", "a-key", "m-key"},
	)
}

func TestRecipe_RoundTripIdempotent(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: helm/test-recipe
  version: "1.0.0"
spec:
  provider: helm
  implements: core/server:1.0.0
  locals:
    z-local: z-value
    a-local: a-value
    m-local: m-value
`)
	assertEntityRoundTrip[Recipe](
		t, src,
		func(r *Recipe) []string { return r.Spec.Locals.Keys() },
		[]string{"z-local", "a-local", "m-local"},
	)
}

func TestFeatureRecipe_RoundTripIdempotent(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: helm/test-fr
  version: "1.0.0"
spec:
  provider: helm
  implements: core/test-feature:1.0.0
  provides:
    z-output: "{{ .values.z }}"
    a-output: "{{ .values.a }}"
    m-output: "{{ .values.m }}"
`)
	assertEntityRoundTrip[FeatureRecipe](
		t, src,
		func(fr *FeatureRecipe) []string { return fr.Spec.Provides.Keys() },
		[]string{"z-output", "a-output", "m-output"},
	)
}

func TestManifest_RoundTripIdempotent(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    zulu-provider:
      components:
        type:
          core/server: recipes/server-z:1.0.0
    alpha-provider:
      components:
        type:
          core/server: recipes/server-a:1.0.0
    mike-provider:
      components:
        type:
          core/server: recipes/server-m:1.0.0
`)
	assertEntityRoundTrip[Manifest](
		t, src,
		func(m *Manifest) []string { return m.Spec.Mappings.Legacy.Keys() },
		[]string{"zulu-provider", "alpha-provider", "mike-provider"},
	)
}

// TestManifest_NestedOrderedMapRoundTrip exercises OrderedMap[OrderedMap[V]]
// through the Manifest's two-tier mapping structure
// (Mappings.<provider>.Components.Type). Author order must be preserved at
// both the outer (provider) and inner (component-type) levels.
func TestManifest_NestedOrderedMapRoundTrip(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: nested-test
  version: "1.0.0"
spec:
  mappings:
    helm:
      components:
        type:
          core/zoo: zoo-recipe:1.0.0
          core/alpha: alpha-recipe:1.0.0
          core/middle: middle-recipe:1.0.0
    compose:
      components:
        type:
          core/zoo: compose-zoo-recipe:1.0.0
          core/alpha: compose-alpha-recipe:1.0.0
`)

	var first Manifest
	if err := yaml.Unmarshal(src, &first); err != nil {
		t.Fatalf("first Unmarshal: %v", err)
	}

	// Outer level — provider order in spec.mappings.Legacy.
	wantOuter := []string{"helm", "compose"}
	if got := first.Spec.Mappings.Legacy.Keys(); !slices.Equal(got, wantOuter) {
		t.Errorf("outer Mappings legacy keys = %v, want %v", got, wantOuter)
	}

	// Inner level — component-type order under helm.
	helmMappings, ok := first.Spec.Mappings.Legacy.Get("helm")
	if !ok {
		t.Fatal("helm mappings missing")
	}
	if helmMappings.Components == nil {
		t.Fatal("helm.components nil")
	}
	wantInner := []string{"core/zoo", "core/alpha", "core/middle"}
	if got := helmMappings.Components.Type.Keys(); !slices.Equal(got, wantInner) {
		t.Errorf("inner helm components.type keys = %v, want %v", got, wantInner)
	}

	// Round-trip idempotence preserves both levels.
	out1, err := yaml.Marshal(&first)
	if err != nil {
		t.Fatalf("first Marshal: %v", err)
	}
	var second Manifest
	if err := yaml.Unmarshal(out1, &second); err != nil {
		t.Fatalf("re-Unmarshal: %v", err)
	}
	out2, err := yaml.Marshal(&second)
	if err != nil {
		t.Fatalf("re-Marshal: %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("nested round-trip not idempotent:\nfirst:\n%s\nsecond:\n%s", out1, out2)
	}

	// Inner author order survives.
	helmMappings2, _ := second.Spec.Mappings.Legacy.Get("helm")
	if got := helmMappings2.Components.Type.Keys(); !slices.Equal(got, wantInner) {
		t.Errorf("after round-trip, inner helm components.type keys = %v, want %v", got, wantInner)
	}
}
