// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// catalogPassFeature is a Feature whole-catalog correctness accepts; with extends
// naming an identity no catalog holds it is one the extends gate refuses.
func catalogPassFeature(extends string) string {
	doc := `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/secret-value
  version: 1.0.0
  description: A single secret value delivered to the attached component.
  labels:
    feature.aggrega.ai/kind: secret
spec:
`
	if extends != "" {
		doc += "  extends: " + extends + "\n"
	}
	return doc + `  schema:
    values:
      key:
        type: string
        required: true
        description: Environment variable name the credential is delivered as.
`
}

// catalogPassesWorkspace stands a workspace whose Releases read catalog variants
// of their own: a clean one, a broken one, one outside the workspace root shared
// by no other Release, and a Release whose entry switches its catalog off. It
// returns the workspace root.
func catalogPassesWorkspace(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	put := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	put(filepath.Join(root, workspaceconfig.ConfigFilename), `workspace:
  releases:
    release-a.yaml:
      catalog_dirs:
        - ./variant-a
    release-b.yaml:
      catalog_dirs:
        - ./variant-b
    release-elsewhere.yaml:
      catalog_dirs:
        - ../elsewhere
    release-off.yaml:
      catalog_dirs: []
`)
	for _, r := range []string{"release-a.yaml", "release-b.yaml", "release-elsewhere.yaml", "release-off.yaml"} {
		put(filepath.Join(root, r),
			"apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: probe/"+strings.TrimSuffix(r, ".yaml")+"\n  version: 1.0.0\nspec: {}\n")
	}
	put(filepath.Join(root, "variant-a", "feature", "secret-value.yaml"), catalogPassFeature(""))
	put(filepath.Join(root, "variant-b", "feature", "secret-value.yaml"), catalogPassFeature("core/no-such-parent"))
	put(filepath.Join(base, "elsewhere", "feature", "secret-value.yaml"), catalogPassFeature(""))
	return root
}

