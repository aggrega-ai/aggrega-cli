// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/requestcancel"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// newTestSession constructs the process-level session an in-memory test drives,
// with launchRoot naming the workspace the harness declares for pushes.
//
// It does NOT call workspaceconfig.Resolve. The production launch path does, but
// a resolution here would read whatever configuration the machine running the
// test happens to carry — a global file, an AGGREGA_CONFIG pointing somewhere —
// and the settings under test would then vary by machine. The built-in defaults
// are what every test wants unless it says otherwise, so they are supplied
// directly.
func newTestSession(t *testing.T, launchRoot string, stdin io.Reader, stdout io.Writer) *session {
	t.Helper()
	sess := &session{
		launchRoot: launchRoot,
		launchCfg:  workspaceconfig.Default(),
		workspaces: newWorkspaceRegistry(),
		in:         bufio.NewReader(stdin),
		out:        stdout,
		stderr:     io.Discard,
	}
	// Production initializes serverCtx in dispatchLoop before spawning the
	// reader goroutine. Tests that drive handle() directly bypass that
	// path, so initialize here; the per-request contexts handle() derives
	// then have a live parent.
	sess.serverCtx, sess.serverCancel = context.WithCancel(context.Background())
	sess.cancelTracker = requestcancel.New()
	// The harness stands in for a client that declared its workspace at connect,
	// which is what makes a pushed diagnostic reach the test's own buffer. A test
	// asserting the confinement declares something else and reads the silence.
	sess.setPushRoot(launchRoot)
	t.Cleanup(sess.serverCancel)
	return sess
}

// newTestWorkspace registers a workspace entry under root on sess and returns it,
// built from the built-in default configuration for the reason newTestSession
// gives. An entry already held for root is returned as it stands, so a test can
// reach the same warm caches a second request would.
func newTestWorkspace(sess *session, root string) *Server {
	key := filepath.Clean(root)
	sess.workspaces.mu.Lock()
	defer sess.workspaces.mu.Unlock()
	if ws := sess.workspaces.byRoot[key]; ws != nil {
		return ws
	}
	cfg := workspaceconfig.Default()
	ws := &Server{
		session:       sess,
		workspaceRoot: root,
		wsConfig:      cfg,
		// lazyCatalog is populated with empty configuration so
		// handleInvalidate's Reset() call doesn't panic on a nil
		// pointer. Tests that exercise lazy-lookup behavior populate
		// their own configured instance; this default suffices for
		// every test that doesn't touch the lookup directly.
		lazyCatalog:     catalog.NewLazyLookup(root, "", nil, nil),
		parseCache:      newCache(0, 0),
		diagnostics:     map[string]semanticEntry{},
		renderDiags:     map[string][]Diagnostic{},
		conventionDiags: map[string][]Diagnostic{},
	}
	sess.workspaces.byRoot[key] = ws
	return ws
}

// reanchorTestWorkspace re-publishes ws under its CURRENT root and returns it,
// pointing the session's push declaration at that root too.
//
// A test that changes a workspace entry's root after building it calls this. The
// registry is keyed on the root and a request names that key, so an entry left
// under its old key is invisible to the request naming its new one — and that
// request is then answered from a workspace resolved fresh off disk, carrying
// none of the test's catalog configuration. The symptom is a handler reporting
// that no entity matches a URI the fixture plainly declares, which reads like a
// resolution defect rather than a harness one.
func reanchorTestWorkspace(t *testing.T, ws *Server) *Server {
	t.Helper()
	if ws.workspaceRoot == "" {
		t.Fatalf("reanchorTestWorkspace: the entry carries no workspace root to key it by")
	}
	ws.session.launchRoot = ws.workspaceRoot
	ws.setPushRoot(ws.workspaceRoot)
	ws.workspaces.mu.Lock()
	defer ws.workspaces.mu.Unlock()
	ws.workspaces.byRoot[filepath.Clean(ws.workspaceRoot)] = ws
	return ws
}

// newTestServer constructs a Server suitable for in-memory unit
// tests: a session whose launch and declared-push workspace is
// workspaceRoot, plus the workspace entry for that root. stdin/stdout are
// bytes.Buffer values the test caller populates and inspects.
func newTestServer(t *testing.T, workspaceRoot string, stdin io.Reader, stdout io.Writer) *Server {
	t.Helper()
	sess := newTestSession(t, workspaceRoot, stdin, stdout)
	return newTestWorkspace(sess, workspaceRoot)
}

