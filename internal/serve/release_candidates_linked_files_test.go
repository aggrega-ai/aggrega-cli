// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestReleaseCandidatesNameEachLinkedFileOutsideTheRoot drives
// aggrega/releaseCandidates and then aggrega/activeRelease over real workspace
// roots holding symbolic links to files, each root resolved from a real
// configuration file, and reads back the Releases listed, unreadLinkedFiles and
// unreadLinkedDirs, and the Release the cascade settled on.
//
// Each row lays one root beside one directory outside it holding a Release of its
// own, outside-release.yaml, and a YAML file that is not a Release,
// outside-values.yaml. A link leading to either is not read and is named, unless
// the configuration names the file it leads to as a Release; a row that read one
// shows up as that link among the Releases listed, and — where the root holds no
// other Release — as the cascade settling on it. The lists are compared in order,
// because the order is the walk's: the ordering row is laid so that the walk's
// order and a plain sort of the same names disagree.
//
// Every row also pins that unreadLinkedFiles reaches the wire as an array, never
// as a null.
func TestReleaseCandidatesNameEachLinkedFileOutsideTheRoot(t *testing.T) {
	t.Parallel()

	const noConfiguration = "workspace: {}\n"

	rows := []struct {
		name string
		// config is the root's configuration file; it may name places under root
		// and outside. Nil writes one naming no Release.
		config func(t *testing.T, root, outside string) string
		// lay builds the row's files and links under root.
		lay func(t *testing.T, root, outside string)
		// wantFiles and wantDirs are the two fields, in order.
		wantFiles []string
		wantDirs  []string
		// wantReleases are the Releases listed, relative to root, in walk order.
		wantReleases []string
		// wantActive is the Release the cascade settles on; nil for none.
		wantActive func(root, outside string) string
	}{
		{
			name: "a link to a Release file outside the root is not read, and is named where it sits",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles:    []string{"linked-release.yaml"},
			wantDirs:     []string{},
			wantReleases: nil,
		},
		{
			name: "a link given relative to where it sits is named when it leads out",
			lay: func(t *testing.T, root, outside string) {
				rel, err := filepath.Rel(root, filepath.Join(outside, "outside-release.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				plantLink(t, rel, filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles:    []string{"linked-release.yaml"},
			wantDirs:     []string{},
			wantReleases: nil,
		},
		{
			name: "a link in a subdirectory is named by its path from the root, and the Release inside is the one settled on",
			lay: func(t *testing.T, root, outside string) {
				writeRelease(t, root, "app-release.yaml", "release/app")
				plantDir(t, filepath.Join(root, "envs"))
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "envs", "linked.yaml"))
			},
			wantFiles:    []string{"envs/linked.yaml"},
			wantDirs:     []string{},
			wantReleases: []string{"app-release.yaml"},
			wantActive:   inRoot("app-release.yaml"),
		},
		{
			name: "a link to a YAML file outside that is not a Release is named too, since its kind is not known without reading it",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "outside-values.yaml"), filepath.Join(root, "values.yaml"))
			},
			wantFiles:    []string{"values.yaml"},
			wantDirs:     []string{},
			wantReleases: nil,
		},
		{
			name: "build.authoring_release naming the link has it read and listed",
			config: func(t *testing.T, root, outside string) string {
				return "workspace: {}\nbuild:\n  authoring_release: linked-release.yaml\n"
			},
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: []string{"linked-release.yaml"},
			wantActive:   inRoot("linked-release.yaml"),
		},
		{
			name: "build.authoring_release naming the file the link leads to has the link read and listed",
			config: func(t *testing.T, root, outside string) string {
				return "workspace: {}\nbuild:\n  authoring_release: " + filepath.Join(outside, "outside-release.yaml") + "\n"
			},
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: []string{"linked-release.yaml"},
			wantActive: func(root, outside string) string {
				return filepath.Join(outside, "outside-release.yaml")
			},
		},
		{
			name: "a workspace.releases key naming the link has it read and listed, and the cascade settles on it as the only Release",
			config: func(t *testing.T, root, outside string) string {
				return "workspace:\n  releases:\n    linked-release.yaml: {}\n"
			},
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: []string{"linked-release.yaml"},
			wantActive:   inRoot("linked-release.yaml"),
		},
		{
			name: "a workspace.releases key naming the file the link leads to has the link read and listed",
			config: func(t *testing.T, root, outside string) string {
				rel, err := filepath.Rel(root, filepath.Join(outside, "outside-release.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				return "workspace:\n  releases:\n    " + filepath.ToSlash(rel) + ": {}\n"
			},
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: []string{"linked-release.yaml"},
			wantActive:   inRoot("linked-release.yaml"),
		},
		{
			name: "the configuration naming one linked file leaves another link leading out unread",
			config: func(t *testing.T, root, outside string) string {
				return "workspace:\n  releases:\n    linked-release.yaml: {}\n"
			},
			lay: func(t *testing.T, root, outside string) {
				writeRelease(t, outside, "second-release.yaml", "release/second")
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
				plantLink(t, filepath.Join(outside, "second-release.yaml"), filepath.Join(root, "other.yaml"))
			},
			wantFiles:    []string{"other.yaml"},
			wantDirs:     []string{},
			wantReleases: []string{"linked-release.yaml"},
			wantActive:   inRoot("linked-release.yaml"),
		},
		{
			name: "a link to a Release file inside the root is read at the link's place, and its file at its own",
			lay: func(t *testing.T, root, outside string) {
				writeRelease(t, root, "real/staging.yaml", "release/staging")
				plantLink(t, filepath.Join(root, "real", "staging.yaml"), filepath.Join(root, "alias.yaml"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: []string{"alias.yaml", "real/staging.yaml"},
		},
		{
			name: "a link to a directory outside the root is named among the directories, not the files",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, outside, filepath.Join(root, "shared"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{"shared"},
			wantReleases: nil,
		},
		{
			name: "a link named like a YAML file that leads to a directory outside is named among the directories only",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, outside, filepath.Join(root, "shared.yaml"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{"shared.yaml"},
			wantReleases: nil,
		},
		{
			name: "a link leading nowhere is neither read nor named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "missing.yaml"), filepath.Join(root, "gone.yaml"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: nil,
		},
		{
			name: "a link pointing at itself is neither read nor named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(root, "loop.yaml"), filepath.Join(root, "loop.yaml"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: nil,
		},
		{
			name: "a link inside a directory the walk leaves out is never met",
			lay: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(root, "node_modules"))
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "node_modules", "linked.yaml"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: nil,
		},
		{
			name: "a link to a file outside that is not YAML is neither read nor named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "notes.txt"), filepath.Join(root, "notes.txt"))
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: nil,
		},
		{
			name: "several links are named in the order the walk meets them",
			lay: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(root, "a"))
				target := filepath.Join(outside, "outside-release.yaml")
				plantLink(t, target, filepath.Join(root, "b.yaml"))
				plantLink(t, target, filepath.Join(root, "a-c.yaml"))
				plantLink(t, target, filepath.Join(root, "a", "z.yaml"))
			},
			wantFiles:    []string{"a/z.yaml", "a-c.yaml", "b.yaml"},
			wantDirs:     []string{},
			wantReleases: nil,
		},
		{
			name: "a workspace holding no link names none",
			lay: func(t *testing.T, root, outside string) {
				writeRelease(t, root, "app-release.yaml", "release/app")
			},
			wantFiles:    []string{},
			wantDirs:     []string{},
			wantReleases: []string{"app-release.yaml"},
			wantActive:   inRoot("app-release.yaml"),
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			outside := t.TempDir()
			writeRelease(t, outside, "outside-release.yaml", "release/outside")
			plantFile(t, filepath.Join(outside, "outside-values.yaml"), "replicas: 3\n")
			plantFile(t, filepath.Join(outside, "notes.txt"), "notes\n")
			row.lay(t, root, outside)
			config := noConfiguration
			if row.config != nil {
				config = row.config(t, root, outside)
			}
			srv := linkedFilesServer(t, root, config)

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
			encoded, present := raw["unreadLinkedFiles"]
			if !present {
				t.Fatalf("unreadLinkedFiles is absent from the answer; want it present, empty where nothing is named (raw: %s)", resp.Result)
			}
			if trimmed := strings.TrimSpace(string(encoded)); !strings.HasPrefix(trimmed, "[") {
				t.Fatalf("unreadLinkedFiles reached the wire as %s; want an array, empty where nothing is named", trimmed)
			}

			var got releaseCandidatesResult
			if err := json.Unmarshal(resp.Result, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			if !sameInOrder(got.UnreadLinkedFiles, row.wantFiles) {
				t.Errorf("unreadLinkedFiles = %q; want %q", got.UnreadLinkedFiles, row.wantFiles)
			}
			if !sameInOrder(got.UnreadLinkedDirs, row.wantDirs) {
				t.Errorf("unreadLinkedDirs = %q; want %q", got.UnreadLinkedDirs, row.wantDirs)
			}
			var listed []string
			for _, abs := range got.Releases {
				rel, err := filepath.Rel(root, abs)
				if err != nil {
					t.Fatal(err)
				}
				listed = append(listed, filepath.ToSlash(rel))
			}
			if !sameInOrder(listed, row.wantReleases) {
				t.Errorf("releases listed %q; want %q — a link leading out was read, or one the configuration names was not",
					listed, row.wantReleases)
			}

			want := ""
			if row.wantActive != nil {
				want = row.wantActive(root, outside)
			}
			if active := activeReleaseAnswer(t, srv); active.Release != want {
				t.Errorf("the cascade settled on %q; want %q", active.Release, want)
			}
		})
	}
}

// linkedFilesServer builds a daemon over root from a real configuration file,
// written to root with content config and resolved the way a workspace is
// resolved for a request, so a workspace.releases key reaches the walk in the
// form the configuration's own loading gives it. The machine-wide layer is a
// file of the test's own, read through a frozen lookup, so nothing on the
// machine running the test takes part.
func linkedFilesServer(t *testing.T, root, config string) *Server {
	t.Helper()
	plantFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), config)
	machineWide := filepath.Join(t.TempDir(), "machine-wide.yaml")
	plantFile(t, machineWide, "registries: []\n")
	lookup := func(key string) string {
		if key == workspaceconfig.EnvConfigOverride {
			return machineWide
		}
		return ""
	}
	rw, err := workspaceconfig.Resolve(root, root, lookup)
	if err != nil {
		t.Fatalf("resolve the workspace at %s: %v", root, err)
	}
	srv := newTestServer(t, root, nil, nil)
	srv.wsConfig = rw.Config()
	srv.resolvedWorkspace = rw
	return srv
}

// inRoot returns the expectation that the cascade settles on rel under root.
func inRoot(rel string) func(root, outside string) string {
	return func(root, _ string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
}

// sameInOrder reports whether got and want hold the same names in the same
// order, a nil list and an empty one alike.
func sameInOrder(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
