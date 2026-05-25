// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// newTestServer constructs a Server suitable for in-memory unit
// tests. The supplied workspaceRoot is used for catalog lookup
// triggers; stdin/stdout are bytes.Buffer values the test caller
// populates and inspects.
func newTestServer(t *testing.T, workspaceRoot string, stdin io.Reader, stdout io.Writer) *Server {
	t.Helper()
	return &Server{
		workspaceRoot: workspaceRoot,
		wsConfig:      workspaceconfig.Default(),
		parseCache:    newCache(),
		diagnostics:   map[string][]Diagnostic{},
		in:            bufio.NewReader(stdin),
		out:           stdout,
		stderr:        io.Discard,
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

// rawFramed wraps an arbitrary body in JSON-RPC framing (for
// malformed-body tests).
func rawFramed(body string) []byte {
	return []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body))
}

// decodeOneResponse reads a single Content-Length-framed message from
// out and decodes it as jsonRPCResponse.
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

// runOneRequest drives a single request through the dispatch loop and
// returns the decoded response.
func runOneRequest(t *testing.T, srv *Server, in *bytes.Buffer, out *bytes.Buffer) jsonRPCResponse {
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
	if err := srv.handle(&req); err != nil {
		t.Fatalf("handle: %v", err)
	}
	return decodeOneResponse(t, out)
}

func TestServeAggregaValidateParseOnlyReturnsDiagnostics(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	file := filepath.Join(tmp, "x.yaml")
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n"
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
		Content: "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n  unindented: bad\n",
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

func TestServeAggregaResolveReturnsNullStub(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/resolve", resolveParams{URI: "file:///x.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("stub resolve returned error: %+v", resp.Error)
	}
	var result resolveResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Location != nil {
		t.Errorf("resolve stub should return Location: nil; got %+v", result.Location)
	}
}

func TestServeAggregaFindReferencesReturnsEmptyStub(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/findReferences", resolveParams{URI: "file:///x.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("stub findReferences returned error: %+v", resp.Error)
	}
	var result findReferencesResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Locations) != 0 {
		t.Errorf("findReferences stub should return empty Locations; got %d entries", len(result.Locations))
	}
}

func TestServeAggregaHoverReturnsNullStub(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/hover", resolveParams{URI: "file:///x.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("stub hover returned error: %+v", resp.Error)
	}
	var result hoverResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Contents != nil {
		t.Errorf("hover stub should return Contents: nil; got %+v", result.Contents)
	}
}

func TestServeAggregaCompletionReturnsEmptyStub(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/completion", resolveParams{URI: "file:///x.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("stub completion returned error: %+v", resp.Error)
	}
	var result completionResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("completion stub should return empty Items; got %d entries", len(result.Items))
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
		if werr := srv.writeError(nil, jsonRPCParseError, "parse error: "+jerr.Error()); werr != nil {
			t.Fatalf("writeError: %v", werr)
		}
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