// serializerContext returns a context carrying a per-request sink over
// a freshly-started serializer writing to out, plus a flush function
// that closes the serializer and waits for it to drain. Tests that
// drive a write surface directly (notifications, single handlers) use
// this so they exercise the one production write path instead of a
// bypass. The sink uses arrival sequence 1.
func serializerContext(t *testing.T, out io.Writer) (context.Context, func()) {
	t.Helper()
	ser := newSerializer(out)
	go ser.run()
	ctx := withRequestSink(context.Background(), &requestSink{ser: ser, seq: 1})
	return ctx, func() { ser.closeAndWait() }
}

// declareWorkspace returns req's params carrying a `workspaceRoot` key naming root,
// which is what a well-behaved client sends on every request that acts on a
// workspace.
//
// It is a harness convenience and it is bounded in three ways, because a harness
// that supplied more than a client does would blind the assertions that matter.
//
// It supplies the parameter only for a method the routing table says needs one,
// read from that table rather than from a list here — so the harness mirrors the
// rule instead of holding a second copy of it, and the handshake, whose own
// workspaceRoot field means something else entirely, is left untouched.
//
// It never overwrites a value the test set, so a test naming its own workspace —
// a second one, or a deliberately bad one — is sent exactly what it wrote.
//
// And it is absent from the verbatim helpers below, which put the test's bytes
// on the wire as written. Every assertion about the parameter itself goes
// through those: a refusal cannot be proved through a helper that supplies the
// thing being refused.
func declareWorkspace(t *testing.T, req *jsonRPCRequest, root string) json.RawMessage {
	t.Helper()
	route, known := methodIndex[req.Method]
	if !known || route.workspaceFree {
		return req.Params
	}
	fields := map[string]json.RawMessage{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &fields); err != nil {
			// Not an object: a malformed-params test. Leave it exactly as written.
			return req.Params
		}
	}
	if _, already := fields["workspaceRoot"]; already {
		return req.Params
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal workspace root: %v", err)
	}
	fields["workspaceRoot"] = encoded
	merged, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal params with workspace: %v", err)
	}
	return merged
}

// driveHandle runs a single parsed request through the production
// serializer path, writing its framed response to out. It mirrors the
// dispatch loop's per-request flow (install a serializer, run the
// handler under arrival sequence 1, close the slot, flush) so tests
// that need to inspect raw stdout exercise the one write path.
//
// The request is sent naming srv's workspace, per declareWorkspace.
func driveHandle(t *testing.T, srv *Server, req *jsonRPCRequest, out io.Writer) {
	t.Helper()
	req.Params = declareWorkspace(t, req, srv.workspaceRoot)
	driveHandleVerbatim(t, srv.session, req, out)
}

// driveHandleVerbatim is driveHandle with the request put on the wire exactly as
// written: no workspace is supplied for it. Tests about the workspace parameter
// itself use this.
func driveHandleVerbatim(t *testing.T, sess *session, req *jsonRPCRequest, out io.Writer) {
	t.Helper()
	ser := newSerializer(out)
	go ser.run()
	frame := sess.handle(ser, 1, req)
	ser.emit(1, frame)
	ser.closeAndWait()
	select {
	case serr := <-ser.errCh:
		t.Fatalf("serializer write error: %v", serr)
	default:
	}
}

// framedRequest returns a Content-Length-framed JSON-RPC request body
// for the supplied method + params, with a deterministic id.
func framedRequest(id int, method string, params any) []byte {
	paramBytes, _ := json.Marshal(params)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  json.RawMessage(paramBytes),
	})
	return append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))), body...)
}

// framedRequestIn is framedRequest with the workspace the request acts on named
// in its params, the way a client sends it.
//
// A test that writes framed bytes straight onto the daemon's stdin drives the
// real dispatch loop, so nothing sits between those bytes and the router that
// could supply the parameter — the bytes ARE the request. Such a test names the
// workspace here.
func framedRequestIn(t *testing.T, srv *Server, id int, method string, params any) []byte {
	t.Helper()
	return framedRequestAt(t, srv.workspaceRoot, id, method, params)
}

// framedRequestAt is framedRequestIn against a workspace root the caller holds
// rather than one read off a Server. A test that writes its request script
// before the server exists uses it.
func framedRequestAt(t *testing.T, root string, id int, method string, params any) []byte {
	t.Helper()
	paramBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	req := &jsonRPCRequest{Method: method, Params: paramBytes}
	withRoot := declareWorkspace(t, req, root)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  withRoot,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))), body...)
}

// rawFramed wraps an arbitrary body in JSON-RPC framing (for
// malformed-body tests).
func rawFramed(body string) []byte {
	return []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body))
}

