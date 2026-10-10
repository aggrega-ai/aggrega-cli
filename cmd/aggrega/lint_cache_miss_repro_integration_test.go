// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gencatalog"
	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// buildVersionedLinterWasm compiles the publishing-linter crate to a .wasm with a
// describe()-reported build-provenance version, into an isolated target dir so the
// shared fixture artifact is untouched. The version is required because publish's
// cross-check (and gen-catalog) demand the entity's metadata.version equal the
// guest's describe() version; the plain fixture build leaves it empty. The plugin
// dir is resolved from the caller's pre-chdir working directory. Skips cleanly
// when the wasm toolchain is absent; a genuine compile failure is a hard fail.
func buildVersionedLinterWasm(t *testing.T, version string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	pluginDir := filepath.Clean(filepath.Join(wd, "..", "..", "plugins", "publishing-linter"))
	manifest := filepath.Join(pluginDir, "Cargo.toml")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("publishing-linter crate missing at %s: %v", manifest, err)
	}
	targetDir := t.TempDir()
	cmd := exec.Command("cargo", "build", "--release", "--target", "wasm32-unknown-unknown",
		"--manifest-path", manifest, "--target-dir", targetDir)
	cmd.Env = append(os.Environ(), "AGGREGA_VERSION="+version, "AGGREGA_COMMIT=reprobuild")
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		if strings.Contains(outStr, "wasm32-unknown-unknown") && strings.Contains(outStr, "not installed") {
			t.Skipf("wasm32-unknown-unknown target not installed; run `rustup target add wasm32-unknown-unknown`\n%s", outStr)
		}
		t.Fatalf("cargo build publishing-linter: %v\n%s", err, outStr)
	}
	artifact := filepath.Join(targetDir, "wasm32-unknown-unknown", "release", "publishing_linter.wasm")
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("cargo build succeeded but %s is missing: %v", artifact, err)
	}
	return artifact
}

