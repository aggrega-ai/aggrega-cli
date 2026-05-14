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
	if _, ok := agg.Spec.Components.Get("web"); !ok {
		t.Error("expected component 'web'")
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
        feature.myco/enterprise-binding:
          feature: core/bar:1.0.0
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err != nil {
		t.Fatalf("namespaced prefixes should be accepted; got error: %v", err)
	}
}

