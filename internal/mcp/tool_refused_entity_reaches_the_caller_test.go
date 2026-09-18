// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// A catalog file the load refused never enters the catalog, so a Release that
// reaches it fails on the REFERENCE — a true statement about a document that is
// correct, naming nothing the caller can act on. These cases assert that what a
// caller receives instead names the refused document, the condition it raised
// and the authoring contract for its kind.
//
// Every assertion reads the RESULT, because that is the whole of what a caller
// gets: the text a person reads, the structured half a program branches on, and
// the documentation pointer that decides which file they go and open.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// refusedSourceField is the one authored change that makes the fixture's Recipe
// unloadable: the Recipe schema declares spec.source as a choice between a
// string and an object, so a sequence matches neither. It is a shipped
// constraint rather than one this test introduced — nothing about the schema is
// altered here.
const refusedSourceField = "  source:\n    - ./charts/api-server\n"

// refusedRecipeWorkspace materialises a workspace that resolves clean, fills its
// lockfile, and only THEN breaks the Recipe its Manifest maps onto.
//
// The order is the shape an author meets and it is load-bearing: a surface that
// produces an artifact holds the Release to its lockfile, so a workspace broken
// before the resolution would be refused for a missing or drifted lockfile —
// ahead of the gate these cases are about — and every case would still look like
// it refused while covering nothing.
//
// The fixture is COPIED rather than used in place, because a resolution writes:
// the lockfile, the artifact cache, and the resolved content digests it freezes
// into each writable recipe file.
func refusedRecipeWorkspace(t *testing.T) (root string, dirs []string, releasePath, recipePath string) {
	t.Helper()
	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "fullstack-app")
	root = t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	dirs = []string{filepath.Join(root, "catalog")}
	releasePath = filepath.Join(root, "release.yaml")
	recipePath = filepath.Join(root, "catalog", "recipes", "helm-synthesized-with-artifacts", "api-server.yaml")

	// The workspace block anchors the root here rather than letting discovery
	// walk up into the checkout the suite runs inside.
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}

	prep := newTestServer(t)
	if res := callPull(t, prep, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs}); res.IsError {
		t.Fatalf("the preparing resolution refused, so this fixture reaches none of the gates these cases "+
			"are about: %s", res.Content[0].Text)
	}

	sound, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatalf("read the recipe this fixture breaks: %v", err)
	}
	const anchor = "  capability: helm\n"
	if !strings.Contains(string(sound), anchor) {
		t.Fatalf("the fixture recipe no longer carries %q, so this test would author its refusal into the "+
			"wrong place and assert nothing", anchor)
	}
	broken := strings.Replace(string(sound), anchor, anchor+refusedSourceField, 1)
	if err := os.WriteFile(recipePath, []byte(broken), 0o644); err != nil {
		t.Fatalf("author the refusal: %v", err)
	}
	return root, dirs, releasePath, recipePath
}

// requireNamesTheRefusedRecipe fails unless the answer names the refused file,
// the condition it raised, and the authoring contract for the kind that file
// declared — and fails if it points at the Release contract instead, which is
// the document the reader has no reason to open.
func requireNamesTheRefusedRecipe(t *testing.T, surface, answer, recipePath string) {
	t.Helper()
	for _, want := range []string{
		recipePath,
		"E0215",
		"spec.source",
		"skipped at load",
		"aggrega-docs/language-reference/entities/recipe.md",
	} {
		if !strings.Contains(answer, want) {
			t.Errorf("%s does not carry %q:\n%s", surface, want, answer)
		}
	}
	if strings.Contains(answer, "entities/release.md") {
		t.Errorf("%s sends the reader to the Release contract for a fault authored in a Recipe:\n%s", surface, answer)
	}
}

// TestBuildToolNamesTheRefusedDocument is the build half. A caller driving the
// compiler through this protocol has only the answer to route on, so a refusal
// that reaches neither the text nor the structured half leaves it with a
// resolution failure about a file that is correct.
func TestBuildToolNamesTheRefusedDocument(t *testing.T) {
	root, dirs, releasePath, recipePath := refusedRecipeWorkspace(t)
	s := newTestServer(t)

	res := callTool(t, s, "build", buildArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if !res.IsError {
		t.Fatalf("a Release reaching a refused Recipe must refuse, or this is not the case the assertions "+
			"below are about:\n%s", res.Content[0].Text)
	}
	requireNamesTheRefusedRecipe(t, "the build tool", res.Content[0].Text, recipePath)
}

// TestValidateReleaseToolNamesTheRefusedDocument is the release-scope validate
// half. It resolves the same closure through a different entry point, so a
// repair applied at one of the two would leave this one reporting the reference
// alone.
func TestValidateReleaseToolNamesTheRefusedDocument(t *testing.T) {
	root, dirs, releasePath, recipePath := refusedRecipeWorkspace(t)
	s := newTestServer(t)

	res := callTool(t, s, "validate", validateArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if !res.IsError {
		t.Fatalf("validation over a Release reaching a refused Recipe must refuse:\n%s", res.Content[0].Text)
	}
	requireNamesTheRefusedRecipe(t, "the validate tool at release scope", res.Content[0].Text, recipePath)
}

// TestValidateEntityToolTextCarriesItsVerdict is the entity scope, where the
// refusal was never absent — it was in the structured half only.
//
// One capability answering at two fidelities depending on which half the caller
// reads is the defect: the half a person reads named the file and its kind and
// stopped, while the half a program reads carried the condition, its short code
// and the constraint. The assertion is the equality rather than the presence,
// because presence alone would pass on a text half carrying some other sentence.
func TestValidateEntityToolTextCarriesItsVerdict(t *testing.T) {
	root, dirs, _, recipePath := refusedRecipeWorkspace(t)
	s := newTestServer(t)

	res := callTool(t, s, "validate", validateArgs{Entity: recipePath, WorkspaceRoot: root, CatalogDirs: dirs})
	payload := unmarshalValidatePayload(t, res)
	if payload.Valid {
		t.Fatalf("the refused Recipe must not validate, or this case covers nothing:\n%s", res.Content[0].Text)
	}
	if len(payload.Diagnostics) == 0 {
		t.Fatal("the structured half carries no diagnostic, so there is nothing for the text half to match")
	}
	for _, d := range payload.Diagnostics {
		if !strings.Contains(res.Content[0].Text, d) {
			t.Errorf("the text half drops a diagnostic the structured half carries.\n--- missing ---\n%s\n--- text ---\n%s",
				d, res.Content[0].Text)
		}
	}
}