// catalogScopeOverAListener calls the validate tool's whole-catalog scope over a
// real listener serving root, presenting a credential that carries the read
// scope alone, and returns the tool's own result.
//
// The credential is not incidental. A call reaching a listener with none is
// refused before any tool runs, so a row sending one would measure the admission
// rather than the passes and the locations it came to measure.
func catalogScopeOverAListener(t *testing.T, root string) *callToolResult {
	t.Helper()
	f := listenerOverWorkspace(t, root)
	got := f.callTool(t, f.grantOf(t, scopeRead), "validate", map[string]any{"catalog": true})
	if got.status != http.StatusOK {
		t.Fatalf("the catalog scope was answered %d over the listener (body %q)", got.status, got.raw)
	}
	if got.frame.Error != nil {
		t.Fatalf("the catalog scope was answered with an error envelope over the listener: %+v", got.frame.Error)
	}
	var result struct {
		Content           []toolTextContent `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
		IsError           bool              `json:"isError"`
	}
	if err := json.Unmarshal(got.frame.Result, &result); err != nil {
		t.Fatalf("the answer is not a tool result: %v (body %q)", err, got.raw)
	}
	if len(result.Content) == 0 || result.Content[0].Type != "text" {
		t.Fatalf("the catalog scope answered over the listener with no text block (body %q)", got.raw)
	}
	return &callToolResult{Content: result.Content, StructuredContent: result.StructuredContent, IsError: result.IsError}
}

// TestValidateToolCatalogScopeRunsOncePerRelease pins the tool's whole-catalog
// scope in a workspace whose Releases read catalog directories of their own: the
// same passes `aggrega validate` runs, each naming its Releases and the
// directories it read with its own verdict, the broken variant failing its pass
// and no other, the pass reading nothing valid and empty, and the call's own
// verdict covering every pass. Over the process's own streams a directory is told
// as it is held; over a listener, one inside the workspace by its name relative to
// the root and one outside it not at all.
func TestValidateToolCatalogScopeRunsOncePerRelease(t *testing.T) {
	root := catalogPassesWorkspace(t)
	elsewhere := filepath.Join(filepath.Dir(root), "elsewhere")

	cases := []struct {
		name     string
		listener bool
		wantA    string
		wantElse string
	}{
		{name: "over the process's own streams", wantA: filepath.Join(root, "variant-a"), wantElse: elsewhere},
		{name: "over a listener", listener: true, wantA: "variant-a", wantElse: keptBackLocationWords},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res *callToolResult
			if tc.listener {
				res = catalogScopeOverAListener(t, root)
			} else {
				res = callTool(t, newTestServer(t), "validate", validateArgs{Catalog: true, WorkspaceRoot: root})
			}
			if res.IsError {
				t.Fatalf("the catalog scope refused the call: %s", res.Content[0].Text)
			}
			payload := unmarshalValidatePayload(t, res)

			if payload.Scope != validateScopeCatalog || payload.Valid {
				t.Errorf("scope %q valid %v; want the catalog scope, invalid because one pass failed", payload.Scope, payload.Valid)
			}
			if len(payload.Diagnostics) != 1 || !strings.Contains(payload.Diagnostics[0], "core/no-such-parent") {
				t.Errorf("the call's diagnostics are %v; want the broken variant's one extends finding", payload.Diagnostics)
			}
			if len(payload.Passes) != 4 {
				t.Fatalf("the answer carries %d pass(es); want 4: %+v", len(payload.Passes), payload.Passes)
			}
			want := []struct {
				release string
				dirs    []string
				valid   bool
				diags   int
			}{
				{"release-a.yaml", []string{tc.wantA}, true, 0},
				{"release-b.yaml", nil, false, 1},
				{"release-elsewhere.yaml", []string{tc.wantElse}, true, 0},
				{"release-off.yaml", []string{}, true, 0},
			}
			for i, w := range want {
				got := payload.Passes[i]
				if strings.Join(got.Releases, ",") != w.release {
					t.Errorf("pass %d was run for %v; want %s", i, got.Releases, w.release)
				}
				if w.dirs != nil && (got.CatalogDirs == nil || strings.Join(got.CatalogDirs, ",") != strings.Join(w.dirs, ",")) {
					t.Errorf("pass %d read %#v; want %#v", i, got.CatalogDirs, w.dirs)
				}
				if got.Valid != w.valid || len(got.Diagnostics) != w.diags {
					t.Errorf("pass %d valid %v with %d diagnostic(s); want %v and %d: %v",
						i, got.Valid, len(got.Diagnostics), w.valid, w.diags, got.Diagnostics)
				}
			}
			if !strings.Contains(res.Content[0].Text, "Release release-off.yaml reads no catalog directory") {
				t.Errorf("the text does not report the pass reading nothing:\n%s", res.Content[0].Text)
			}
		})
	}
}

// TestValidateToolCatalogScopeOverASingleCatalogCarriesNoPasses is the other shape
// of workspace, and the positive control for the one above: a workspace naming no
// Release under `workspace.releases` is validated once over its own stack and
// answered with no passes key, exactly as it always was.
func TestValidateToolCatalogScopeOverASingleCatalogCarriesNoPasses(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, _ := fullstackWorkspace(t)

	res := callTool(t, s, "validate", validateArgs{Catalog: true, WorkspaceRoot: root, CatalogDirs: []string{catalogDir}})
	if res.IsError {
		t.Fatalf("validate catalog scope reported protocol error: %s", res.Content[0].Text)
	}
	if strings.Contains(string(res.StructuredContent), `"passes"`) {
		t.Errorf("a workspace naming no Release was answered with passes: %s", res.StructuredContent)
	}
	if strings.Contains(res.Content[0].Text, "once for each Release") {
		t.Errorf("a single catalog's text speaks of Releases:\n%s", res.Content[0].Text)
	}
}
