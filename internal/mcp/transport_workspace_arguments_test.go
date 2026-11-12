// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Which workspace a call may name is a property of how the caller reached the
// server, so it is proved on both transports against one fixture: the arguments
// that name a workspace are refused over HTTP and honoured over standard input
// and output.
//
// The refusal is asserted on the failing result a caller actually reads — the
// tool result flagged as a failure, and the text inside it — rather than on the
// helper that composes it, because an argument dropped in silence and an
// argument refused are the same call from the outside and differ only there.

// callOverHTTP drives one tools/call against a handler built the way the command
// layer builds it, and returns the tool result it answered with.
//
// The test-only authenticator is installed on the handler the constructor
// returned, because the value it ships with refuses every request and what these
// rows are about is what a caller past that check may name.
func callOverHTTP(t *testing.T, workspaceRoot, tool string, args map[string]any) *callToolResult {
	t.Helper()
	handler, err := NewHTTPHandler(context.Background(),
		Config{Stderr: io.Discard, WorkspaceRoot: workspaceRoot}, defaultTransportConfig())
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	h, ok := handler.(*httpHandler)
	if !ok {
		t.Fatalf("NewHTTPHandler returned %T", handler)
	}
	h.auth = acceptAllAuthenticator{}

	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	body := modernRequest(t, 1, "tools/call", map[string]any{"name": tool, "arguments": args})
	req, err := http.NewRequest(http.MethodPost, ts.URL+endpointPath, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for name, value := range modernHeaders("tools/call", tool) {
		req.Header.Set(name, value)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post tools/call %s: %v", tool, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call %s over HTTP: status = %d, body %q", tool, resp.StatusCode, raw)
	}

	var frame rpcResponse
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("the answer is not a JSON-RPC frame: %q: %v", raw, err)
	}
	if frame.Error != nil {
		t.Fatalf("the call was answered with an error envelope, which a caller may never be shown: %s",
			frame.Error.Message)
	}
	var call callToolResult
	if err := json.Unmarshal(frame.Result, &call); err != nil {
		t.Fatalf("the result is not a tool result: %v", err)
	}
	if len(call.Content) == 0 {
		t.Fatal("the tool result carries no text block for a caller to read")
	}
	return &call
}

// TestANamedWorkspaceIsRefusedOverHTTP drives the refusal on the transport it
// binds. Each row sends one of the two arguments that name a workspace and reads
// the answer a caller gets.
func TestANamedWorkspaceIsRefusedOverHTTP(t *testing.T) {
	served := rootedWorkspace(t)
	elsewhere := rootedWorkspace(t)

	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
		// refused says whether this row is a refusal; want are the fragments the
		// text must carry, so a row fails on the sentence a caller acts on rather
		// than on there being some failure or other.
		refused bool
		want    []string
	}{
		{
			name:    "the workspace root names another workspace",
			tool:    "list-releases",
			args:    map[string]any{"workspace_root": elsewhere},
			refused: true,
			want:    []string{"workspace_root", "fixed when it was started"},
		},
		{
			name:    "the catalog directories name a directory to read beside it",
			tool:    "list-entities",
			args:    map[string]any{"catalog_dirs": []string{elsewhere}},
			refused: true,
			want:    []string{"catalog_dirs", "fixed when it was started", "workspace.catalog_dirs"},
		},
		{
			name:    "both are named at once",
			tool:    "list-entities",
			args:    map[string]any{"workspace_root": elsewhere, "catalog_dirs": []string{elsewhere}},
			refused: true,
			want:    []string{"workspace_root", "catalog_dirs", "fixed when it was started"},
		},
		{
			// The positive control. Without it every row above would pass against a
			// server that refused every call it was given.
			name:    "neither is named",
			tool:    "list-releases",
			args:    map[string]any{},
			refused: false,
		},
		{
			// An empty value selects no workspace, so there is nothing to refuse. It
			// is the row that keeps the refusal keyed on a workspace having been
			// named rather than on the key being present.
			name:    "the arguments are present and empty",
			tool:    "list-entities",
			args:    map[string]any{"workspace_root": "", "catalog_dirs": []string{}},
			refused: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := callOverHTTP(t, served, tc.tool, tc.args)
			text := res.Content[0].Text
			if res.IsError != tc.refused {
				t.Fatalf("the result is flagged as a failure = %v, want %v; its text is %q",
					res.IsError, tc.refused, text)
			}
			for _, fragment := range tc.want {
				if !strings.Contains(text, fragment) {
					t.Errorf("the refusal does not carry %q, so a caller cannot act on it; it reads %q",
						fragment, text)
				}
			}
		})
	}
}

