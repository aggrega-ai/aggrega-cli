// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

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

// TestGitignoreWalkerMatchParity exercises the per-directory matcher
// over a representative `.gitignore` covering each pattern class: a
// plain entry, a glob, a negation, a root-anchored entry, a
// directory-only entry, and a `**` deep-match. It pins matcher behavior
// directly (not through the LSP server) so the pattern semantics are
// asserted at the smallest surface.
func TestGitignoreWalkerMatchParity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeWorkspaceTree(t, dir, map[string]string{
		".gitignore": "secret.yaml\n" + // plain entry
			"*.tmp.yaml\n" + // glob
			"*.keepme.yaml\n" + // glob whose negation follows
			"!special.keepme.yaml\n" + // negation (re-include)
			"/anchored.yaml\n" + // root-anchored
			"builddir/\n" + // directory-only
			"**/deep.yaml\n", // ** deep-match (new capability)
	})

	w := newGitignoreWalker(dir)

	cases := []struct {
		name  string
		rel   string
		isDir bool
		want  bool
	}{
		{"plain entry", "secret.yaml", false, true},
		{"glob", "a.tmp.yaml", false, true},
		{"negation re-includes", "special.keepme.yaml", false, false},
		{"glob still excludes non-negated", "other.keepme.yaml", false, true},
		{"anchored matches at root", "anchored.yaml", false, true},
		{"anchored does not match in subdir", "sub/anchored.yaml", false, false},
		{"dir-only matches directory", "builddir", true, true},
		{"dir-only does not match file", "builddir", false, false},
		{"doublestar deep match", "x/y/deep.yaml", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, filepath.FromSlash(tc.rel))
			if got := w.matchesAny(path, tc.isDir); got != tc.want {
				t.Errorf("matchesAny(%q, isDir=%v) = %v; want %v", tc.rel, tc.isDir, got, tc.want)
			}
		})
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

// TestRelComponentsAcceptsLeadingDoubleDotName is one of four per-site cases
// covering a deliberate loosening: a directory whose name begins with two dots
// followed by a letter is an ordinary directory, not an escape.
//
// The walker previously judged containment with a bare two-dot prefix test on
// the root-relative path, so "..cache/app.yaml" read as an escape and the file
// silently vanished from every workspace-wide answer the daemon gives — find
// references, workspace validation — with no diagnostic anywhere. The predicate
// judges where the path lands instead, so the directory is walked.
//
// Each of the four sites carries its own case rather than sharing one: they were
// four separate copies of the test, and a single shared assertion would pass
// while three of them were still wrong.
func TestRelComponentsAcceptsLeadingDoubleDotName(t *testing.T) {
	t.Parallel()
	root := filepath.Join(string(filepath.Separator), "ws")
	w := newGitignoreWalker(root)

	got := w.relComponents(filepath.Join(root, "..cache", "app.yaml"))
	want := []string{"..cache", "app.yaml"}
	if len(got) != len(want) {
		t.Fatalf("relComponents(%q) = %v; want %v — a name beginning with two dots is not an escape", "..cache/app.yaml", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("relComponents component %d = %q; want %q", i, got[i], want[i])
		}
	}

	// The refusal the loosening must not have taken with it.
	if out := w.relComponents(filepath.Join(root, "..", "elsewhere", "app.yaml")); out != nil {
		t.Errorf("relComponents accepted a path above the root, returning %v; the loosening must not reach a genuine escape", out)
	}
	// The root itself is still not a component list.
	if out := w.relComponents(root); out != nil {
		t.Errorf("relComponents(root) = %v; want nil", out)
	}
}

// TestAncestorsToRootAcceptsLeadingDoubleDotName is the second of the four
// per-site cases. The ancestor chain feeds .gitignore lookup, so the same
// two-dot prefix test made every `.gitignore` inside such a directory
// unreadable — the file was walked but its exclusions were never applied.
func TestAncestorsToRootAcceptsLeadingDoubleDotName(t *testing.T) {
	t.Parallel()
	root := filepath.Join(string(filepath.Separator), "ws")
	w := newGitignoreWalker(root)

	got := w.ancestorsToRoot(filepath.Join(root, "..cache", "app.yaml"))
	want := []string{filepath.Join(root, "..cache"), root}
	if len(got) != len(want) {
		t.Fatalf("ancestorsToRoot(%q) = %v; want %v", "..cache/app.yaml", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ancestorsToRoot entry %d = %q; want %q", i, got[i], want[i])
		}
	}

	// The refusal the loosening must not have taken with it.
	if out := w.ancestorsToRoot(filepath.Join(root, "..", "elsewhere", "app.yaml")); len(out) != 0 {
		t.Errorf("ancestorsToRoot accepted a path above the root, returning %v; the loosening must not reach a genuine escape", out)
	}
}

