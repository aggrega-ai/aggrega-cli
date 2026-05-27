// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// decodeOneNotification reads a single Content-Length-framed
// JSON-RPC notification from out and decodes it. Sibling to
// decodeOneResponse (which decodes responses keyed on id);
// notifications have no id, so the type is distinct.
func decodeOneNotification(t *testing.T, out *bytes.Buffer) jsonRPCNotificationRead {
	t.Helper()
	r := bufio.NewReader(out)
	body, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var n jsonRPCNotificationRead
	if err := json.Unmarshal(body, &n); err != nil {
		t.Fatalf("unmarshal notification: %v (body: %s)", err, body)
	}
	return n
}

// jsonRPCNotificationRead is the test-side decode shape; Params
// stays as json.RawMessage so callers unmarshal into the typed
// params struct themselves.
type jsonRPCNotificationRead struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// TestHandleIrGraphLockfileAbsentEmits1003 verifies that an
// aggrega/irGraph request against a workspace with a Release path
// that resolves but no sibling lockfile produces the
// lockfile-absent envelope with Data.File pointing at the expected
// lockfile path. The post-absent-gate flow (resolveReleaseClosure
// + verifyDriftAndPublish + pipeline) never fires because the gate
// blocks.
func TestHandleIrGraphLockfileAbsentEmits1003(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	var out bytes.Buffer
	srv := newTestServer(t, tmp, nil, &out)

	missing := "release.yaml"
	var in bytes.Buffer
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: missing}))
	resp := runOneRequest(t, srv, &in, &out)

	if resp.Error == nil {
		t.Fatalf("expected error envelope; got result: %s", resp.Result)
	}
	if resp.Error.Code != errLockfileAbsent {
		t.Errorf("Code: got %d, want %d (errLockfileAbsent)", resp.Error.Code, errLockfileAbsent)
	}
	if resp.Error.Data == nil {
		t.Fatalf("expected Data field populated; got nil")
	}
	wantLockfile := filepath.Join(tmp, "release.lock")
	if resp.Error.Data.File != wantLockfile {
		t.Errorf("Data.File: got %q, want %q", resp.Error.Data.File, wantLockfile)
	}
}

// TestPublishDiagnosticsWireShape verifies that the
// aggrega/publishDiagnostics notification fires with the expected
// wire payload on diagnostic state change. Drives the helper
// directly and inspects the on-wire JSON. Pins the wire-level
// field names (file, diagnostics) and the empty-array semantics
// for "clear this entry."
func TestPublishDiagnosticsWireShape(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	var out bytes.Buffer
	srv := newTestServer(t, tmp, nil, &out)

	// Drift-detected state: populate one diagnostic on a file.
	srv.diagnostics["/tmp/release.lock"] = []Diagnostic{
		{Line: 1, Column: 1, Severity: "warning", Message: "drifted"},
	}
	if err := srv.publishDiagnostics("/tmp/release.lock"); err != nil {
		t.Fatalf("publishDiagnostics drift state: %v", err)
	}

	notif := decodeOneNotification(t, &out)
	if notif.Method != "aggrega/publishDiagnostics" {
		t.Errorf("method: got %q, want aggrega/publishDiagnostics", notif.Method)
	}
	var params publishDiagnosticsParams
	if err := json.Unmarshal(notif.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params.File != "/tmp/release.lock" {
		t.Errorf("File: got %q, want /tmp/release.lock", params.File)
	}
	if len(params.Diagnostics) != 1 {
		t.Fatalf("Diagnostics: got %d, want 1", len(params.Diagnostics))
	}
	if params.Diagnostics[0].Severity != "warning" {
		t.Errorf("Severity: got %q, want warning", params.Diagnostics[0].Severity)
	}
	if params.Diagnostics[0].Message != "drifted" {
		t.Errorf("Message: got %q, want drifted", params.Diagnostics[0].Message)
	}

	// Drift-cleared state: empty array means "clear this entry."
	delete(srv.diagnostics, "/tmp/release.lock")
	if err := srv.publishDiagnostics("/tmp/release.lock"); err != nil {
		t.Fatalf("publishDiagnostics drift-cleared state: %v", err)
	}
	notif = decodeOneNotification(t, &out)
	if err := json.Unmarshal(notif.Params, &params); err != nil {
		t.Fatalf("unmarshal cleared-state params: %v", err)
	}
	if len(params.Diagnostics) != 0 {
		t.Errorf("Diagnostics on clear: got %d, want 0 (the empty-array clear signal)", len(params.Diagnostics))
	}
	if params.File != "/tmp/release.lock" {
		t.Errorf("File on clear: got %q, want /tmp/release.lock", params.File)
	}
}

// TestVerifyDriftAndUpdateCacheOnLoadFailure verifies the
// load-failure branch in verifyDriftAndUpdateCache: when the
// lockfile file exists at the path but fails to parse (malformed
// YAML), the drift state clears (no stale diagnostic) and a stderr
// line is emitted. The wire-level publishDiagnostics fires from the
// caller's verifyDriftAndPublish wrapper.
func TestVerifyDriftAndUpdateCacheOnLoadFailure(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	var out, stderr bytes.Buffer
	srv := newTestServer(t, tmp, nil, &out)
	srv.stderr = &stderr

	// Pre-populate the diagnostic so we can observe it being
	// cleared on load failure.
	lockfilePath := filepath.Join(tmp, "release.lock")
	srv.diagnostics[lockfilePath] = []Diagnostic{
		{Line: 1, Column: 1, Severity: "warning", Message: "previous drift"},
	}

	// Write a malformed lockfile (so LoadLockfile returns an error).
	if err := os.WriteFile(lockfilePath, []byte("this: is: not: valid: yaml: ["), 0o644); err != nil {
		t.Fatalf("write malformed lockfile: %v", err)
	}

	// Closure shape doesn't matter on the load-failure branch — the
	// error fires before the closure is consulted.
	srv.verifyDriftAndUpdateCache(lockfilePath, closure.Closure{})

	// Prior drift diagnostic should be cleared.
	if _, ok := srv.diagnostics[lockfilePath]; ok {
		t.Errorf("expected diagnostics entry to be cleared on load failure; still present")
	}
	if stderr.Len() == 0 {
		t.Errorf("expected stderr log on lockfile load failure; got empty")
	}
}
