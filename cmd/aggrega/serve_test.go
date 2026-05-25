// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
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

// TestServeExitsWhenNoWorkspaceConfig spawns the daemon from a
// directory with no .aggrega-config.yaml anywhere in the parent
// chain. Asserts the daemon exits with non-zero exit code before
// reading any JSON-RPC interaction.
func TestServeExitsWhenNoWorkspaceConfig(t *testing.T) {
	bin := buildServeBinary(t)
	// Use the test's TempDir as the daemon's CWD; the temp dir is
	// under $TMPDIR which has no .aggrega-config.yaml at its root
	// (Linux: /tmp). The daemon's parent walk reaches the filesystem
	// root and exits.
	dir := t.TempDir()
	cmd := exec.Command(bin, "serve")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("daemon should have exited non-zero; got success with output: %s", output)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 0 {
			t.Errorf("expected non-zero exit code; got 0")
		}
	}
	if !strings.Contains(string(output), "workspace") {
		t.Errorf("expected output mentioning workspace; got: %s", output)
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
	resp := readOneResponse(t, stdout, 30*time.Second)
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
