// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAggregate_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "aggregate.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test-app
  version: "1.0.0"
spec:
  components:
    web:
      type: core/server:1.0.0
`
	os.WriteFile(f, []byte(yaml), 0644)

	agg, err := LoadAggregate(f)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Metadata.Name != "test-app" {
		t.Errorf("expected name test-app, got %s", agg.Metadata.Name)
	}
	if agg.Metadata.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", agg.Metadata.Version)
	}
	// Load normalizes: a top-level components surface folds into the
	// synthesized `default` subsystem (root cleared).
	if agg.Spec.Components.Len() != 0 {
		t.Errorf("top-level components must fold into the default subsystem; got %d", agg.Spec.Components.Len())
	}
	def, ok := agg.Spec.Subsystems.Get("default")
	if !ok {
		t.Fatal("expected a synthesized 'default' subsystem holding the top-level components")
	}
	if !def.Synthesized {
		t.Error("the synthesized default subsystem must be marked Synthesized")
	}
	if _, ok := def.Components.Get("web"); !ok {
		t.Error("expected component 'web' in the synthesized default subsystem")
	}
}

func TestLoadAggregate_FileNotFound(t *testing.T) {
	_, err := LoadAggregate("/nonexistent/aggregate.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, ErrYAMLLoad) {
		t.Errorf("expected ErrYAMLLoad, got %v", err)
	}
}

func TestLoadAggregate_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bad.yaml")
	// YAML v3 is lenient with many inputs; use a truly malformed document
	os.WriteFile(f, []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n\t- mixed tabs and spaces"), 0644)

	_, err := LoadAggregate(f)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !errors.Is(err, ErrYAMLLoad) {
		t.Errorf("expected ErrYAMLLoad, got %v", err)
	}
}

func TestLoadRelease_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "release.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    aws-prod:
      directory: ./output
      components:
        web:
          infra:
            replicas: 3
`
	os.WriteFile(f, []byte(yaml), 0644)

	rel, err := LoadRelease(f)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Metadata.Name != "prod" {
		t.Errorf("expected name prod, got %s", rel.Metadata.Name)
	}
	if rel.Metadata.Environment != "production" {
		t.Errorf("expected environment production, got %s", rel.Metadata.Environment)
	}
	if _, ok := rel.Spec.Boundaries.Get("aws-prod"); !ok {
		t.Error("expected boundary 'aws-prod'")
	}
}

func TestLoadManifest_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    helm:
      components:
        type:
          "core/server": "recipes/helm-server:1.0.0"
`
	os.WriteFile(f, []byte(yaml), 0644)

	man, err := LoadManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	if man.Metadata.Name != "test-manifest" {
		t.Errorf("expected name test-manifest, got %s", man.Metadata.Name)
	}
	if _, ok := man.Spec.Mappings.Legacy.Get("helm"); !ok {
		t.Error("expected legacy mappings for 'helm' provider")
	}
}

func TestLoadManifest_RejectsReservedPrefix_FlatShape(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        aggrega.ai/forbidden-binding:
          feature: core/forbidden:1.0.0
          maps:
            infra:
              - target
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR on reserved-prefix binding; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("error missing CATALOG_ERROR prefix: %s", msg)
	}
	if !strings.Contains(msg, "aggrega.ai/forbidden-binding") {
		t.Errorf("error message should name the offending key; got: %s", msg)
	}
	if !strings.Contains(msg, "reserved prefix") {
		t.Errorf("error message should explain the reservation; got: %s", msg)
	}
	if !errors.Is(err, ErrManifestReservedPrefix) {
		t.Errorf("error should wrap ErrManifestReservedPrefix (not ErrYAMLLoad); got chain: %v", err)
	}
	if errors.Is(err, ErrYAMLLoad) {
		t.Errorf("error should NOT wrap ErrYAMLLoad — the YAML parsed successfully; the failure is a semantic reservation check")
	}
}

func TestLoadManifest_RejectsReservedPrefix_LegacyShape(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    helm:
      features:
        annotations:
          aggrega.ai/legacy-shape-also-rejected:
            feature: core/foo:1.0.0
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR on reserved-prefix binding under legacy tier; got nil")
	}
	if !strings.Contains(err.Error(), "CATALOG_ERROR") {
		t.Errorf("legacy-shape rejection should also use CATALOG_ERROR prefix; got: %s", err)
	}
}

func TestLoadManifest_AcceptsNamespacedPrefix(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.aggrega.ai/standard-library-binding:
          feature: core/foo:1.0.0
          maps:
            infra:
              - field_a
        feature.myco/enterprise-binding:
          feature: core/bar:1.0.0
          maps:
            values:
              - field_b
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err != nil {
		t.Fatalf("namespaced prefixes should be accepted; got error: %v", err)
	}
}

func TestLoadManifest_RejectsCrossBindingPillarCollision(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/first-binding:
          feature: core/shared:1.0.0
          maps:
            infra:
              - pool_size
        feature.myco/second-binding:
          feature: core/shared:1.0.0
          maps:
            infra:
              - pool_size
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR on cross-binding (path, pillar) collision; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("error missing CATALOG_ERROR prefix: %s", msg)
	}
	if !strings.Contains(msg, "first-binding") || !strings.Contains(msg, "second-binding") {
		t.Errorf("error should name both colliding bindings; got: %s", msg)
	}
	if !strings.Contains(msg, "core/shared:1.0.0") {
		t.Errorf("error should name the target Feature; got: %s", msg)
	}
	if !strings.Contains(msg, "pool_size") {
		t.Errorf("error should name the colliding path; got: %s", msg)
	}
	if !errors.Is(err, ErrManifestCrossBindingPillarCollision) {
		t.Errorf("error should wrap ErrManifestCrossBindingPillarCollision; got chain: %v", err)
	}
}

func TestLoadManifest_RejectsCrossBindingCollision_NestedPath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/first:
          feature: core/shared:1.0.0
          maps:
            values:
              - ingress.host
        feature.myco/second:
          feature: core/shared:1.0.0
          maps:
            values:
              - ingress.host
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR on nested-path collision; got nil")
	}
	if !strings.Contains(err.Error(), "ingress.host") {
		t.Errorf("error should name the colliding nested path; got: %v", err)
	}
}

func TestLoadManifest_AcceptsCrossBindingDifferentSubkeyOrPillar(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	// Two bindings targeting the same Feature with different (path, pillar)
	// pairs are legitimate — distinct routing slots compose without collision.
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/dev-binding:
          feature: core/shared:1.0.0
          maps:
            values:
              - db_name
        feature.myco/ops-binding:
          feature: core/shared:1.0.0
          maps:
            infra:
              - pool_size
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err != nil {
		t.Fatalf("distinct (path, pillar) pairs on the same Feature should be accepted; got error: %v", err)
	}
}
