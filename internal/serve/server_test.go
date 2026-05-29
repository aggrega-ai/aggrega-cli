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

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// newTestServer constructs a Server suitable for in-memory unit
// tests. The supplied workspaceRoot is used for catalog lookup
// triggers; stdin/stdout are bytes.Buffer values the test caller
// populates and inspects.
func newTestServer(t *testing.T, workspaceRoot string, stdin io.Reader, stdout io.Writer) *Server {
	t.Helper()
	srv := &Server{
		workspaceRoot: workspaceRoot,
		wsConfig:      workspaceconfig.Default(),
		// lazyCatalog is populated with empty configuration so
		// handleInvalidate's Reset() call doesn't panic on a nil
		// pointer. Tests that exercise lazy-lookup behavior populate
		// their own configured instance; this default suffices for
		// every test that doesn't touch the lookup directly.
		lazyCatalog:      catalog.NewLazyLookup(workspaceRoot, "", nil, nil),
		parseCache:       newCache(0, 0),
		diagnostics:      map[string][]Diagnostic{},
		in:               bufio.NewReader(stdin),
		out:              stdout,
		stderr:           io.Discard,
		positionEncoding: yamlpos.PositionEncodingUTF16,
	}
	// Production initializes serverCtx in dispatchLoop before spawning the
	// reader goroutine. Tests that drive handle() directly bypass that
	// path, so initialize here; the per-request contexts handle() derives
	// then have a live parent.
	srv.serverCtx, srv.serverCancel = context.WithCancel(context.Background())
	t.Cleanup(srv.serverCancel)
	return srv
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

// TestServeAggregaInvalidateWithGranularityFileFallsThroughToCoarse
// verifies that a non-default Granularity value is accepted without
// error and produces the coarse-default workspace-level invalidation.
// At this WI, only the workspace mode is implemented; the field is
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

// TestServeAggregaInvalidateDropsRecipeCatalog verifies the
// workspace-level recipeCatalog field is cleared on invalidate. The
// test pre-populates the field directly (bypassing the lazy-load
// path) and asserts it is nil after invalidate. Subsequent
// aggrega/irGraph would re-load via resolveRecipeCatalog (covered
// transitively by existing irGraph tests; this test pins only the
// drop-on-invalidate behavior).
func TestServeAggregaInvalidateDropsRecipeCatalog(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)
	// Pre-populate recipeCatalog with a non-nil sentinel. An empty
	// *catalog.Catalog is a valid value for nil-vs-non-nil checks
	// without needing the real catalog walker.
	srv.recipeCatalog = &catalog.Catalog{}
	if srv.recipeCatalog == nil {
		t.Fatalf("test setup: recipeCatalog should be non-nil before invalidate")
	}
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/invalidate", invalidateParams{File: "any.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("invalidate returned error: %+v", resp.Error)
	}
	if srv.recipeCatalog != nil {
		t.Errorf("recipeCatalog should be nil after invalidate; got non-nil")
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
	yamlBody := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: invalidate-test/comp
  version: "1.0.0"
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
	srv.recipeCatalog = &catalog.Catalog{} // sentinel

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
	// After both calls, state matches what a single call produces:
	// recipeCatalog nil.
	if srv.recipeCatalog != nil {
		t.Errorf("recipeCatalog should be nil after back-to-back invalidates")
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
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: my-comp\nspec:\n  extends: parent-comp\n"
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
	content := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: my-comp\nspec:\n  extends: parent-comp\n"
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
	if srv.positionEncoding != yamlpos.PositionEncodingUTF8 {
		t.Errorf("srv.positionEncoding = %q; want utf-8 (persisted on Server)", srv.positionEncoding)
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
	if srv.positionEncoding != yamlpos.PositionEncodingUTF32 {
		t.Errorf("srv.positionEncoding = %q; want utf-32", srv.positionEncoding)
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
	if srv.positionEncoding != yamlpos.PositionEncodingUTF8 {
		t.Fatalf("post-initialize srv.positionEncoding = %q; want utf-8", srv.positionEncoding)
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
	if srv.positionEncoding != yamlpos.PositionEncodingUTF8 {
		t.Errorf("srv.positionEncoding mutated by validate; got %q; want utf-8", srv.positionEncoding)
	}
}
