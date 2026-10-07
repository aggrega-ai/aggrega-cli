// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// linkRootWorkspace lays one workspace under base/real — a configuration file
// carrying the workspace block, an Aggregate, two Releases, and two links INSIDE it
// leading out of it, one to a Release file and one to a directory holding another
// — and the roots that reach it: base/via, a link to it; base/viarel, a relative
// link to it; and base/up/real, reached through a link above it. It returns base.
func linkRootWorkspace(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	outside := filepath.Join(base, "outside")
	release := func(name string) string {
		return "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: " + name + "\n  version: \"1.0.0\"\n"
	}
	for path, body := range map[string]string{
		filepath.Join(realDir, workspaceconfig.ConfigFilename): "workspace: {}\n",
		filepath.Join(realDir, "aggregate.yaml"):               "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: app\nspec:\n  components: {}\n",
		filepath.Join(realDir, "release-a.yaml"):               release("a"),
		filepath.Join(realDir, "release-b.yaml"):               release("b"),
		filepath.Join(outside, "release-out.yaml"):             release("out"),
		filepath.Join(outside, "dir", "release-in-dir.yaml"):   release("in-dir"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []struct{ target, at string }{
		{filepath.Join(outside, "release-out.yaml"), filepath.Join(realDir, "leads-out.yaml")},
		{filepath.Join(outside, "dir"), filepath.Join(realDir, "leads-out-dir")},
		{realDir, filepath.Join(base, "via")},
		{"real", filepath.Join(base, "viarel")},
		{base, filepath.Join(base, "up")},
	} {
		if err := os.Symlink(l.target, l.at); err != nil {
			t.Skipf("symbolic links are not available here: %v", err)
		}
	}
	return base
}

// TestARootOpenedThroughALinkAnswersAsTheWorkspaceItLeadsTo drives the language
// server through its exported entry point, launched as the editor launches it —
// in the workspace, discovering it — and declares at the handshake, and names on
// every request, a root reaching that workspace one way per row: as itself (the
// control), through a link to it, through a relative link to it, and through a
// link above it. Every row is answered as the workspace it reaches: the handshake
// reports what anchored it, the Release listing and the entity index list its
// documents, and both name the two links inside it leading out as not read — the
// boundary judged through the root as it is judged directly. Every path is spelled
// from the root the request named: a Release absolutely beneath it, an entity and
// an unread link relative to it.
func TestARootOpenedThroughALinkAnswersAsTheWorkspaceItLeadsTo(t *testing.T) {
	base := linkRootWorkspace(t)
	for _, row := range []struct{ name, root string }{
		{"control: the root as itself", "real"},
		{"a root that is a link to the workspace", "via"},
		{"a root that is a relative link to the workspace", "viarel"},
		{"a root reached through a link above it", filepath.Join("up", "real")},
	} {
		t.Run(row.name, func(t *testing.T) {
			pinDaemonOfflineEnvironment(t, "")
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			// The launch discovers the workspace from the directory it runs in, as a
			// process the editor starts in the folder it opened does.
			t.Chdir(filepath.Join(base, "real"))
			root := filepath.Join(base, row.root)

			answers := daemonExchange(t, "", workspaceconfig.FlagOverlay{}, [][]byte{
				framedRequestAt(t, root, 1, "aggrega/initialize", map[string]any{
					"capabilities": map[string]any{}, "workspaceRoot": root}),
				framedRequestAt(t, root, 2, "aggrega/releaseCandidates", map[string]any{}),
				framedRequestAt(t, root, 3, "aggrega/catalogIndex", map[string]any{}),
			})
			for i, a := range answers {
				if a.Error != nil {
					t.Fatalf("request %d refused: %s", i+1, a.Error.Message)
				}
			}

			var hello initializeResult
			decodeLinkRootAnswer(t, answers[0].Result, &hello)
			if hello.WorkspaceRoot != root {
				t.Errorf("the handshake reports the root %q; want the one declared, %q", hello.WorkspaceRoot, root)
			}
			if hello.WorkspaceMarker != workspaceconfig.MarkerConfig.String() {
				t.Errorf("the handshake reports the anchor %q; want %q, what anchored the workspace this root leads to",
					hello.WorkspaceMarker, workspaceconfig.MarkerConfig.String())
			}

			var releases releaseCandidatesResult
			decodeLinkRootAnswer(t, answers[1].Result, &releases)
			wantReleases := []string{filepath.Join(root, "release-a.yaml"), filepath.Join(root, "release-b.yaml")}
			if !slices.Equal(releases.Releases, wantReleases) {
				t.Errorf("aggrega/releaseCandidates lists %v; want %v — the workspace's Releases, spelled beneath "+
					"the root the request named", releases.Releases, wantReleases)
			}
			for _, r := range wantReleases {
				if want := strings.TrimSuffix(r, ".yaml") + ".lock"; releases.Lockfiles[r] != want {
					t.Errorf("the lockfile of %s is %q; want %q", r, releases.Lockfiles[r], want)
				}
			}
			requireUnreadLinks(t, "aggrega/releaseCandidates", releases.UnreadLinkedDirs, releases.UnreadLinkedFiles)

			var index catalogIndexResult
			decodeLinkRootAnswer(t, answers[2].Result, &index)
			var files []string
			for _, e := range index.Entities {
				files = append(files, e.Kind+" "+e.File)
			}
			wantFiles := []string{"Aggregate aggregate.yaml", "Release release-a.yaml", "Release release-b.yaml"}
			if !slices.Equal(files, wantFiles) {
				t.Errorf("aggrega/catalogIndex lists %v; want %v — the workspace's own documents, each named "+
					"relative to the root, and nothing read through a link leading out", files, wantFiles)
			}
			requireUnreadLinks(t, "aggrega/catalogIndex", index.UnreadLinkedDirs, index.UnreadLinkedFiles)
		})
	}
}

// requireUnreadLinks holds a listing to naming the two links inside the workspace
// that lead out of it, each relative to the root, as not read.
func requireUnreadLinks(t *testing.T, method string, dirs, files []string) {
	t.Helper()
	if !slices.Equal(dirs, []string{"leads-out-dir"}) {
		t.Errorf("%s names the directories %v as not read; want [leads-out-dir]", method, dirs)
	}
	if !slices.Equal(files, []string{"leads-out.yaml"}) {
		t.Errorf("%s names the files %v as not read; want [leads-out.yaml]", method, files)
	}
}

// decodeLinkRootAnswer decodes one answer's result into into.
func decodeLinkRootAnswer(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}