// decodeOneResponse reads a single Content-Length-framed message from
// out and decodes it as jsonRPCResponse.
//
// It reads the FIRST frame, and tests rely on that: a notification pushed ahead
// of the answer breaks the decode, which is how a test built on it asserts that
// its request pushed nothing. A test whose request is expected to push reads the
// answer through decodeResponsePastPushes instead.
func decodeOneResponse(t *testing.T, out *bytes.Buffer) jsonRPCResponse {
	t.Helper()
	r := bufio.NewReader(out)
	body, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, body)
	}
	return resp
}

// decodeResponsePastPushes reads every Content-Length-framed message on out and
// returns the response together with the aggrega/publishDiagnostics
// notifications that preceded it, in the order they were written.
//
// It exists for a request that pushes before it answers — one that finds a
// Release's lockfile gone withdraws the drift finding on that path before it
// refuses — so the pushes are handed to the test rather than skipped: a test
// reading through here can still assert on what was pushed, and one that has no
// interest in it says so by discarding the second value.
func decodeResponsePastPushes(t *testing.T, out *bytes.Buffer) (jsonRPCResponse, []publishDiagnosticsParams) {
	t.Helper()
	var pushes []publishDiagnosticsParams
	for _, body := range readAllFrames(t, out) {
		var f wireFrame
		if err := json.Unmarshal(body, &f); err != nil {
			t.Fatalf("decode frame: %v\nframe: %s", err, body)
		}
		if f.Method != "" && len(f.ID) == 0 {
			// A notification. Only a diagnostics publication is handed back; any
			// other kind is not what these tests are about.
			if f.Method == "aggrega/publishDiagnostics" {
				var params publishDiagnosticsParams
				if err := json.Unmarshal(f.Params, &params); err != nil {
					t.Fatalf("decode publishDiagnostics params: %v\nparams: %s", err, f.Params)
				}
				pushes = append(pushes, params)
			}
			continue
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("unmarshal response: %v (body: %s)", err, body)
		}
		return resp, pushes
	}
	t.Fatalf("no response was written; %d publication(s) were", len(pushes))
	return jsonRPCResponse{}, nil
}

// runOneRequest drives a single request through the same serializer
// path production uses and returns the decoded response. It installs a
// serializer over out, runs the handler under arrival sequence 1,
// closes the handler's ordered slot with the returned body, and waits
// for the serializer to flush before decoding — so the in-memory
// harness exercises the one write path rather than a bypass.
// The request is sent naming srv's workspace, per declareWorkspace.
func runOneRequest(t *testing.T, srv *Server, in *bytes.Buffer, out *bytes.Buffer) jsonRPCResponse {
	t.Helper()
	return runOneRequestIn(t, srv.session, srv.workspaceRoot, in, out)
}

// runOneRequestIn is runOneRequest sending the request against the workspace
// root named here rather than the one the Server carries. Tests driving one
// daemon over two workspaces use it.
func runOneRequestIn(t *testing.T, sess *session, root string, in *bytes.Buffer, out *bytes.Buffer) jsonRPCResponse {
	t.Helper()
	sess.in = bufio.NewReader(in)
	sess.out = out
	body, err := readMessage(sess.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	req.Params = declareWorkspace(t, &req, root)
	return dispatchOne(t, sess, &req, out)
}

// runOneRequestPastPushes is runOneRequest for a request expected to push before
// it answers. It returns the answer together with the diagnostics publications
// that preceded it, read by decodeResponsePastPushes, rather than decoding the
// first frame as the answer.
func runOneRequestPastPushes(t *testing.T, srv *Server, in *bytes.Buffer, out *bytes.Buffer) (jsonRPCResponse, []publishDiagnosticsParams) {
	t.Helper()
	srv.in = bufio.NewReader(in)
	srv.out = out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, out)
	return decodeResponsePastPushes(t, out)
}

