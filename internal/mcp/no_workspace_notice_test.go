// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// noWorkspaceSentenceOpening is how the sentence an answer carries opens, for
// counting it in an answer's text. The sentence's home is noWorkspaceSentence.
const noWorkspaceSentenceOpening = "No Aggrega workspace was found from "

// writeNoticeFixture writes body to path, creating the directories above it.
func writeNoticeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// markedNoticeWorkspace returns a fresh folder carrying a workspace
// configuration, so it anchors itself.
func markedNoticeWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeNoticeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\noffline: true\n")
	return root
}

// TestAToolAnswerSaysWhichFolderItUsedWhereNoWorkspaceWasFound calls a tool
// through the server's one exit, on a server reached over its own streams and
// started in a folder no workspace resolves from: with no launch root and no root
// on the call the answer says, once, which folder it used and how to name the
// workspace; with a root on the call, or a launch root, it says nothing. No field
// is added to the structured payload.
//
// 🔴 PLANTED DEFECT: drop the withNoWorkspaceNotice call from handleToolsCall, and
// the first row fails.
//
// Not parallel: a server launched with no root resolves from the process working
// directory, which is the condition under test.
func TestAToolAnswerSaysWhichFolderItUsedWhereNoWorkspaceWasFound(t *testing.T) {
	for _, tc := range []struct {
		name string
		// launch returns the root the server was launched with, and args the
		// call's arguments.
		launch func(t *testing.T) string
		args   func(t *testing.T) map[string]any
		says   bool
	}{
		{name: "no launch root and no root on the call", says: true,
			launch: func(*testing.T) string { return "" },
			args:   func(*testing.T) map[string]any { return map[string]any{} }},
		{name: "the call names its workspace",
			launch: func(*testing.T) string { return "" },
			args: func(t *testing.T) map[string]any {
				return map[string]any{argumentWorkspaceRoot: markedNoticeWorkspace(t)}
			}},
		{name: "the server was launched with a root",
			launch: func(t *testing.T) string { return markedNoticeWorkspace(t) },
			args:   func(*testing.T) map[string]any { return map[string]any{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			folder := aDirectoryNoWorkspaceResolvesFrom(t)
			launch := tc.launch(t)
			args := tc.args(t)
			t.Chdir(folder)

			res := callTool(t, unlaunchedServer(launch), "list-releases", args)
			if res.IsError {
				t.Fatalf("list-releases was refused: %s", res.Content[0].Text)
			}
			text := res.Content[len(res.Content)-1].Text
			count := strings.Count(text, noWorkspaceSentenceOpening)
			switch {
			case tc.says && (count != 1 || !strings.Contains(text, folder)):
				t.Errorf("the answer says the notice %d times, or not of %s:\n%s", count, folder, text)
			case tc.says && !strings.Contains(text, argumentWorkspaceRoot):
				t.Errorf("the answer does not name %s, the argument that names the workspace:\n%s", argumentWorkspaceRoot, text)
			case !tc.says && count != 0:
				t.Errorf("the answer says the notice although %s:\n%s", tc.name, text)
			}
			if strings.Contains(string(res.StructuredContent), noWorkspaceSentenceOpening) {
				t.Errorf("the structured payload carries the notice: %s", res.StructuredContent)
			}
		})
	}
}

// TestTheNoticeIsNeverAddedOverAListener hands the step a result whose workspace
// found nothing and was named by nobody, on a server reached over a listener: the
// result comes back as it was composed. A listener refuses to start in such a
// folder unless it is handed a root, so this is the step's own guard rather than a
// state the listener reaches.
func TestTheNoticeIsNeverAddedOverAListener(t *testing.T) {
	folder := aDirectoryNoWorkspaceResolvesFrom(t)
	rw, err := workspaceconfig.Resolve(folder, "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve %s: %v", folder, err)
	}
	for _, tc := range []struct {
		transport transportMode
		says      bool
	}{{transportStdio, true}, {transportHTTP, false}} {
		t.Run(transportName(tc.transport), func(t *testing.T) {
			s := &server{transport: tc.transport}
			res, _ := textResult("composed", nil)
			workspace, err := s.toolWorkspaceOf(rw, nil)
			if err != nil {
				t.Fatalf("compose the workspace context: %v", err)
			}
			res.workspace = workspace
			got := s.withNoWorkspaceNotice(res).Content[0].Text
			if says := got != "composed"; says != tc.says {
				t.Errorf("over %s the answer reads %q; want the notice added: %v", transportName(tc.transport), got, tc.says)
			}
		})
	}
}

