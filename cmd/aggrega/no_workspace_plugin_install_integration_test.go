// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Build-tagged `integration` because it drives the in-process OCI registry, whose
// test-only dependency must not enter the default test graph.
package main

import (
	"bytes"
	"context"
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

// TestIntegration_ARegistryPluginInstallOutsideAWorkspaceIsCachedForTheMachine
// runs the production fetch a registry plugin install performs, against the
// in-process registry with a CLI plugin published on it, once in a folder nobody
// marked with nothing naming a root and once in the same folder marked as a
// workspace. The environment names a machine-wide cache inside the test's scratch
// tree.
//
// Marked, the fetch pulls the plugin and caches its bytes in the workspace's own
// artifact cache. Unmarked, it pulls the plugin and caches its bytes in the
// machine-wide artifact cache instead: no .aggrega appears, and a resolution
// started beneath the folder still anchors nothing.
//
// 🔴 PLANTED DEFECT: cache the fetched bytes under the resolution's own root and
// artifact cache directory whatever it found, and the unmarked row fails — the
// fetch creates the folder's .aggrega directory.
func TestIntegration_ARegistryPluginInstallOutsideAWorkspaceIsCachedForTheMachine(t *testing.T) {
	var requests atomic.Int64
	host := startRegistry(t, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
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
	pin := digest.ContentDigest(published)

	for _, tc := range []struct {
		name   string
		marked bool
	}{
		{name: "a folder nobody marked and nothing named"},
		{name: "the same folder marked as a workspace", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := requests.Load()
			machine := t.TempDir()
			global := filepath.Join(machine, "global.yaml")
			writeFixture(t, global, "default_registry: \""+host+"\"\noffline: false\n")
			lookup := func(name string) string {
				switch name {
				case "AGGREGA_CONFIG":
					return global
				case "HOME":
					return filepath.Join(machine, "home")
				case "XDG_CACHE_HOME":
					return filepath.Join(machine, "cache")
				case "LOCALAPPDATA":
					return filepath.Join(machine, "localappdata")
				}
				return ""
			}
			folder := folderNoWorkspaceResolvesFrom(t)
			if tc.marked {
				writeWorkspaceConfig(t, folder, "workspace: {}\n")
			}
			rw, err := workspaceconfig.Resolve(folder, "", lookup)
			if err != nil {
				t.Fatalf("resolve %s: %v", folder, err)
			}
			if rw.FoundNoWorkspace() == tc.marked {
				t.Fatalf("precondition: %s resolves as found-nothing=%v, the row needs %v", folder, rw.FoundNoWorkspace(), !tc.marked)
			}

			src, err := productionInstallPluginFetch(context.Background(), rw, ref)
			if err != nil {
				t.Fatalf("the install's fetch failed: %v", err)
			}
			if !bytes.Equal(src.Bytes, published) {
				t.Fatalf("the install fetched %q, want the published bytes", src.Bytes)
			}
			if requests.Load() == before {
				t.Fatal("the install's fetch asked the registry nothing, so where its bytes landed proves nothing")
			}

			root, dir := folder, rw.Config().ResolvedArtifactCacheDir()
			if !tc.marked {
				root, dir, err = rw.MachineArtifactCache()
				if err != nil {
					t.Fatalf("the machine-wide cache does not resolve: %v", err)
				}
			}
			slot := ocitest.MustCachePath(root, dir, oci.PluginArtifactKind(), ref, pin)
			if _, serr := os.Stat(filepath.Join(slot, "blob")); serr != nil {
				t.Fatalf("the install's fetch cached nothing at %s (%v)", slot, serr)
			}
			if !tc.marked {
				assertNoCacheAndStillNoWorkspace(t, folder)
			}
		})
	}
}