// runOneRequestVerbatim is runOneRequest with the request put on the wire
// exactly as written: no workspace is supplied for it. Tests about the workspace
// parameter itself use this.
func runOneRequestVerbatim(t *testing.T, sess *session, in *bytes.Buffer, out *bytes.Buffer) jsonRPCResponse {
	t.Helper()
	sess.in = bufio.NewReader(in)
	sess.out = out
	body, err := readMessage(sess.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return dispatchOne(t, sess, &req, out)
}

// dispatchOne runs req through the production per-request flow and decodes the
// one response it produced.
func dispatchOne(t *testing.T, sess *session, req *jsonRPCRequest, out *bytes.Buffer) jsonRPCResponse {
	t.Helper()
	ser := newSerializer(out)
	go ser.run()
	frame := sess.handle(ser, 1, req)
	ser.emit(1, frame)
	ser.closeAndWait()
	select {
	case serr := <-ser.errCh:
		t.Fatalf("serializer write error: %v", serr)
	default:
	}
	return decodeOneResponse(t, out)
}

// newLazyCascadeServer builds a Server for the lazy-cascade contract
// tests. Unlike newTestServer it does NOT suppress the cascade guard, so
// the first Release-context request runs the real cascade against
// workspaceRoot — the production behavior under test here.
func newLazyCascadeServer(t *testing.T, workspaceRoot string, in io.Reader, out io.Writer) *Server {
	t.Helper()
	sess := newTestSession(t, workspaceRoot, in, out)
	return newTestWorkspace(sess, workspaceRoot)
}

// TestValidateWithNoReleaseAnswersForTheDocumentAlone pins the contract for a
// request that names no Release: it is answered, not refused, and the answer
// says which of the two answers it is.
//
// Naming none is a request in its own right — for the checks a document can be
// held to on its own — so the reply carries the findings those checks produced
// and the checked key naming what they covered. Asserting the absence of an
// invalid-params envelope is half the point: refusing here would take the parse
// half of the handler down with the semantic half, and the parse half is what
// every cache-driven operation reads.
//
// The workspace holds exactly one Release, which is the case a daemon inferring
// its own would have resolved. That is deliberate in the other direction now:
// the answer must not quietly acquire that Release. The rows below produce
// findings a document answers for by itself, so a run that had resolved a
// Release anyway would still be caught by the checked key.
func TestValidateWithNoReleaseAnswersForTheDocumentAlone(t *testing.T) {
	ws := t.TempDir()
	writeRelease(t, ws, "release.yaml", "release/only")

	// A Release with values: on a boundary component — a document-envelope
	// violation, which the envelope leg reports without consulting any Release.
	envelopeViolation := `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: r
  environment: dev
  version: "1.0.0"
spec:
  aggregate: a
  manifest: m
  boundaries:
    main:
      directory: ./d
      components:
        api:
          values:
            port: 9999
`

	cases := []struct {
		name    string
		file    string
		content string
	}{
		{"unparseable-buffer", "broken.yaml", "key: [unterminated\n"},
		{"document-envelope-violation", "candidate.yaml", envelopeViolation},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := newLazyCascadeServer(t, ws, nil, nil)
			var in, out bytes.Buffer
			in.Write(framedRequest(1, "aggrega/validate", validateParams{
				File:    filepath.Join(ws, tc.file),
				Content: tc.content,
			}))
			resp := runOneRequest(t, srv, &in, &out)
			if resp.Error != nil {
				t.Fatalf("validate naming no Release was refused: %+v", resp.Error)
			}
			var result validateResult
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			if len(result.Diagnostics) == 0 {
				t.Errorf("no findings reported; the checks a document answers for by itself did not run")
			}
			if result.Checked != checkedDocument {
				t.Errorf("Checked = %q; want %q, so this answer cannot be read as a full verdict", result.Checked, checkedDocument)
			}
		})
	}
}

// TestValidateWithAReleaseOmitsTheCheckedKey is the other side of that
// discriminator, and without it the key above proves only that a constant can be
// set. A request naming a Release gets the method's ordinary answer, so the key
// is absent — which is what lets a client tell one answer from the other by
// reading a single field.
func TestValidateWithAReleaseOmitsTheCheckedKey(t *testing.T) {
	ws := t.TempDir()
	writeRelease(t, ws, "release.yaml", "release/only")

	srv := newLazyCascadeServer(t, ws, nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    filepath.Join(ws, "x.yaml"),
		Content: "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: x\n  version: \"1.0.0\"\nspec:\n  components: {}\n",
		Release: "release.yaml",
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("validate naming a Release returned an error envelope: %+v", resp.Error)
	}
	// Read as a bare object rather than into validateResult: the contract is
	// that the key is absent from the wire, and unmarshalling into the struct
	// would report an absent key and an empty one identically.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &raw); err != nil {
		t.Fatalf("unmarshal result object: %v", err)
	}
	if v, present := raw["checked"]; present {
		t.Errorf("checked is present as %s on an answer that named a Release; it must be omitted", v)
	}
}

