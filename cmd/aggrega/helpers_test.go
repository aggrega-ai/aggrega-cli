// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteArtifacts_HappyPath pins the per-file write contract:
// each entry lands at outRoot/<relPath> with parent dirs created on
// demand.
func TestWriteArtifacts_HappyPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	files := map[string][]byte{
		"a/b/c.yaml":    []byte("c-content"),
		"top.txt":       []byte("top-content"),
		"e/nested.json": []byte("e-content"),
	}
	if err := writeArtifacts(tmp, files); err != nil {
		t.Fatalf("writeArtifacts: %v", err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(tmp, rel))
		if err != nil {
			t.Errorf("read %q: %v", rel, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%q = %q; want %q", rel, got, want)
		}
	}
}

// TestWriteArtifacts_RejectsEmptyPath pins the empty-path guard.
func TestWriteArtifacts_RejectsEmptyPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	err := writeArtifacts(tmp, map[string][]byte{"": []byte("x")})
	if err == nil {
		t.Errorf("empty path should error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty; got %v", err)
	}
}

// TestWriteArtifacts_RejectsTraversal pins the path-traversal guard.
func TestWriteArtifacts_RejectsTraversal(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	err := writeArtifacts(tmp, map[string][]byte{"../escape.yaml": []byte("x")})
	if err == nil {
		t.Errorf("traversal path should error")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error should mention invalid; got %v", err)
	}
	// Pure ".." should also fail.
	err = writeArtifacts(tmp, map[string][]byte{"..": []byte("x")})
	if err == nil {
		t.Errorf("pure .. path should error")
	}
}

