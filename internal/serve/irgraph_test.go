// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// writeStubLockfile creates an empty file at the conventional
// lockfile path for the given Release path. Used by tests that need
// to reach handleIrGraph's post-absent-gate code paths (the
// resolveReleaseClosure call and downstream) without triggering the
// 1003 lockfile-absent envelope.
func writeStubLockfile(t *testing.T, releasePath string) {
	t.Helper()
	lockfilePath := lockfile.PathFor(releasePath)
	if err := os.WriteFile(lockfilePath, []byte{}, 0o644); err != nil {
		t.Fatalf("write stub lockfile %s: %v", lockfilePath, err)
	}
}

// TestIrGraphReleaseLoadFailurePopulatesData exercises the
// errEntityNotResolvable emission site in handleIrGraph when the
// Release file does not exist. Verifies the on-wire envelope
// carries data.file = the absolute path the daemon tried to load.
//
// Complements the existing parse / cache / invalidate coverage in
// server_test.go.
func TestIrGraphReleaseLoadFailurePopulatesData(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	var out bytes.Buffer
	srv := newTestServer(t, tmp, nil, &out)

	// Dispatch aggrega/irGraph with a Release path that does not
	// exist. A stub lockfile at the conventional path lets the
	// flow pass the lockfile-absent gate (1003) and reach the
	// Release-load failure path inside resolveReleaseClosure.
	missing := "release-not-here.yaml"
	writeStubLockfile(t, filepath.Join(tmp, missing))
	var in bytes.Buffer
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: missing}))
	resp := runOneRequest(t, srv, &in, &out)

	if resp.Error == nil {
		t.Fatalf("expected error envelope; got result: %s", resp.Result)
	}
	if resp.Error.Code != errEntityNotResolvable {
		t.Errorf("Code: got %d, want %d", resp.Error.Code, errEntityNotResolvable)
	}

	// Decode the typed Data field — direct access to the typed
	// struct is fine because the test is in package serve.
	if resp.Error.Data == nil {
		t.Fatalf("expected Data field populated for Release-load failure; got nil")
	}
	wantFile := filepath.Join(tmp, missing)
	if resp.Error.Data.File != wantFile {
		t.Errorf("Data.File: got %q, want %q", resp.Error.Data.File, wantFile)
	}
	// Line and column are zero (omitted from the wire) — load
	// failures do not have a sub-file source position.
	if resp.Error.Data.Line != 0 {
		t.Errorf("Data.Line: got %d, want 0 (load failure has no sub-file position)", resp.Error.Data.Line)
	}
	if resp.Error.Data.Column != 0 {
		t.Errorf("Data.Column: got %d, want 0", resp.Error.Data.Column)
	}
}

// TestIrGraphResponseWireShapePreservesData pins the JSON
// round-trip from the handler's *jsonRPCError envelope through
// dispatchIrGraph's writeErrorEnvelope to the wire. Inspects the
// JSON bytes via a permissive map so this test pins the wire field
// names (data, file) — not the Go field names.
//
// Regression guard against accidental reversion to writeError
// (which silently drops the Data field).
func TestIrGraphResponseWireShapePreservesData(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	// Drive dispatch manually so the response bytes remain in `out`
	// for inspection. runOneRequest consumes `out` via its inner
	// decodeOneResponse call and would leave nothing to read. The
	// stub lockfile lets the flow reach resolveReleaseClosure's
	// Release-load failure path (which populates Data.File via the
	// structured error-data envelope).
	missing := "missing.yaml"
	writeStubLockfile(t, filepath.Join(tmp, missing))
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: missing}))
	srv := newTestServer(t, tmp, &in, &out)

	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, &out)

	// out now holds the framed response. Strip the header and
	// inspect the body as a permissive map.
	raw := out.String()
	headerEnd := bytes.Index([]byte(raw), []byte("\r\n\r\n"))
	if headerEnd < 0 {
		t.Fatalf("framed response missing header terminator")
	}
	jsonBody := raw[headerEnd+4:]

	var generic map[string]any
	if err := json.Unmarshal([]byte(jsonBody), &generic); err != nil {
		t.Fatalf("unmarshal: %v (body: %s)", err, jsonBody)
	}
	envelope, ok := generic["error"].(map[string]any)
	if !ok {
		t.Fatalf("response missing error envelope: %v", generic)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("error envelope missing data field; got: %v", envelope)
	}
	file, ok := data["file"].(string)
	if !ok {
		t.Fatalf("data.file missing or not a string; got: %v", data)
	}
	if file == "" {
		t.Errorf("data.file should name the missing Release path; got empty string")
	}
	// data.line and data.column should NOT appear on the wire
	// (omitempty on zero-valued ints).
	if _, present := data["line"]; present {
		t.Errorf("data.line should be omitted (was zero); got present in JSON: %v", data)
	}
	if _, present := data["column"]; present {
		t.Errorf("data.column should be omitted (was zero); got present in JSON: %v", data)
	}
}
