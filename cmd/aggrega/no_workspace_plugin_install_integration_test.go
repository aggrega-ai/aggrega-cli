// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Build-tagged `integration` because it drives the in-process OCI registry, whose
// test-only dependency must not enter the default test graph.
package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci/ocitest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestIntegration_ARegistryPluginInstallIsRefusedInAFolderNoWorkspaceResolvesFrom
// runs the production fetch a registry plugin install performs, against the
// in-process registry with a CLI plugin published on it, once in a folder nobody
// marked with nothing naming a root and once in the same folder marked as a
// workspace. The registry counts the requests it answers once the plugin is
// published.
//
// Marked, the fetch pulls the plugin and caches its bytes under the folder.
// Unmarked, it is refused before anything is fetched, naming the folder: the
// registry is asked for nothing, no .aggrega appears, and a resolution started
// beneath the folder still anchors nothing.
//
// 🔴 PLANTED DEFECT: delete the CacheWriteRefusal block in
// productionInstallPluginFetch, and the unmarked row fails — the fetch pulls the
// plugin and creates the folder's .aggrega directory.
func TestIntegration_ARegistryPluginInstallIsRefusedInAFolderNoWorkspaceResolvesFrom(t *testing.T) {
	var counting atomic.Bool
	var requests atomic.Int64
	host := startRegistry(t, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if counting.Load() {
				requests.Add(1)
			}
			h.ServeHTTP(w, r)
		})
	})
	repo, err := oci.NewRemoteRepository(host+"/plugin/cli/greet", &http.Client{})
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	// The fetch reads the published bytes and never describes them, so they need not
	// be a module: what is asserted is whether they were fetched and where they land.
	published := []byte("the published plugin's bytes")
	if _, err := oci.PushPluginManifest(context.Background(), repo, "1.0.0", []byte(cliPluginEntityYAML), published, nil); err != nil {
		t.Fatalf("publish plugin manifest: %v", err)
	}
	ref := "oci://" + host + "/plugin/cli/greet:1.0.0"
	counting.Store(true)

	for _, tc := range []struct {
		name   string
		marked bool
	}{
		{name: "a folder nobody marked and nothing named"},
		{name: "the same folder marked as a workspace", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := requests.Load()
			folder := folderNoWorkspaceResolvesFrom(t)
			config := "default_registry: \"" + host + "\"\noffline: false\n"
			if tc.marked {
				config = "workspace: {}\n" + config
			}
			writeWorkspaceConfig(t, folder, config)
			rw := resolveRow(t, folder, tc.marked)

			src, err := productionInstallPluginFetch(context.Background(), rw, ref)

			if tc.marked {
				if err != nil {
					t.Fatalf("the install's fetch in a workspace failed, so the refused row proves nothing: %v", err)
				}
				if !bytes.Equal(src.Bytes, published) {
					t.Fatalf("the install fetched %q, want the published bytes", src.Bytes)
				}
				slot := ocitest.MustCachePath(folder, rw.Config().ResolvedArtifactCacheDir(), oci.PluginArtifactKind(), ref, digest.ContentDigest(published))
				if _, serr := os.Stat(filepath.Join(slot, "blob")); serr != nil {
					t.Fatalf("the install's fetch in a workspace cached nothing at %s (%v), so the refused row's missing cache proves nothing", slot, serr)
				}
				return
			}
			var refused *workspaceconfig.CacheWriteRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("the install's fetch returned %v, want the refusal of a cache written in a folder that is no workspace", err)
			}
			if refused.Root() != folder {
				t.Errorf("the refusal names %s, want %s", refused.Root(), folder)
			}
			if n := requests.Load() - before; n != 0 {
				t.Errorf("the refused install asked the registry %d time(s)", n)
			}
			assertNoCacheAndStillNoWorkspace(t, folder)
		})
	}
}