// TestResolveSiblingYAML_AllBranches pins each documented branch:
// explicit-extension passthrough, sibling .yaml exists, sibling .yml
// exists, neither exists (fallback to .yaml).
func TestResolveSiblingYAML_AllBranches(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// Explicit extension passthrough.
	got := resolveSiblingYAML(tmp, "foo.yaml")
	if got != filepath.Join(tmp, "foo.yaml") {
		t.Errorf("explicit .yaml passthrough; got %q", got)
	}
	got = resolveSiblingYAML(tmp, "foo.yml")
	if got != filepath.Join(tmp, "foo.yml") {
		t.Errorf("explicit .yml passthrough; got %q", got)
	}

	// .yaml exists.
	if err := os.WriteFile(filepath.Join(tmp, "with-yaml.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got = resolveSiblingYAML(tmp, "with-yaml")
	if got != filepath.Join(tmp, "with-yaml.yaml") {
		t.Errorf(".yaml-exists; got %q", got)
	}

	// .yml exists (no .yaml).
	if err := os.WriteFile(filepath.Join(tmp, "with-yml.yml"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got = resolveSiblingYAML(tmp, "with-yml")
	if got != filepath.Join(tmp, "with-yml.yml") {
		t.Errorf(".yml-exists; got %q", got)
	}

	// Neither exists — fallback to .yaml.
	got = resolveSiblingYAML(tmp, "absent")
	if got != filepath.Join(tmp, "absent.yaml") {
		t.Errorf("fallback; got %q", got)
	}
}

// TestIsPathInput_AdditionalBranches expands coverage on the
// path-discriminator beyond what TestIsPathInput already pins.
func TestIsPathInput_AdditionalBranches(t *testing.T) {
	t.Parallel()
	// Coverage of forward-slash absolute path.
	if !isPathInput("/absolute/path") {
		t.Errorf("/absolute → should be path")
	}
	if !isPathInput("relative.yaml") {
		t.Errorf(".yaml suffix → should be path")
	}
	if !isPathInput("relative.yml") {
		t.Errorf(".yml suffix → should be path")
	}
	if isPathInput("plain-name") {
		t.Errorf("plain name → should NOT be path")
	}
}

// TestInspectPath_NonExistentFile pins the read-error path.
func TestInspectPath_NonExistentFile(t *testing.T) {
	t.Parallel()
	err := inspectPath(resolveTempWorkspace(t), "/nonexistent/file.yaml", nil, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("missing file should error")
	}
}

// TestInspectPath_MalformedYAMLPreamble pins the parse-error path.
func TestInspectPath_MalformedYAMLPreamble(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(path, []byte("::: not yaml :::"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := inspectPath(resolveTempWorkspace(t), path, nil, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("malformed YAML should error")
	}
}

// TestInspectCatalogComponent_MissingCatalog pins the catalog-load
// error path.
func TestInspectCatalogComponent_MissingCatalog(t *testing.T) {
	t.Parallel()
	err := inspectCatalogEntity(resolveTempWorkspace(t), "any-name", []string{"/nonexistent/dir"}, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("missing catalog should error")
	}
}

// TestInspectCatalogComponent_ComponentNotFound pins the not-found
// dispatch arm.
func TestInspectCatalogComponent_ComponentNotFound(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	// Empty catalog dir.
	err := inspectCatalogEntity(resolveTempWorkspace(t), "absent-component", []string{tmp}, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("missing component should error")
	}
	if !strings.Contains(err.Error(), "no catalog entity of any kind") {
		t.Errorf("error should state no entity matched across kinds; got %v", err)
	}
}

// TestInspectCatalogComponent_HappyPath pins the success path: a
// catalog with one Component → render its schema.
func TestInspectCatalogComponent_HappyPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	compYAML := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
  usesSchema:
    db:
      selector: 'component.aggrega.ai/type=stateful-server'
`
	if err := os.WriteFile(filepath.Join(tmp, "component.yaml"), []byte(compYAML), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := inspectCatalogEntity(resolveTempWorkspace(t), "core/server", []string{tmp}, testInspectOptions(io.Discard)); err != nil {
		t.Errorf("happy: %v", err)
	}
}

// TestInspectComponentFile_MissingNameUsesPath pins the path-fallback
// for unnamed Components.
func TestInspectComponentFile_MissingNameUsesPath(t *testing.T) {
	t.Parallel()
	src := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nspec:\n  schema:\n    values:\n      x: {type: string}\n")
	if err := renderComponentFile(nil, "/some/file.yaml", src, nil, testInspectOptions(io.Discard)); err != nil {
		t.Errorf("happy: %v", err)
	}
}

// TestInspectComponentFile_NoSchemaDeclared pins the no-schema branch.
func TestInspectComponentFile_NoSchemaDeclared(t *testing.T) {
	t.Parallel()
	src := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: x\nspec: {}\n")
	if err := renderComponentFile(nil, "/some/file.yaml", src, nil, testInspectOptions(io.Discard)); err != nil {
		t.Errorf("no-schema: %v", err)
	}
}

// TestInspectComponentFile_MalformedYAML pins the parse error.
func TestInspectComponentFile_MalformedYAML(t *testing.T) {
	t.Parallel()
	src := []byte("::: not yaml :::")
	if err := renderComponentFile(nil, "/some/file.yaml", src, nil, testInspectOptions(io.Discard)); err == nil {
		t.Errorf("malformed should error")
	}
}

// TestInspectComponentFile_WithExtends pins the extends-header branch.
func TestInspectComponentFile_WithExtends(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: child
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  extends: parent
  schema:
    values:
      x: { type: string }
`)
	if err := renderComponentFile(nil, "/some/file.yaml", src, nil, testInspectOptions(io.Discard)); err != nil {
		t.Errorf("extends: %v", err)
	}
}

// TestRunBuild_MissingCatalog pins the catalog-load error path.
func TestRunBuild_MissingCatalog(t *testing.T) {
	t.Parallel()
	err := dispatchBuild(resolveTempWorkspace(t), "/nonexistent/release.yaml", []string{"/nonexistent/catalog"}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Errorf("missing catalog should error")
	}
}

// TestRunBuild_MissingRelease pins the release-load error path.
func TestRunBuild_MissingRelease(t *testing.T) {
	t.Parallel()
	err := dispatchBuild(resolveTempWorkspace(t), "/nonexistent/release.yaml", []string{t.TempDir()}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Errorf("missing release should error")
	}
}

// TestRunInspect_OCI pins the OCI not-implemented branch.
func TestRunInspect_OCI(t *testing.T) {
	t.Parallel()
	err := dispatchInspect(resolveTempWorkspace(t), "oci://hub/lib@1#/schemas/X", nil, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("OCI URI should error")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error should mention not yet implemented; got %v", err)
	}
}

// TestRunInspect_CatalogComponentBranch pins the catalog-name dispatch
// path (non-path input + non-OCI input).
func TestRunInspect_CatalogComponentBranch(t *testing.T) {
	t.Parallel()
	err := dispatchInspect(resolveTempWorkspace(t), "absent-component", []string{t.TempDir()}, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("absent component should error")
	}
}

// TestRunInspect_PathBranch pins the path-dispatch branch.
func TestRunInspect_PathBranch(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "x.yaml")
	if err := os.WriteFile(path, []byte("apiVersion: x\nkind: Manifest\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := dispatchInspect(resolveTempWorkspace(t), path, []string{tmp}, testInspectOptions(io.Discard))
	// Manifest → not-yet-implemented; surfaces as error.
	if err == nil {
		t.Errorf("manifest dispatch should error")
	}
}
