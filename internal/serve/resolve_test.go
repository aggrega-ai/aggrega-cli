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

	// Cursor on "core/web-server" — line 8 (spec.extends value), char 14.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      childPath,
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
	aggRecipeImplements := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: postgres-recipe\n  version: 1.0.0\nspec:\n  implements: core/postgres\n  target: helm\n"
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
	recipe := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: my-recipe\n  version: 1.0.0\nspec:\n  implements: core/cache\n  target: helm\n"
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

// captureStderr swaps srv.stderr for a buffer that the test inspects
// after the request. Used by tests that assert stderr warnings
// (duplicate-name, version-mismatch).
func captureStderr(srv *Server) *bytes.Buffer {
	buf := &bytes.Buffer{}
	srv.stderr = buf
	return buf
}

func TestResolveUsesTargetVerboseFormFindsSibling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "uses-target", "aggregate.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on the "db" target value: line 13 (0-based), char 20.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 14, Character: 20},
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
		t.Fatal("Location nil; want sibling-component-key Location")
	}
	if result.Location.URI != aggPath {
		t.Errorf("Location.URI = %q; want %q (same-document)", result.Location.URI, aggPath)
	}
	// "db" key lives at line 7 (0-based) of the fixture.
	if result.Location.Range.Start.Line != 7 {
		t.Errorf("Location.Range.Start.Line = %d; want 7", result.Location.Range.Start.Line)
	}
}

func TestResolveUsesTargetSimpleFormFindsSibling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "uses-target-simple-form", "aggregate.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "db" in the simple list: line 13 (0-based), char 12.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 13, Character: 12},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.Range.Start.Line != 7 {
		t.Fatalf("Location = %+v; want sibling-key Location at line 7", result.Location)
	}
}

func TestResolveUsesTargetBareStringShorthandFindsSibling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "uses-target-bare-string-shorthand", "aggregate.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "db" in "hr-db: {target: db}": line 14 (0-based), char 21.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 14, Character: 21},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.Range.Start.Line != 7 {
		t.Fatalf("Location = %+v; want sibling-key Location at line 7", result.Location)
	}
}

func TestResolveUsesTargetMissingReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "uses-target-missing", "aggregate.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "nonexistent-sibling" target value: line 12 (0-based), char 20.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 12, Character: 25},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location != nil {
		t.Errorf("Location = %+v; want nil (target name does not exist among siblings)", result.Location)
	}
}

func TestResolveManifestRecipeReferenceFindsRecipe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifestPath, manifestContent := copyFixtureFile(t, "manifest-mappings", "manifest.yaml", dir)
	recipePath, _ := copyFixtureFile(t, "manifest-mappings", "recipe.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, manifestPath, manifestContent)

	// Cursor on the recipe URI value: line 9 (0-based), char 24.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      manifestPath,
		Position: yamlpos.Position{Line: 9, Character: 24},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.URI != recipePath {
		t.Fatalf("Location = %+v; want pointer at %s", result.Location, recipePath)
	}
}

func TestResolveManifestRecipeReferenceWithVersionedURI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifestPath, manifestContent := copyFixtureFile(t, "manifest-mappings-versioned-uri", "manifest.yaml", dir)
	recipePath, _ := copyFixtureFile(t, "manifest-mappings-versioned-uri", "recipe.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, manifestPath, manifestContent)

	// Cursor on "recipes/helm-server:1.0.0" value: line 9 (0-based), char 24.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      manifestPath,
		Position: yamlpos.Position{Line: 9, Character: 24},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.URI != recipePath {
		t.Fatalf("Location = %+v; want pointer at %s (version stripped)", result.Location, recipePath)
	}
}

func TestResolveExtendsWithVersionedURI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	parent := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/web-server\n  version: 1.0.0\nspec: {}\n"
	child := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: app/api-server\n  version: 1.0.0\nspec:\n  extends: core/web-server:1.0.0\n"
	parentPath := filepath.Join(dir, "parent.yaml")
	childPath := filepath.Join(dir, "child.yaml")
	if err := os.WriteFile(parentPath, []byte(parent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(childPath, []byte(child), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, childPath, child)

	// Cursor on "core/web-server:1.0.0" value: line 6 (0-based), char 14.
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
	if result.Location == nil || result.Location.URI != parentPath {
		t.Fatalf("Location = %+v; want pointer at %s (extends version stripped)", result.Location, parentPath)
	}
}

func TestResolveComponentsTypeWithVersionedURI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "versioned-uri-aggregate", "aggregate.yaml", dir)
	targetPath, _ := copyFixtureFile(t, "versioned-uri-aggregate", "target-component.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "core/server:1.0.0" value: line 7 (0-based), char 15.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 8, Character: 15},
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
		t.Fatalf("Location = %+v; want pointer at %s (components-type version stripped)", result.Location, targetPath)
	}
}

