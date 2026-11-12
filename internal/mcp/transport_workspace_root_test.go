// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Which workspace a serving process answers from is a property of the process,
// so it is proved the same way on both transports: the same fixture, the same
// call, and nothing named on the call itself. The three rows below are the
// launched root honoured over HTTP, the same root honoured over standard input
// and output, and a process launched without one answering from the directory it
// was started in.

// rootedWorkspace writes a workspace holding one Release and returns its root.
//
// The Release is named so that nothing but a read of the file could list it:
// the name says nothing about the kind. The workspace declares itself offline so
// that starting a server over it reaches no registry — what these rows are about
// is which directory a call resolves in, and a network round trip on the way
// there would be measuring something else.
func rootedWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		".aggrega-config.yaml": "workspace: {}\noffline: true\n",
		"parts/green.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: the-launched-workspaces-release
  version: "1.0.0"
  environment: production
spec:
  manifest: ./manifest.yaml
`,
	}
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// theLaunchedWorkspacesRelease is the path, relative to the workspace root, that
// only the fixture above holds. A listing carrying it was taken in that
// workspace and nowhere else.
const theLaunchedWorkspacesRelease = "parts/green.yaml"

// listReleasesRequest is one tools/call naming no workspace of its own, which is
// the whole point: what answers it is the workspace the process was launched
// over.
//
// It does name the shape, and that is not the same concession. The evidence
// these rows rest on is a path only the fixture holds, and the paths are what
// the detailed shape carries — the shape a call names decides how much of the
// answer comes back, never which directory it was taken in. Leaving the shape
// out would leave the rows asserting the workspace root the answer echoes, which
// a server that resolved the right directory and read a different one would
// satisfy.
func listReleasesRequest(t *testing.T) string {
	t.Helper()
	return modernRequest(t, 1, "tools/call", map[string]any{
		"name":      "list-releases",
		"arguments": map[string]any{"response_format": "detailed"},
	})
}

// releasesFromAnswer reads a list-releases answer down to the listing it
// carries: the workspace root it was taken in, as the caller was told it, and the
// paths it reported.
//
// It checks the shape before it hands the paths back, because an answer carrying
// no paths is what every one of these rows reads as "the wrong directory was
// listed". The two are indistinguishable from the caller's side, so the shape is
// asserted where it can still be named rather than inferred later from an empty
// slice.
func releasesFromAnswer(t *testing.T, raw string) listReleasesResult {
	t.Helper()
	var frame rpcResponse
	if err := json.Unmarshal([]byte(raw), &frame); err != nil {
		t.Fatalf("the answer is not a JSON-RPC frame: %q: %v", raw, err)
	}
	if frame.Error != nil {
		t.Fatalf("list-releases was refused: %s", frame.Error.Message)
	}
	var call callToolResult
	if err := json.Unmarshal(frame.Result, &call); err != nil {
		t.Fatalf("the result is not a tool result: %v", err)
	}
	if call.IsError {
		t.Fatalf("the tool reported a failure: %s", call.Content[0].Text)
	}
	var listed listReleasesResult
	if err := json.Unmarshal(call.StructuredContent, &listed); err != nil {
		t.Fatalf("the structured content is not a release listing: %v", err)
	}
	if listed.ResponseFormat != responseFormatDetailed {
		t.Fatalf("the answer came back in the %q shape, which carries no paths at all; the rows below read the "+
			"paths as their evidence, so the call has to ask for them", listed.ResponseFormat)
	}
	return listed
}

// listReleasesOverHTTP starts the transport the way the command layer starts it
// — through the exported constructor, given the process's own configuration —
// and asks it to list Releases.
//
// The test-only authenticator is installed afterwards, on the handler the
// constructor returned, because the production value refuses every request and
// what this row is about is the workspace behind the credential check rather
// than the check itself.
func listReleasesOverHTTP(t *testing.T, workspaceRoot string) string {
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

	req, err := http.NewRequest(http.MethodPost, ts.URL+endpointPath, strings.NewReader(listReleasesRequest(t)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for name, value := range modernHeaders("tools/call", "list-releases") {
		req.Header.Set(name, value)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post list-releases: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list-releases over HTTP: status = %d, body %q", resp.StatusCode, raw)
	}
	return string(raw)
}

// listReleasesOverStdio runs the same server on the transport it has always
// spoken, driving one message and letting the run end on end of input.
func listReleasesOverStdio(t *testing.T, workspaceRoot string) string {
	t.Helper()
	var out strings.Builder
	err := Run(context.Background(), Config{
		Stdin:         strings.NewReader(listReleasesRequest(t) + "\n"),
		Stdout:        &out,
		Stderr:        io.Discard,
		WorkspaceRoot: workspaceRoot,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("the run framed %d lines on standard output, want exactly one: %q", len(lines), out.String())
	}
	return lines[0]
}

// A process launched over a workspace answers from that workspace on either
// transport, and the two transports answer alike.
//
// The listing's paths are the evidence on both. The root the answer echoes is a
// second piece of evidence only where the caller is told it: a caller reaching a
// listener is not told where the served workspace sits on the host, so over HTTP
// the root is required to be kept back and marked instead.
func TestTheLaunchedWorkspaceIsHonouredOnBothTransports(t *testing.T) {
	root := rootedWorkspace(t)

	for _, tc := range []struct {
		name             string
		call             func(*testing.T, string) string
		keepsTheRootBack bool
	}{
		{"over HTTP", listReleasesOverHTTP, true},
		{"over standard input and output", listReleasesOverStdio, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listed := releasesFromAnswer(t, tc.call(t, root))
			switch {
			case tc.keepsTheRootBack:
				if listed.WorkspaceRoot != nil || !listed.WorkspaceRootWithheld {
					t.Errorf("the answer names the workspace root %q (withheld=%v); a caller reaching a listener "+
						"is not told where it sits", told(listed.WorkspaceRoot), listed.WorkspaceRootWithheld)
				}
			case told(listed.WorkspaceRoot) != root:
				t.Errorf("the call was answered in %q, want the launched workspace %q", told(listed.WorkspaceRoot), root)
			}
			if !slices.Contains(listed.Releases, theLaunchedWorkspacesRelease) {
				t.Errorf("the listing is %v and does not hold %q, so it was not taken in the launched workspace",
					listed.Releases, theLaunchedWorkspacesRelease)
			}
		})
	}
}

// A process launched without a workspace resolves one from the directory it was
// started in.
//
// The discriminator is this test's working directory against the two rows above,
// which run in the package's own directory and still answer in the fixture: here
// the process is started IN the fixture and told nothing, and answers there. So
// a field that was silently ignored would fail the rows above, and a field that
// overrode the working directory rather than defaulting to it would fail this
// one. The listing's paths are what says so: the root the answer would echo is
// a place on the host a caller reaching a listener is not told.
func TestAProcessLaunchedWithoutAWorkspaceResolvesFromItsWorkingDirectory(t *testing.T) {
	root := rootedWorkspace(t)
	t.Chdir(root)

	listed := releasesFromAnswer(t, listReleasesOverHTTP(t, ""))
	if listed.WorkspaceRoot != nil || !listed.WorkspaceRootWithheld {
		t.Errorf("a process launched with no workspace names its root %q (withheld=%v) to a caller reaching a "+
			"listener, which is not told where it sits", told(listed.WorkspaceRoot), listed.WorkspaceRootWithheld)
	}
	if !slices.Contains(listed.Releases, theLaunchedWorkspacesRelease) {
		t.Errorf("the listing is %v and does not hold %q, so it was not taken in the directory the process "+
			"was started in", listed.Releases, theLaunchedWorkspacesRelease)
	}
}