// TestWorkspaceWalkAppliesGitignoreToLeadingDoubleDotName proves the loosening at
// the level a user sees, and the direction is worth stating because it inverts.
//
// The over-broad prefix test made relComponents report "outside the root" for a
// root-relative path beginning with two dots. matchesAny reads that as "no
// components to match", which is not an exclusion — so such a file was reported
// as NOT ignored and was visited unconditionally, with every `.gitignore` rule
// naming it silently inert. Correcting the containment predicate LOOSENS the
// predicate and, through it, restores gitignore filtering for those paths.
//
// A directory whose name begins with two dots also begins with a dot, so the
// walk's separate hidden-name exclusion skips it before containment is ever
// consulted. That rule is a hidden-file convention rather than a containment
// test, it is documented as its own bullet on the walk, and it is untouched
// here — which is why this case is pinned on a FILE, where the containment
// answer is the one that decides.
func TestWorkspaceWalkAppliesGitignoreToLeadingDoubleDotName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeWorkspaceTree(t, dir, map[string]string{
		".gitignore":     "..scratch.yaml\n",
		"..scratch.yaml": "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/scratch\n  version: 1.0.0\nspec: {}\n",
		"app.yaml":       "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/app\n  version: 1.0.0\nspec: {}\n",
	})

	srv := newTestServer(t, dir, nil, nil)
	srv.stderr = drainStderr()

	var seen []string
	err := srv.walkWorkspaceYAML(context.Background(), func(path, _ string) error {
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		seen = append(seen, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walkWorkspaceYAML: %v", err)
	}

	var sawScratch, sawApp bool
	for _, s := range seen {
		switch s {
		case "..scratch.yaml":
			sawScratch = true
		case "app.yaml":
			sawApp = true
		}
	}
	if !sawApp {
		t.Fatalf("walk visited %v; it did not reach app.yaml, so this test is measuring a broken walk rather than the exclusion", seen)
	}
	if sawScratch {
		t.Errorf("walk visited %v; `.gitignore` names ..scratch.yaml, and a name beginning with two dots is inside the root like any other, so its exclusion must apply", seen)
	}
}

// TestWorkspaceDocTraversalDeliversCachedDocumentsInPathOrder pins the
// ordering contract of the shared document traversal, through the
// projection that depends on it most sharply.
//
// enumerateRecipeImplements keeps the FIRST document to declare a given
// name and imposes no ordering of its own, so its answer is only
// well-defined if the traversal's delivery order is. The parse cache
// iterates in Go map order, so without the traversal's own ordering two
// documents declaring one name would each win on some fraction of calls
// and the daemon would rank completions differently between two
// identical requests.
//
// Two Recipe documents deliberately share metadata.name and differ in
// spec.implements, which is the only shape where the tie-break is
// observable at all.
func TestWorkspaceDocTraversalDeliversCachedDocumentsInPathOrder(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"a-first.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: shared
spec:
  implements: from-a
`,
		"b-second.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: shared
spec:
  implements: from-b
`,
	}
	writeWorkspaceTree(t, root, files)
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})
	for rel := range files {
		abs := filepath.Join(root, rel)
		content, err := os.ReadFile(abs)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		srv.parseCache.Put(abs, &doc, content)
	}

	// The lower path wins, on every call.
	for i := 1; i <= 20; i++ {
		got := srv.enumerateRecipeImplements(context.Background(), "Recipe", srv.workspaceTerms().reach)
		if got["shared"] != "from-a" {
			t.Fatalf("call %d: implements = %q, want %q — the traversal delivered the higher path first", i, got["shared"], "from-a")
		}
	}
}