func TestResolveFeaturesTypeWithVersionedURI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    api:\n      type: core/api\n      features:\n        scaling:\n          type: feature/autoscaler:1.0.0\n"
	feat := "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: feature/autoscaler\n  version: 1.0.0\nspec: {}\n"
	aggPath := filepath.Join(dir, "agg.yaml")
	featPath := filepath.Join(dir, "feat.yaml")
	if err := os.WriteFile(aggPath, []byte(agg), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(featPath, []byte(feat), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, agg)

	// Cursor on "feature/autoscaler:1.0.0": line 11 (0-based), char 18.
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
	if result.Location == nil || result.Location.URI != featPath {
		t.Fatalf("Location = %+v; want pointer at %s (features-type version stripped)", result.Location, featPath)
	}
}

func TestResolveRecipeImplementsWithVersionedURI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/cache\n  version: 1.0.0\nspec: {}\n"
	recipe := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: redis-recipe\n  version: 1.0.0\nspec:\n  implements: core/cache:1.0.0\n"
	targetPath := filepath.Join(dir, "target.yaml")
	recipePath := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(targetPath, []byte(target), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(recipePath, []byte(recipe), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, recipePath, recipe)

	// Cursor on "core/cache:1.0.0": line 6 (0-based), char 16.
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
		t.Fatalf("Location = %+v; want pointer at %s (implements version stripped)", result.Location, targetPath)
	}
}

func TestResolveVersionMismatchEmitsWarning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "version-mismatch", "aggregate.yaml", dir)
	targetPath, _ := copyFixtureFile(t, "version-mismatch", "target-component.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	stderrBuf := captureStderr(srv)
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "core/server:1.0.0": line 7 (0-based), char 15.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 8, Character: 15},
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
		t.Fatalf("Location = %+v; want pointer at %s (navigation succeeds despite version mismatch)", result.Location, targetPath)
	}
	stderrText := stderrBuf.String()
	if !strings.Contains(stderrText, "version mismatch") {
		t.Errorf("stderr = %q; want 'version mismatch' warning", stderrText)
	}
}

func TestResolveReleaseAggregatePathFindsAggregate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	releasePath, releaseContent := copyFixtureFile(t, "release-paths", "release.yaml", dir)
	appPath, _ := copyFixtureFile(t, "release-paths", "app.yaml", dir)
	copyFixtureFile(t, "release-paths", "app-manifest.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, releasePath, releaseContent)

	// Cursor on "app" in spec.aggregate: line 7 (0-based), char 14.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      releasePath,
		Position: yamlpos.Position{Line: 7, Character: 14},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.URI != appPath {
		t.Fatalf("Location = %+v; want pointer at %s", result.Location, appPath)
	}
}

func TestResolveReleaseManifestPathFindsManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	releasePath, releaseContent := copyFixtureFile(t, "release-paths", "release.yaml", dir)
	copyFixtureFile(t, "release-paths", "app.yaml", dir)
	manifestPath, _ := copyFixtureFile(t, "release-paths", "app-manifest.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, releasePath, releaseContent)

	// Cursor on "app-manifest" in spec.manifest: line 8 (0-based), char 13.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      releasePath,
		Position: yamlpos.Position{Line: 8, Character: 13},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location == nil || result.Location.URI != manifestPath {
		t.Fatalf("Location = %+v; want pointer at %s", result.Location, manifestPath)
	}
}

func TestResolveSubsystemNestedComponentsType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "subsystem-nested-components-type", "aggregate.yaml", dir)
	targetPath, _ := copyFixtureFile(t, "subsystem-nested-components-type", "target-component.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "core/postgres": line 10 (0-based), char 18.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 10, Character: 18},
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

func TestResolveRecursiveSubsystemNesting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "recursive-subsystem-nesting", "aggregate.yaml", dir)
	targetPath, _ := copyFixtureFile(t, "recursive-subsystem-nesting", "target-component.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "core/redis": line 12 (0-based), char 22.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 12, Character: 22},
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
		t.Fatalf("Location = %+v; want pointer at %s (recursive nesting)", result.Location, targetPath)
	}
}

func TestResolveSubsystemNestedFeaturesType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "subsystem-nested-features-type", "aggregate.yaml", dir)
	targetPath, _ := copyFixtureFile(t, "subsystem-nested-features-type", "target-feature.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "feature/autoscaler": line 13 (0-based), char 24.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 13, Character: 24},
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
		t.Fatalf("Location = %+v; want pointer at %s (subsystem-nested features-type)", result.Location, targetPath)
	}
}