// TestValidateWithNoReleaseStillSeedsTheParseCache pins the capability the
// refusing shape would have removed, at the surface that consumes it rather
// than at the cache. aggrega/diagnostics answers only from a parse-cache entry
// and refuses with file-not-validated when there is none, so driving it after a
// Release-less validation is what proves the seeding happened — and by
// extension that hover, completion, resolve, findReferences and
// findImplementations can still answer in a workspace holding no Release.
//
// The workspace here holds none at all, which is the case no later work can
// repair: there is no Release for a client to name.
func TestValidateWithNoReleaseStillSeedsTheParseCache(t *testing.T) {
	ws := t.TempDir()
	srv := newLazyCascadeServer(t, ws, nil, nil)
	file := filepath.Join(ws, "aggregate.yaml")
	content := "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: x\n  version: \"1.0.0\"\nspec:\n  components: {}\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("write aggregate: %v", err)
	}

	var vin, vout bytes.Buffer
	vin.Write(framedRequest(1, "aggrega/validate", validateParams{File: file, Content: content}))
	if resp := runOneRequest(t, srv, &vin, &vout); resp.Error != nil {
		t.Fatalf("validate naming no Release was refused: %+v", resp.Error)
	}

	var din, dout bytes.Buffer
	din.Write(framedRequest(2, "aggrega/diagnostics", diagnosticsParams{File: file}))
	resp := runOneRequest(t, srv, &din, &dout)
	if resp.Error != nil {
		t.Fatalf("diagnostics after a Release-less validation returned %+v; the parse cache was not seeded", resp.Error)
	}
}

// TestValidateRefusesAReleaseOutsideTheWorkspace pins the other half of the
// parameter's contract on this surface: the value is a workspace-relative path
// naming a document inside the root, and an absolute path or one escaping the
// root is refused rather than normalised. Normalising would hide the same
// mistake in every client that made it.
func TestValidateRefusesAReleaseOutsideTheWorkspace(t *testing.T) {
	ws := t.TempDir()
	writeRelease(t, ws, "release.yaml", "release/only")

	srv := newLazyCascadeServer(t, ws, nil, nil)
	file := filepath.Join(ws, "x.yaml")

	for _, release := range []string{filepath.Join(ws, "release.yaml"), "../elsewhere/release.yaml"} {
		release := release
		t.Run(release, func(t *testing.T) {
			var in, out bytes.Buffer
			in.Write(framedRequest(1, "aggrega/validate", validateParams{File: file, Content: "kind: Foo\n", Release: release}))
			resp := runOneRequest(t, srv, &in, &out)
			if resp.Error == nil {
				t.Fatalf("validate accepted release %q: %s", release, resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("Code = %d; want %d", resp.Error.Code, jsonRPCInvalidParams)
			}
		})
	}
}

func TestServeAggregaValidateParseOnlyReturnsDiagnostics(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	file := filepath.Join(tmp, "x.yaml")
	content := "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: x\n  version: \"1.0.0\"\nspec:\n  components: {}\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	srv := newTestServer(t, tmp, nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    file,
		Content: content,
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("expected empty diagnostics for well-formed YAML; got %d entries", len(result.Diagnostics))
	}
	if _, ok := srv.parseCache.Get(file); !ok {
		t.Errorf("parse cache not populated after well-formed validate")
	}
}

func TestServeAggregaValidateParseErrorReturnsLineColumn(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    "bad.yaml",
		Content: "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n  unindented: bad\n",
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("unexpected envelope error: %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Diagnostics) == 0 {
		t.Fatalf("expected at least one diagnostic for malformed YAML")
	}
	d := result.Diagnostics[0]
	if d.Line < 1 {
		t.Errorf("diagnostic line should be >= 1; got %d", d.Line)
	}
	if d.Column < 1 {
		t.Errorf("diagnostic column should be >= 1 (never 0); got %d", d.Column)
	}
	if d.Severity != "error" {
		t.Errorf("diagnostic severity = %q; want \"error\"", d.Severity)
	}
}