// TestAToolsWorkspaceRootThatIsNotThereIsRefusedNamingTheArgument sends a call
// naming a workspace root that is not there, and one naming a file, and reads the
// refusal: it names the call's argument and quotes the value as sent, as the
// command line names its flag or its variable.
//
// 🔴 PLANTED DEFECT: restore the explicit branch of the shared resolution to take
// the root as given, and both rows fail — the listing answers over a folder that
// does not exist.
func TestAToolsWorkspaceRootThatIsNotThereIsRefusedNamingTheArgument(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-folder")
	file := filepath.Join(t.TempDir(), "a-file")
	writeNoticeFixture(t, file, "x")
	for _, tc := range []struct {
		name, root, says string
	}{
		{"a folder that is not there", missing, "which cannot be used"},
		{"a file", file, "which is not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := callTool(t, unlaunchedServer(""), "list-releases", map[string]any{argumentWorkspaceRoot: tc.root})
			if !res.IsError {
				t.Fatalf("a call naming %s as its workspace root was answered: %s", tc.root, res.Content[0].Text)
			}
			text := res.Content[0].Text
			if want := "the call's '" + argumentWorkspaceRoot + `' names the workspace root "` + tc.root + `", ` + tc.says; !strings.Contains(text, want) {
				t.Errorf("the refusal reads:\n%s\nwant it to carry %q", text, want)
			}
		})
	}
}