func TestResolveDuplicateNameEmitsWarning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath, aggContent := copyFixtureFile(t, "duplicate-names", "aggregate.yaml", dir)
	copyFixtureFile(t, "duplicate-names", "first.yaml", dir)
	copyFixtureFile(t, "duplicate-names", "second.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	stderrBuf := captureStderr(srv)
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on "core/duplicate" reference: line 8 (0-based), char 18.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 8, Character: 18},
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
		t.Fatal("Location nil; want first-match-wins Location")
	}
	if !strings.Contains(stderrBuf.String(), "duplicate metadata.name") {
		t.Errorf("stderr = %q; want 'duplicate metadata.name' warning", stderrBuf.String())
	}
}

func TestFindReferencesAggregatesManifestRecipeReferences(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	recipePath, recipeContent := copyFixtureFile(t, "manifest-mappings", "recipe.yaml", dir)
	manifestPath, _ := copyFixtureFile(t, "manifest-mappings", "manifest.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, recipePath, recipeContent)

	// Cursor on Recipe's metadata.name: line 3, char 10.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/findReferences", findReferencesParams{
		URI:      recipePath,
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
	if len(result.Locations) == 0 {
		t.Fatalf("expected at least one reference from Manifest mapping value; got 0")
	}
	var foundManifest bool
	for _, loc := range result.Locations {
		if loc.URI == manifestPath {
			foundManifest = true
		}
	}
	if !foundManifest {
		t.Errorf("expected reference at %s; got Locations = %+v", manifestPath, result.Locations)
	}
}

func TestFindReferencesEmitsDuplicateNameWarning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	firstPath, firstContent := copyFixtureFile(t, "duplicate-names", "first.yaml", dir)
	copyFixtureFile(t, "duplicate-names", "second.yaml", dir)
	copyFixtureFile(t, "duplicate-names", "aggregate.yaml", dir)
	srv := newTestServer(t, dir, nil, nil)
	stderrBuf := captureStderr(srv)
	validateFile(t, srv, firstPath, firstContent)

	// Cursor on first.yaml's metadata.name "core/duplicate": line 3, char 12.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/findReferences", findReferencesParams{
		URI:      firstPath,
		Position: yamlpos.Position{Line: 3, Character: 12},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("findReferences returned error: %+v", resp.Error)
	}
	if !strings.Contains(stderrBuf.String(), "duplicate metadata.name") {
		t.Errorf("stderr = %q; want 'duplicate metadata.name' warning at findReferences", stderrBuf.String())
	}
}

// TestResolveReleasePathFallsBackToDocumentStartWhenMetadataNameAbsent
// pins the document-start fallback behavior of resolveReleasePath when
// the target file has no metadata.name. The fallback was shipped in
// the prior resolve-family work; this test makes it load-bearing so
// future refactors catch any regression.
func TestResolveReleasePathFallsBackToDocumentStartWhenMetadataNameAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Release fixture referencing a target Aggregate that lacks
	// metadata.name (a degenerate but valid YAML).
	releaseContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: prod-release\n  version: 1.0.0\nspec:\n  aggregate: nameless\n"
	releasePath := filepath.Join(dir, "release.yaml")
	if err := os.WriteFile(releasePath, []byte(releaseContent), 0o644); err != nil {
		t.Fatalf("write release: %v", err)
	}
	// Target file has spec but no metadata.name.
	namelessContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nspec: {}\n"
	namelessPath := filepath.Join(dir, "nameless.yaml")
	if err := os.WriteFile(namelessPath, []byte(namelessContent), 0o644); err != nil {
		t.Fatalf("write nameless: %v", err)
	}

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, releasePath, releaseContent)

	// Cursor on "nameless" path value: line 6 (0-based), char 14.
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/resolve", resolveParams{
		URI:      releasePath,
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
		t.Fatal("Location nil; want document-start Location at target path")
	}
	if result.Location.URI != namelessPath {
		t.Errorf("Location.URI = %q; want %q", result.Location.URI, namelessPath)
	}
	zero := yamlpos.Position{Line: 0, Character: 0}
	if result.Location.Range.Start != zero {
		t.Errorf("Range.Start = %+v; want %+v (document-start fallback)", result.Location.Range.Start, zero)
	}
	if result.Location.Range.End != zero {
		t.Errorf("Range.End = %+v; want %+v (document-start fallback)", result.Location.Range.End, zero)
	}
}
