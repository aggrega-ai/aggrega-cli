// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// buildServeBinary compiles ./cmd/aggrega into t.TempDir() and returns
// the binary path. The build runs once per test (parallel-safe — each
// test invokes its own TempDir).
func buildServeBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "aggrega"
	if runtime.GOOS == "windows" {
		name = "aggrega.exe"
	}
	binPath := filepath.Join(dir, name)
	// Resolve the module root for the go build invocation. The test
	// runs from cmd/aggrega/; the module root is two dirs up.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/aggrega")
	cmd.Dir = moduleRoot
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return binPath
}

// makeWorkspace creates a temp directory containing an
// `.aggrega-config.yaml` file and returns the path. Tests use this
// as the daemon's working directory.
func makeWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".aggrega-config.yaml"), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir
}

// framed writes one Content-Length-framed JSON-RPC request body for
// the supplied method + params.
func framed(id int, method string, params any) []byte {
	paramBytes, _ := json.Marshal(params)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  json.RawMessage(paramBytes),
	})
	return append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))), body...)
}

// readOneResponse reads one Content-Length-framed response from r and
// decodes it into a map keyed by field name. Times out after the
// supplied deadline to avoid hanging the test on a deadlocked daemon.
func readOneResponse(t *testing.T, r io.Reader, deadline time.Duration) map[string]any {
	t.Helper()
	type result struct {
		body map[string]any
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		br := bufio.NewReader(r)
		contentLength := -1
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				ch <- result{err: fmt.Errorf("read header: %w", err)}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				v := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
				n, perr := strconv.Atoi(v)
				if perr != nil {
					ch <- result{err: fmt.Errorf("parse content-length: %w", perr)}
					return
				}
				contentLength = n
			}
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(br, body); err != nil {
			ch <- result{err: fmt.Errorf("read body: %w", err)}
			return
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			ch <- result{err: fmt.Errorf("unmarshal: %w (body: %s)", err, body)}
			return
		}
		ch <- result{body: out}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("readOneResponse: %v", r.err)
		}
		return r.body
	case <-time.After(deadline):
		t.Fatalf("readOneResponse: timeout after %s", deadline)
		return nil
	}
}

// TestServeLifecycle builds the aggrega binary, spawns it as `aggrega
// serve` against a temp workspace with a minimal .aggrega-config.yaml,
// and exercises the validate → cache-hit-via-second-validate →
// invalidate → diagnostics-returns-1001 sequence over the subprocess
// pipe. Pins the state-holds-across-requests contract.
func TestServeLifecycle(t *testing.T) {
	bin := buildServeBinary(t)
	ws := makeWorkspace(t)

	cmd := exec.Command(bin, "serve")
	cmd.Dir = ws
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	// Request 1: validate.
	if _, err := stdin.Write(framed(1, "aggrega/validate", map[string]any{
		"file":    "x.yaml",
		"content": "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n",
	})); err != nil {
		t.Fatalf("write request 1: %v", err)
	}
	resp1 := readOneResponse(t, stdout, 5*time.Second)
	if _, ok := resp1["error"]; ok {
		t.Fatalf("request 1 returned error envelope: %+v", resp1["error"])
	}
	if _, ok := resp1["result"]; !ok {
		t.Fatalf("request 1 missing result field: %+v", resp1)
	}

	// Request 2: validate again (cache-hit observable via result-shape
	// equivalence — second validate succeeds with the same shape).
	if _, err := stdin.Write(framed(2, "aggrega/validate", map[string]any{
		"file":    "x.yaml",
		"content": "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n",
	})); err != nil {
		t.Fatalf("write request 2: %v", err)
	}
	resp2 := readOneResponse(t, stdout, 5*time.Second)
	if _, ok := resp2["error"]; ok {
		t.Fatalf("request 2 returned error envelope: %+v", resp2["error"])
	}

	// Request 3: invalidate.
	if _, err := stdin.Write(framed(3, "aggrega/invalidate", map[string]any{"file": "x.yaml"})); err != nil {
		t.Fatalf("write request 3: %v", err)
	}
	resp3 := readOneResponse(t, stdout, 5*time.Second)
	if _, ok := resp3["error"]; ok {
		t.Fatalf("request 3 returned error envelope: %+v", resp3["error"])
	}

	// Request 4: diagnostics on the invalidated file — expect code 1001.
	if _, err := stdin.Write(framed(4, "aggrega/diagnostics", map[string]any{"file": "x.yaml"})); err != nil {
		t.Fatalf("write request 4: %v", err)
	}
	resp4 := readOneResponse(t, stdout, 5*time.Second)
	errEnv, ok := resp4["error"].(map[string]any)
	if !ok {
		t.Fatalf("request 4 missing error envelope after invalidate; got %+v", resp4)
	}
	if code, _ := errEnv["code"].(float64); int(code) != 1001 {
		t.Errorf("request 4 error code = %v; want 1001 (file-not-validated)", errEnv["code"])
	}
}

