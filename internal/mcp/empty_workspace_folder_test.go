// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestALaunchRefusedForAnEmptyWorkspaceFolderIsTheAnswerToEveryCallNamingNoWorkspace
// serves a launch that named no root and was refused for it all the same — the
// desktop application starting the server with its Workspace folder setting
// empty — and drives the server's own dispatch from a working folder that holds
// a workspace, so that any call resolving that folder would be answered there
// rather than refused.
//
// Every call that acts in a workspace and names none of its own answers with the
// refusal, closing with the pointer to this server's own page: a tool running
// inside the server, the listing that answers over a held workspace, the
// configuration reading, the cache inventory, and a tool running a command in a
// separate process, which starts no command. A catalog read and a documentation
// read each answer with an error carrying it. What acts in no workspace answers
// as on any server — a schema read by tool, and the tool listing — and a call
// naming a workspace of its own is answered there. A listener is refused one at
// construction.
//
// 🔴 PLANTED DEFECTS: have launchRefusalFor return nil whatever it holds, and
// every refusal row fails, answered from the working folder's workspace; drop it
// from resolveListingWorkspace alone and the list-entities row fails; drop it
// from launchRootRefusal alone and the cli-work row fails with a command
// started, and the cache-status row with the machine-wide caches; drop the
// refusal from NewHTTPHandler and the listener row fails.
func TestALaunchRefusedForAnEmptyWorkspaceFolderIsTheAnswerToEveryCallNamingNoWorkspace(t *testing.T) {
	// Not parallel: it moves the process working directory into a workspace.
	startedIn := t.TempDir()
	if err := os.WriteFile(filepath.Join(startedIn, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write the workspace in the started-in folder: %v", err)
	}
	t.Chdir(startedIn)
	global := filepath.Join(t.TempDir(), "machine-wide.yaml")
	if err := os.WriteFile(global, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", global, err)
	}

	var seen recordedRun
	s := newCommandToolServer(t, recordingFolderRunner(&seen))
	s.launchRoot = ""
	s.launchRefusal = &workspaceconfig.EmptyWorkspaceFolderError{}
	s.flags = workspaceconfig.FlagOverlay{ConfigPath: global}
	want := "resolve workspace: " + s.launchRefusal.Error()
	pointer := "\n\n" + docPointerLine(docServerReference)

	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"validate", map[string]any{"release": "release.yaml"}},
		{"list-entities", map[string]any{}},
		{"config-show", map[string]any{}},
		{"cache-status", map[string]any{}},
		{"cli-work", map[string]any{}},
	} {
		res := callTool(t, s, c.tool, c.args)
		if !res.IsError || !strings.Contains(res.Content[0].Text, want) {
			t.Errorf("%s %v was not answered with the launch's refusal (isError %v); want it to carry %q:\n%s",
				c.tool, c.args, res.IsError, want, res.Content[0].Text)
			continue
		}
		if !strings.HasSuffix(res.Content[0].Text, pointer) {
			t.Errorf("%s %v refused the launch's workspace without closing with %q:\n%s",
				c.tool, c.args, pointer, res.Content[0].Text)
		}
	}
	if seen.argv != nil {
		t.Errorf("a command was started on a launch refused for naming no root: %v in %q", seen.argv, seen.dir)
	}

	for _, uri := range []string{"catalog://component/core/compute/api-server", "langref://entities/component"} {
		if _, rerr := readResource(t, s, uri); rerr == nil || !strings.Contains(rerr.Message, want) {
			t.Errorf("a read of %s was answered %+v; want an error carrying %q", uri, rerr, want)
		}
	}

	if res := callTool(t, s, "get-schema", getSchemaArgs{Kind: "component"}); res.IsError {
		t.Errorf("get-schema, which acts in no workspace, was refused: %s", res.Content[0].Text)
	}
	listed, rpcErr := s.handleToolsList(context.Background())
	if tools, ok := listed.(listToolsResult); rpcErr != nil || !ok || len(tools.Tools) == 0 {
		t.Errorf("tools/list answered %+v, %+v; want the tool list", listed, rpcErr)
	}
	own := t.TempDir()
	if res := callTool(t, s, "list-entities", map[string]any{"workspace_root": own}); res.IsError {
		t.Errorf("a call naming a workspace of its own was refused: %s", res.Content[0].Text)
	}
	if refusal := s.launchRefusalFor(own); refusal != nil {
		t.Errorf("a call naming a workspace of its own meets the launch's refusal: %v", refusal)
	}

	// Offline, so a handler built under the planted defect acquires nothing from
	// the network on its way to failing the row.
	offline := true
	_, err := NewHTTPHandler(context.Background(), Config{
		Stderr:               io.Discard,
		WorkspaceRootRefusal: s.launchRefusal,
		Flags:                workspaceconfig.FlagOverlay{ConfigPath: global, Offline: &offline},
	}, defaultTransportConfig())
	var empty *workspaceconfig.EmptyWorkspaceFolderError
	if !errors.As(err, &empty) {
		t.Errorf("a listener built over a launch refused for naming no root returned %v; want that refusal", err)
	}
}