func TestServeAggregaInvalidateDropsCacheEntry(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	file := filepath.Join(tmp, "y.yaml")
	if err := os.WriteFile(file, []byte("kind: Foo\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	srv := newTestServer(t, tmp, nil, nil)
	// Seed the cache via a validate first.
	var in1, out1 bytes.Buffer
	in1.Write(framedRequest(1, "aggrega/validate", validateParams{File: file, Content: "kind: Foo\n"}))
	runOneRequest(t, srv, &in1, &out1)
	if _, ok := srv.parseCache.Get(file); !ok {
		t.Fatalf("parse cache not seeded after validate")
	}
	// Invalidate.
	var in2, out2 bytes.Buffer
	in2.Write(framedRequest(2, "aggrega/invalidate", invalidateParams{File: file}))
	resp := runOneRequest(t, srv, &in2, &out2)
	if resp.Error != nil {
		t.Fatalf("invalidate returned error: %+v", resp.Error)
	}
	var result invalidateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.Invalidated {
		t.Errorf("invalidate response should report invalidated: true")
	}
	if _, ok := srv.parseCache.Get(file); ok {
		t.Errorf("parse cache entry still present after invalidate")
	}
}

// TestServeAggregaInvalidateWithGranularityFileFallsThroughToCoarse
// verifies that a non-default Granularity value is accepted without
// error and produces the coarse-default workspace-level invalidation.
// Only the workspace mode is implemented today; the field is
// forward-compatible reservation for future per-file / per-entity
// modes.
func TestServeAggregaInvalidateWithGranularityFileFallsThroughToCoarse(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/invalidate", invalidateParams{
		File:        "anything.yaml",
		Granularity: "file",
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("invalidate with granularity=file returned error: %+v", resp.Error)
	}
	var result invalidateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.Invalidated {
		t.Errorf("invalidated should be true regardless of granularity field")
	}
}

// TestServeAggregaInvalidateResetsLazyCatalog verifies the lazy
// lookup's hot cache is cleared on invalidate. The test populates
// the hot cache via a Tier-1 walk against a fixture, removes the
// underlying file, calls invalidate, then asserts the subsequent
// Component(uri) misses — proving the cache was reset (otherwise
// the cached entity would have survived the file removal).
func TestServeAggregaInvalidateResetsLazyCatalog(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	catalogDir := filepath.Join(tmp, "catalog")
	componentDir := filepath.Join(catalogDir, "component")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yamlBody := `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: invalidate-test/comp
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec: {}
`
	yamlPath := filepath.Join(componentDir, "comp.yaml")
	if err := os.WriteFile(yamlPath, []byte(yamlBody), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	srv := newTestServer(t, tmp, nil, nil)
	// Replace the default empty lazyCatalog with one configured
	// against the fixture catalog dir.
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{catalogDir}, nil)
	lookup := srv.lazyCatalog.AsCatalogLookup()

	// Populate the hot cache via Tier-1 walk.
	if _, ok := lookup.Component("invalidate-test/comp"); !ok {
		t.Fatalf("initial Component lookup missed; fixture wiring broken")
	}
	// Remove the underlying file; without invalidate the hot cache
	// would still serve the cached entity.
	if err := os.Remove(yamlPath); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}

	// Invalidate.
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/invalidate", invalidateParams{File: yamlPath}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("invalidate returned error: %+v", resp.Error)
	}

	// After invalidate + file removal, the next lookup must miss.
	if _, ok := lookup.Component("invalidate-test/comp"); ok {
		t.Errorf("Component should miss after invalidate + file removal; hot cache stale")
	}
}

// TestServeAggregaInvalidateBackToBackIdempotent verifies that two
// sequential invalidate calls both succeed without error and leave
// the daemon in the same state. Sequential dispatch + idempotent
// drop primitives mean the second call is a no-op in observable
// terms; "no thrashing" at the call boundary.
func TestServeAggregaInvalidateBackToBackIdempotent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)

	for i := 1; i <= 2; i++ {
		var in, out bytes.Buffer
		in.Write(framedRequest(i, "aggrega/invalidate", invalidateParams{File: "any.yaml"}))
		resp := runOneRequest(t, srv, &in, &out)
		if resp.Error != nil {
			t.Fatalf("invalidate call %d returned error: %+v", i, resp.Error)
		}
		var result invalidateResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("call %d unmarshal: %v", i, err)
		}
		if !result.Invalidated {
			t.Errorf("call %d: invalidated should be true", i)
		}
	}
}

func TestServeAggregaDiagnosticsReturns1001WhenNotCached(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/diagnostics", diagnosticsParams{File: "never-validated.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error == nil {
		t.Fatalf("expected error envelope; got result %s", resp.Result)
	}
	if resp.Error.Code != errFileNotValidated {
		t.Errorf("error code = %d; want %d (file-not-validated)", resp.Error.Code, errFileNotValidated)
	}
	if !strings.Contains(resp.Error.Message, "never-validated.yaml") {
		t.Errorf("error message should name the missing file; got %q", resp.Error.Message)
	}
}

