// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
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
	if _, ok := agg.Spec.Components["web"]; !ok {
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
	if _, ok := rel.Spec.Boundaries["aws-prod"]; !ok {
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
	if _, ok := man.Spec.Mappings["helm"]; !ok {
		t.Error("expected mappings for 'helm' provider")
	}
}
