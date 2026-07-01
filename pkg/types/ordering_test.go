// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Author-order preservation tests for each 8-entity kind.
//
// Each test loads a YAML fixture whose keys are deliberately NOT in alphabetical
// order, unmarshals into the Go entity type, and asserts that the OrderedMap
// field's Keys() iteration matches the source author order.

func TestAggregate_AuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  components:
    charlie:
      type: core/server:1.0.0
    alpha:
      type: core/server:1.0.0
    bravo:
      type: core/server:1.0.0
`)

	var agg Aggregate
	if err := yaml.Unmarshal(src, &agg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := []string{"charlie", "alpha", "bravo"}
	got := agg.Spec.Components.Keys()
	if !slices.Equal(got, want) {
		t.Errorf("Components keys = %v, want %v (author order lost)", got, want)
	}
}

func TestRelease_AuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: test
  environment: dev
  version: "1.0.0"
spec:
  boundaries:
    zulu:
      description: z
    alpha:
      description: a
    mike:
      description: m
`)

	var rel Release
	if err := yaml.Unmarshal(src, &rel); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := []string{"zulu", "alpha", "mike"}
	got := rel.Spec.Boundaries.Keys()
	if !slices.Equal(got, want) {
		t.Errorf("Boundaries keys = %v, want %v (author order lost)", got, want)
	}
}

func TestComponent_AuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
    zebra: z
    alpha: a
    mid: m
spec:
  usesSchema:
    zsocket:
      selector: component.aggrega.ai/type=x
    asocket:
      selector: component.aggrega.ai/type=y
    msocket:
      selector: component.aggrega.ai/type=z
`)

	var comp Component
	if err := yaml.Unmarshal(src, &comp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantLabels := []string{"component.aggrega.ai/type", "zebra", "alpha", "mid"}
	gotLabels := comp.Metadata.Labels.Keys()
	if !slices.Equal(gotLabels, wantLabels) {
		t.Errorf("Labels keys = %v, want %v", gotLabels, wantLabels)
	}

	wantSockets := []string{"zsocket", "asocket", "msocket"}
	gotSockets := comp.Spec.UsesSchema.Keys()
	if !slices.Equal(gotSockets, wantSockets) {
		t.Errorf("UsesSchema keys = %v, want %v", gotSockets, wantSockets)
	}
}

func TestFeature_AuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/monitoring
  version: "1.0.0"
spec:
  appliesTo: "zone=prod, access=internal"
  appliesToAnyOf:
    - zone: prod
      access: internal
      mesh: enabled
`)

	var feat Feature
	if err := yaml.Unmarshal(src, &feat); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if feat.Spec.AppliesTo != "zone=prod, access=internal" {
		t.Errorf("AppliesTo = %q, want the authored selector string", feat.Spec.AppliesTo)
	}
	if len(feat.Spec.AppliesToAnyOf) != 1 || !feat.Spec.AppliesToAnyOf[0].IsMap() {
		t.Fatalf("AppliesToAnyOf = %+v, want one flat-map item", feat.Spec.AppliesToAnyOf)
	}
	want := []string{"zone", "access", "mesh"}
	got := feat.Spec.AppliesToAnyOf[0].Labels.Keys()
	if !slices.Equal(got, want) {
		t.Errorf("AppliesToAnyOf[0] keys = %v, want %v", got, want)
	}
}

func TestRecipe_AuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: helm-server
  version: "1.0.0"
spec:
  provider: helm
  implements: core/server:1.0.0
  locals:
    zhostname: z
    ahost: a
    mname: m
  provides:
    zout: z-template
    aout: a-template
    mout: m-template
`)

	var r Recipe
	if err := yaml.Unmarshal(src, &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantLocals := []string{"zhostname", "ahost", "mname"}
	gotLocals := r.Spec.Locals.Keys()
	if !slices.Equal(gotLocals, wantLocals) {
		t.Errorf("Locals keys = %v, want %v", gotLocals, wantLocals)
	}

	wantProv := []string{"zout", "aout", "mout"}
	gotProv := r.Spec.Provides.Keys()
	if !slices.Equal(gotProv, wantProv) {
		t.Errorf("Provides keys = %v, want %v", gotProv, wantProv)
	}
}

func TestFeatureRecipe_AuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: mon-prometheus
  version: "1.0.0"
spec:
  provider: helm-chart
  implements: core/monitoring
  locals:
    zlocal: z
    alocal: a
  provides:
    zprovide: z-template
    aprovide: a-template
`)

	var fr FeatureRecipe
	if err := yaml.Unmarshal(src, &fr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantLocals := []string{"zlocal", "alocal"}
	gotLocals := fr.Spec.Locals.Keys()
	if !slices.Equal(gotLocals, wantLocals) {
		t.Errorf("Locals keys = %v, want %v", gotLocals, wantLocals)
	}

	wantProv := []string{"zprovide", "aprovide"}
	gotProv := fr.Spec.Provides.Keys()
	if !slices.Equal(gotProv, wantProv) {
		t.Errorf("Provides keys = %v, want %v", gotProv, wantProv)
	}
}

func TestManifest_AuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
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

	var m Manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := []string{"zulu-provider", "alpha-provider", "mike-provider"}
	got := m.Spec.Mappings.Legacy.Keys()
	if !slices.Equal(got, want) {
		t.Errorf("Mappings legacy keys = %v, want %v", got, want)
	}
}

func TestProvider_AuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	// Provider currently has no OrderedMap fields.
	// Placeholder round-trip: verify Provider unmarshals without error.
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: plugins/helm-engine.wasm
  workloadSchema: |
    values: { port: int }
`)

	var p Provider
	if err := yaml.Unmarshal(src, &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Metadata.Name != "helm" {
		t.Errorf("Metadata.Name = %q, want helm", p.Metadata.Name)
	}
}

func TestWorkload_RoundTripPlaceholder(t *testing.T) {
	t.Parallel()
	// Workload struct body lands in synthesis-foundations. This test verifies
	// the slot declarations unmarshal in author order once fields are populated.
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Workload
metadata:
  name: test
  version: "1.0.0"
containers:
  zcontainer:
    image: z:latest
  acontainer:
    image: a:latest
  mcontainer:
    image: m:latest
`)

	var w Workload
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := []string{"zcontainer", "acontainer", "mcontainer"}
	got := w.Containers.Keys()
	if !slices.Equal(got, want) {
		t.Errorf("Containers keys = %v, want %v", got, want)
	}
}
