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
)

// TestReleaseCandidatesNameEachLinkedDirectoryOutsideTheRoot drives
// aggrega/releaseCandidates over real workspace roots holding symbolic links and
// reads unreadLinkedDirs back off the wire.
//
// Each row lays one root beside one directory outside it that holds a Release of
// its own, so a row that named the wrong link, or descended into one, shows up as
// a wrong list or as that outside Release appearing among the listed ones. The
// list is compared in order, because the order is the walk's and is part of the
// answer: the ordering row is laid so that the walk's order and a plain sort of
// the same names disagree.
//
// Every row also pins that the field reaches the wire as an array, never as a
// null, and that the Releases listed are exactly the ones inside the root, at
// their own paths.
func TestReleaseCandidatesNameEachLinkedDirectoryOutsideTheRoot(t *testing.T) {
	t.Parallel()

	const release = "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: x\n"

	rows := []struct {
		name string
		// lay builds the row's links and files under root; outside is a directory
		// beside the root holding outside-release.yaml and a file, notes.txt.
		lay func(t *testing.T, root, outside string)
		// wantUnread is the field, in order.
		wantUnread []string
		// wantReleases are the Releases listed, relative to root, in walk order.
		wantReleases []string
	}{
		{
			name: "a link to a directory outside the root is named where it sits",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, outside, filepath.Join(root, "shared"))
			},
			wantUnread:   []string{"shared"},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "a link in a subdirectory is named by its path from the root",
			lay: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(root, "envs"))
				plantLink(t, outside, filepath.Join(root, "envs", "shared"))
			},
			wantUnread:   []string{"envs/shared"},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "a link given relative to where it sits is named when it leads out",
			lay: func(t *testing.T, root, outside string) {
				rel, err := filepath.Rel(root, outside)
				if err != nil {
					t.Fatal(err)
				}
				plantLink(t, rel, filepath.Join(root, "shared"))
			},
			wantUnread:   []string{"shared"},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "a link to a directory inside the root is not named, and what it leads to is listed at its own path",
			lay: func(t *testing.T, root, outside string) {
				plantFile(t, filepath.Join(root, "real", "staging.yaml"), release)
				plantLink(t, filepath.Join(root, "real"), filepath.Join(root, "alias"))
			},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml", "real/staging.yaml"},
		},
		{
			name: "a link to the root itself is not named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, root, filepath.Join(root, "self"))
			},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "a link inside a directory the walk leaves out is not named",
			lay: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(root, "node_modules"))
				plantLink(t, outside, filepath.Join(root, "node_modules", "shared"))
			},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "a link inside a hidden directory is not named",
			lay: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(root, ".cache"))
				plantLink(t, outside, filepath.Join(root, ".cache", "shared"))
			},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "a link bearing a name the walk leaves out is not named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, outside, filepath.Join(root, "vendor"))
			},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "a link to a file outside the root is not named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "notes.txt"), filepath.Join(root, "notes.txt"))
			},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "a link leading nowhere is not named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "missing"), filepath.Join(root, "gone"))
			},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "a link pointing at itself is not named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(root, "loop"), filepath.Join(root, "loop"))
			},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "two links pointing at each other are not named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(root, "there"), filepath.Join(root, "here"))
				plantLink(t, filepath.Join(root, "here"), filepath.Join(root, "there"))
			},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name: "several links are named in the order the walk meets them",
			lay: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(root, "a"))
				plantLink(t, outside, filepath.Join(root, "b-link"))
				plantLink(t, outside, filepath.Join(root, "a-c"))
				plantLink(t, outside, filepath.Join(root, "a", "z-link"))
			},
			wantUnread:   []string{"a/z-link", "a-c", "b-link"},
			wantReleases: []string{"app-release.yaml"},
		},
		{
			name:         "a workspace holding no link names none",
			lay:          func(t *testing.T, root, outside string) {},
			wantUnread:   []string{},
			wantReleases: []string{"app-release.yaml"},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			outside := t.TempDir()
			plantFile(t, filepath.Join(root, "app-release.yaml"), release)
			plantFile(t, filepath.Join(outside, "outside-release.yaml"), release)
			plantFile(t, filepath.Join(outside, "notes.txt"), "notes\n")
			row.lay(t, root, outside)

			srv := newTestServer(t, root, nil, nil)
			var in, out bytes.Buffer
			in.Write(framedRequest(1, "aggrega/releaseCandidates", map[string]any{}))
			resp := runOneRequest(t, srv, &in, &out)
			if resp.Error != nil {
				t.Fatalf("releaseCandidates refused: %+v", resp.Error)
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(resp.Result, &raw); err != nil {
				t.Fatalf("unmarshal raw result: %v (raw: %s)", err, resp.Result)
			}
			encoded, present := raw["unreadLinkedDirs"]
			if !present {
				t.Fatalf("unreadLinkedDirs is absent from the answer; want it present, empty where nothing is named (raw: %s)", resp.Result)
			}
			if trimmed := strings.TrimSpace(string(encoded)); !strings.HasPrefix(trimmed, "[") {
				t.Fatalf("unreadLinkedDirs reached the wire as %s; want an array, empty where nothing is named", trimmed)
			}

			var got releaseCandidatesResult
			if err := json.Unmarshal(resp.Result, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			if strings.Join(got.UnreadLinkedDirs, "\n") != strings.Join(row.wantUnread, "\n") || len(got.UnreadLinkedDirs) != len(row.wantUnread) {
				t.Errorf("unreadLinkedDirs = %q; want %q", got.UnreadLinkedDirs, row.wantUnread)
			}

			var listed []string
			for _, abs := range got.Releases {
				rel, err := filepath.Rel(root, abs)
				if err != nil {
					t.Fatal(err)
				}
				listed = append(listed, filepath.ToSlash(rel))
			}
			if strings.Join(listed, "\n") != strings.Join(row.wantReleases, "\n") {
				t.Errorf("releases listed %q; want %q — a link was descended into, or a Release at its own path was missed", listed, row.wantReleases)
			}
		})
	}
}

// plantLink creates a symbolic link at at pointing to target.
func plantLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Fatalf("create the link %s -> %s: %v", at, target, err)
	}
}

// plantDir creates dir and its parents.
func plantDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// plantFile writes content to path, creating its parent directories.
func plantFile(t *testing.T, path, content string) {
	t.Helper()
	plantDir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