// TestARefusedWorkspaceArgumentIsNotHonoured is the half the assertion above
// cannot make: that the refusal stopped the call rather than merely annotating
// it.
//
// The discriminator is the Release only the other workspace holds. A refusal
// that answered anyway would list it, and a refusal that answered from the
// served workspace would be indistinguishable from one that silently dropped the
// argument — so the row asserts no listing came back at all.
func TestARefusedWorkspaceArgumentIsNotHonoured(t *testing.T) {
	served := rootedWorkspace(t)
	elsewhere := rootedWorkspace(t)

	res := callOverHTTP(t, served, "list-releases", map[string]any{"workspace_root": elsewhere})
	if !res.IsError {
		t.Fatalf("naming another workspace was not refused: %s", res.Content[0].Text)
	}
	if len(res.StructuredContent) != 0 {
		t.Errorf("the refusal carries a structured listing, so the call was answered as well as refused: %s",
			res.StructuredContent)
	}
	if strings.Contains(res.Content[0].Text, theLaunchedWorkspacesRelease) {
		t.Errorf("the refusal names %q, so it read the workspace it refused to be pointed at: %s",
			theLaunchedWorkspacesRelease, res.Content[0].Text)
	}
}

// TestAMountedHandlerRefusesANamedWorkspaceToo pins where the session is marked
// as one reached over HTTP.
//
// The fixture mounts a handler on a server it built itself, which is the route a
// caller embedding this handler takes and the route every other transport proof
// here takes. Marking the session in the exported constructor alone would leave
// all of them serving requests while still claiming to speak this process's own
// streams — the arguments would be honoured, this row would fail, and the failure
// is the whole reason it exists.
func TestAMountedHandlerRefusesANamedWorkspaceToo(t *testing.T) {
	f := newHTTPFixture(t, defaultTransportConfig(), nil)

	body := modernRequest(t, 1, "tools/call", map[string]any{
		"name":      "list-releases",
		"arguments": map[string]any{"workspace_root": rootedWorkspace(t)},
	})
	got := f.post(t, modernHeaders("tools/call", "list-releases"), body)
	if got.frame.Error != nil {
		t.Fatalf("the call was answered with an error envelope: %s", got.frame.Error.Message)
	}

	var call callToolResult
	if err := json.Unmarshal(got.frame.Result, &call); err != nil {
		t.Fatalf("the result is not a tool result: %v", err)
	}
	if !call.IsError {
		t.Fatalf("a handler mounted on a server built by its caller honoured workspace_root: %s", got.raw)
	}
	if !strings.Contains(call.Content[0].Text, argumentWorkspaceRoot) {
		t.Errorf("the refusal does not name the argument sent; it reads %q", call.Content[0].Text)
	}
}

// TestANamedWorkspaceIsHonouredOverStdio is the control on the other transport,
// whose caller launched this process and is at its keyboard.
//
// It drives the same dispatch a client drives — tools/call by name — against a
// session composed the way the standard-input transport composes one, and reads
// the listing back to prove the named workspace was the one answered in rather
// than merely accepted.
func TestANamedWorkspaceIsHonouredOverStdio(t *testing.T) {
	served := rootedWorkspace(t)
	named := rootedWorkspace(t)

	s := &server{out: io.Discard, logw: io.Discard, launchRoot: served, transport: transportStdio}

	var got listReleasesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-releases", map[string]any{"workspace_root": named}), &got)
	if told(got.WorkspaceRoot) != named {
		t.Errorf("the call was answered in %q, want the workspace it named, %q", told(got.WorkspaceRoot), named)
	}
}
