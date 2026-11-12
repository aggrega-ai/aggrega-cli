// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
)

// TestIsPathInput_AdditionalBranches expands coverage on the
// path-discriminator beyond what TestIsPathInput already pins.
func TestIsPathInput_AdditionalBranches(t *testing.T) {
	t.Parallel()
	// Coverage of forward-slash absolute path.
	if !isPathInput("/absolute/path") {
		t.Errorf("/absolute → should be path")
	}
	if !isPathInput("relative.yaml") {
		t.Errorf(".yaml suffix → should be path")
	}
	if !isPathInput("relative.yml") {
		t.Errorf(".yml suffix → should be path")
	}
	if isPathInput("plain-name") {
		t.Errorf("plain name → should NOT be path")
	}
}

// TestInspectPath_NonExistentFile pins the read-error path.
func TestInspectPath_NonExistentFile(t *testing.T) {
	t.Parallel()
	err := inspectPath(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), "/nonexistent/file.yaml", nil, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("missing file should error")
	}
}

// TestInspectPath_MalformedYAMLPreamble pins the parse-error path.
func TestInspectPath_MalformedYAMLPreamble(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(path, []byte("::: not yaml :::"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := inspectPath(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("malformed YAML should error")
	}
}

// TestInspectCatalogComponent_MissingCatalog pins the catalog-load
// error path.
func TestInspectCatalogComponent_MissingCatalog(t *testing.T) {
	t.Parallel()
	err := inspectCatalogEntity(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), "any-name", []string{"/nonexistent/dir"}, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("missing catalog should error")
	}
}

// TestInspectCatalogComponent_ComponentNotFound pins the not-found
// dispatch arm.
func TestInspectCatalogComponent_ComponentNotFound(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	// Empty catalog dir.
	err := inspectCatalogEntity(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), "absent-component", []string{tmp}, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("missing component should error")
	}
	if !strings.Contains(err.Error(), "no catalog entity of any kind") {
		t.Errorf("error should state no entity matched across kinds; got %v", err)
	}
}

// TestInspectCatalogComponent_HappyPath pins the success path: a
// catalog with one Component → render its schema.
func TestInspectCatalogComponent_HappyPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	compYAML := `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
  usesSchema:
    db:
      selector: 'component.aggrega.ai/type=stateful-server'
`
	if err := os.WriteFile(filepath.Join(tmp, "component.yaml"), []byte(compYAML), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := inspectCatalogEntity(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), "core/server", []string{tmp}, testInspectOptions(io.Discard)); err != nil {
		t.Errorf("happy: %v", err)
	}
}

// TestInspectComponentFile_MissingNameUsesPath pins the path-fallback
// for unnamed Components.
func TestInspectComponentFile_MissingNameUsesPath(t *testing.T) {
	t.Parallel()
	src := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Component\nspec:\n  schema:\n    values:\n      x: {type: string}\n")
	if err := renderComponentFile(context.Background(), nil, "/some/file.yaml", src, nil, testInspectOptions(io.Discard)); err != nil {
		t.Errorf("happy: %v", err)
	}
}

// TestInspectComponentFile_NoSchemaDeclared pins the no-schema branch.
func TestInspectComponentFile_NoSchemaDeclared(t *testing.T) {
	t.Parallel()
	src := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: x\nspec: {}\n")
	if err := renderComponentFile(context.Background(), nil, "/some/file.yaml", src, nil, testInspectOptions(io.Discard)); err != nil {
		t.Errorf("no-schema: %v", err)
	}
}

// TestInspectComponentFile_MalformedYAML pins the parse error.
func TestInspectComponentFile_MalformedYAML(t *testing.T) {
	t.Parallel()
	src := []byte("::: not yaml :::")
	if err := renderComponentFile(context.Background(), nil, "/some/file.yaml", src, nil, testInspectOptions(io.Discard)); err == nil {
		t.Errorf("malformed should error")
	}
}

// TestInspectComponentFile_WithExtends pins the extends-header branch.
func TestInspectComponentFile_WithExtends(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: child
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  extends: parent
  schema: inherited
`)
	if err := renderComponentFile(context.Background(), nil, "/some/file.yaml", src, nil, testInspectOptions(io.Discard)); err != nil {
		t.Errorf("extends: %v", err)
	}
}

// TestRunBuild_MissingCatalog pins the catalog-load error path.
func TestRunBuild_MissingCatalog(t *testing.T) {
	t.Parallel()
	err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), "/nonexistent/release.yaml", []string{"/nonexistent/catalog"}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Errorf("missing catalog should error")
	}
}

// TestRunBuild_MissingRelease pins the release-load error path.
func TestRunBuild_MissingRelease(t *testing.T) {
	t.Parallel()
	err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), "/nonexistent/release.yaml", []string{t.TempDir()}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Errorf("missing release should error")
	}
}

// TestRunInspect_OCI pins that a registry reference with a library-entry
// fragment appended — `#/schemas/X`, the suffix that selects one entry inside a
// library — is refused for its shape. The fragment's own slash leaves no tag or
// digest where the reference is read for one, so the refusal names the reference
// as typed and the two forms a registry reference is written in, and it is raised
// before anything is resolved or fetched.
//
// Two things make "before anything is fetched" observable rather than inferred.
// The workspace is offline, so a reading that got as far as the egress gate would
// be refused as offline instead of for its shape. And the reference names a
// loopback listener that counts every connection it accepts, whatever protocol
// arrives on it, so a reading that dialled the host the reference names by any
// route — through the egress gate or around it — is counted.
func TestRunInspect_OCI(t *testing.T) {
	t.Parallel()

	var dials atomic.Int64
	listener := httptest.NewUnstartedServer(http.NotFoundHandler())
	listener.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			dials.Add(1)
		}
	}
	listener.Start()
	t.Cleanup(listener.Close)
	ref := "oci://" + listener.Listener.Addr().String() + "/lib@1#/schemas/X"

	root := t.TempDir()
	writeWorkspaceConfig(t, root, "workspace: {}\noffline: true")
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	err := dispatchInspect(context.Background(), rw, ref, nil, testInspectOptions(io.Discard))
	if err == nil {
		t.Fatalf("dispatchInspect(%q) succeeded; a reference naming no tag or digest must be refused", ref)
	}
	if errors.Is(err, egress.ErrOfflineEgress()) {
		t.Fatalf("err = %v; want the shape refusal, raised ahead of the egress gate", err)
	}
	msg := err.Error()
	for _, want := range []string{
		strconv.Quote(ref),
		"is not a registry reference",
		"oci://<host>/<repository>:<tag>",
		"oci://<host>/<repository>@<digest>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("err = %q; want it to carry %q", msg, want)
		}
	}
	if n := dials.Load(); n != 0 {
		t.Errorf("the listener the reference names accepted %d connection(s); a malformed reference must be refused before anything is dialled", n)
	}
}

// TestRunInspect_CatalogComponentBranch pins the catalog-name dispatch
// path (non-path input + non-OCI input).
func TestRunInspect_CatalogComponentBranch(t *testing.T) {
	t.Parallel()
	err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), "absent-component", []string{t.TempDir()}, testInspectOptions(io.Discard))
	if err == nil {
		t.Errorf("absent component should error")
	}
}

// TestRunInspect_PathBranch pins the path-dispatch branch.
func TestRunInspect_PathBranch(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "x.yaml")
	if err := os.WriteFile(path, []byte("apiVersion: x\nkind: Manifest\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, []string{tmp}, testInspectOptions(io.Discard))
	// Manifest → not-yet-implemented; surfaces as error.
	if err == nil {
		t.Errorf("manifest dispatch should error")
	}
}
