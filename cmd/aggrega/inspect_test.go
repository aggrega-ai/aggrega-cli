// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// TestInspect_OciUri_ReturnsNotImplemented pins the OCI-URI branch's
// not-yet-implemented error message. The branch returns a
// forward-compatible error rather than silently falling through to
// the catalog-name path; operators see a clear signal that OCI
// inspect is deferred.
func TestInspect_OciUri_ReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	err := runInspect(nil, []string{"oci://registry.example.com/components/api:1.0.0"})
	if err == nil {
		t.Fatal("expected not-implemented error for oci:// argument; got nil")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error message should signal not-yet-implemented; got: %v", err)
	}
}

// TestInspect_UnknownKind_ReturnsError pins the kind-dispatch
// forward-compatibility behavior. A Manifest file (or any other
// non-Component/non-Release kind) returns the "not yet implemented for
// kind X" error so future inspector branches can be added without
// breaking the existing dispatch surface.
func TestInspect_UnknownKind_ReturnsError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.yaml")
	writeFixture(t, manifestPath, `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	err := runInspect(nil, []string{manifestPath})
	if err == nil {
		t.Fatal("expected not-implemented-for-kind error for Manifest input; got nil")
	}
	if !strings.Contains(err.Error(), "kind Manifest") {
		t.Errorf("error should name the kind; got: %v", err)
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error should signal not-yet-implemented; got: %v", err)
	}
}

// TestInspect_MissingKind_ReturnsError pins the missing-preamble
// failure mode. A YAML file without an apiVersion/kind preamble can't
// be inspected by kind dispatch; the error message tells the operator
// exactly what's missing.
func TestInspect_MissingKind_ReturnsError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bogusPath := filepath.Join(root, "bogus.yaml")
	writeFixture(t, bogusPath, `some_field: value
another_field: 42
`)
	err := runInspect(nil, []string{bogusPath})
	if err == nil {
		t.Fatal("expected error on missing-kind file; got nil")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("error should mention the missing kind; got: %v", err)
	}
}

// TestInspect_ComponentFilePath_RendersSchema pins the Component-file
// branch. A Component authored as a standalone YAML file inspects with
// the same output shape as the catalog-name path's schema rendering:
// a header line naming the Component and its source path, followed by
// the rendered schema body.
func TestInspect_ComponentFilePath_RendersSchema(t *testing.T) {
	root := t.TempDir()
	compPath := filepath.Join(root, "component.yaml")
	writeFixture(t, compPath, `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
spec:
  schema: |
    values: { port?: int | *8080 }
`)

	out, err := captureStdout(func() error {
		return runInspect(nil, []string{compPath})
	})
	if err != nil {
		t.Fatalf("Component file path should inspect cleanly; got: %v", err)
	}
	if !strings.Contains(out, "// Component: core/server") {
		t.Errorf("output missing Component header naming core/server; got:\n%s", out)
	}
	if !strings.Contains(out, compPath) {
		t.Errorf("output missing source path suffix on the header; got:\n%s", out)
	}
	if !strings.Contains(out, "port?: int | *8080") {
		t.Errorf("output missing rendered schema body; got:\n%s", out)
	}
}

// captureStdout redirects os.Stdout for the duration of fn, returning
// what fn wrote. Used by tests that need to assert on user-facing
// output of commands that print directly to stdout.
//
// Pipe teardown runs in a defer so a panic in fn still closes the
// writer, unblocks the reader goroutine, and drains the captured
// buffer — the goroutine would otherwise leak past the test and (if
// the panic doesn't tear down the process) hold the pipe's file
// descriptor. Named returns let the defer populate the output after
// the drain completes, so callers see what fn actually wrote.
func captureStdout(fn func() error) (out string, fnErr error) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("pipe: %w", err)
	}
	os.Stdout = w

	done := make(chan struct{})
	var captured strings.Builder
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				captured.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		close(done)
	}()

	defer func() {
		os.Stdout = originalStdout
		w.Close()
		<-done
		r.Close()
		out = captured.String()
	}()

	fnErr = fn()
	return
}

// TestIsPathInput pins the input-form detector that drives kind
// dispatch. Path-shaped inputs route to the file loader; non-path
// inputs route to the catalog-name path.
func TestIsPathInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"./release.yaml", true},
		{"../release.yaml", true},
		{"/abs/path/release.yaml", true},
		{"release.yaml", true},
		{"release.yml", true},
		{"core/server", false},
		{"core/server/api-server", false},
		{"some-name", false},
	}
	for _, c := range cases {
		got := isPathInput(c.in)
		if got != c.want {
			t.Errorf("isPathInput(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestProvenanceBracket pins the trailing-bracket format for both
// explicit and annotation-driven Feature attachments. The bracket
// format is part of the inspect command's user-facing contract on
// Release inspection; regression here would silently break
// operator-readable output.
func TestProvenanceBracket(t *testing.T) {
	t.Parallel()
	// Explicit attachment: the bracket reads exactly [explicit].
	got := provenanceBracketShim("explicit", "", "")
	if got != "[explicit]" {
		t.Errorf("explicit bracket = %q, want [explicit]", got)
	}

	// Annotation attachment: the bracket carries the annotation key
	// and source location.
	got = provenanceBracketShim("annotation", "feature.myco/rewrite-host", "release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host")
	want := "[annotation: feature.myco/rewrite-host from release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host]"
	if got != want {
		t.Errorf("annotation bracket = %q, want %q", got, want)
	}
}

// provenanceBracketShim constructs an IRFeatureEntry with the given
// provenance fields and renders its bracket. Wraps the package-private
// provenanceBracket so the test can name field values without an
// otherwise-noisy literal construction.
func provenanceBracketShim(origin, key, source string) string {
	return provenanceBracket(types.IRFeatureEntry{
		Origin:           origin,
		AnnotationKey:    key,
		AnnotationSource: source,
	})
}
