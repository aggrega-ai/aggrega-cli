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

// completionRequest drives one aggrega/completion through the
// dispatch loop and decodes the response.
func completionRequest(t *testing.T, srv *Server, uri string, line, char int) completionResult {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/completion", completionParams{
		URI:      uri,
		Position: yamlpos.Position{Line: line, Character: char},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("completion returned error: %+v", resp.Error)
	}
	var result completionResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal completion result: %v", err)
	}
	return result
}

// writeCompletionFixture writes content under dir/name and returns the full path.
func writeCompletionFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

func TestCompletionDispatchKindCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n"
	path := writeCompletionFixture(t, dir, "agg.yaml", content)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, path, content)

	// Cursor on "Aggregate" value at line 1, char 10.
	result := completionRequest(t, srv, path, 1, 10)
	if len(result.Items) != 10 {
		t.Errorf("kind completion returned %d items; want 10 entity kinds", len(result.Items))
	}
	labels := map[string]bool{}
	for _, it := range result.Items {
		labels[it.Label] = true
	}
	for _, want := range []string{"Component", "Feature", "Recipe", "Aggregate", "Manifest", "Release", "Plugin", "FeatureRecipe", "SchemaLibrary", "MappingLibrary"} {
		if !labels[want] {
			t.Errorf("kind candidate %q missing from output", want)
		}
	}
}

func TestCompletionDispatchApiVersionCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n"
	path := writeCompletionFixture(t, dir, "agg.yaml", content)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, path, content)

	// Cursor on "aggrega.ai/v1alpha1" value at line 0, char 14.
	result := completionRequest(t, srv, path, 0, 14)
	if len(result.Items) != 1 {
		t.Errorf("apiVersion completion returned %d items; want 1", len(result.Items))
	}
}

func TestCompletionDispatchExtendsCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Sibling Component fixture in workspace.
	writeCompletionFixture(t, dir, "parent.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/parent\n  version: 1.0.0\nspec: {}\n")
	// Child Component file whose cursor will be on spec.extends.
	// Use a placeholder value so yamlpos can classify the cursor on
	// a scalar (cursors on whitespace past EOL don't classify).
	childContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: app/child\n  version: 1.0.0\nspec:\n  extends: placeholder\n"
	childPath := writeCompletionFixture(t, dir, "child.yaml", childContent)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, childPath, childContent)

	// Cursor on spec.extends value (the "placeholder" scalar): line 6, char 15.
	result := completionRequest(t, srv, childPath, 6, 15)
	labels := map[string]bool{}
	for _, it := range result.Items {
		labels[it.Label] = true
	}
	if !labels["core/parent"] {
		t.Errorf("extends completion missing 'core/parent' candidate; got: %v", labelsList(result.Items))
	}
	if !labels["app/child"] {
		t.Errorf("extends completion missing 'app/child' candidate (self-reference is permitted); got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchComponentsTypeCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompletionFixture(t, dir, "target.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec: {}\n")
	aggContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    api:\n      type: placeholder\n"
	aggPath := writeCompletionFixture(t, dir, "agg.yaml", aggContent)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on spec.components.api.type placeholder: line 8, char 15.
	result := completionRequest(t, srv, aggPath, 8, 15)
	if !hasLabel(result.Items, "core/server") {
		t.Errorf("components.type completion missing 'core/server'; got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchImplementsCursorRecipe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompletionFixture(t, dir, "target.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/cache\n  version: 1.0.0\nspec: {}\n")
	recipeContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: cache-recipe\n  version: 1.0.0\nspec:\n  implements: placeholder\n  provider: helm\n"
	recipePath := writeCompletionFixture(t, dir, "recipe.yaml", recipeContent)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, recipePath, recipeContent)

	// Cursor on spec.implements placeholder: line 6, char 18.
	result := completionRequest(t, srv, recipePath, 6, 18)
	if !hasLabel(result.Items, "core/cache") {
		t.Errorf("implements completion missing 'core/cache'; got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchImplementsCursorFeatureRecipe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A Feature URI in the workspace (the FeatureRecipe's
	// spec.implements: cursor should enumerate Features, not Components).
	writeCompletionFixture(t, dir, "feature.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: feature/scaling\n  version: 1.0.0\nspec: {}\n")
	// A Component in the same workspace — should NOT appear since
	// FeatureRecipe.spec.implements targets Features only.
	writeCompletionFixture(t, dir, "component.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/x\n  version: 1.0.0\nspec: {}\n")
	frContent := "apiVersion: aggrega.ai/v1alpha1\nkind: FeatureRecipe\nmetadata:\n  name: scaling-fr\n  version: 1.0.0\nspec:\n  implements: placeholder\n  provider: helm\n"
	frPath := writeCompletionFixture(t, dir, "fr.yaml", frContent)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, frPath, frContent)

	// Cursor on spec.implements placeholder: line 6, char 18.
	result := completionRequest(t, srv, frPath, 6, 18)
	if !hasLabel(result.Items, "feature/scaling") {
		t.Errorf("FeatureRecipe.implements completion missing 'feature/scaling'; got: %v", labelsList(result.Items))
	}
	if hasLabel(result.Items, "core/x") {
		t.Errorf("FeatureRecipe.implements completion incorrectly included Component 'core/x'; host-kind dispatch should filter to Features only. got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchSchemaKeywordCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec:\n  schema:\n    properties:\n      port:\n        type: integer\n"
	path := writeCompletionFixture(t, dir, "comp.yaml", content)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, path, content)

	// Cursor inside spec.schema.properties at line 7, char 8.
	result := completionRequest(t, srv, path, 7, 8)
	if !hasLabel(result.Items, "type") {
		t.Errorf("schema-keyword completion missing 'type'; got: %v", labelsList(result.Items))
	}
	if !hasLabel(result.Items, "x-aggrega-selector") {
		t.Errorf("schema-keyword completion missing 'x-aggrega-selector'; got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchSchemaTypeValueCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec:\n  schema:\n    properties:\n      port:\n        type: integer\n"
	path := writeCompletionFixture(t, dir, "comp.yaml", content)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, path, content)

	// Cursor on the VALUE "integer" at line 9, char 18 (mid-word).
	// Expected: primitive-type candidates (object, string, integer, ...),
	// NOT JSON Schema keywords.
	result := completionRequest(t, srv, path, 9, 18)
	if !hasLabel(result.Items, "object") {
		t.Errorf("schema-type-value completion missing 'object'; got: %v", labelsList(result.Items))
	}
	if !hasLabel(result.Items, "string") {
		t.Errorf("schema-type-value completion missing 'string'; got: %v", labelsList(result.Items))
	}
	// Confirm we got the primitive-type set, not the keyword set —
	// "type" the keyword should NOT appear in primitive-type candidates.
	if hasLabel(result.Items, "type") {
		t.Errorf("schema-type-value completion incorrectly returned keyword 'type'; expected primitives only. got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchSchemaTypeAsArrayCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Type-as-array: JSON Schema 2020-12 allows `type: [string, "null"]`.
	// Cursor on the array element should still offer primitive-type candidates.
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/x\n  version: 1.0.0\nspec:\n  schema:\n    properties:\n      port:\n        type:\n          - integer\n"
	path := writeCompletionFixture(t, dir, "comp.yaml", content)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, path, content)

	// Cursor on "integer" array element at line 10, char 14.
	result := completionRequest(t, srv, path, 10, 14)
	if !hasLabel(result.Items, "object") {
		t.Errorf("schema-type-as-array completion missing 'object'; got: %v", labelsList(result.Items))
	}
	if !hasLabel(result.Items, "null") {
		t.Errorf("schema-type-as-array completion missing 'null'; got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchSchemaValueOfNonTypeKeyReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Cursor on the VALUE side of description: inside spec.schema.
	// Per the deferred-value design, this returns empty candidates
	// (no keyword candidates, no primitive candidates — schema-value
	// completion is shipped only for type: keys today).
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec:\n  schema:\n    description: A free-form description.\n"
	path := writeCompletionFixture(t, dir, "comp.yaml", content)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, path, content)

	// Cursor on "A free-form description." at line 7, char 25.
	result := completionRequest(t, srv, path, 7, 25)
	if len(result.Items) != 0 {
		t.Errorf("schema-value-of-non-type returned %d items; want empty (deferred)", len(result.Items))
	}
}

func TestCompletionDispatchReleaseAggregatePathCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompletionFixture(t, dir, "app.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec: {}\n")
	releaseContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: prod\n  version: 1.0.0\nspec:\n  aggregate: placeholder\n"
	releasePath := writeCompletionFixture(t, dir, "release.yaml", releaseContent)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, releasePath, releaseContent)

	// Cursor on spec.aggregate placeholder: line 6, char 17.
	result := completionRequest(t, srv, releasePath, 6, 17)
	if !hasLabel(result.Items, "app") {
		t.Errorf("aggregate-path completion missing 'app'; got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchManifestRecipeReferenceCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompletionFixture(t, dir, "recipe.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: recipes/helm-server\n  version: 1.0.0\nspec:\n  implements: core/server\n  provider: helm\n")
	manifestContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: prod\n  version: 1.0.0\nspec:\n  mappings:\n    components:\n      type:\n        core/server: placeholder\n"
	manifestPath := writeCompletionFixture(t, dir, "manifest.yaml", manifestContent)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, manifestPath, manifestContent)

	// Cursor on mapping value-side placeholder: line 9, char 25.
	result := completionRequest(t, srv, manifestPath, 9, 25)
	if !hasLabel(result.Items, "recipes/helm-server") {
		t.Errorf("manifest-recipe-reference completion missing 'recipes/helm-server'; got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchUsesTargetCursor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aggContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/postgres\n    api:\n      type: core/api\n      uses:\n        backend:\n          primary:\n            target: placeholder\n"
	aggPath := writeCompletionFixture(t, dir, "agg.yaml", aggContent)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on target placeholder: line 14, char 23.
	result := completionRequest(t, srv, aggPath, 14, 23)
	if !hasLabel(result.Items, "db") {
		t.Errorf("uses-target completion missing sibling 'db'; got: %v", labelsList(result.Items))
	}
	if !hasLabel(result.Items, "api") {
		t.Errorf("uses-target completion missing self 'api' (intra-document, self-permitted); got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchUnknownCursorReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/x\n  version: 1.0.0\nspec: {}\n"
	path := writeCompletionFixture(t, dir, "comp.yaml", content)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, path, content)

	// Cursor on metadata.version at line 4, char 12 — not a
	// completion-recognized context.
	result := completionRequest(t, srv, path, 4, 12)
	if len(result.Items) != 0 {
		t.Errorf("unknown-cursor completion returned %d items; want empty", len(result.Items))
	}
}

func TestCompletionDispatchTriggerSlashPrefixNarrows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCompletionFixture(t, dir, "a.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/server\n  version: 1.0.0\nspec: {}\n")
	writeCompletionFixture(t, dir, "b.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/web-server\n  version: 1.0.0\nspec: {}\n")
	writeCompletionFixture(t, dir, "c.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: app/api\n  version: 1.0.0\nspec: {}\n")
	aggContent := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    api:\n      type: core/\n"
	aggPath := writeCompletionFixture(t, dir, "agg.yaml", aggContent)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, aggPath, aggContent)

	// Cursor on spec.components.api.type with cursor right after "core/" at line 8, char 17.
	// Include a triggerCharacter "/" so the dispatcher narrows by the typed prefix "core/".
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/completion", completionParams{
		URI:      aggPath,
		Position: yamlpos.Position{Line: 8, Character: 17},
		Context:  &completionContext{TriggerKind: 2, TriggerCharacter: "/"},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("completion returned error: %+v", resp.Error)
	}
	var result completionResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Expect only "core/*" candidates (app/api should NOT appear).
	if hasLabel(result.Items, "app/api") {
		t.Errorf("trigger-slash narrowing failed; 'app/api' should be filtered out. got: %v", labelsList(result.Items))
	}
	if !hasLabel(result.Items, "core/server") {
		t.Errorf("trigger-slash narrowing dropped 'core/server'; got: %v", labelsList(result.Items))
	}
}

func TestCompletionDispatchClassifierUnknownReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/x\n  version: 1.0.0\n  description: A free-form description scalar.\nspec: {}\n"
	path := writeCompletionFixture(t, dir, "comp.yaml", content)
	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	validateFile(t, srv, path, content)

	// Cursor on metadata.description value — no completion classification.
	result := completionRequest(t, srv, path, 5, 30)
	if len(result.Items) != 0 {
		t.Errorf("classifier-unknown cursor returned %d items; want empty (no recognized completion context)", len(result.Items))
	}
}

// --- helpers ---

func hasLabel(items []CompletionItem, want string) bool {
	for _, it := range items {
		if it.Label == want {
			return true
		}
	}
	return false
}

func labelsList(items []CompletionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

// quiet unused-import linter
var _ = strings.Contains