func TestServeAggregaDiagnosticsReturns1001WhenMtimeNewer(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	file := filepath.Join(tmp, "stale.yaml")
	if err := os.WriteFile(file, []byte("kind: Foo\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	srv := newTestServer(t, tmp, nil, nil)
	// Validate first; cache populates.
	var in1, out1 bytes.Buffer
	in1.Write(framedRequest(1, "aggrega/validate", validateParams{File: file, Content: "kind: Foo\n"}))
	runOneRequest(t, srv, &in1, &out1)
	// Advance mtime so it's newer than cachedAt.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(file, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	// Diagnostics should now report 1001 (cache evicted by Get's mtime check).
	var in2, out2 bytes.Buffer
	in2.Write(framedRequest(2, "aggrega/diagnostics", diagnosticsParams{File: file}))
	resp := runOneRequest(t, srv, &in2, &out2)
	if resp.Error == nil {
		t.Fatalf("expected 1001 envelope after mtime advance; got result %s", resp.Result)
	}
	if resp.Error.Code != errFileNotValidated {
		t.Errorf("error code = %d; want %d (mtime eviction → file-not-validated)", resp.Error.Code, errFileNotValidated)
	}
}

func TestServeAggregaResolveReturns1001WhenFileNotValidated(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/resolve", resolveParams{URI: "file:///never-validated.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error == nil {
		t.Fatal("resolve on un-validated file should return error envelope; got result")
	}
	if resp.Error.Code != errFileNotValidated {
		t.Errorf("error code = %d; want %d (file-not-validated)", resp.Error.Code, errFileNotValidated)
	}
}

func TestServeAggregaFindReferencesReturnsEmptyWhenCursorNotOnMetadataName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "comp.yaml")
	content := "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: my-comp\nspec:\n  extends: parent-comp\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := newTestServer(t, dir, nil, nil)
	var in1, out1 bytes.Buffer
	in1.Write(framedRequest(1, "aggrega/validate", validateParams{File: file, Content: content}))
	runOneRequest(t, srv, &in1, &out1)
	var in2, out2 bytes.Buffer
	in2.Write(framedRequest(2, "aggrega/findReferences", findReferencesParams{
		URI: file,
		// Cursor on spec.extends value — NOT a metadata.name. Result
		// should be empty (LSP convention: no error, empty list).
		Position: yamlpos.Position{Line: 5, Character: 13},
	}))
	resp := runOneRequest(t, srv, &in2, &out2)
	if resp.Error != nil {
		t.Fatalf("findReferences returned error: %+v", resp.Error)
	}
	var result findReferencesResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Locations) != 0 {
		t.Errorf("findReferences with cursor not on metadata.name should return empty Locations; got %d", len(result.Locations))
	}
}

func TestServeAggregaFindImplementationsReturnsEmptyWhenCursorNotOnMetadataName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "comp.yaml")
	content := "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: my-comp\nspec:\n  extends: parent-comp\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := newTestServer(t, dir, nil, nil)
	var in1, out1 bytes.Buffer
	in1.Write(framedRequest(1, "aggrega/validate", validateParams{File: file, Content: content}))
	runOneRequest(t, srv, &in1, &out1)
	var in2, out2 bytes.Buffer
	in2.Write(framedRequest(2, "aggrega/findImplementations", findImplementationsParams{
		URI:      file,
		Position: yamlpos.Position{Line: 5, Character: 13},
	}))
	resp := runOneRequest(t, srv, &in2, &out2)
	if resp.Error != nil {
		t.Fatalf("findImplementations returned error: %+v", resp.Error)
	}
	var result findImplementationsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Locations) != 0 {
		t.Errorf("findImplementations with cursor not on metadata.name should return empty Locations; got %d", len(result.Locations))
	}
}

func TestServeAggregaHoverReturns1001WhenFileNotValidated(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/hover", hoverParams{URI: "file:///never-validated.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error == nil {
		t.Fatal("hover on un-validated file should return error envelope; got result")
	}
	if resp.Error.Code != errFileNotValidated {
		t.Errorf("error code = %d; want %d (file-not-validated)", resp.Error.Code, errFileNotValidated)
	}
}

func TestServeAggregaCompletionReturns1001WhenFileNotValidated(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/completion", completionParams{URI: "file:///never-validated.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error == nil {
		t.Fatal("completion on un-validated file should return error envelope; got result")
	}
	if resp.Error.Code != errFileNotValidated {
		t.Errorf("error code = %d; want %d (file-not-validated)", resp.Error.Code, errFileNotValidated)
	}
}

