// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cliplugin"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveSidecarWorkspace builds a hermetic workspace whose config binds a
// sidecar plugin directory, writes leaf.wasm into that directory with the given
// bytes, and returns the resolved workspace. It anchors the root at a fresh temp
// directory with a `workspace:` marker so discovery never climbs to an ancestor.
func resolveSidecarWorkspace(t *testing.T, leaf string, bytes []byte) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	sidecar := filepath.Join(root, "sidecar")
	if err := os.MkdirAll(sidecar, 0o755); err != nil {
		t.Fatalf("mkdir sidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, leaf+".wasm"), bytes, 0o644); err != nil {
		t.Fatalf("write sidecar artifact: %v", err)
	}
	cfg := "workspace: {}\nplugin_sidecar_dir: sidecar\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return rw
}

// TestInstalledPluginRunner_ResolvesSidecarURI locks the install-to-run
// resolution symmetry: a runtime runner built from a workspace whose sidecar
// directory holds the artifact resolves a bare sidecar URI, whereas the bare
// cascade (a runner with no workspace, the pre-fix empty resolver) fails at
// resolution with "not resolvable through any tier". This is the exact
// asymmetry that let `plugin install moo` succeed while `aggrega moo` failed.
func TestInstalledPluginRunner_ResolvesSidecarURI(t *testing.T) {
	const uri = "moo"
	// Arbitrary non-wasm bytes: resolution reads and returns them (the sidecar
	// tier verifies only against a digest pin, of which there is none here), so
	// this exercises resolution without a compiled guest. The runner's later
	// load of these bytes fails, but with a load error — never the resolution
	// error the empty resolver produces.
	sidecarBytes := []byte("sidecar-artifact-bytes")
	rw := resolveSidecarWorkspace(t, uri, sidecarBytes)

	// Resolution level: the workspace resolver returns the sidecar bytes; the
	// bare cascade (nil workspace) cannot resolve the URI at all.
	resolved, err := workspacePluginResolver(rw)(uri)
	if err != nil {
		t.Fatalf("workspace resolver should resolve the sidecar URI; got %v", err)
	}
	if string(resolved.Bytes) != string(sidecarBytes) {
		t.Fatalf("resolved bytes = %q; want the sidecar artifact bytes", resolved.Bytes)
	}
	if _, err := workspacePluginResolver(nil)(uri); err == nil || !strings.Contains(err.Error(), "not resolvable") {
		t.Fatalf("bare cascade should fail to resolve %q; got %v", uri, err)
	}

	// Run level: the runtime dispatcher built from the workspace gets past
	// resolution (failing only at the load of the non-guest bytes), while the
	// dispatcher built from no workspace fails at resolution exactly as the
	// pre-fix empty resolver did.
	ctx := context.Background()

	_, sidecarDigest, runErr := installedPluginRunnerFor(rw).Run(ctx, uri, uri, nil)
	if runErr == nil {
		t.Fatalf("running non-guest sidecar bytes should fail at load, not succeed")
	}
	if errors.Is(runErr, cliplugin.ErrNoResolver) || strings.Contains(runErr.Error(), "not resolvable") {
		t.Fatalf("workspace runner must resolve the sidecar URI, not fail resolution; got %v", runErr)
	}
	// Resolution succeeds before the load fails, so the workspace runner returns
	// the content digest of the resolved sidecar bytes even on a load failure.
	if want := describeCacheDigest(sidecarBytes); sidecarDigest != want {
		t.Fatalf("resolved-bytes digest = %q; want %q", sidecarDigest, want)
	}

	_, _, emptyErr := installedPluginRunnerFor(nil).Run(ctx, uri, uri, nil)
	if emptyErr == nil || !strings.Contains(emptyErr.Error(), "not resolvable") {
		t.Fatalf("no-workspace runner should fail resolution with the pre-fix error; got %v", emptyErr)
	}
}
