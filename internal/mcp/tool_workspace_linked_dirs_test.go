// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheReleaseListingNamesEachLinkedDirectoryOutsideTheWorkspace drives
// list-releases through the real tools/call dispatch over workspace roots holding
// symbolic links, on both transports, and reads unread_linked_dirs and the
// answer's text back.
//
// Each row lays its root beside one directory outside it that holds a Release of
// its own, so a row that named the wrong link, or listed what lies behind one,
// shows up as a wrong list, a wrong count, or that outside Release among the
// paths. The list is compared in order, because the order is the walk's: the
// ordering row is laid so that the walk's order and a plain sort disagree.
//
// Every row also pins three things whatever it lays: the field reaches the caller
// as an array, never as null; the place a link leads to is named nowhere in the
// answer, structured or text, on either transport; and the text carries the one
// line naming the links exactly where the field names any, and no such line where
// it names none.
func TestTheReleaseListingNamesEachLinkedDirectoryOutsideTheWorkspace(t *testing.T) {
	const release = "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: probe\n  version: \"1.0.0\"\n"
	const linesOpening = "No Release inside these links is listed"

	machineWide := filepath.Join(t.TempDir(), "machine-wide.yaml")
	writeLinkedDirsFile(t, machineWide, "registries: []\n")
	t.Setenv(workspaceconfig.EnvConfigOverride, machineWide)

	rows := []struct {
		name      string
		transport transportMode
		args      map[string]any
		// releases are the Release files written under the root, relative to it.
		releases []string
		// lay builds the row's links under root; outside is the directory beside
		// it holding outside-release.yaml.
		lay        func(t *testing.T, root, outside string)
		wantUnread []string
		wantCount  int
	}{
		{
			name:      "a link to a directory outside the root is named in the summary",
			transport: transportStdio,
			args:      map[string]any{},
			releases:  []string{"app-release.yaml"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, outside, filepath.Join(root, "shared"))
			},
			wantUnread: []string{"shared"},
			wantCount:  1,
		},
		{
			name:      "a link to a directory outside the root is named beside the records",
			transport: transportStdio,
			args:      map[string]any{"response_format": "detailed"},
			releases:  []string{"app-release.yaml"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, outside, filepath.Join(root, "shared"))
			},
			wantUnread: []string{"shared"},
			wantCount:  1,
		},
		{
			name:      "a page from the middle still carries every link, since the links describe the workspace",
			transport: transportStdio,
			args:      map[string]any{"response_format": "detailed", "limit": 1, "offset": 1},
			releases:  []string{"app-release.yaml", "envs/prod.yaml"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, outside, filepath.Join(root, "shared"))
			},
			wantUnread: []string{"shared"},
			wantCount:  2,
		},
		{
			name:      "links are named by their path from the root, in the order the walk meets them",
			transport: transportStdio,
			args:      map[string]any{},
			releases:  []string{"app-release.yaml"},
			lay: func(t *testing.T, root, outside string) {
				if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
					t.Fatal(err)
				}
				linkLinkedDirs(t, outside, filepath.Join(root, "b-link"))
				linkLinkedDirs(t, outside, filepath.Join(root, "a-c"))
				linkLinkedDirs(t, outside, filepath.Join(root, "a", "z-link"))
			},
			wantUnread: []string{"a/z-link", "a-c", "b-link"},
			wantCount:  1,
		},
		{
			name:      "a link to a directory inside the root is not named, and what it leads to is listed at its own path",
			transport: transportStdio,
			args:      map[string]any{"response_format": "detailed"},
			releases:  []string{"app-release.yaml", "real/staging.yaml"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(root, "real"), filepath.Join(root, "alias"))
			},
			wantUnread: []string{},
			wantCount:  2,
		},
		{
			name:      "a link bearing a name the walk leaves out is not named",
			transport: transportStdio,
			args:      map[string]any{},
			releases:  []string{"app-release.yaml"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, outside, filepath.Join(root, "vendor"))
			},
			wantUnread: []string{},
			wantCount:  1,
		},
		{
			name:       "a workspace holding no link names none and says nothing about links",
			transport:  transportStdio,
			args:       map[string]any{},
			releases:   []string{"app-release.yaml"},
			lay:        func(t *testing.T, root, outside string) {},
			wantUnread: []string{},
			wantCount:  1,
		},
		{
			name:      "a workspace whose only Release sits behind such a link lists none and says why",
			transport: transportStdio,
			args:      map[string]any{},
			releases:  nil,
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, outside, filepath.Join(root, "shared"))
			},
			wantUnread: []string{"shared"},
			wantCount:  0,
		},
		{
			name:      "over a listener the same names are told, and nothing about where they lead",
			transport: transportHTTP,
			args:      map[string]any{},
			releases:  []string{"app-release.yaml"},
			lay: func(t *testing.T, root, outside string) {
				if err := os.MkdirAll(filepath.Join(root, "envs"), 0o755); err != nil {
					t.Fatal(err)
				}
				linkLinkedDirs(t, outside, filepath.Join(root, "envs", "shared"))
				linkLinkedDirs(t, outside, filepath.Join(root, "shared"))
			},
			wantUnread: []string{"envs/shared", "shared"},
			wantCount:  1,
		},
		{
			name:      "over a listener the detailed records carry them too",
			transport: transportHTTP,
			args:      map[string]any{"response_format": "detailed"},
			releases:  []string{"app-release.yaml"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, outside, filepath.Join(root, "shared"))
			},
			wantUnread: []string{"shared"},
			wantCount:  1,
		},
		{
			name:       "over a listener a workspace holding no link names none",
			transport:  transportHTTP,
			args:       map[string]any{},
			releases:   []string{"app-release.yaml"},
			lay:        func(t *testing.T, root, outside string) {},
			wantUnread: []string{},
			wantCount:  1,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			writeLinkedDirsFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\noffline: true\n")
			writeLinkedDirsFile(t, filepath.Join(outside, "outside-release.yaml"), release)
			for _, rel := range row.releases {
				writeLinkedDirsFile(t, filepath.Join(root, filepath.FromSlash(rel)), release)
			}
			row.lay(t, root, outside)

			s := &server{out: io.Discard, logw: io.Discard, launchRoot: root, transport: row.transport}
			res := callToolOn(t, s, listenerRequest(), "list-releases", row.args)
			if res.IsError {
				t.Fatalf("list-releases refused: %s", res.Content[0].Text)
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(res.StructuredContent, &raw); err != nil {
				t.Fatalf("decode the structured answer: %v (%s)", err, res.StructuredContent)
			}
			encoded, present := raw["unread_linked_dirs"]
			if !present {
				t.Fatalf("unread_linked_dirs is absent from the answer; want it present, empty where nothing is named: %s",
					res.StructuredContent)
			}
			if trimmed := strings.TrimSpace(string(encoded)); !strings.HasPrefix(trimmed, "[") {
				t.Fatalf("unread_linked_dirs reached the caller as %s; want an array, empty where nothing is named", trimmed)
			}

			var got listReleasesResult
			decodeStructured(t, res, &got)
			if strings.Join(got.UnreadLinkedDirs, "\n") != strings.Join(row.wantUnread, "\n") ||
				len(got.UnreadLinkedDirs) != len(row.wantUnread) {
				t.Errorf("unread_linked_dirs = %q; want %q", got.UnreadLinkedDirs, row.wantUnread)
			}
			if got.Count != row.wantCount {
				t.Errorf("count = %d; want %d — a link was descended into, or a Release at its own path was missed",
					got.Count, row.wantCount)
			}
			for _, path := range got.Releases {
				if strings.Contains(path, "outside-release") {
					t.Errorf("the listing carries %q, a Release behind a link leading out of the workspace", path)
				}
			}
			if row.transport == transportHTTP && (got.WorkspaceRoot != nil || !got.WorkspaceRootWithheld) {
				t.Errorf("over a listener the workspace root was told (withheld=%v); want it kept back and marked",
					got.WorkspaceRootWithheld)
			}

			// The place a link leads to is told to no caller; the root is told at the
			// keyboard and kept back from a listener.
			text := res.Content[0].Text
			untold := []string{outside}
			if row.transport == transportHTTP {
				untold = append(untold, root)
			}
			for _, place := range untold {
				if strings.Contains(text, place) || strings.Contains(string(res.StructuredContent), place) {
					t.Errorf("the answer names %s, a place a caller here is not told:\ntext: %s\nstructured: %s",
						place, text, res.StructuredContent)
				}
			}
			if len(row.wantUnread) == 0 {
				if strings.Contains(text, linesOpening) {
					t.Errorf("the text names links though the answer names none:\n%s", text)
				}
				return
			}
			line := linesOpening + ", because each leads to a directory outside the workspace and none is searched: " +
				strings.Join(row.wantUnread, ", ")
			if !strings.HasSuffix(text, "\n\n"+line) {
				t.Errorf("the text does not close with the line naming the links; want it to end with\n  %q\nin:\n%s",
					line, text)
			}
		})
	}
}

// linkLinkedDirs creates a symbolic link at at pointing to target.
func linkLinkedDirs(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Fatalf("create the link %s -> %s: %v", at, target, err)
	}
}

// writeLinkedDirsFile writes content to path, creating its parent directories.
func writeLinkedDirsFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
