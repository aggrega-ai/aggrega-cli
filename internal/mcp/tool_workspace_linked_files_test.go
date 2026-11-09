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

// TestTheReleaseListingNamesEachLinkedFileOutsideTheWorkspace drives list-releases
// through the real tools/call dispatch over workspace roots holding symbolic links
// to files, on both transports, each root carrying a real configuration file, and
// reads unread_linked_files, the Releases listed and the answer's text back.
//
// Each row lays its root beside one directory outside it holding a Release of its
// own. A link leading to it is not read and is named, unless the configuration
// names that Release; a row that read one shows up as a count too high, or as the
// link among the paths, and one that refused a configured Release as a count too
// low.
//
// Every row also pins three things whatever it lays: the field reaches the caller
// as an array, never as null; the place a link leads to is named nowhere in the
// answer, structured or text, on either transport; and the text carries the one
// line naming the linked files exactly where the field names any, closing the
// answer, and no such line where it names none.
func TestTheReleaseListingNamesEachLinkedFileOutsideTheWorkspace(t *testing.T) {
	const release = "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: probe\n  version: \"1.0.0\"\n"
	const filesOpening = "No Release behind these links is listed"
	const dirsLine = "No Release inside these links is listed, because each leads to a directory outside the workspace " +
		"and none is searched: "
	const noConfiguration = "workspace: {}\noffline: true\n"

	machineWide := filepath.Join(t.TempDir(), "machine-wide.yaml")
	writeLinkedDirsFile(t, machineWide, "registries: []\n")
	t.Setenv(workspaceconfig.EnvConfigOverride, machineWide)

	rows := []struct {
		name      string
		transport transportMode
		args      map[string]any
		// config is the root's configuration file.
		config string
		// releases are the Release files written under the root, relative to it.
		releases []string
		// lay builds the row's links under root; outside is the directory beside
		// it holding outside-release.yaml.
		lay       func(t *testing.T, root, outside string)
		wantFiles []string
		wantDirs  []string
		wantCount int
		// wantPaths, where set, are the paths the detailed answer carries.
		wantPaths []string
	}{
		{
			name:      "a workspace whose only Release sits behind a link to a file outside lists none and says why",
			transport: transportStdio,
			args:      map[string]any{},
			config:    noConfiguration,
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles: []string{"linked-release.yaml"},
			wantDirs:  []string{},
			wantCount: 0,
		},
		{
			name:      "a link to a file outside is named beside the records, which carry only the Releases inside",
			transport: transportStdio,
			args:      map[string]any{"response_format": "detailed"},
			config:    noConfiguration,
			releases:  []string{"app-release.yaml"},
			lay: func(t *testing.T, root, outside string) {
				if err := os.MkdirAll(filepath.Join(root, "envs"), 0o755); err != nil {
					t.Fatal(err)
				}
				linkLinkedDirs(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "envs", "linked.yaml"))
			},
			wantFiles: []string{"envs/linked.yaml"},
			wantDirs:  []string{},
			wantCount: 1,
			wantPaths: []string{"app-release.yaml"},
		},
		{
			name:      "a workspace.releases key naming the link has it listed and not named",
			transport: transportStdio,
			args:      map[string]any{"response_format": "detailed"},
			config:    "workspace:\n  releases:\n    linked-release.yaml: {}\noffline: true\n",
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles: []string{},
			wantDirs:  []string{},
			wantCount: 1,
			wantPaths: []string{"linked-release.yaml"},
		},
		{
			name:      "build.authoring_release naming the link has it listed and not named",
			transport: transportStdio,
			args:      map[string]any{"response_format": "detailed"},
			config:    "workspace: {}\nbuild:\n  authoring_release: linked-release.yaml\noffline: true\n",
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles: []string{},
			wantDirs:  []string{},
			wantCount: 1,
			wantPaths: []string{"linked-release.yaml"},
		},
		{
			name:      "a link to a directory and a link to a file, each leading out, are each named in their own list and line",
			transport: transportStdio,
			args:      map[string]any{},
			config:    noConfiguration,
			releases:  []string{"app-release.yaml"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, outside, filepath.Join(root, "shared"))
				linkLinkedDirs(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles: []string{"linked-release.yaml"},
			wantDirs:  []string{"shared"},
			wantCount: 1,
		},
		{
			name:      "a workspace holding no link names none and says nothing about linked files",
			transport: transportStdio,
			args:      map[string]any{},
			config:    noConfiguration,
			releases:  []string{"app-release.yaml"},
			lay:       func(t *testing.T, root, outside string) {},
			wantFiles: []string{},
			wantDirs:  []string{},
			wantCount: 1,
		},
		{
			name:      "over a listener the same link is named, and nothing about where it leads",
			transport: transportHTTP,
			args:      map[string]any{"response_format": "detailed"},
			config:    noConfiguration,
			releases:  []string{"app-release.yaml"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles: []string{"linked-release.yaml"},
			wantDirs:  []string{},
			wantCount: 1,
			wantPaths: []string{"app-release.yaml"},
		},
		{
			name:      "over a listener a link the configuration names is listed at its own place",
			transport: transportHTTP,
			args:      map[string]any{"response_format": "detailed"},
			config:    "workspace:\n  releases:\n    linked-release.yaml: {}\noffline: true\n",
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles: []string{},
			wantDirs:  []string{},
			wantCount: 1,
			wantPaths: []string{"linked-release.yaml"},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			writeLinkedDirsFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), row.config)
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
			encoded, present := raw["unread_linked_files"]
			if !present {
				t.Fatalf("unread_linked_files is absent from the answer; want it present, empty where nothing is named: %s",
					res.StructuredContent)
			}
			if trimmed := strings.TrimSpace(string(encoded)); !strings.HasPrefix(trimmed, "[") {
				t.Fatalf("unread_linked_files reached the caller as %s; want an array, empty where nothing is named", trimmed)
			}

			var got listReleasesResult
			decodeStructured(t, res, &got)
			if strings.Join(got.UnreadLinkedFiles, "\n") != strings.Join(row.wantFiles, "\n") ||
				len(got.UnreadLinkedFiles) != len(row.wantFiles) {
				t.Errorf("unread_linked_files = %q; want %q", got.UnreadLinkedFiles, row.wantFiles)
			}
			if strings.Join(got.UnreadLinkedDirs, "\n") != strings.Join(row.wantDirs, "\n") ||
				len(got.UnreadLinkedDirs) != len(row.wantDirs) {
				t.Errorf("unread_linked_dirs = %q; want %q", got.UnreadLinkedDirs, row.wantDirs)
			}
			if got.Count != row.wantCount {
				t.Errorf("count = %d; want %d — a link leading out was read, or one the configuration names was not",
					got.Count, row.wantCount)
			}
			if row.wantPaths != nil && strings.Join(got.Releases, "\n") != strings.Join(row.wantPaths, "\n") {
				t.Errorf("releases = %q; want %q", got.Releases, row.wantPaths)
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
			if len(row.wantFiles) == 0 {
				if strings.Contains(text, filesOpening) {
					t.Errorf("the text names linked files though the answer names none:\n%s", text)
				}
				return
			}
			line := filesOpening + ", because each leads to a file outside the workspace that the workspace " +
				"configuration does not name as a Release, and none is read: " + strings.Join(row.wantFiles, ", ")
			closing := "\n\n" + line
			if len(row.wantDirs) > 0 {
				closing = "\n\n" + dirsLine + strings.Join(row.wantDirs, ", ") + closing
			}
			if !strings.HasSuffix(text, closing) {
				t.Errorf("the text does not close with the lines naming the links; want it to end with\n  %q\nin:\n%s",
					closing, text)
			}
		})
	}
}