func TestServeMalformedJSONReturnsParseErrorEnvelope(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(rawFramed("not-valid-json{"))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if jerr := json.Unmarshal(body, &req); jerr != nil {
		envBytes, merr := marshalErrorResponse(nil, jsonRPCParseError, "parse error: "+jerr.Error())
		if merr != nil {
			t.Fatalf("marshalErrorResponse: %v", merr)
		}
		ser := newSerializer(&out)
		go ser.run()
		ser.emit(1, envBytes)
		ser.closeAndWait()
	}
	resp := decodeOneResponse(t, &out)
	if resp.Error == nil {
		t.Fatalf("expected parse-error envelope; got result %s", resp.Result)
	}
	if resp.Error.Code != jsonRPCParseError {
		t.Errorf("error code = %d; want %d (parse-error)", resp.Error.Code, jsonRPCParseError)
	}
}

func TestServeUnknownMethodReturnsMethodNotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/bogus-method", map[string]any{}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error == nil {
		t.Fatalf("expected method-not-found envelope; got result %s", resp.Result)
	}
	if resp.Error.Code != jsonRPCMethodNotFound {
		t.Errorf("error code = %d; want %d (method-not-found)", resp.Error.Code, jsonRPCMethodNotFound)
	}
}

func TestServeAggregaInitializeSelectsUTF8WhenOffered(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/initialize", initializeParams{
		Capabilities: clientCapabilities{
			General: &generalCapabilities{
				PositionEncodings: []string{"utf-16", "utf-8"},
			},
		},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("initialize returned error: %+v", resp.Error)
	}
	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Capabilities.PositionEncoding != "utf-8" {
		t.Errorf("PositionEncoding = %q; want utf-8 (server-preference)", result.Capabilities.PositionEncoding)
	}
	if srv.positionEncoding() != yamlpos.PositionEncodingUTF8 {
		t.Errorf("srv.positionEncoding() = %q; want utf-8 (persisted on Server)", srv.positionEncoding())
	}
}

func TestServeAggregaInitializeDefaultsToUTF16WhenNoNegotiation(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	// No general capability — daemon defaults to UTF-16.
	in.Write(framedRequest(1, "aggrega/initialize", initializeParams{}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("initialize returned error: %+v", resp.Error)
	}
	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Capabilities.PositionEncoding != "utf-16" {
		t.Errorf("PositionEncoding = %q; want utf-16 (default when no negotiation)", result.Capabilities.PositionEncoding)
	}
}

func TestServeAggregaInitializeSelectsUTF32WhenOnlyOption(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/initialize", initializeParams{
		Capabilities: clientCapabilities{
			General: &generalCapabilities{
				PositionEncodings: []string{"utf-32"},
			},
		},
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("initialize returned error: %+v", resp.Error)
	}
	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Capabilities.PositionEncoding != "utf-32" {
		t.Errorf("PositionEncoding = %q; want utf-32", result.Capabilities.PositionEncoding)
	}
	if srv.positionEncoding() != yamlpos.PositionEncodingUTF32 {
		t.Errorf("srv.positionEncoding() = %q; want utf-32", srv.positionEncoding())
	}
}

func TestServeAggregaInitializeUpdatesEncodingForSubsequentValidate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srv := newTestServer(t, dir, nil, nil)
	// Initialize negotiates UTF-8.
	var in1, out1 bytes.Buffer
	in1.Write(framedRequest(1, "aggrega/initialize", initializeParams{
		Capabilities: clientCapabilities{
			General: &generalCapabilities{
				PositionEncodings: []string{"utf-8"},
			},
		},
	}))
	runOneRequest(t, srv, &in1, &out1)
	if srv.positionEncoding() != yamlpos.PositionEncodingUTF8 {
		t.Fatalf("post-initialize srv.positionEncoding() = %q; want utf-8", srv.positionEncoding())
	}
	// Subsequent validate succeeds — the encoding is now bound on the
	// Server and threaded through resolve.go's BuildLineIndexWithEncoding
	// calls. (Validate itself doesn't build a LineIndex; the assertion
	// is on Server state, which subsequent resolve operations consume.)
	file := filepath.Join(dir, "x.yaml")
	if err := os.WriteFile(file, []byte("kind: Foo\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var in2, out2 bytes.Buffer
	in2.Write(framedRequest(2, "aggrega/validate", validateParams{File: file, Content: "kind: Foo\n"}))
	resp := runOneRequest(t, srv, &in2, &out2)
	if resp.Error != nil {
		t.Fatalf("validate returned error after initialize: %+v", resp.Error)
	}
	if srv.positionEncoding() != yamlpos.PositionEncodingUTF8 {
		t.Errorf("srv.positionEncoding() mutated by validate; got %q; want utf-8", srv.positionEncoding())
	}
}
