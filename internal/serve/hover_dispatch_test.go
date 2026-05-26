// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// writeFile writes content to path under dir, returning the full path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

func hoverRequest(t *testing.T, srv *Server, uri string, line, char int) hoverResult {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/hover", hoverParams{
		URI:      uri,
		Position: yamlpos.Position{Line: line, Character: char},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("hover returned error: %+v", resp.Error)
	}
	var result hoverResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal hover result: %v", err)
	}
	return result
}

func TestHoverComponentRendersExtendsTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "parent.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/web-server\n  version: 1.0.0\n  description: Web server base.\nspec: {}\n")
	childPath := writeFile(t, dir, "child.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: app/api\n  version: 1.0.0\nspec:\n  extends: core/web-server\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, childPath, mustRead(t, childPath))

	// Cursor on "core/web-server" in spec.extends: line 6 (0-based), char 14.
	result := hoverRequest(t, srv, childPath, 6, 14)
	if result.Contents == nil {
		t.Fatal("Contents nil; want parent Component hover")
	}
	if !strings.Contains(result.Contents.Value, "**Component** `core/web-server`") {
		t.Errorf("hover content missing parent header; got: %s", result.Contents.Value)
	}
	if !strings.Contains(result.Contents.Value, "Web server base.") {
		t.Errorf("hover content missing parent description")
	}
}

func TestHoverFeatureRendersExtendsTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "parent.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: feature/base\n  version: 1.0.0\nspec: {}\n")
	childPath := writeFile(t, dir, "child.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: feature/derived\n  version: 1.0.0\nspec:\n  extends: feature/base\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, childPath, mustRead(t, childPath))

	result := hoverRequest(t, srv, childPath, 6, 14)
	if result.Contents == nil {
		t.Fatal("Contents nil; want parent Feature hover")
	}
	if !strings.Contains(result.Contents.Value, "**Feature** `feature/base`") {
		t.Errorf("hover content missing parent Feature header; got: %s", result.Contents.Value)
	}
}

func TestHoverRecipeRendersImplementsTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "target.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/cache\n  version: 1.0.0\nspec: {}\n")
	recipePath := writeFile(t, dir, "recipe.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: cache-recipe\n  version: 1.0.0\nspec:\n  implements: core/cache\n  provider: helm\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, recipePath, mustRead(t, recipePath))

	result := hoverRequest(t, srv, recipePath, 6, 16)
	if result.Contents == nil {
		t.Fatal("Contents nil; want Component target hover")
	}
	if !strings.Contains(result.Contents.Value, "**Component** `core/cache`") {
		t.Errorf("hover content missing target header; got: %s", result.Contents.Value)
	}
}

func TestHoverFeatureRecipeRendersImplementsTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "target.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: feature/scaling\n  version: 1.0.0\nspec: {}\n")
	frPath := writeFile(t, dir, "fr.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: FeatureRecipe\nmetadata:\n  name: scaling-fr\n  version: 1.0.0\nspec:\n  implements: feature/scaling\n  provider: helm\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, frPath, mustRead(t, frPath))

	result := hoverRequest(t, srv, frPath, 6, 16)
	if result.Contents == nil {
		t.Fatal("Contents nil; want Feature target hover")
	}
	if !strings.Contains(result.Contents.Value, "**Feature** `feature/scaling`") {
		t.Errorf("hover content missing target Feature header; got: %s", result.Contents.Value)
	}
}

func TestHoverComponentsTypeRendersTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "target.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec: {}\n")
	aggPath := writeFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    api:\n      type: core/server\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, mustRead(t, aggPath))

	// Cursor on "core/server" at line 8 (0-based), char 14.
	result := hoverRequest(t, srv, aggPath, 8, 14)
	if result.Contents == nil {
		t.Fatal("Contents nil; want Component target hover")
	}
	if !strings.Contains(result.Contents.Value, "**Component** `core/server`") {
		t.Errorf("hover content missing Component header; got: %s", result.Contents.Value)
	}
}

func TestHoverFeaturesTypeRendersTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "target.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: feature/autoscaler\n  version: 1.0.0\nspec: {}\n")
	aggPath := writeFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    api:\n      type: core/api\n      features:\n        scaling:\n          type: feature/autoscaler\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, mustRead(t, aggPath))

	// Cursor on "feature/autoscaler" at line 11 (0-based), char 18.
	result := hoverRequest(t, srv, aggPath, 11, 18)
	if result.Contents == nil {
		t.Fatal("Contents nil; want Feature target hover")
	}
	if !strings.Contains(result.Contents.Value, "**Feature** `feature/autoscaler`") {
		t.Errorf("hover content missing Feature header; got: %s", result.Contents.Value)
	}
}

func TestHoverAggregateRendersFromReleaseAggregatePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "app.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/postgres\n")
	releasePath := writeFile(t, dir, "release.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: prod\n  version: 1.0.0\nspec:\n  aggregate: app\n  manifest: prod-manifest\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, releasePath, mustRead(t, releasePath))

	// Cursor on "app" at line 6 (0-based), char 14.
	result := hoverRequest(t, srv, releasePath, 6, 14)
	if result.Contents == nil {
		t.Fatal("Contents nil; want Aggregate hover via Release path")
	}
	if !strings.Contains(result.Contents.Value, "**Aggregate** `my-app`") {
		t.Errorf("hover content missing Aggregate header; got: %s", result.Contents.Value)
	}
}

func TestHoverManifestRendersFromReleaseManifestPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "prod-manifest.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: prod-manifest\n  version: 1.0.0\nspec:\n  mappings:\n    components:\n      type:\n        core/server: recipes/helm-server\n")
	releasePath := writeFile(t, dir, "release.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: prod\n  version: 1.0.0\nspec:\n  aggregate: app\n  manifest: prod-manifest\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, releasePath, mustRead(t, releasePath))

	// Cursor on "prod-manifest" at line 7 (0-based), char 14.
	result := hoverRequest(t, srv, releasePath, 7, 14)
	if result.Contents == nil {
		t.Fatal("Contents nil; want Manifest hover via Release path")
	}
	if !strings.Contains(result.Contents.Value, "**Manifest** `prod-manifest`") {
		t.Errorf("hover content missing Manifest header; got: %s", result.Contents.Value)
	}
}

func TestHoverManifestRecipeReferenceRendersRecipe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "recipe.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: recipes/helm-server\n  version: 1.0.0\nspec:\n  implements: core/server\n  provider: helm\n")
	manifestPath := writeFile(t, dir, "manifest.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: prod\n  version: 1.0.0\nspec:\n  mappings:\n    components:\n      type:\n        core/server: recipes/helm-server\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, manifestPath, mustRead(t, manifestPath))

	// Cursor on "recipes/helm-server" mapping value side: line 9 (0-based), char 25.
	result := hoverRequest(t, srv, manifestPath, 9, 25)
	if result.Contents == nil {
		t.Fatal("Contents nil; want Recipe hover via Manifest mapping value")
	}
	if !strings.Contains(result.Contents.Value, "**Recipe** `recipes/helm-server`") {
		t.Errorf("hover content missing Recipe header; got: %s", result.Contents.Value)
	}
}

func TestHoverUsesTargetRendersComponentInstance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath := writeFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/postgres\n      description: The database.\n    api:\n      type: core/api\n      uses:\n        backend:\n          primary:\n            target: db\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, mustRead(t, aggPath))

	// Cursor on "db" target value at line 15 (0-based), char 20.
	result := hoverRequest(t, srv, aggPath, 15, 20)
	if result.Contents == nil {
		t.Fatal("Contents nil; want ComponentInstance hover")
	}
	if !strings.Contains(result.Contents.Value, "**Component instance** `db`") {
		t.Errorf("hover content missing ComponentInstance header; got: %s", result.Contents.Value)
	}
	if !strings.Contains(result.Contents.Value, "Type: `core/postgres`") {
		t.Errorf("hover content missing Type line")
	}
}

func TestHoverUsesTargetSimpleListRendersComponentInstance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath := writeFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/postgres\n    api:\n      type: core/api\n      uses:\n        backend:\n          - db\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, mustRead(t, aggPath))

	// Cursor on "db" simple-list element: line 13 (0-based), char 12.
	result := hoverRequest(t, srv, aggPath, 13, 12)
	if result.Contents == nil {
		t.Fatal("Contents nil; want ComponentInstance hover (simple-list form)")
	}
	if !strings.Contains(result.Contents.Value, "**Component instance** `db`") {
		t.Errorf("hover content missing ComponentInstance header; got: %s", result.Contents.Value)
	}
}

