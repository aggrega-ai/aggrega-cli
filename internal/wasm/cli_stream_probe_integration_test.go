// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildCLIStreamProbe builds the cli-stream-probe fixture guest to a .wasm
// artifact in a temp directory and returns its path. The guest is a standalone
// module (its own go.mod), so the build runs with GOWORK=off, mirroring the
// PDK-example build scripts. The Go toolchain is the test toolchain itself and
// the wasip1 target ships with it, so a build failure is a genuine failure
// (t.Fatalf), never a toolchain skip.
func buildCLIStreamProbe(t *testing.T) string {
	t.Helper()
	srcDir := filepath.Join("testdata", "cli-stream-probe")
	if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
		t.Fatalf("cli-stream-probe fixture source missing at %s: %v", srcDir, err)
	}
	out := filepath.Join(t.TempDir(), "cli-stream-probe.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "./")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cli-stream-probe guest: %v\noutput:\n%s", err, output)
	}
	return out
}

// TestCLIStreamProbe_CompileTimeLoadRejectsStreaming is the load-bearing
// disjointness proof: a guest importing the streaming host function
// aggrega_cli_write_stdout, loaded through the compile-time build profile
// (Loader.Load), is rejected with an error wrapping ErrWasmManifestRejected.
// The streaming name is absent from the shared allowlist the build profile
// validates against, so a build can never bind streaming — the AOT-purity
// guarantee for the terminal-only streaming grant.
func TestCLIStreamProbe_CompileTimeLoadRejectsStreaming(t *testing.T) {
	ctx := context.Background()
	path := buildCLIStreamProbe(t)

	loader := &Loader{}
	_, err := loader.Load(ctx, PluginSpec{Name: "cli-stream-probe", Path: path})
	if err == nil {
		t.Fatal("Load accepted a guest importing aggrega_cli_write_stdout; the compile-time profile must reject it")
	}
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("Load error = %v; want an error wrapping ErrWasmManifestRejected", err)
	}
}

// TestCLIStreamProbe_RuntimeLoadStreamsStdout proves the runtime CLI profile
// accepts the same guest and streams its output: loaded through Loader.LoadCLI
// with an injected stdout buffer, the guest's cli_run export writes its fixed
// payload to that buffer and returns exit code 0 (a nil Call error). The stderr
// stream stays untouched.
func TestCLIStreamProbe_RuntimeLoadStreamsStdout(t *testing.T) {
	ctx := context.Background()
	path := buildCLIStreamProbe(t)

	var stdout, stderr bytes.Buffer
	loader := &Loader{}
	plugin, err := loader.LoadCLI(ctx, PluginSpec{Name: "cli-stream-probe", Path: path}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("LoadCLI accepted-path load failed: %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })

	// A nil Call error is exit code 0; a non-zero guest exit would wrap
	// ErrWasmPluginExited.
	if _, err := plugin.Call(ctx, "cli_run", nil); err != nil {
		t.Fatalf("cli_run call: %v", err)
	}

	if got := stdout.String(); got != "streamed-cli-output\n" {
		t.Fatalf("streamed stdout = %q; want %q", got, "streamed-cli-output\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q; want empty (guest streamed only to stdout)", stderr.String())
	}
}
