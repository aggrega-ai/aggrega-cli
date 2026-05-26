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

// writeWorkspaceTree builds a runtime workspace under root with the
// supplied map of relative paths to file contents. Intermediate
// directories are created automatically. Inline construction (rather
// than testdata fixtures) avoids the awkward interaction where
// committed `.gitignore` fixtures would themselves be subject to the
// project's own gitignore rules during test discovery.
func writeWorkspaceTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

func TestWorkspaceWalkHonorsNestedGitignore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Target: tests/.gitignore excludes *.test.yaml. The Component
	// fixture inside tests/ is the "trap" — it would otherwise match
	// findReferences but must be excluded.
	target := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/duplicate\n  version: 1.0.0\nspec: {}\n"
	writeWorkspaceTree(t, dir, map[string]string{
		"tests/.gitignore":          "*.test.yaml\n",
		"tests/component.test.yaml": target, // trap: nested .gitignore excludes
		"app.yaml":                  "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/duplicate\n  version: 1.0.0\nspec: {}\n",
		"aggregate.yaml":            "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/duplicate\n",
	})

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	aggContent, _ := os.ReadFile(filepath.Join(dir, "aggregate.yaml"))
	validateFile(t, srv, filepath.Join(dir, "aggregate.yaml"), string(aggContent))

	// findReferences on metadata.name "core/duplicate" — the walker
	// should find the reference at aggregate.yaml's spec.components.db.type
	// but MUST NOT include tests/component.test.yaml (which would also
	// claim metadata.name "core/duplicate" if it were not gitignored).
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/findReferences", findReferencesParams{
		URI:      filepath.Join(dir, "app.yaml"),
		Position: yamlpos.Position{Line: 3, Character: 12},
	}))
	// Need to validate app.yaml first (cursor source must be cached).
	srv2 := newTestServer(t, dir, nil, nil)
	srv2.stderr = drainStderr()
	appContent, _ := os.ReadFile(filepath.Join(dir, "app.yaml"))
	validateFile(t, srv2, filepath.Join(dir, "app.yaml"), string(appContent))

	resp := runOneRequest(t, srv2, &in, &out)
	if resp.Error != nil {
		t.Fatalf("findReferences returned error: %+v", resp.Error)
	}
	var result findReferencesResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Assert: no trap path in the result Locations.
	for _, loc := range result.Locations {
		if strings.Contains(loc.URI, "tests/component.test.yaml") {
			t.Errorf("walker returned nested-.gitignore-excluded path: %s", loc.URI)
		}
	}
}

// TestWorkspaceWalkParentSkipDirOverridesNestedNegation pins the
// walker's current behavior: when a parent .gitignore excludes a
// directory, filepath.WalkDir returns SkipDir before descending into
// that directory, so any nested .gitignore inside the excluded
// directory never loads. Negation patterns (!) inside the nested file
// are unreachable through the directory walk.
//
// This is consistent with how `git` itself handles excluded
// directories. If a future change implements cross-directory negation by
// NOT short-circuiting at the SkipDir boundary, this assertion flips
// — at which point the test becomes the spec for the new behavior.
func TestWorkspaceWalkParentSkipDirOverridesNestedNegation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/important\n  version: 1.0.0\nspec: {}\n"
	writeWorkspaceTree(t, dir, map[string]string{
		// Root .gitignore excludes build/ (non-hardcoded directory).
		".gitignore": "build/\n",
		// Sub-directory .gitignore re-includes important.yaml.
		"build/.gitignore":     "!important.yaml\n",
		"build/important.yaml": target,
		"build/skip.yaml":      "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/skip\n  version: 1.0.0\nspec: {}\n",
		"aggregate.yaml":       "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/important\n",
	})

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	aggPath := filepath.Join(dir, "aggregate.yaml")
	aggContent, _ := os.ReadFile(aggPath)
	validateFile(t, srv, aggPath, string(aggContent))

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
	// PINNED: parent SkipDir short-circuit means build/important.yaml
	// is unreachable even though its containing .gitignore says
	// "!important.yaml". A future implementation that walks excluded
	// directories specifically to honor cross-file negation would
	// flip this assertion.
	if result.Location != nil {
		t.Errorf("expected nil Location (parent SkipDir short-circuits nested negation); got %s", result.Location.URI)
	}
}

func TestWorkspaceWalkAncestorPrecedenceDeepestWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/deepest\n  version: 1.0.0\nspec: {}\n"
	writeWorkspaceTree(t, dir, map[string]string{
		// Root .gitignore excludes *.yaml (which would normally
		// filter everything including the target).
		".gitignore": "*.deepfilter.yaml\n",
		// Nested .gitignore: no pattern; effectively passes through.
		// The deepest-first traversal still hits the root pattern.
		"sub/.gitignore":                "# no patterns here\n",
		"sub/component.deepfilter.yaml": target, // excluded by root
		"sub/visible.yaml":              target, // not excluded
		"aggregate.yaml":                "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: my-app\n  version: 1.0.0\nspec:\n  components:\n    db:\n      type: core/deepest\n",
	})

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()
	aggPath := filepath.Join(dir, "aggregate.yaml")
	aggContent, _ := os.ReadFile(aggPath)
	validateFile(t, srv, aggPath, string(aggContent))

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
	// Expect: sub/visible.yaml is the resolved target (excluded
	// candidate sub/component.deepfilter.yaml is filtered by root
	// .gitignore even when consulted from the sub-directory).
	if result.Location == nil {
		t.Fatal("Location nil; expected sub/visible.yaml as the resolved target")
	}
	if !strings.Contains(result.Location.URI, "visible.yaml") {
		t.Errorf("Location.URI = %q; want path containing visible.yaml", result.Location.URI)
	}
}
