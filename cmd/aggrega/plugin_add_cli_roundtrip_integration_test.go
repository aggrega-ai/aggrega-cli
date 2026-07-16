// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's CLI-plugin add-to-offline-run round-trip integration test.
// Build-tagged `integration` so the fast unit run excludes it (and the test-only
// go-containerregistry loopback dependency stays out of the default test graph);
// CI runs it via `go test -tags=integration ./cmd/aggrega/...`.
//
// It stands up an in-process real HTTP OCI registry, publishes a CLI plugin's
// .wasm as the content layer of a version-tagged manifest, then drives the REAL
// add path — fetch over the wire, verify, pin by content digest, cache both
// blobs, describe, and record — with no fetch or describe seam overridden. It
// then proves the pinned bytes resolve OFFLINE from the workspace cache and the
// guest runs, and that a tampered cache halts with the integrity error. This
// closes the coverage gap the earlier resolver-asymmetry defect hid: that an add
// that never cached its bytes left a later offline run unable to resolve them.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cliplugin"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cliPluginEntityYAML is the config-blob entity for the published plugin
// manifest. The add path reads the .wasm content layer and its describe()
// self-report, not this blob, so it only needs to be a plausible CLI Plugin
// entity for the single-manifest publish shape.
const cliPluginEntityYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: greet
  pluginType: cli
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
`

// TestIntegration_PluginAddCLI_FetchCachesForOfflineRun proves the two-sided
// contract: `aggrega plugin add cli` does a real OCI pull that caches the guest
// bytes by their content pin, and a later offline resolution reads exactly those
// cached bytes and runs the guest — no manual pull, no runtime fetch.
func TestIntegration_PluginAddCLI_FetchCachesForOfflineRun(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Hermetic tree construction during the add: no configured registrations.
	withCLIRegistrations(t, nil)

	// A guest that streams real stdout through the CLI host functions, so the
	// offline run proves the cached bytes both resolved and executed to output.
	guest := buildInstallGuest(t, "cli-multi-guest")

	host := startRegistry(t, nil)
	// Publish the .wasm as the single content layer of a version-tagged manifest —
	// the exact shape the add path's pull reads its bytes from.
	repo, err := oci.NewRemoteRepository(host + "/plugin/cli/greet")
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	if _, err := oci.PushPluginManifest(context.Background(), repo, "1.0.0", []byte(cliPluginEntityYAML), guest, nil); err != nil {
		t.Fatalf("publish plugin manifest: %v", err)
	}

	ref := "oci://" + host + "/plugin/cli/greet:1.0.0"
	root := t.TempDir()

	configPath := filepath.Join(root, workspaceconfig.ConfigFilename)
	resolveWS := func() *workspaceconfig.ResolvedWorkspace {
		rw, rerr := workspaceconfig.Resolve(root, "")
		if rerr != nil {
			t.Fatalf("resolve workspace: %v", rerr)
		}
		return rw
	}

	// Online for the add. The plugin is published unsigned to a registry with no
	// signature requirement, so the verify pass runs (it is not disabled) and finds
	// nothing to require — the verify-if-present default.
	if err := os.WriteFile(configPath, []byte("workspace: {}\ndefault_registry: \""+host+"\"\noffline: false\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw := resolveWS()

	// REAL add: neither the fetch nor the describe seam is overridden, so the
	// production fetch pulls the bytes, verifies, pins by content digest, caches
	// both blobs, describes the guest, and records the pin.
	var out bytes.Buffer
	if err := pluginInstallRun(context.Background(), rw, ref, false /*global*/, true /*workspace*/, false /*acceptCaps*/, true /*assumeYes*/, nil, strings.NewReader(""), &out); err != nil {
		t.Fatalf("plugin add cli (real fetch): %v", err)
	}

	// The config pins the prefixed content digest of the fetched bytes.
	wantPin := digest.ContentDigest(guest)
	refs := reResolveCLI(t, rw)
	if len(refs) != 1 || refs[0].URI != ref || refs[0].Digest != wantPin {
		t.Fatalf("plugins.cli pin = %+v; want one {uri:%q digest:%q}", refs, ref, wantPin)
	}

	// The fetch cached both self-verifying blobs under the (ref, prefixed-pin) slot
	// — the same slot key the runtime cache tier reads. The cached URI segment is
	// ref verbatim, so add-time and run-time address one slot.
	cacheDir := rw.Config().ResolvedArtifactCacheDir()
	slot := oci.CachePath(root, cacheDir, ref, wantPin)
	for _, name := range []string{"blob", "manifest"} {
		if _, err := os.Stat(filepath.Join(slot, name)); err != nil {
			t.Fatalf("expected cached %q at slot %s: %v", name, slot, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(slot, "blob")); !bytes.Equal(b, guest) {
		t.Fatal("cached content blob is not the published .wasm bytes")
	}

	// OFFLINE: flip the workspace to offline in place — preserving the plugins.cli
	// pin the add wrote — then re-resolve. The runtime resolver wires no puller
	// regardless, so a cache miss could not fetch; the offline flag makes that
	// explicit. Resolution reads the pinned bytes from the workspace cache.
	body, rerr := os.ReadFile(configPath)
	if rerr != nil {
		t.Fatalf("read config after add: %v", rerr)
	}
	flipped := strings.Replace(string(body), "offline: false", "offline: true", 1)
	if flipped == string(body) {
		t.Fatal("offline flag not flipped; config anchor drifted")
	}
	if err := os.WriteFile(configPath, []byte(flipped), 0o644); err != nil {
		t.Fatalf("rewrite config offline: %v", err)
	}
	rwOffline := resolveWS()

	// Run gate: the freshly resolved digest equals the pin (no drift), offline.
	gotPin, err := resolveCLIPluginDigest(rwOffline, ref)
	if err != nil {
		t.Fatalf("offline digest resolve: %v (must read the cache, not fetch)", err)
	}
	if gotPin != wantPin {
		t.Fatalf("offline resolved digest = %q; want the pin %q", gotPin, wantPin)
	}

	// The command tree surfaces the command offline from the cached describe.
	var haveCmd bool
	for _, r := range cliRegistrationsFromWorkspace(context.Background(), rwOffline) {
		if r.name == "greet-a" {
			haveCmd = true
		}
	}
	if !haveCmd {
		t.Fatal("offline registration did not surface the greet-a command from the cache")
	}

	// The runner resolves the cached bytes offline and the guest runs to exit 0,
	// streaming its output — no ociNotWiredHint.
	var stdout bytes.Buffer
	runner := cliplugin.Runner{Resolve: workspacePluginResolver(rwOffline), Digest: digest.ContentDigest, Stdout: &stdout, Stderr: io.Discard}
	code, runPin, err := runner.Run(context.Background(), ref, "greet-a", nil)
	if err != nil {
		t.Fatalf("offline run: %v (must resolve from cache, not fetch)", err)
	}
	if code != 0 {
		t.Fatalf("offline run exit = %d; want 0", code)
	}
	if runPin != wantPin {
		t.Fatalf("offline run digest = %q; want the pin %q", runPin, wantPin)
	}
	if !strings.Contains(stdout.String(), "hello from A") {
		t.Fatalf("offline run stdout = %q; want the guest greeting", stdout.String())
	}

	// NEGATIVE: a corrupted cache blob no longer hashes to the pin, so the offline
	// resolution halts with the integrity error rather than running drifted bytes.
	if err := os.WriteFile(filepath.Join(slot, "blob"), append([]byte("tampered"), guest...), 0o644); err != nil {
		t.Fatalf("tamper blob: %v", err)
	}
	if _, err := workspacePluginResolver(rwOffline)(ref); err == nil || !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Fatalf("tampered cache should halt with ErrFetchIntegrity; got %v", err)
	}

	// NEGATIVE: an unknown plugin URI still halts loudly (no silent empty resolve).
	if _, err := workspacePluginResolver(rwOffline)("oci://" + host + "/plugin/cli/absent:9.9.9"); err == nil {
		t.Fatal("an absent plugin URI must halt, not resolve to nothing")
	}
}