// noteWorkspace lays down, in a marked workspace that is offline, a Release whose
// Manifest binds a Recipe no tier holds, so a pull fails on a reference that
// resolved to nothing. It returns the workspace and the Release.
func noteWorkspace(t *testing.T) (root, release string) {
	t.Helper()
	root = markedNoticeWorkspace(t)
	writeNoticeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  components:
    web:
      type: example/compute/missing:1.0.0
`)
	writeNoticeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/missing:1.0.0: compose/missing:1.0.0
`)
	release = filepath.Join(root, "release.yaml")
	writeNoticeFixture(t, release, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: app-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: app
  manifest: app-manifest
  boundaries:
    app:
      directory: app
      components:
        web: {}
`)
	return root, release
}

// TestThePullToolsRefusalCarriesTheNoteAndTheConditionsCodes runs the pull tool
// over a Release whose resolution finds nothing for a reference, and reads the
// refusal: it carries the workspace's own account of where local entities are
// read from — the note the command line's refusal carries, from the one
// composition — and names the condition's short code and its descriptive code,
// read off the failure's chain.
//
// 🔴 PLANTED DEFECTS: restore AnnotateResolutionFailure as a hook only the command
// line supplies, and the note row fails; restore explainToolErrorAt to read the
// short code out of the message text alone, and the codes row fails.
func TestThePullToolsRefusalCarriesTheNoteAndTheConditionsCodes(t *testing.T) {
	t.Parallel()
	root, release := noteWorkspace(t)
	s := unlaunchedServer("")
	tw, err := s.resolveToolWorkspace(root, nil)
	if err != nil {
		t.Fatalf("resolve %s: %v", root, err)
	}
	note := strings.TrimSpace(tw.rw.LocalCatalogAdviceNote(release))
	if note == "" {
		t.Fatal("precondition: the workspace composes no note for this Release, so the row would compare nothing")
	}
	condition, err := haltref.Resolve("E0114")
	if err != nil {
		t.Fatalf("resolve E0114: %v", err)
	}

	res := callTool(t, s, "pull", pullArgs{Release: release, WorkspaceRoot: root})
	if !res.IsError {
		t.Fatalf("the pull of a Release binding a Recipe no tier holds completed: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, note) {
		t.Errorf("the refusal does not carry the workspace's note\nrefusal:\n%s\nnote:\n%s", text, note)
	}
	for _, code := range []string{condition.ShortCode, condition.Code} {
		if !strings.Contains(text, code) {
			t.Errorf("the refusal does not name %s:\n%s", code, text)
		}
	}
}

// TestThePullToolInAFolderNoWorkspaceResolvesFromWritesNoCache calls the pull
// tool on a server started, with no launch root, in a folder no workspace
// resolves from, over a Release in that folder whose own folder anchors nothing:
// the call is refused before anything is fetched, with the refusal the command
// line gives naming the folder, and the folder holds no cache afterwards.
//
// 🔴 PLANTED DEFECT: remove the CacheWriteRefusal call from pullops.Run, and the
// refusal row fails — the resolution runs and fails on what it could not fetch.
//
// Not parallel: the server resolves from the process working directory.
func TestThePullToolInAFolderNoWorkspaceResolvesFromWritesNoCache(t *testing.T) {
	folder := aDirectoryNoWorkspaceResolvesFrom(t)
	release := filepath.Join(folder, "production.yaml")
	writeNoticeFixture(t, release, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: app-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: app
  manifest: app-manifest
  boundaries:
    app:
      directory: app
      components:
        web: {}
`)
	writeNoticeFixture(t, filepath.Join(folder, "app.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  components:
    web:
      type: example/compute/missing:1.0.0
`)
	writeNoticeFixture(t, filepath.Join(folder, "app-mappings.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/missing:1.0.0: compose/missing:1.0.0
`)
	t.Setenv("AGGREGA_OFFLINE", "true")
	t.Chdir(folder)
	// The call names its Release absolutely, so its workspace is found again from
	// that Release's folder, spelled as the Release's path spells it.
	rw, err := workspaceconfig.Resolve(folder, "")
	if err != nil {
		t.Fatalf("resolve %s: %v", folder, err)
	}
	want := rw.CacheWriteRefusal()
	if want == nil {
		t.Fatalf("precondition: the folder %s resolves as a workspace (marker %v)", folder, rw.Marker())
	}

	res := callTool(t, unlaunchedServer(""), "pull", pullArgs{Release: release})
	if !res.IsError {
		t.Fatalf("the pull in a folder that is no workspace completed: %s", res.Content[0].Text)
	}
	if text := res.Content[0].Text; !strings.Contains(text, want.Error()) {
		t.Errorf("the refusal does not carry the cache refusal\nrefusal:\n%s\nwant it to carry:\n%s", text, want)
	}
	if _, err := os.Stat(filepath.Join(folder, ".aggrega")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s/.aggrega exists after the refused call (stat: %v)", folder, err)
	}
}

// TestTheStdioGuidanceSaysWhereACallNamingNoWorkspaceIsAnswered reads the
// connect-time guidance on both transports: over the process's own streams it says
// where a call naming no workspace is answered — the launch root, else the
// workspace found from the folder the server was started in — and names the
// argument that names another; over a listener, where a call cannot name one,
// it says neither.
func TestTheStdioGuidanceSaysWhereACallNamingNoWorkspaceIsAnswered(t *testing.T) {
	t.Parallel()
	const where = "A call that names no workspace is answered in the folder this server was launched with"
	overStdio, overHTTP := instructionsFor(transportStdio), instructionsFor(transportHTTP)
	if !strings.Contains(overStdio, where) || !strings.Contains(overStdio, "send "+argumentWorkspaceRoot+" on a call") {
		t.Errorf("the guidance over standard input and output does not say where a call naming no workspace is answered")
	}
	if strings.Contains(overHTTP, where) || strings.Contains(overHTTP, argumentWorkspaceRoot) {
		t.Errorf("the guidance over a listener says where a call naming no workspace is answered, or names %s, "+
			"which such a caller cannot send", argumentWorkspaceRoot)
	}
}
