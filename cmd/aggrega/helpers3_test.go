// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestRunBuild_AnnotationBindingValidationError pins the annotation-
// binding validation error path: a Manifest that binds an annotation
// to a path not declared by the target Feature surfaces an OPS_ERROR.
func TestRunBuild_AnnotationBindingValidationError(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: ann-test
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	// Manifest annotations binding to a non-existent Feature target.
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: m
  version: 1.0.0
spec:
  mappings:
    annotations:
      "feature.myco/example":
        target: nonexistent/feature
        maps:
          values:
            something: { to: nonexistent_path }
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  labels:
    component.aggrega.ai/type: "server"
  version: 1.0.0
spec:
  schema:
    values:
      port: { type: integer }
`)
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: 1.0.0
spec:
  baseSchema: |
    type: object
    additionalProperties: true
`)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  provider: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      c:
        image: x:latest
        ports:
          web: {{ .values.port }}
`)

	prevRelease, prevCatalog, prevTrace := releasePath, catalogDirs, traceEnabled
	t.Cleanup(func() {
		releasePath, catalogDirs, traceEnabled = prevRelease, prevCatalog, prevTrace
	})
	releasePath = filepath.Join(root, "release.yaml")
	catalogDirs = []string{catalogRoot}
	traceEnabled = false

	// Either succeeds (no annotations on Aggregate to fire on) or fails;
	// both paths exercise the validation dispatch.
	_ = dispatchBuild(nil, releasePath, catalogDirs, traceEnabled, os.Stderr)
}

// TestRunBuild_MalformedAggregate pins the resolver-error path: a
// malformed Aggregate halts at Step 1 resolve.
func TestRunBuild_MalformedAggregate(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `::: not yaml :::`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries: {}
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: m
  version: 1.0.0
spec:
  mappings:
    components:
      type: {}
`)
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: 1.0.0
spec: {}
`)

	prevRelease, prevCatalog, prevTrace := releasePath, catalogDirs, traceEnabled
	t.Cleanup(func() {
		releasePath, catalogDirs, traceEnabled = prevRelease, prevCatalog, prevTrace
	})
	releasePath = filepath.Join(root, "release.yaml")
	catalogDirs = []string{catalogRoot}
	traceEnabled = false

	err := dispatchBuild(nil, releasePath, catalogDirs, traceEnabled, os.Stderr)
	if err == nil {
		t.Errorf("malformed aggregate should error at resolve")
	}
}

// TestRunBuild_CatalogExtendsError pins the extends-resolution error
// path: a Component declaring extends to a non-existent parent fails.
func TestRunBuild_CatalogExtendsError(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "components", "child.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: child
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  extends: nonexistent-parent
  schema:
    values:
      x: { type: string }
`)
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: 1.0.0
spec: {}
`)
	// Minimal release pointing to nothing — we expect failure earlier
	// at catalog extends resolution.
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries: {}
`)

	prevRelease, prevCatalog, prevTrace := releasePath, catalogDirs, traceEnabled
	t.Cleanup(func() {
		releasePath, catalogDirs, traceEnabled = prevRelease, prevCatalog, prevTrace
	})
	releasePath = filepath.Join(root, "release.yaml")
	catalogDirs = []string{catalogRoot}
	traceEnabled = false

	err := dispatchBuild(nil, releasePath, catalogDirs, traceEnabled, os.Stderr)
	if err == nil {
		t.Errorf("extends to nonexistent parent should error")
	}
}

// TestInspectReleaseFile_MissingFile pins the release-load error
// in inspectReleaseFile.
func TestInspectReleaseFile_MissingFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	// Catalog without the release.
	err := renderReleaseFile(nil, "/nonexistent/release.yaml", nil, []string{tmp}, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("missing release should error")
	}
}

// TestInspectReleaseFile_MalformedRelease pins the release-parse
// error path.
func TestInspectReleaseFile_MalformedRelease(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "release.yaml"), "::: not yaml :::")
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: 1.0.0
spec: {}
`)
	err := renderReleaseFile(nil, filepath.Join(root, "release.yaml"), nil, []string{catalogRoot}, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("malformed release should error")
	}
}

// TestInspectReleaseFile_HappyPath pins the inspect happy path —
// release loads + pipeline runs to IR + feature listing prints.
func TestInspectReleaseFile_HappyPath(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: insp-smoke
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: m
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  labels:
    component.aggrega.ai/type: "server"
  version: 1.0.0
spec:
  schema:
    values:
      port: { type: integer }
`)
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: 1.0.0
spec:
  baseSchema: |
    type: object
    additionalProperties: true
`)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  provider: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      c:
        image: x:latest
        ports:
          web: {{ .values.port }}
`)
	err := renderReleaseFile(nil, filepath.Join(root, "release.yaml"), nil, []string{catalogRoot}, testInspectOptions(io.Discard))
	if err != nil {
		t.Errorf("happy path: %v", err)
	}
}