// genNonEmbeddedLinterEntity synthesizes a publish-valid Plugin entity for a
// NON-EMBEDDED linter from a real .wasm guest, using the production gen-catalog
// path (describe() + .wasm-import derivation), so the entity's version, exports,
// host-function stamp, and command mirror agree with the guest and the push-time
// cross-check accepts it. name is the bare metadata.name; the identity coordinate
// is linter/<name>. Choosing a name other than the built-in publishing linter's
// keeps the coordinate off the embed tier — resolution must reach a later tier,
// not embedded.PublishingLinterURI. Returns the generated entity path and its
// describe()-reported version.
func genNonEmbeddedLinterEntity(t *testing.T, pluginPath, name string) (entityPath, version string) {
	t.Helper()
	overlayPath := filepath.Join(t.TempDir(), "overlay.yaml")
	writeFixture(t, overlayPath, "apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: "+name+"\n")
	outDir := t.TempDir()
	if err := gencatalog.Run(context.Background(), gencatalog.Options{
		PluginPath:  pluginPath,
		OverlayPath: overlayPath,
		OutputDir:   outDir,
	}, io.Discard); err != nil {
		t.Fatalf("gen-catalog non-embedded linter entity: %v", err)
	}
	coordDir := filepath.Join(outDir, "linter", name)
	versions, err := os.ReadDir(coordDir)
	if err != nil {
		t.Fatalf("read generated entity dir %s: %v", coordDir, err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected exactly one generated version under %s; got %d", coordDir, len(versions))
	}
	version = versions[0].Name()
	entityPath = filepath.Join(coordDir, version, "plugin.yaml")
	return entityPath, version
}

// TestIntegration_LinterCacheMiss_PulledGuestUnreachableAtLint is a failing-first
// gate: it proves that a non-embedded linter guest which `aggrega pull` fetches
// and caches (as a verdict-socket plugin, cache-not-pin) is unreachable when the
// linter socket resolves it, because resolveLinterPlugin builds a plugin resolver
// wired with the override source only — no workspace-root/cache-dir, so the
// content-cache tier is absent from the cascade.
//
// The test asserts the DESIRED end state — the linter resolves the pulled guest —
// so it FAILS today (witnessing the bug) and passes once the linter's resolver is
// wired with the cache tier. Two companion subtests bound the fault: one confirms
// the guest IS present in the content cache after pull (pull-side caching is
// sound), and one confirms a cache-wired resolver over the SAME cache and SAME
// artifact reference resolves it (the cache read and the resolution cascade are
// sound). The only thing that cannot resolve the guest is resolveLinterPlugin's
// under-wired resolver — localising the fault to that construction site.
func TestIntegration_LinterCacheMiss_PulledGuestUnreachableAtLint(t *testing.T) {
	// A real, non-embedded linter guest with a describe() version. The
	// publishing-linter .wasm is reused for its bytes; publishing it under the
	// identity linter/publishing-custom (not the built-in linter/publishing) keeps
	// it off the embed tier, so resolution must reach the content cache. Built
	// before any chdir. Skips cleanly only if the wasm toolchain is absent.
	pluginPath := buildVersionedLinterWasm(t, "1.0.0")
	std := standardCatalogDir(t)
	exampleDir := catalogOnlyExampleDir(t)

	host := startRegistry(t, nil)
	pushCatalog(t, host, std)

	const linterName = "publishing-custom"
	entityPath, version := genNonEmbeddedLinterEntity(t, pluginPath, linterName)
	linterURI := types.PluginCoordinate("linter", linterName) + ":" + version

	// Publish the non-embedded linter as a single OCI manifest (entity config blob
	// + inline .wasm), the shape `aggrega pull` caches a verdict-socket plugin from.
	// The publish runs before the consumer workspace below exists, so it declares
	// its own: the registry it pushes to is named explicitly and it needs no
	// catalog of its own.
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{}), EntityPath: entityPath, PluginPath: pluginPath, Registry: host}); err != nil {
		t.Fatalf("publish non-embedded linter %s: %v", linterURI, err)
	}

	// A consumer workspace (the catalog-only example) that configures the linter
	// socket to the non-embedded linter and resolves everything over the registry.
	ws := t.TempDir()
	copyTree(t, exampleDir, ws)
	writeWorkspaceConfig(t, ws,
		"default_registry: \""+host+"\"\n"+
			"auto_pull: true\n"+
			"offline: false\n"+
			"plugins:\n"+
			"  linter:\n"+
			"    - uri: "+linterURI+"\n")
	_ = os.Remove(filepath.Join(ws, "release.lock"))
	_ = os.RemoveAll(filepath.Join(ws, ".aggrega"))
	_ = os.RemoveAll(filepath.Join(ws, "dist"))
	t.Chdir(ws)

	// aggrega pull — resolves the release closure and caches every verdict-socket
	// plugin (the configured linter among them) into the content cache.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml"}); err != nil {
		t.Fatalf("pull: %v", err)
	}

	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()

	// Read the guest back through the exact cache-not-pin method the build's
	// verdict path uses. This is the entity + its .wasm layer digest as pull left
	// them in the content cache.
	lazy := catalog.NewLazyLookup(rw.Root(), cacheDir, nil, nil)
	entity, wasmDigest, cached, err := lazy.PluginFromCacheByURI(linterURI)
	if err != nil {
		t.Fatalf("read cached linter entity %s: %v", linterURI, err)
	}
	artifactRef := entity.ArtifactReference()

	t.Run("pull_caches_non_embedded_linter_guest", func(t *testing.T) {
		if !cached {
			t.Fatalf("pull did not cache the non-embedded linter %s; the repro premise (guest present in cache) is broken", linterURI)
		}
		if wasmDigest == "" {
			t.Fatalf("cached linter %s has an empty .wasm layer digest", linterURI)
		}
		t.Logf("guest present in content cache: entity=%s version=%s artifact=%s wasmDigest=%s cacheDir=%s",
			entity.Metadata.Name, entity.Metadata.Version, artifactRef, wasmDigest, cacheDir)
	})

	t.Run("cache_and_cascade_resolve_the_guest_when_wired", func(t *testing.T) {
		// A resolver wired with the workspace-root/cache-dir tier — and the
		// cache-not-pin verify-on-read posture a verdict socket uses — over the SAME
		// cache and SAME artifact reference resolveLinterPlugin would resolve. If
		// this cannot resolve the guest, the fault is NOT localised to construction
		// and the arc's premise is wrong (escalate).
		wired := pluginpath.NewResolver(
			pluginpath.ResolutionContext{
				WorkspaceRoot: rw.Root(),
				CacheDir:      cacheDir,
			},
			pluginpath.PinPostureVerifyOnRead,
			pluginpath.PinMaps{
				Pins:        map[string]string{artifactRef: wasmDigest},
				PinVersions: map[string]string{artifactRef: wasmDigest},
			},
		)
		resolved, err := wired.Resolve(artifactRef)
		if err != nil {
			t.Fatalf("cache-wired resolver could not resolve the guest; fault is NOT localised to ResolveLinterPlugins construction: %v", err)
		}
		if len(resolved.Bytes) == 0 {
			t.Fatalf("cache-wired resolver returned empty bytes for %s", artifactRef)
		}
		t.Logf("cache-wired resolver resolved the guest from tier %q (%d bytes) — cache read + cascade are sound", resolved.Tier, len(resolved.Bytes))
	})

	t.Run("linter_socket_resolves_the_pulled_guest", func(t *testing.T) {
		// The linter socket's own resolver, entity translation supplied so the
		// failure isolates to artifact resolution (not entity lookup): the entity is
		// resolvable, its .wasm is in the cache — the ONLY missing piece is the
		// cache tier in ResolveLinterPlugins' resolver. This asserts the desired end
		// state (the guest resolves), so it FAILS while the resolver omits the cache
		// tier and passes once it is wired.
		pluginByURI := func(string) (types.Plugin, bool) { return entity, true }
		attachments, err := lintops.ResolveLinterPlugins("", wsCfg, resolver.VerdictPluginTrust{}, pluginByURI)
		if err != nil {
			if errors.Is(err, oci.ErrFetchNotFound()) || errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
				t.Fatalf("linter socket cannot resolve the pulled+cached guest %s: the content-cache tier is absent from ResolveLinterPlugins' resolver: %v", linterURI, err)
			}
			t.Fatalf("linter socket cannot resolve the pulled+cached guest %s: %v", linterURI, err)
		}
		if len(attachments) != 1 {
			t.Fatalf("one configured linter attachment should resolve to one attachment; got %d", len(attachments))
		}
		resolved := attachments[0].Plugin
		if len(resolved.Bytes) == 0 {
			t.Fatalf("linter socket resolved %s with empty bytes", linterURI)
		}
		t.Logf("linter socket resolved the guest from tier %q (%d bytes)", resolved.Tier, len(resolved.Bytes))
	})
}
