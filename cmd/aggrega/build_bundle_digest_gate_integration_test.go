// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's build-time signature-bundle integrity gate coverage. It closes
// the gap where the verified signature-bundle digest a pull records on a plugin
// row was write-only from the build's view: the build now re-hashes the cached
// signature-bundle blob against that recorded digest before instantiating a
// signed guest — an offline byte/hash check, no cryptographic re-verification
// and no network/trust-root touch. The check is verify-WHEN-signed: an unsigned
// guest (no recorded signature fact) is never newly refused.
//
// Each arm stands up a real in-process OCI registry, publishes a config-socket
// mutator guest (signed for the positive/tamper arms, unsigned for the
// non-refusal arm), pulls under a keyed verify-if-present trust policy so the
// signed guest's bundle is cached and its digest recorded, then drives a real
// hermetic build (no puller wired) and asserts the gate's behavior from the
// build's outcome.
package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// publishSocketPluginEntitySigned publishes a REAL kind:Plugin entity for a
// workspace-level config socket, signed ONCE with the given keyed signer,
// through the production publish path. Under the single-manifest model the
// entity YAML (config blob) and the inline .wasm ride one OCI manifest tagged by
// metadata.version, signed once, so the single signature transitively covers the
// .wasm. It is the signed, socket-typed counterpart of publishSocketPluginEntity
// (pluginType/export are configurable so the same helper serves a mutator or a
// policy socket). Returns the .wasm content digest the consumer pins at pull.
func publishSocketPluginEntitySigned(t *testing.T, host, socketName, version, pluginPath, pluginType, export string, signer signing.Signer) string {
	t.Helper()
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin fixture %s: %v", pluginPath, err)
	}
	contentDigest := digest.ContentDigest(wasmBytes)

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	bareName := filepath.Base(socketName)
	writeFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: `+bareName+`
  version: "`+version+`"
  pluginType: `+pluginType+`
spec:
  abiVersion: v1alpha1
  exports:
    - name: `+export+`
`)
	sign := true
	if perr := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: entityDir}),
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   host,
		Sign:       &sign,
		Signer:     signer,
	}); perr != nil {
		t.Fatalf("publish signed socket plugin entity %s:%s: %v", socketName, version, perr)
	}
	return contentDigest
}

// writeVerifyIfPresentConfigSocketWorkspace writes a consumer workspace (the same
// aggregate / manifest / release the config-socket round-trip uses) plus an
// .aggrega-config.yaml whose trust policy pins host's keyed public key but does
// NOT require signatures. Under this verify-if-present posture a present
// signature is verified against the pinned key (and its bundle cached + digest
// recorded), while the unsigned fixture catalog entities are tolerated. The
// supplied plugins block attaches the socket guest. Returns the workspace path
// (not yet chdir'd into).
func writeVerifyIfPresentConfigSocketWorkspace(t *testing.T, host string, pinnedPubPEM []byte, pluginsBlock string) string {
	t.Helper()
	ws := t.TempDir()
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: compose/svc:1.0.0
`)
	writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app: {}
