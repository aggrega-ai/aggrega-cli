// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// copyFixtureFile copies the named fixture from testdata/resolve/<shape>
// into dir, preserving the basename, and returns the destination path
// and content. Tests use this to populate a workspace tempdir with
// known-good YAML before driving validate + resolve through the daemon.
func copyFixtureFile(t *testing.T, shape, name, dir string) (string, string) {
	t.Helper()
	src := filepath.Join("testdata", "resolve", shape, name)
	content, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", src, err)
	}
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", dst, err)
	}
	return dst, string(content)
}

// validateFile drives the daemon's aggrega/validate handler for the
// supplied file + content. Used by resolve tests to seed the parse
// cache before issuing a resolve / findReferences / findImplementations.
func validateFile(t *testing.T, srv *Server, file, content string) {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{File: file, Content: content}))
	runOneRequest(t, srv, &in, &out)
}

// drainStderr discards anything the daemon writes to stderr during a
// workspace walk (parse errors for unrelated fixture files, the
// surrounding testdata harness, etc.).
func drainStderr() io.Writer {
	return io.Discard
}

func TestResolveExtendsValueFindsParentComponent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	parentPath, _ := copyFixtureFile(t, "extends", "parent.yaml", dir)
	childPath, childContent := copyFixtureFile(t, "extends", "child.yaml", dir)

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, childPath, childContent)

	// Cursor on "core/web-server" — line 6 (spec.extends value), char 14.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      childPath,
		Position: yamlpos.Position{Line: 6, Character: 14},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil {
		t.Fatal("resolve returned nil Location; want pointer at parent.yaml metadata.name")
	}
	if result.Location.URI != parentPath {
		t.Errorf("Location.URI = %q; want %q", result.Location.URI, parentPath)
	}
	if result.Location.Range.Start.Line != 3 {
		t.Errorf("Location.Range.Start.Line = %d; want 3 (parent's metadata.name line)", result.Location.Range.Start.Line)
	}
}

func TestResolveComponentsTypeFindsTargetComponent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	targetPath, _ := copyFixtureFile(t, "components-type", "target-component.yaml", dir)
	aggPath, aggContent := copyFixtureFile(t, "components-type", "referring-aggregate.yaml", dir)

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "core/postgres" — line 8 (spec.components.db.type value), char 14.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 8, Character: 14},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.URI != targetPath {
		t.Fatalf("Location = %+v; want pointer at %s", result.Location, targetPath)
	}
}

func TestResolveFeaturesTypeFindsTargetFeature(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	targetPath, _ := copyFixtureFile(t, "features-type", "target-feature.yaml", dir)
	aggPath, aggContent := copyFixtureFile(t, "features-type", "referring-aggregate.yaml", dir)

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "feature/autoscaler" — line 11 (Component-nested feature
	// type value), char 18.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 11, Character: 18},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.URI != targetPath {
		t.Fatalf("Location = %+v; want pointer at %s", result.Location, targetPath)
	}
}

func TestResolveRecipeImplementsFindsTargetComponent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	targetPath, _ := copyFixtureFile(t, "implements", "target-component.yaml", dir)
	recipePath, recipeContent := copyFixtureFile(t, "implements", "recipe.yaml", dir)

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, recipePath, recipeContent)

	// Cursor on "core/cache" — line 6 (spec.implements value), char 16.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      recipePath,
		Position: yamlpos.Position{Line: 6, Character: 16},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.URI != targetPath {
		t.Fatalf("Location = %+v; want pointer at %s", result.Location, targetPath)
	}
}

func TestResolveMetadataNameReturnsSelfLocation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, content := copyFixtureFile(t, "extends", "parent.yaml", dir)

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, path, content)

	// Cursor on the entity's own metadata.name — line 3, char 10.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      path,
		Position: yamlpos.Position{Line: 3, Character: 10},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.URI != path {
		t.Fatalf("self-resolve Location = %+v; want pointer at same file %s", result.Location, path)
	}
	if result.Location.Range.Start.Line != 3 {
		t.Errorf("Location.Range.Start.Line = %d; want 3", result.Location.Range.Start.Line)
	}
}

func TestFindReferencesAggregatesAllReferenceShapes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	targetPath, targetContent := copyFixtureFile(t, "components-type", "target-component.yaml", dir)
	// Three referencing files in the same workspace; all reference
	// core/postgres in one of the three URI shapes.
	aggExtends := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: postgres-replica\n  version: 1.0.0\nspec:\n  extends: core/postgres\n"
	aggComponentsType := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: app-a\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/postgres\n"
	aggRecipeImplements := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: postgres-recipe\n  version: 1.0.0\nspec:\n  implements: core/postgres\n  provider: helm\n"
	for name, content := range map[string]string{
		"extends.yaml":           aggExtends,
		"components-type.yaml":   aggComponentsType,
		"recipe-implements.yaml": aggRecipeImplements,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, targetPath, targetContent)

	// Cursor on metadata.name of target-component.yaml — line 3, char 10.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/findReferences", findReferencesParams{
		URI:      targetPath,
		Position: yamlpos.Position{Line: 3, Character: 10},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("findReferences returned error: %+v", resp.Error)
	}
	var result findReferencesResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Locations) != 3 {
		t.Fatalf("expected 3 references across the three shapes; got %d: %+v", len(result.Locations), result.Locations)
	}
	// Verify the three URIs are distinct fixture files.
	seen := map[string]bool{}
	for _, loc := range result.Locations {
		seen[filepath.Base(loc.URI)] = true
	}
	for _, want := range []string{"extends.yaml", "components-type.yaml", "recipe-implements.yaml"} {
		if !seen[want] {
			t.Errorf("expected reference at %s; got Locations = %+v", want, result.Locations)
		}
	}
}

