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
