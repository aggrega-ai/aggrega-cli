// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cliplugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// buildGuest builds a fixture guest under testdata/<name> to a .wasm artifact in
// a temp directory and returns its bytes. Each guest is a standalone module (its
// own go.mod), so the build runs with GOWORK=off, mirroring the PDK-example
// build scripts. The Go toolchain is the test toolchain itself and the wasip1
// target ships with it, so a build failure is a genuine failure, never a
// toolchain skip.
func buildGuest(t *testing.T, name string) []byte {
	t.Helper()
	srcDir := filepath.Join("testdata", name)
	if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
		t.Fatalf("fixture source missing at %s: %v", srcDir, err)
	}
	out := filepath.Join(t.TempDir(), name+".wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "./")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s guest: %v\noutput:\n%s", name, err, output)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read built %s guest: %v", name, err)
	}
	return b
}

// bytesLookup returns a resolver seam that hands the runner the supplied guest
// bytes for any URI, with an empty ResolvedPlugin.URI so the bridge's
// best-effort provenance recording is skipped (no filesystem side effect).
func bytesLookup(b []byte) pluginpath.Lookup {
	return func(string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{Bytes: b}, nil
	}
}

// recordingWriter records each Write call's payload as a separate element, so a
// test can distinguish per-call streaming (many writes) from a single
// buffered-then-dumped write.
type recordingWriter struct {
	writes [][]byte
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	w.writes = append(w.writes, cp)
	return len(p), nil
}

func (w *recordingWriter) String() string {
	var buf bytes.Buffer
	for _, wr := range w.writes {
		buf.Write(wr)
	}
	return buf.String()
}

// TestRunner_ExitCodeZero drives the cli-args-exit guest through the runner with
// a zero exit-code argument: the guest streams its two chunks to the injected
// stdout writer and returns exit code 0, which the runner returns with no error.
func TestRunner_ExitCodeZero(t *testing.T) {
	ctx := context.Background()
	guest := buildGuest(t, "cli-args-exit")

	var stdout recordingWriter
	var stderr bytes.Buffer
	r := Runner{
		Resolve: bytesLookup(guest),
		Digest:  func(b []byte) string { return fmt.Sprintf("len-%d", len(b)) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code, digest, err := r.Run(ctx, "test://cli-args-exit", []string{"0", "hello"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d; want 0", code)
	}
	// The Digest hook is applied to the resolved bytes and returned, so the caller
	// can pin the running bytes without re-resolving.
	if want := fmt.Sprintf("len-%d", len(guest)); digest != want {
		t.Fatalf("returned digest = %q; want %q", digest, want)
	}
	if got, want := stdout.String(), "arg:hello\nsecond\n"; got != want {
		t.Fatalf("streamed stdout = %q; want %q", got, want)
	}
	// Two separate host-function writes prove the host streams each write to the
	// injected writer as it arrives rather than buffering the whole run.
	if n := len(stdout.writes); n != 2 {
		t.Fatalf("stdout received %d write(s); want 2 (incremental streaming, not buffered-then-dumped): %q",
			n, stdout.writes)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q; want empty", stderr.String())
	}
}

// TestRunner_ExitCodeNonZero drives the same guest with a non-zero exit-code
// argument: the guest returns exit code 3, which the runner returns as the
// command's status VALUE (code == 3, err == nil) — the non-zero exit is not
// swallowed into an error.
func TestRunner_ExitCodeNonZero(t *testing.T) {
	ctx := context.Background()
	guest := buildGuest(t, "cli-args-exit")

	var stdout recordingWriter
	var stderr bytes.Buffer
	r := Runner{Resolve: bytesLookup(guest), Stdout: &stdout, Stderr: &stderr}

	code, _, err := r.Run(ctx, "test://cli-args-exit", []string{"3", "boom"})
	if err != nil {
		t.Fatalf("Run returned error for a non-zero command exit (must be a status, not an error): %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d; want 3", code)
	}
	if got, want := stdout.String(), "arg:boom\nsecond\n"; got != want {
		t.Fatalf("streamed stdout = %q; want %q", got, want)
	}
}

// TestRunner_RefusesNonCLIPlugin proves the runner refuses a plugin whose
// describe() reports a non-CLI type: it loads and describes the provider-typed
// noncli-probe guest, then returns an error wrapping ErrPluginNotCLI without
// invoking any command export.
func TestRunner_RefusesNonCLIPlugin(t *testing.T) {
	ctx := context.Background()
	guest := buildGuest(t, "noncli-probe")

	var stdout, stderr bytes.Buffer
	r := Runner{Resolve: bytesLookup(guest), Stdout: &stdout, Stderr: &stderr}

	code, _, err := r.Run(ctx, "test://noncli-probe", nil)
	if !errors.Is(err, ErrPluginNotCLI) {
		t.Fatalf("Run error = %v; want an error wrapping ErrPluginNotCLI", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d; want 0 on a host-side refusal", code)
	}
}

// TestRunner_NoResolver proves a Runner with no resolver seam returns
// ErrNoResolver rather than panicking.
func TestRunner_NoResolver(t *testing.T) {
	code, _, err := Runner{}.Run(context.Background(), "test://whatever", nil)
	if !errors.Is(err, ErrNoResolver) {
		t.Fatalf("Run error = %v; want an error wrapping ErrNoResolver", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d; want 0", code)
	}
}