func TestHoverUsesTargetBareStringShorthandRendersComponentInstance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath := writeFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/postgres\n    api:\n      type: core/api\n      uses:\n        backend:\n          hr-db: db\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, mustRead(t, aggPath))

	// Cursor on "db" value (bare-string shorthand): line 13, char 17.
	result := hoverRequest(t, srv, aggPath, 13, 17)
	if result.Contents == nil {
		t.Fatal("Contents nil; want ComponentInstance hover (bare-string shorthand)")
	}
	if !strings.Contains(result.Contents.Value, "**Component instance** `db`") {
		t.Errorf("hover content missing ComponentInstance header; got: %s", result.Contents.Value)
	}
}

func TestHoverUsesTargetMissingReturnsNullContents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath := writeFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    api:\n      type: core/api\n      uses:\n        backend:\n          primary:\n            target: nonexistent\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, mustRead(t, aggPath))

	// Cursor on "nonexistent" at line 12 (0-based), char 25.
	result := hoverRequest(t, srv, aggPath, 12, 25)
	if result.Contents != nil {
		t.Errorf("Contents = %+v; want nil (no sibling matches)", result.Contents)
	}
}

func TestHoverReleaseAggregatePathRendersWarningOnKindMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// File at "app" is actually a Manifest (operator typo).
	writeFile(t, dir, "app.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: misnamed\n  version: 1.0.0\nspec:\n  mappings: {}\n")
	releasePath := writeFile(t, dir, "release.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: prod\n  version: 1.0.0\nspec:\n  aggregate: app\n")
	srv := newTestServer(t, dir, nil, nil)
	stderr := captureStderr(srv)
	validateFile(t, srv, releasePath, mustRead(t, releasePath))

	// Cursor on "app" at line 6 (0-based), char 14.
	result := hoverRequest(t, srv, releasePath, 6, 14)
	if result.Contents == nil {
		t.Fatal("Contents nil; want kind-mismatch warning + actual-kind body")
	}
	if !strings.Contains(result.Contents.Value, "**Kind mismatch**") {
		t.Errorf("hover content missing kind-mismatch warning; got: %s", result.Contents.Value)
	}
	if !strings.Contains(result.Contents.Value, "**Manifest**") {
		t.Errorf("hover content missing actual-kind (Manifest) body")
	}
	if !strings.Contains(stderr.String(), "kind mismatch") {
		t.Errorf("stderr missing kind-mismatch log line; got: %s", stderr.String())
	}
}

func TestHoverReturnsNullContentsForSchemaPropertyCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	compPath := writeFile(t, dir, "comp.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec:\n  schema:\n    properties:\n      port:\n        type: integer\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, compPath, mustRead(t, compPath))

	// Cursor on "port" key inside spec.schema.properties — line 8 (0-based), char 8.
	result := hoverRequest(t, srv, compPath, 8, 8)
	if result.Contents != nil {
		t.Errorf("Contents = %+v; want nil (SchemaProperty deferred to completion WI)", result.Contents)
	}
}

func TestHoverCursorOnUsesAliasKeyReturnsNullContents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggPath := writeFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/postgres\n    api:\n      type: core/api\n      uses:\n        backend:\n          hr-db: db\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, mustRead(t, aggPath))

	// Cursor on the alias KEY "hr-db" at line 13, char 12. Side==Key → Unknown → null Contents.
	result := hoverRequest(t, srv, aggPath, 13, 12)
	if result.Contents != nil {
		t.Errorf("Contents = %+v; want nil (Side==Key on alias key)", result.Contents)
	}
}

func TestHoverReturnsNullContentsWhenCursorNotOnReferenceShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	compPath := writeFile(t, dir, "comp.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec: {}\n")
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, compPath, mustRead(t, compPath))

	// Cursor on "version: 1.0.0" — line 4 (0-based), char 14. Not a reference shape.
	result := hoverRequest(t, srv, compPath, 4, 14)
	if result.Contents != nil {
		t.Errorf("Contents = %+v; want nil (cursor not on reference shape)", result.Contents)
	}
}

// mustRead reads a file's contents for tests; t.Fatalf on error.
func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