func TestFindImplementationsReturnsRecipesPlusChildren(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	targetPath, targetContent := copyFixtureFile(t, "implements", "target-component.yaml", dir)
	// One Recipe implementing the target + one Component extending it.
	recipe := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: my-recipe\n  version: 1.0.0\nspec:\n  implements: core/cache\n  provider: helm\n"
	child := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: cache-with-metrics\n  version: 1.0.0\nspec:\n  extends: core/cache\n"
	if err := os.WriteFile(filepath.Join(dir, "recipe.yaml"), []byte(recipe), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.yaml"), []byte(child), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, targetPath, targetContent)

	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/findImplementations", findImplementationsParams{
		URI:      targetPath,
		Position: yamlpos.Position{Line: 3, Character: 10},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("findImplementations returned error: %+v", resp.Error)
	}
	var result findImplementationsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Locations) != 2 {
		t.Fatalf("expected 2 implementations (1 recipe + 1 child); got %d: %+v", len(result.Locations), result.Locations)
	}
	seen := map[string]bool{}
	for _, loc := range result.Locations {
		seen[filepath.Base(loc.URI)] = true
	}
	if !seen["recipe.yaml"] || !seen["child.yaml"] {
		t.Errorf("expected recipe.yaml + child.yaml; got %+v", seen)
	}
}

func TestWorkspaceWalkExcludesStandardDirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Plant a reference inside .git/, .aggrega/, node_modules/, vendor/, dist/.
	// The walker MUST NOT include any of these.
	excluded := []string{".git", ".aggrega", "node_modules", "vendor", "dist"}
	planted := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: should-not-be-found\n  version: 1.0.0\nspec:\n  implements: target-uri\n"
	for _, name := range excluded {
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		if err := os.WriteFile(filepath.Join(sub, "trap.yaml"), []byte(planted), 0o644); err != nil {
			t.Fatalf("write trap: %v", err)
		}
	}
	// Plant the legitimate target file at the workspace root.
	targetContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: target-uri\n  version: 1.0.0\nspec: {}\n"
	targetPath := filepath.Join(dir, "target.yaml")
	if err := os.WriteFile(targetPath, []byte(targetContent), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, targetPath, targetContent)

	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/findImplementations", findImplementationsParams{
		URI:      targetPath,
		Position: yamlpos.Position{Line: 3, Character: 10},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("findImplementations returned error: %+v", resp.Error)
	}
	var result findImplementationsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Locations) != 0 {
		for _, loc := range result.Locations {
			if strings.Contains(loc.URI, "/.git/") || strings.Contains(loc.URI, "/.aggrega/") ||
				strings.Contains(loc.URI, "/node_modules/") || strings.Contains(loc.URI, "/vendor/") ||
				strings.Contains(loc.URI, "/dist/") {
				t.Errorf("walker returned excluded path: %s", loc.URI)
			}
		}
	}
}

func TestWorkspaceWalkHonorsGitignore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// .gitignore excludes the ignored/ directory.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	ignoredDir := filepath.Join(dir, "ignored")
	if err := os.MkdirAll(ignoredDir, 0o755); err != nil {
		t.Fatalf("mkdir ignored: %v", err)
	}
	planted := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: ignored-recipe\n  version: 1.0.0\nspec:\n  implements: target-uri\n"
	if err := os.WriteFile(filepath.Join(ignoredDir, "trap.yaml"), []byte(planted), 0o644); err != nil {
		t.Fatalf("write trap: %v", err)
	}
	targetContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: target-uri\n  version: 1.0.0\nspec: {}\n"
	targetPath := filepath.Join(dir, "target.yaml")
	if err := os.WriteFile(targetPath, []byte(targetContent), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, targetPath, targetContent)

	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/findImplementations", findImplementationsParams{
		URI:      targetPath,
		Position: yamlpos.Position{Line: 3, Character: 10},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("findImplementations returned error: %+v", resp.Error)
	}
	var result findImplementationsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, loc := range result.Locations {
		if strings.Contains(loc.URI, "/ignored/") {
			t.Errorf("walker returned .gitignore-excluded path: %s", loc.URI)
		}
	}
}
