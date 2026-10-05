// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool's answer to a refusal raised while rendering: its text carries the
// refusal in the compiler's words with every cause, and its payload carries the
// refusal as one entry naming where it points.
//
// Every expectation is read from the fixture this file writes or from the
// condition's own registration, never from the code under test.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// renderRefusalWorkspace copies the end-to-end fixture into a temporary workspace,
// so a fault can be written into its mapped Recipe without touching the fixture
// other tests read.
func renderRefusalWorkspace(t *testing.T) (root, catalogDir, releasePath, recipePath string) {
	t.Helper()
	src, _, _ := fullstackWorkspace(t)
	root = t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	recipePath = filepath.Join(root, "catalog", "recipes", "helm-synthesized-with-artifacts", "api-server.yaml")
	return root, filepath.Join(root, "catalog"), filepath.Join(root, "release.yaml"), recipePath
}

// writeRefusalFault replaces anchor with replacement in the file at path, failing
// when the anchor is gone, so a fixture that moves cannot leave a row passing over
// a workspace that renders.
func writeRefusalFault(t *testing.T, path, anchor, replacement string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(body), anchor) {
		t.Fatalf("%s no longer carries %q; the fault would not be written", path, anchor)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(body), anchor, replacement, 1)), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// authoredKeyPosition is the one-based line and column of the first line of the
// file at path whose text, indentation aside, begins with key.
func authoredKeyPosition(t *testing.T, path, key string) (line, column int) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, text := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimLeft(text, " ")
		if strings.HasPrefix(trimmed, key) {
			return i + 1, len(text) - len(trimmed) + 1
		}
	}
	t.Fatalf("%s carries no line beginning with %q", path, key)
	return 0, 0
}

// TestBuildToolCarriesARenderRefusalInTheCompilersWordsAndWhereItPoints is the
// build tool's half of carrying a refusal raised while rendering: one fault per
// row, the control first.
func TestBuildToolCarriesARenderRefusalInTheCompilersWordsAndWhereItPoints(t *testing.T) {
	for _, tc := range []struct {
		name string
		// write puts the fault into the Recipe at recipePath and returns the key the
		// refusal must point at and the token the fixture authored that its words
		// must carry; the control writes nothing.
		write     func(t *testing.T, recipePath string) (key, token string)
		condition error
	}{
		{
			name:  "a Release that renders",
			write: func(t *testing.T, recipePath string) (string, string) { return "", "" },
		},
		{
			name: "a Workload naming a slot its Component does not declare",
			write: func(t *testing.T, recipePath string) (string, string) {
				writeRefusalFault(t, recipePath, "        image: api-server:latest\n",
					"        image: api-server:latest\n"+
						"        attachments:\n"+
						"          ADMIN_TOKEN:\n"+
						"            from: admin_keyy\n"+
						"            as: env\n")
				return ".spec.workload", "admin_keyy"
			},
			condition: validator.ErrWorkloadFeaturesSchema(),
		},
		{
			name: "a Recipe assertion that fails",
			write: func(t *testing.T, recipePath string) (string, string) {
				writeRefusalFault(t, recipePath, "  provides:\n",
					"  assertions:\n"+
						"    port-is-declared: '{{ fail \"the port of this server must be declared\" }}'\n"+
						"  provides:\n")
				return ".spec.assertions.port-is-declared", "the port of this server must be declared"
			},
			condition: renderer.ErrAssertionFailed(),
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			root, catalogDir, releasePath, recipePath := renderRefusalWorkspace(t)
			key, token := tc.write(t, recipePath)

			res := callTool(t, newTestServer(t), "build", buildArgs{
				Release:       releasePath,
				WorkspaceRoot: root,
				CatalogDirs:   []string{catalogDir},
			})
			payload := unmarshalBuildPayload(t, res)
			text := res.Content[0].Text

			if tc.condition == nil {
				if res.IsError {
					t.Fatalf("the control refused: %s", text)
				}
				if payload.Refusal != nil {
					t.Errorf("a build that rendered carries a refusal entry: %+v", *payload.Refusal)
				}
				return
			}
			if !res.IsError {
				t.Fatalf("the fault did not refuse, so nothing below is about a refusal: %s", text)
			}

			sentence, _ := cerrors.MessageOf(tc.condition)
			code, _ := cerrors.CodeOf(tc.condition)
			shortCode, _ := cerrors.ShortCodeOf(tc.condition)

			// The text: the refusal beneath the outer frame, and the condition's
			// descriptive code a caller matches on.
			for _, want := range []string{token, sentence, code} {
				if !strings.Contains(text, want) {
					t.Errorf("the answer's text drops %q — it reports that the render failed and not why:\n%s",
						want, text)
				}
			}

			// The entry.
			entry := payload.Refusal
			if entry == nil {
				t.Fatalf("the refusal carries no structured entry, so a caller reading fields has only the "+
					"sentence to parse:\n%s", text)
			}
			wantLine, wantColumn := authoredKeyPosition(t, recipePath,
				key[strings.LastIndex(key, ".")+1:]+":")
			if got := told(entry.File); filepath.Clean(got) != filepath.Clean(recipePath) {
				t.Errorf("the entry names %q; the refusal points into %s", got, recipePath)
			}
			if entry.FileWithheld {
				t.Error("the entry withholds its file from a caller over the process's own streams")
			}
			if entry.KeyPath != key {
				t.Errorf("the entry names key %q; want %q", entry.KeyPath, key)
			}
			if entry.Line != wantLine || entry.Column != wantColumn {
				t.Errorf("the entry is at %d:%d; the key is at %d:%d", entry.Line, entry.Column, wantLine, wantColumn)
			}
			if entry.Severity != "halt" {
				t.Errorf("the entry's severity is %q; the finding that stopped a build is at the halting tier", entry.Severity)
			}
			if entry.Code != code || entry.ShortCode != shortCode {
				t.Errorf("the entry names %q / %q; the condition is %q / %q", entry.Code, entry.ShortCode, code, shortCode)
			}
			if !strings.Contains(entry.Message, token) || !strings.Contains(text, entry.Message) {
				t.Errorf("the entry's message is not the refusal the text carries:\nentry: %s\ntext:\n%s",
					entry.Message, text)
			}
		})
	}
}
