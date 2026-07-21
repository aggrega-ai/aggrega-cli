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
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveCachedPluginWorkspace builds a hermetic workspace whose config records an
// installed CLI plugin (a plugins.cli {uri, digest} entry) and stages that
// artifact's bytes into the workspace content cache exactly as `plugin install`
// leaves them — the cache slot keyed by the content digest the pin records. It
// returns the resolved workspace. The root is a fresh temp directory with a
// `workspace:` marker so discovery never climbs to an ancestor.
func resolveCachedPluginWorkspace(t *testing.T, uri string, bytes []byte) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	dig := digest.ContentDigest(bytes)
	cfg := "workspace: {}\nplugins:\n  cli:\n    - uri: " + uri + "\n      digest: " + dig + "\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := oci.WriteCached(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), uri, dig, bytes); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	return rw
}

// TestInstalledPluginRunner_ResolvesCachedURI locks the install-to-run
// resolution symmetry: a runtime runner built from a workspace whose content
// cache holds the installed artifact resolves a bare CLI-plugin URI, whereas the
// bare cascade (a runner with no workspace, the pre-fix empty resolver) fails at
// resolution with "not resolvable through any tier". This is the exact asymmetry
// that let `plugin install moo` succeed while `aggrega moo` failed.
func TestInstalledPluginRunner_ResolvesCachedURI(t *testing.T) {
	const uri = "moo"
	// Arbitrary non-wasm bytes: resolution reads and returns them (the cache tier
	// verifies against the pin, which matches the content digest we record), so
	// this exercises resolution without a compiled guest. The runner's later load
	// of these bytes fails, but with a load error — never the resolution error the
	// empty resolver produces.
	cachedBytes := []byte("installed-artifact-bytes")
	rw := resolveCachedPluginWorkspace(t, uri, cachedBytes)

	// Resolution level: the workspace resolver returns the cached bytes; the
	// bare cascade (nil workspace) cannot resolve the URI at all.
	resolved, err := workspacePluginResolver(rw)(uri)
	if err != nil {
		t.Fatalf("workspace resolver should resolve the cached URI; got %v", err)
	}
	if string(resolved.Bytes) != string(cachedBytes) {
		t.Fatalf("resolved bytes = %q; want the cached artifact bytes", resolved.Bytes)
	}
	if _, err := workspacePluginResolver(nil)(uri); err == nil || !strings.Contains(err.Error(), "not resolvable") {
		t.Fatalf("bare cascade should fail to resolve %q; got %v", uri, err)
	}

	// Run level: the runtime dispatcher built from the workspace gets past
	// resolution (failing only at the load of the non-guest bytes), while the
	// dispatcher built from no workspace fails at resolution exactly as the
	// pre-fix empty resolver did.
	ctx := context.Background()

	_, cachedDigest, runErr := installedPluginRunnerFor(rw).Run(ctx, uri, uri, nil)
	if runErr == nil {
		t.Fatalf("running non-guest cached bytes should fail at load, not succeed")
	}
	if errors.Is(runErr, cliplugin.ErrNoResolver) || strings.Contains(runErr.Error(), "not resolvable") {
		t.Fatalf("workspace runner must resolve the cached URI, not fail resolution; got %v", runErr)
	}
	// Resolution succeeds before the load fails, so the workspace runner returns
	// the content digest of the resolved cached bytes even on a load failure.
	if want := digest.ContentDigest(cachedBytes); cachedDigest != want {
		t.Fatalf("resolved-bytes digest = %q; want %q", cachedDigest, want)
	}

	_, _, emptyErr := installedPluginRunnerFor(nil).Run(ctx, uri, uri, nil)
	if emptyErr == nil || !strings.Contains(emptyErr.Error(), "not resolvable") {
		t.Fatalf("no-workspace runner should fail resolution with the pre-fix error; got %v", emptyErr)
	}
}