`)
	var b strings.Builder
	b.WriteString("default_registry: \"" + host + "\"\n")
	b.WriteString("auto_pull: true\n")
	b.WriteString("offline: false\n")
	b.WriteString("registries:\n")
	b.WriteString("  - host: \"" + host + "\"\n")
	b.WriteString("    signing:\n      required: false\n")
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pinnedPubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	b.WriteString(pluginsBlock)
	writeWorkspaceConfig(t, ws, b.String())
	return ws
}

// findLockfilePluginRow returns the .wasm artifact row for uri, failing when
// absent.
func findLockfilePluginRow(t *testing.T, lf *lockfile.Lockfile, uri string) *lockfile.Plugin {
	t.Helper()
	for i := range lf.Plugins {
		if lf.Plugins[i].URI == uri {
			return &lf.Plugins[i]
		}
	}
	t.Fatalf(".wasm artifact %q absent from lockfile plugins (have %v)", uri, lf.Plugins)
	return nil
}

// overwriteCachedBlobByDigest walks the workspace artifact cache under cwd,
// finds the content blob whose SHA-256 matches wantDigest, and overwrites it with
// tamper bytes. It fails if no matching blob is found. Walking by content digest
// avoids reconstructing the URI-encoded cache-slot path, so a tamper targets the
// exact bytes the resolver reads regardless of the slot layout.
func overwriteCachedBlobByDigest(t *testing.T, cacheRoot, wantDigest string, tamper []byte) {
	t.Helper()
	found := false
	err := filepath.WalkDir(cacheRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Base(path) != "blob" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if digest.ContentDigest(data) == wantDigest {
			if werr := os.WriteFile(path, tamper, 0o644); werr != nil {
				return werr
			}
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk cache %s: %v", cacheRoot, err)
	}
	if !found {
		t.Fatalf("no cached blob with content digest %q found under %s to tamper", wantDigest, cacheRoot)
	}
}

// TestIntegration_BuildBundleDigestGate proves the build-time offline
// signature-bundle integrity gate for a registry-pulled, signed config-socket
// guest, plus its verify-when-signed scoping. All arms share one keyed signer and
// the compose-provider fixture catalog; each arm uses its own registry +
// workspace so a tamper in one never leaks into another.
func TestIntegration_BuildBundleDigestGate(t *testing.T) {
	// Resolve fixtures BEFORE any chdir (umbrella-root discovery walks up from
	// cwd); a clean skip only if the toolchain is absent.
	mutatorPath := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
	std := standardCatalogDir(t)

	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	const mutatorSocket = "governance/tag-injector"
	const proofMutatorLabel = "mutator-proof"
	pluginsBlock := "plugins:\n  governance:\n    - uri: " + mutatorSocket + ":1.0.0\n      role: label-transform\n"
	artifactURIFor := func(string) string { return mutatorSocket }

	// pullSigned publishes the SIGNED mutator, writes a verify-if-present
	// workspace, chdir's into it, and runs the real pull so the guest's bundle is
	// cached and its digest recorded on the plugin row. Returns the loaded
	// lockfile, the .wasm artifact URI, and the workspace directory it built, so
	// each caller's own build declares that directory as the workspace it resolves
	// instead of leaving it to be discovered.
	pullSigned := func(t *testing.T) (*lockfile.Lockfile, string, string) {
		t.Helper()
		host := startRegistry(t, nil)
		configSocketFixtureCatalog(t, host)
		publishSocketPluginEntitySigned(t, host, mutatorSocket, "1.0.0", mutatorPath, "governance", "mutate", signer)
		ws := writeVerifyIfPresentConfigSocketWorkspace(t, host, pubPEM, pluginsBlock)
		t.Chdir(ws)
		if perr := pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
			ReleasePath: "release.yaml", CatalogDirs: []string{std},
		}); perr != nil {
			t.Fatalf("signed pull: %v", perr)
		}
		lf, _, lerr := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if lerr != nil {
			t.Fatalf("load lockfile after signed pull: %v", lerr)
		}
		return lf, artifactURIFor(host), ws
	}

	t.Run("intact_bundle_builds_and_socket_runs", func(t *testing.T) {
		lf, wasmURI, ws := pullSigned(t)
		row := findLockfilePluginRow(t, lf, wasmURI)
		if row.Signature == nil || row.Signature.BundleDigest == "" {
			t.Fatalf(".wasm row recorded no signature bundle digest; verify-when-signed cannot engage: %+v", row.Signature)
		}
		// The cached bundle blob is present at the single-sourced slot the gate reads.
		bundlePath := oci.SignatureBundlePath(ws, ".aggrega/cache", row.Signature.BundleDigest)
		if _, serr := os.Stat(bundlePath); serr != nil {
			t.Fatalf("cached signature bundle absent at %s: %v", bundlePath, serr)
		}

		if berr := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); berr != nil {
			t.Fatalf("intact signed guest must build cleanly; got: %v", berr)
		}
		// The mutator ran: its injected label reached the rendered artifact.
		if found, tree := distTreeContains(t, "dist", proofMutatorLabel); !found {
			t.Errorf("rendered output does not carry the mutator label %q; the guest did not run:\n%s", proofMutatorLabel, tree)
		}
	})

	t.Run("intact_bundle_found_by_a_build_run_from_beneath_the_workspace", func(t *testing.T) {
		lf, wasmURI, ws := pullSigned(t)
		row := findLockfilePluginRow(t, lf, wasmURI)
		if row.Signature == nil || row.Signature.BundleDigest == "" {
			t.Fatalf(".wasm row recorded no signature bundle digest; verify-when-signed cannot engage: %+v", row.Signature)
		}
		// The pull ran from the workspace root and the build runs from a directory
		// beneath it, so the gate finds the cached bundle only because the pull's
		// write and the build's read both join the workspace root.
		sub := filepath.Join(ws, "releases")
		if merr := os.MkdirAll(sub, 0o755); merr != nil {
			t.Fatalf("mkdir %s: %v", sub, merr)
		}
		t.Chdir(sub)
		if berr := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), filepath.Join(ws, "release.yaml"), []string{std}, false, os.Stderr, os.Stdout); berr != nil {
			t.Fatalf("a build run from beneath the workspace must find the bundle the pull cached; got: %v", berr)
		}
		if found, tree := distTreeContains(t, "dist", proofMutatorLabel); !found {
			t.Errorf("rendered output does not carry the mutator label %q; the guest did not run:\n%s", proofMutatorLabel, tree)
		}
	})

	t.Run("tampered_bundle_rejected_before_instantiation", func(t *testing.T) {
		lf, wasmURI, ws := pullSigned(t)
		row := findLockfilePluginRow(t, lf, wasmURI)
		if row.Signature == nil || row.Signature.BundleDigest == "" {
			t.Fatalf(".wasm row recorded no signature bundle digest: %+v", row.Signature)
		}
		// Tamper the CACHED signature bundle blob (the .wasm content pin stays
		// intact), so the halt is attributable to the bundle-digest gate alone.
		bundlePath := oci.SignatureBundlePath(ws, ".aggrega/cache", row.Signature.BundleDigest)
		if werr := os.WriteFile(bundlePath, []byte("tampered-signature-bundle-bytes"), 0o644); werr != nil {
			t.Fatalf("tamper cached bundle %s: %v", bundlePath, werr)
		}

		berr := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout)
		if !errors.Is(berr, pluginpath.ErrPluginBundleDigestMismatch()) {
			t.Fatalf("a tampered cached bundle must halt with ErrPluginBundleDigestMismatch before instantiation; got: %v", berr)
		}
	})

	t.Run("tampered_wasm_rejected_by_content_pin", func(t *testing.T) {
		lf, wasmURI, ws := pullSigned(t)
		row := findLockfilePluginRow(t, lf, wasmURI)
		// Overwrite the cached .wasm content blob → the existing content-digest pin
		// halts before the bundle gate is reached (non-regression).
		cwd, gerr := os.Getwd()
		if gerr != nil {
			t.Fatalf("getwd: %v", gerr)
		}
		overwriteCachedBlobByDigest(t, filepath.Join(cwd, ".aggrega", "cache"), row.Digest, []byte("tampered-wasm-bytes-not-a-module"))

		berr := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout)
		if !errors.Is(berr, oci.ErrFetchIntegrity()) {
			t.Fatalf("a tampered .wasm must halt with ErrFetchIntegrity via the content-digest pin; got: %v", berr)
		}
	})

	t.Run("unsigned_guest_builds_no_new_refusal", func(t *testing.T) {
		host := startRegistry(t, nil)
		configSocketFixtureCatalog(t, host)
		// Publish the mutator UNSIGNED under the same verify-if-present policy (its
		// pinned key never engages because no signature referrer is present).
		publishSocketPluginEntity(t, host, mutatorSocket, "1.0.0", mutatorPath, "governance", "mutate")
		ws := writeVerifyIfPresentConfigSocketWorkspace(t, host, pubPEM, pluginsBlock)
		t.Chdir(ws)
		if perr := pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
			ReleasePath: "release.yaml", CatalogDirs: []string{std},
		}); perr != nil {
			t.Fatalf("unsigned pull under a non-requiring policy: %v", perr)
		}
		lf, _, lerr := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if lerr != nil {
			t.Fatalf("load lockfile after unsigned pull: %v", lerr)
		}
		row := findLockfilePluginRow(t, lf, artifactURIFor(host))
		if row.Signature != nil {
			t.Fatalf("unsigned guest recorded a signature fact %+v; verify-when-signed requires none", row.Signature)
		}
		// The gate is a no-op with no recorded fact: the build is NOT newly refused.
		if berr := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); berr != nil {
			t.Fatalf("an unsigned guest must build per existing policy (no new refusal); got: %v", berr)
		}
		if found, tree := distTreeContains(t, "dist", proofMutatorLabel); !found {
			t.Errorf("unsigned guest build: mutator label %q missing from dist tree:\n%s", proofMutatorLabel, tree)
		}
	})
}