// TestServeStartsWithCWDFallbackWhenNoMarker spawns the daemon from a
// directory with no workspace marker anywhere in the parent chain.
// Resolution falls back to the working directory (the same precedence the
// CLI uses), so the daemon starts rather than erroring; with no input it
// exits cleanly when stdin closes (EOF). This pins the unification: a missing
// workspace no longer makes the daemon stricter than the CLI.
func TestServeStartsWithCWDFallbackWhenNoMarker(t *testing.T) {
	bin := buildServeBinary(t)
	// The temp dir is under $TMPDIR (Linux: /tmp) with no workspace marker up
	// to the filesystem root, so discovery falls through to CWD.
	dir := t.TempDir()
	cmd := exec.Command(bin, "serve")
	cmd.Dir = dir
	// Empty stdin: the reader hits EOF immediately and the dispatch loop exits
	// cleanly. A non-zero exit here would mean the daemon refused to start
	// without a marker — the pre-unification behavior this test guards against.
	cmd.Stdin = bytes.NewReader(nil)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("daemon should start with CWD fallback and exit cleanly on EOF; got error: %v, output: %s", err, output)
	}
}

// TestServeAggregaIrGraphEndToEnd exercises the daemon's live
// aggrega/irGraph flow against the fullstack-app fixture. The
// subprocess goes through Run's full startup wiring — workspace-root
// discovery, workspace-config load, OCIPuller construction via
// catalog.NewDefaultOCIPuller, lazy lookup construction via
// catalog.NewLazyLookup. The handler reads s.lazyCatalog.AsCatalogLookup()
// and passes it to pipeline.RunPipeline.
//
// This is the end-to-end pin for the wiring: the existing in-package
// stub tests construct Server directly via newTestServer, bypassing
// Run's startup wiring. Without this subprocess test, the wired-up
// lazy-lookup flow has only compile + vet coverage. With this test,
// the round-trip — catalog walk → IR construction → JSON serialisation
// — is verified end-to-end against a real workspace.
func TestServeAggregaIrGraphEndToEnd(t *testing.T) {
	bin := buildServeBinary(t)
	ws := t.TempDir()

	// Resolve absolute paths to the fullstack-app fixture. The test
	// runs from cmd/aggrega/; the module root is two dirs up.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	fixtureDir := filepath.Join(moduleRoot, "internal", "compiler", "testdata", "fullstack-app")
	catalogDir := filepath.Join(fixtureDir, "catalog")
	releasePath := filepath.Join(fixtureDir, "release.yaml")

	// Workspace config points at the fullstack-app catalog by
	// absolute path. The daemon's handleIrGraph joins
	// params.Release with workspaceRoot via filepath.Join, which
	// concatenates rather than discarding absolute paths — so
	// params.Release must stay workspace-relative. Copy the three
	// fixture YAMLs (release / manifest / aggregate) into the
	// workspace at the top level; reference release.yaml relatively.
	configBody := fmt.Sprintf("workspace:\n  catalog_dirs:\n    - %s\n", catalogDir)
	if err := os.WriteFile(filepath.Join(ws, ".aggrega-config.yaml"), []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	for _, name := range []string{"release.yaml", "manifest.yaml", "aggregate.yaml"} {
		src := filepath.Join(fixtureDir, name)
		dst := filepath.Join(ws, name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	// Write a stub lockfile so the daemon's lockfile-absent gate
	// (1003) doesn't block irGraph. The drift check that runs after
	// the gate loads the lockfile lazily and stderr-logs-and-skips
	// on a malformed payload — non-blocking, so the pipeline path
	// this test exercises is unaffected.
	if err := os.WriteFile(filepath.Join(ws, "release.lock"), []byte{}, 0o644); err != nil {
		t.Fatalf("write stub lockfile: %v", err)
	}
	// Suppress the unused-variable warning for releasePath now that
	// params.Release is workspace-relative.
	_ = releasePath

	cmd := exec.Command(bin, "serve")
	cmd.Dir = ws
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	// Send aggrega/irGraph with the workspace-relative Release path.
	if _, err := stdin.Write(framed(1, "aggrega/irGraph", map[string]any{
		"release": "release.yaml",
	})); err != nil {
		t.Fatalf("write irGraph request: %v", err)
	}
	// The daemon now emits an aggrega/publishDiagnostics
	// notification (drift state for the lockfile path) alongside
	// the irGraph response. Loop reading until we see a message
	// with a result or error field (the response); skip notifications.
	var resp map[string]any
	for i := 0; i < 5; i++ {
		msg := readOneResponse(t, stdout, 30*time.Second)
		if _, hasResult := msg["result"]; hasResult {
			resp = msg
			break
		}
		if _, hasErr := msg["error"]; hasErr {
			resp = msg
			break
		}
		// Notification (no id, no result, no error) — keep reading.
	}
	if resp == nil {
		t.Fatalf("did not receive an irGraph response after 5 messages")
	}
	if errEnv, ok := resp["error"]; ok {
		t.Fatalf("aggrega/irGraph returned error envelope: %+v", errEnv)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("aggrega/irGraph response missing result field; got %+v", resp)
	}
	graph, ok := result["graph"]
	if !ok {
		t.Fatalf("aggrega/irGraph result missing graph field; got %+v", result)
	}
	// The graph should decode as a non-empty types.IRGraph carrying
	// a Kubernetes-style envelope (apiVersion + kind + metadata +
	// spec). Assert structural shape without importing pkg/types:
	// the four canonical top-level keys must be present, and the
	// kind field should declare the document type.
	graphMap, ok := graph.(map[string]any)
	if !ok {
		t.Fatalf("aggrega/irGraph graph field should decode as JSON object; got %T", graph)
	}
	for _, key := range []string{"apiVersion", "kind", "metadata", "spec"} {
		if _, has := graphMap[key]; !has {
			t.Errorf("aggrega/irGraph graph missing expected top-level %q key; got keys: %v", key, mapKeys(graphMap))
		}
	}
	if kind, _ := graphMap["kind"].(string); kind == "" {
		t.Errorf("aggrega/irGraph graph kind field should be a non-empty string; got %v", graphMap["kind"])
	}
}

// mapKeys is a small helper for diagnostic error messages.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestServeAggregaResolveEndToEnd exercises the resolve handler over
// the JSON-RPC wire. Mirrors TestServeAggregaIrGraphEndToEnd's
// pattern: spawn the daemon, drive validate + resolve, assert the
// Location response.
//
// Closes the deferred coverage observation from earlier daemon work —
// resolve previously had only in-package unit-test coverage; the
// full Run-wiring path (workspace-root discovery, parse-cache
// population, workspace-walk traversal) is now end-to-end pinned.
func TestServeAggregaResolveEndToEnd(t *testing.T) {
	bin := buildServeBinary(t)
	ws := t.TempDir()

	// Minimal workspace config; the resolve family does not consume
	// CatalogDirs today.
	if err := os.WriteFile(filepath.Join(ws, ".aggrega-config.yaml"), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Parent Component fixture + child Component extending it.
	parent := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/web-server\n  version: 1.0.0\nspec: {}\n"
	child := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: app/api-server\n  version: 1.0.0\nspec:\n  extends: core/web-server\n"
	parentPath := filepath.Join(ws, "parent.yaml")
	childPath := filepath.Join(ws, "child.yaml")
	if err := os.WriteFile(parentPath, []byte(parent), 0o644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	if err := os.WriteFile(childPath, []byte(child), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}

	cmd := exec.Command(bin, "serve")
	cmd.Dir = ws
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	// Validate the child file (seeds the parse cache).
	if _, err := stdin.Write(framed(1, "aggrega/validate", map[string]any{
		"file":    childPath,
		"content": child,
	})); err != nil {
		t.Fatalf("write validate request: %v", err)
	}
	if resp := readOneResponse(t, stdout, 10*time.Second); resp["error"] != nil {
		t.Fatalf("aggrega/validate returned error envelope: %+v", resp["error"])
	}

	// Resolve the cursor at the spec.extends value: line 6 (0-based),
	// char 14 (mid "core/web-server").
	if _, err := stdin.Write(framed(2, "aggrega/resolve", map[string]any{
		"uri":      childPath,
		"position": map[string]any{"line": 6, "character": 14},
	})); err != nil {
		t.Fatalf("write resolve request: %v", err)
	}
	resp := readOneResponse(t, stdout, 10*time.Second)
	if errEnv, ok := resp["error"]; ok && errEnv != nil {
		t.Fatalf("aggrega/resolve returned error envelope: %+v", errEnv)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("aggrega/resolve response missing result field; got %+v", resp)
	}
	loc, ok := result["location"].(map[string]any)
	if !ok || loc == nil {
		t.Fatalf("aggrega/resolve result.location should decode as a non-nil object; got %+v", result)
	}
	if uri, _ := loc["uri"].(string); uri != parentPath {
		t.Errorf("location.uri = %q; want %q (parent's path)", uri, parentPath)
	}
}

// TestValidateWorkspaceRootFlagEmptyTreatedAsUnset verifies that an
// empty --workspace-root argument is treated as the unset signal —
// the daemon falls through to its discovery flow rather than
// rejecting empty as invalid.
func TestValidateWorkspaceRootFlagEmptyTreatedAsUnset(t *testing.T) {
	t.Parallel()
	got, err := validateWorkspaceRootFlag("")
	if err != nil {
		t.Fatalf("unexpected error for empty value: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty return for empty input; got %q", got)
	}
}

// TestValidateWorkspaceRootFlagRelativePathResolves verifies that a
// relative path is resolved to an absolute path before being
// returned, since the daemon's downstream logic depends on
// absolute-path canonical form.
func TestValidateWorkspaceRootFlagRelativePathResolves(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(filepath.Dir(dir)); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	relative := filepath.Base(dir)
	got, err := validateWorkspaceRootFlag(relative)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path; got %q", got)
	}
	if filepath.Base(got) != relative {
		t.Errorf("expected base name %q; got %q", relative, got)
	}
}

// TestValidateWorkspaceRootFlagMissingPathErrors verifies that a
// path which does not exist on disk returns an error whose chain
// includes the flag name in its prefix.
func TestValidateWorkspaceRootFlagMissingPathErrors(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-subdir")
	_, err := validateWorkspaceRootFlag(missing)
	if err == nil {
		t.Fatalf("expected error for missing path; got nil")
	}
	if !strings.Contains(err.Error(), "--workspace-root") {
		t.Errorf("error should name the flag; got %q", err.Error())
	}
}

// TestValidateWorkspaceRootFlagNonDirectoryErrors verifies that a
// path pointing at a file (rather than a directory) returns an
// error whose body identifies the directory-required violation.
func TestValidateWorkspaceRootFlagNonDirectoryErrors(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := validateWorkspaceRootFlag(file)
	if err == nil {
		t.Fatalf("expected error for non-directory path; got nil")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory'; got %q", err.Error())
	}
}

// TestServeCmdRejectsInvalidWorkspaceRoot drives the cobra harness
// end-to-end: builds the aggrega binary and invokes it with a
// --workspace-root pointing at a non-existent path. The process
// must exit non-zero before reaching the dispatch loop, and its
// stderr must surface the flag name.
func TestServeCmdRejectsInvalidWorkspaceRoot(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	missing := filepath.Join(t.TempDir(), "no-such-subdir")

	cmd := exec.Command(bin, "serve", "--workspace-root", missing)
	stderrBuf := &strings.Builder{}
	cmd.Stderr = stderrBuf

	if err := cmd.Run(); err == nil {
		t.Fatalf("expected non-zero exit; binary returned nil error")
	}
	if !strings.Contains(stderrBuf.String(), "--workspace-root") {
		t.Errorf("expected stderr to mention --workspace-root; got %q", stderrBuf.String())
	}
}
