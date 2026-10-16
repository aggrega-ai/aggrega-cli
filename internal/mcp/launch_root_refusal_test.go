// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestALaunchRootThatIsNotAFolderIsTheAnswerToEveryCallActingInIt serves a
// launch root that is not an existing folder, handed over exactly as the launch
// was given it and with what supplied it, and drives the server's own dispatch:
// every tool call that acts in that workspace — a tool running inside the
// server, the listing that answers over a held workspace, the cache inventory
// that answers the machine-wide half when nothing resolves, and a tool running a
// command in a separate process — and a catalog read each answer with the
// refusal the launch met, opening with the variable and the desktop setting that
// sets it, quoting the root as given, and each tool call's closing with the
// pointer to this server's own page. No command is started. What acts in no
// workspace answers as it does on any server: a schema read by tool, and the
// tool listing. A call naming a workspace of its own is answered there.
//
// A root that was an existing folder when the server started and has gone since
// is refused naming what supplied it too. A listener's handler shows it: built by
// the exported constructor while the root is there and called after it is
// removed, it answers a call naming no workspace with the variable and the
// setting. A server
// handed a root with no source — what a caller of this package leaving the
// source unset builds; no launch of the command does — names this server's
// launch.
//
// 🔴 PLANTED DEFECTS: drop the launch source from rootSourceOf and every refusal
// row fails, naming this server's launch rather than the variable; drop the
// command tool's launchRootRefusal and the cli-work row fails with the command
// started; drop the cache inventory's case for it and the cache-status row fails,
// answered from the machine-wide caches; drop the source from the HTTP handler's
// construction and the listener row fails, naming this server's launch; let
// explainToolError select by kind for a workspace refusal too and every tool row
// fails its pointer, sent to a kind's page or the index.
func TestALaunchRootThatIsNotAFolderIsTheAnswerToEveryCallActingInIt(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-folder")
	global := filepath.Join(t.TempDir(), "machine-wide.yaml")
	if err := os.WriteFile(global, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", global, err)
	}

	var seen recordedRun
	s := newCommandToolServer(t, recordingFolderRunner(&seen))
	s.launchRoot = missing
	s.launchRootSource = workspaceconfig.EnvWorkspaceRootSource
	s.flags = workspaceconfig.FlagOverlay{ConfigPath: global}
	want := workspaceconfig.EnvWorkspaceRootSource + ` names the workspace root "` + missing + `", which cannot be used: `
	// Every refusal closes by pointing at the page teaching which workspace a call
	// is answered in, whichever kind its tool's other failures point at — the
	// Release page for validate, the Component page for the listing and the
	// configuration reading, the index for the cache inventory and the command.
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
		t.Errorf("a command was started over a launch root that is not a folder: %v in %q", seen.argv, seen.dir)
	}

	if _, rerr := readResource(t, s, "catalog://component/core/compute/api-server"); rerr == nil ||
		!strings.Contains(rerr.Message, want) {
		t.Errorf("a catalog read was answered %+v; want an error carrying %q", rerr, want)
	}

	if res := callTool(t, s, "get-schema", getSchemaArgs{Kind: "component"}); res.IsError {
		t.Errorf("get-schema, which acts in no workspace, was refused: %s", res.Content[0].Text)
	}
	listed, rpcErr := s.handleToolsList(context.Background())
	if tools, ok := listed.(listToolsResult); rpcErr != nil || !ok || len(tools.Tools) == 0 {
		t.Errorf("tools/list answered %+v, %+v; want the tool list", listed, rpcErr)
	}
	if res := callTool(t, s, "list-entities", map[string]any{"workspace_root": t.TempDir()}); res.IsError {
		t.Errorf("a call naming a workspace of its own was refused: %s", res.Content[0].Text)
	}

	offline := true
	served := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(served, 0o755); err != nil {
		t.Fatalf("create %s: %v", served, err)
	}
	h, err := NewHTTPHandler(context.Background(), Config{
		Stderr:              io.Discard,
		WorkspaceRoot:       served,
		WorkspaceRootSource: workspaceconfig.EnvWorkspaceRootSource,
		Flags:               workspaceconfig.FlagOverlay{ConfigPath: global, Offline: &offline},
	}, defaultTransportConfig())
	if err != nil {
		t.Fatalf("NewHTTPHandler over %s: %v", served, err)
	}
	hh, ok := h.(*httpHandler)
	if !ok {
		t.Fatalf("NewHTTPHandler returned %T, want the package's own handler", h)
	}
	hh.auth = acceptAllAuthenticator{}
	ts := httptest.NewServer(hh)
	t.Cleanup(ts.Close)
	if err := os.Remove(served); err != nil {
		t.Fatalf("remove %s: %v", served, err)
	}
	got := (&httpFixture{ts: ts, h: hh}).post(t, modernHeaders("tools/call", "list-entities"),
		modernRequest(t, 1, "tools/call", map[string]any{"name": "list-entities", "arguments": map[string]any{}}))
	var overHTTP struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	opening := workspaceconfig.EnvWorkspaceRootSource + ` names the workspace root "`
	if got.frame.Error != nil || json.Unmarshal(got.frame.Result, &overHTTP) != nil || !overHTTP.IsError ||
		len(overHTTP.Content) == 0 || !strings.Contains(overHTTP.Content[0].Text, opening) {
		t.Errorf("over a listener, a launch root gone since the server started was answered %s; want a tool "+
			"error opening %q", got.raw, opening)
	}

	gone := t.TempDir()
	unnamed := &server{launchRoot: gone, flags: s.flags}
	if err := os.Remove(gone); err != nil {
		t.Fatalf("remove %s: %v", gone, err)
	}
	refusal := unnamed.launchRootRefusal()
	if !errors.Is(refusal, fs.ErrNotExist) ||
		!strings.Contains(refusal.Error(), `this server's launch names the workspace root "`+gone+`"`) {
		t.Errorf("a launch root handed with no source and gone since the server started is refused as %v; "+
			"want it named as this server's launch", refusal)
	}
}
