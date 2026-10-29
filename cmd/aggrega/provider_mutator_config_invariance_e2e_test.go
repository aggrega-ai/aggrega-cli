// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's end-to-end config-invariance coverage for the two
// byte-determining plugin sockets — providers and mutator — resolved from a
// LOCAL fixture registry. The arc law: a plugin that shapes the rendered output
// bytes is pinned at pull and reconstructed from the lockfile at render time, so
// the lockfile completely determines output; a plugins.providers or
// plugins.mutator change made after the pull does not move output (re-pull to
// adopt it).
//
// The SERVE render path (the compilerops ResolveClosure + BuildToBuffer the
// daemon drives under build-on-inspect) reconstructs both sockets from the
// lockfile: a proof flips the live config to a binding/function that WOULD move
// (or break) output if read live, renders WITHOUT a re-pull, and asserts the
// output is unchanged; a control render with a nil lockfile confirms the flipped
// live config WOULD have moved output, so the invariance is load-bearing.
//
// The CLI BUILD path renders through the same lockfile-driven resolution, but it
// runs a lockfile-drift verification before render whose reconstruction does not
// yet re-add the workspace socket entities pinned at pull. A frozen (no-network)
// build of a REGISTRY socket therefore reports drift and halts rather than
// rendering offline. Build-side config-invariance is proven for the achievable
// non-registry sockets by the sibling suites (an embedded provider, which is never
// pinned, and a local mutator, pinned from the bytes beside its entity, so neither drifts);
// the registry-socket frozen-build gap is characterized here as a guarded
// current-state test.
//
// The registry-published provider guest, its custom render dimension, and the
// publish/pull/pin helpers are shared with the custom-provider round-trip suite;
// the mutator fixture catalog helpers are shared with the config-socket suite.
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

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// serveRenderArtifacts drives the compilerops render path — the one the serve
// daemon runs under build-on-inspect — over the workspace at ws and returns
// the rendered artifact buffers. withLockfile selects whether BuildToBuffer
// reconstructs the byte-determining plugin selections from the lockfile (true,
// the serve/build default) or reads them live from the workspace config (false,
// the control). The catalog view carries a NIL puller, so the render is hermetic
// (cache-only) exactly as the daemon's is. catDirs supplies any local catalog
// entities the closure resolves alongside the registry-pulled ones; the core
// provider entities come from the binary's embed rather than from a directory.
func serveRenderArtifacts(t *testing.T, ws, releaseRel string, catDirs []string, withLockfile bool) (map[string][]byte, error) {
	t.Helper()
	rw, err := workspaceconfig.Resolve(ws, "")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	releasePath := filepath.Join(ws, releaseRel)
	dirs := rw.ResolveCatalogDirs(releasePath, catDirs)

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}

	view := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	view.SetPins(catalog.PinsFromLockfile(lf))

	resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("serve ResolveClosure: %v", cerr)
	}

	var lfArg *lockfile.Lockfile
	if withLockfile {
		lfArg = lf
	}
	res, berr := compilerops.BuildToBuffer(context.Background(), compilerops.BuildInputs{
		View:                             view,
		Resolved:                         resolved,
		ReleasePath:                      releasePath,
		WorkspaceConfig:                  wsCfg,
		WorkspaceRoot:                    wsRoot,
		Lockfile:                         lfArg,
		TolerateCatalogViewInconsistency: true,
	}, pipeline.Options{})
	if berr != nil {
		return nil, berr
	}
	return res.Completed().Artifacts, nil
}

func artifactsEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !bytes.Equal(b[k], v) {
			return false
		}
	}
	return true
}

// TestIntegration_ProviderConfigInvariance_Serve proves the arc law for a
// REGISTRY custom provider on the serve render path: after a pull pins the
// capability→plugin binding (and the provider's .wasm), a plugins.providers
// change made after the pull with no re-pull does not move the rendered output —
// the serve render reconstructs the binding from the lockfile. The control (no
// lockfile) confirms the flipped live binding WOULD have halted the render, so
// the invariance is load-bearing.
func TestIntegration_ProviderConfigInvariance_Serve(t *testing.T) {
	const (
		capability  = "stellar"
		pluginCoord = "provider/render-custom"
		pluginURI   = pluginCoord + ":1.0.0"
	)
	providerWasm := ensureRenderProviderWasm(t, capability)
	host := startRegistry(t, nil)
	localProvider := customDimensionCatalog(t, host, capability)
	catDirs := []string{localProvider}
	publishProviderPluginEntity(t, host, pluginCoord, "1.0.0", providerWasm, []string{capability})

	pinned := "plugins:\n  providers:\n    - capability: " + capability + "\n      uri: " + pluginURI + "\n"
	ws := writeCustomProviderWorkspace(t, host, capability, pinned, true)
	t.Chdir(ws)
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
		t.Fatalf("registry pull: %v", err)
	}

	// Baseline serve render under the pinned binding.
	baseline, err := serveRenderArtifacts(t, ws, "release.yaml", catDirs, true)
	if err != nil {
		t.Fatalf("baseline serve render: %v", err)
	}
	if len(baseline) == 0 {
		t.Fatal("baseline serve render produced no artifacts; the invariance assertion would be vacuous")
	}
	if found := artifactsContain(baseline, renderedNodePrefix); !found {
		t.Fatalf("baseline serve render does not carry the pulled provider's %q witness", renderedNodePrefix)
	}

	// Flip the live config: bind the capability to a NON-EXISTENT plugin. Read
	// live, this binding is unsatisfiable and halts render; read from the
	// lockfile, the serve render reconstructs the pinned binding and is unchanged.
	flipped := "plugins:\n  providers:\n    - capability: " + capability + "\n      uri: provider/does-not-exist:9.9.9\n"
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: false\noffline: false\n"+flipped)

	afterFlip, err := serveRenderArtifacts(t, ws, "release.yaml", catDirs, true)
	if err != nil {
		t.Fatalf("serve render after a plugins.providers flip (no re-pull): %v; serve must resolve the binding from the lockfile", err)
	}
	if !artifactsEqual(baseline, afterFlip) {
		t.Fatal("SERVE output moved after a plugins.providers flip with no re-pull; the render must resolve the binding from the lockfile")
	}

	// Control: with NO lockfile, the serve render reads the flipped live config,
	// whose unsatisfiable binding halts — proving the lockfile reconstruction is
	// what holds output invariant, not that the flip was inert.
	if _, cerr := serveRenderArtifacts(t, ws, "release.yaml", catDirs, false); cerr == nil {
		t.Fatal("serve render with the flipped live config (no lockfile) SUCCEEDED; the unsatisfiable binding should have halted it — the invariance is not load-bearing")
	}
}

// TestIntegration_MutatorConfigInvariance_Serve proves the arc law for a
// REGISTRY mutator socket on the serve render path. The mutator rewrites the IR
// (shaping output bytes), so after a pull pins its attachment (coordinate +
// export function), a plugins.mutator function change made after the pull with
// no re-pull does not move output — the serve render reconstructs the attachment
// from the lockfile. The control (no lockfile) confirms the render would fail
// without the lockfile, so the reconstruction is load-bearing.
func TestIntegration_MutatorConfigInvariance_Serve(t *testing.T) {
	mutatorPath := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
	std := standardCatalogDir(t)
	catDirs := []string{std}

	host := startRegistry(t, nil)
	configSocketFixtureCatalog(t, host)
	const (
		mutatorSocket     = "governance/tag-injector"
		proofMutatorLabel = "mutator-proof"
	)
	publishSocketPluginEntity(t, host, mutatorSocket, "1.0.0", mutatorPath, "governance", "mutate")

	pinned := "plugins:\n  governance:\n    - uri: " + mutatorSocket + ":1.0.0\n      role: label-transform\n"
	ws := writeConfigSocketWorkspace(t, host, pinned, true)
	t.Chdir(ws)
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
		t.Fatalf("registry pull (mutator attached): %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if len(lf.Mutators) != 1 || lf.Mutators[0].Plugin != mutatorSocket {
		t.Fatalf("lockfile mutators = %+v; want one attachment pinned at %q", lf.Mutators, mutatorSocket)
	}

	// Baseline serve render (render on): the mutator runs at IR-build and its
	// injected label reaches the rendered artifact.
	baseline, err := serveRenderArtifacts(t, ws, "release.yaml", catDirs, true)
	if err != nil {
		t.Fatalf("baseline serve render: %v", err)
	}
	if !artifactsContain(baseline, proofMutatorLabel) {
		t.Fatal("baseline serve render does not carry the mutator-injected label; the invariance assertion would be vacuous")
	}

	// Flip the live config mutator export function to a NON-EXISTENT export,
	// leaving the URI unchanged. Read live, the bogus export halts; read from the
	// lockfile, the render runs the pinned mutate export and is unchanged.
	flipped := "plugins:\n  governance:\n    - uri: " + mutatorSocket + ":1.0.0\n      role: label-transform\n      function: does-not-exist\n"
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: false\noffline: false\n"+flipped)

	afterFlip, err := serveRenderArtifacts(t, ws, "release.yaml", catDirs, true)
	if err != nil {
		t.Fatalf("serve render after a plugins.mutator function flip (no re-pull): %v; serve must reconstruct the attachment from the lockfile", err)
	}
	if !artifactsEqual(baseline, afterFlip) {
		t.Fatal("SERVE output moved after a plugins.mutator flip with no re-pull; the render must reconstruct the mutator from the lockfile")
	}
	if !artifactsContain(afterFlip, proofMutatorLabel) {
		t.Fatal("post-flip serve render lost the mutator-injected label; the pinned mutate export did not run")
	}

	// Control: with NO lockfile, the render has neither the pinned attachment nor
	// the pins that make the mutator .wasm resolvable, so it halts — proving the
	// lockfile is load-bearing for the invariant render.
	if _, cerr := serveRenderArtifacts(t, ws, "release.yaml", catDirs, false); cerr == nil {
		t.Fatal("serve render with the flipped live config (no lockfile) SUCCEEDED; it should have halted — the invariance is not load-bearing")
	}
}

// TestIntegration_RegistrySocketFrozenBuild_ReconstructsSocketAxis proves the arc
// law's full form on the CLI build path: a frozen (auto_pull off, no network)
// build of a REGISTRY provider or mutator socket renders offline and reproduces
// its output byte-for-byte across rebuilds, because the build's drift-verify
// reconstruction re-adds the workspace-socket entities AND their .wasm from the
// lockfile. The socket selection is a pull-time choice recorded only in the
// lockfile, so the reconstruction reads the lockfile (never live config); the
// render then resolves the socket from those same pins. This is the both-axes
// proof the earlier characterization guarded: the frozen build no longer drifts.
func TestIntegration_RegistrySocketFrozenBuild_ReconstructsSocketAxis(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		const (
			capability  = "stellar"
			pluginCoord = "provider/render-custom"
			pluginURI   = pluginCoord + ":1.0.0"
		)
		providerWasm := ensureRenderProviderWasm(t, capability)
		host := startRegistry(t, nil)
		localProvider := customDimensionCatalog(t, host, capability)
		catDirs := []string{localProvider}
		publishProviderPluginEntity(t, host, pluginCoord, "1.0.0", providerWasm, []string{capability})
		pinned := "plugins:\n  providers:\n    - capability: " + capability + "\n      uri: " + pluginURI + "\n"
		ws := writeCustomProviderWorkspace(t, host, capability, pinned, true)
		t.Chdir(ws)
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
			t.Fatalf("registry pull: %v", err)
		}
		assertFrozenBuildGreenAndByteIdentical(t, ws, host, catDirs, pinned, renderedNodePrefix)
	})

	t.Run("mutator", func(t *testing.T) {
		mutatorPath := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
		std := standardCatalogDir(t)
		catDirs := []string{std}
		host := startRegistry(t, nil)
		configSocketFixtureCatalog(t, host)
		const mutatorSocket = "governance/tag-injector"
		publishSocketPluginEntity(t, host, mutatorSocket, "1.0.0", mutatorPath, "governance", "mutate")
		pinned := "plugins:\n  governance:\n    - uri: " + mutatorSocket + ":1.0.0\n      role: label-transform\n"
		ws := writeConfigSocketWorkspace(t, host, pinned, true)
		t.Chdir(ws)
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
			t.Fatalf("registry pull: %v", err)
		}
		assertFrozenBuildGreenAndByteIdentical(t, ws, host, catDirs, pinned, "mutator-proof")
	})
}

// TestIntegration_RegistrySocketFrozenBuild_TamperedPinStillDrifts proves the
// socket-axis reconstruction did NOT blind the drift check: a hand-edited lockfile
// whose socket .wasm pin no longer names the cached content re-adds nothing on the
// plugin axis, so the drift verify fires ErrLockfileDrift over exactly the
// reconstructed workspace-socket axis. The reconstruction re-reads (re-hashes) the
// pinned content rather than copying the recorded digest, so a moved pin surfaces.
func TestIntegration_RegistrySocketFrozenBuild_TamperedPinStillDrifts(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		const (
			capability  = "stellar"
			pluginCoord = "provider/render-custom"
			pluginURI   = pluginCoord + ":1.0.0"
		)
		providerWasm := ensureRenderProviderWasm(t, capability)
		host := startRegistry(t, nil)
		localProvider := customDimensionCatalog(t, host, capability)
		catDirs := []string{localProvider}
		publishProviderPluginEntity(t, host, pluginCoord, "1.0.0", providerWasm, []string{capability})
		pinned := "plugins:\n  providers:\n    - capability: " + capability + "\n      uri: " + pluginURI + "\n"
		ws := writeCustomProviderWorkspace(t, host, capability, pinned, true)
		t.Chdir(ws)
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
			t.Fatalf("registry pull: %v", err)
		}
		assertFrozenBuildDriftsOnTamperedSocketPin(t, ws, host, catDirs, pinned, pluginCoord)
	})

	t.Run("mutator", func(t *testing.T) {
		mutatorPath := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
		std := standardCatalogDir(t)
		catDirs := []string{std}
		host := startRegistry(t, nil)
		configSocketFixtureCatalog(t, host)
		const mutatorSocket = "governance/tag-injector"
		publishSocketPluginEntity(t, host, mutatorSocket, "1.0.0", mutatorPath, "governance", "mutate")
		pinned := "plugins:\n  governance:\n    - uri: " + mutatorSocket + ":1.0.0\n      role: label-transform\n"
		ws := writeConfigSocketWorkspace(t, host, pinned, true)
		t.Chdir(ws)
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
			t.Fatalf("registry pull: %v", err)
		}
		assertFrozenBuildDriftsOnTamperedSocketPin(t, ws, host, catDirs, pinned, mutatorSocket)
	})
}

// assertFrozenBuildGreenAndByteIdentical freezes the workspace (auto-pull off, so
// no heal-by-re-pull runs), builds twice with no network, and asserts both builds
// succeed, the socket-distinctive witness is present (the pinned socket rendered),
// and the two rendered trees are byte-identical. wsRoot is the workspace the
// caller has already changed into and a pull has already populated the lockfile
// in; the remaining relative paths (the rendered tree, the release file) are
// read from there. The workspace is re-declared per build rather than once,
// because the freeze above rewrites its configuration and each build must see
// the rewritten one.
func assertFrozenBuildGreenAndByteIdentical(t *testing.T, wsRoot, host string, catDirs []string, pinnedBlock, witness string) {
	t.Helper()
	writeWorkspaceConfig(t, wsRoot, "default_registry: \""+host+"\"\nauto_pull: false\noffline: false\n"+pinnedBlock)

	_ = os.RemoveAll("dist")
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}), "release.yaml", catDirs, false, io.Discard, io.Discard); err != nil {
		t.Fatalf("frozen offline build of a registry socket halted; the socket axis must reconstruct from the lockfile: %v", err)
	}
	if found, tree := distTreeContains(t, "dist", witness); !found {
		t.Fatalf("frozen build produced no %q witness; the pinned socket did not render:\n%s", witness, tree)
	}
	first := hashDistTree(t, "dist")

	_ = os.RemoveAll("dist")
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}), "release.yaml", catDirs, false, io.Discard, io.Discard); err != nil {
		t.Fatalf("second frozen offline build halted: %v", err)
	}
	if second := hashDistTree(t, "dist"); second != first {
		t.Fatalf("frozen builds not byte-identical: %s != %s", second, first)
	}
}

// assertFrozenBuildDriftsOnTamperedSocketPin freezes the workspace, tampers the
// socket .wasm ARTIFACT pin in the lockfile (both the cache-addressing version and
// the content digest, to a coordinate the cache cannot serve), then builds with no
// network and asserts the drift verify fires ErrLockfileDrift naming the socket.
// wsRoot is the workspace the caller has already changed into, which is also where
// the lockfile being tampered with lives.
func assertFrozenBuildDriftsOnTamperedSocketPin(t *testing.T, wsRoot, host string, catDirs []string, pinnedBlock, socketCoord string) {
	t.Helper()
	writeWorkspaceConfig(t, wsRoot, "default_registry: \""+host+"\"\nauto_pull: false\noffline: false\n"+pinnedBlock)

	lfPath := lockfile.PathFor("release.yaml")
	lf, _, err := lockfile.LoadLockfile(lfPath)
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	const bogus = "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"
	tampered := false
	for i := range lf.Plugins {
		if lf.Plugins[i].URI == socketCoord {
			lf.Plugins[i].ResolvedVersion = bogus
			lf.Plugins[i].Digest = bogus
			tampered = true
		}
	}
	if !tampered {
		t.Fatalf("socket .wasm artifact %q not found in lockfile plugins %v", socketCoord, lf.Plugins)
	}
	if werr := lockfile.WriteLockfile(lfPath, lf); werr != nil {
		t.Fatalf("write tampered lockfile: %v", werr)
	}

	_ = os.RemoveAll("dist")
	err = dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}), "release.yaml", catDirs, false, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("frozen build with a tampered socket .wasm pin SUCCEEDED; drift over the reconstructed workspace-socket axis was not detected — the fix blinded the drift check")
	}
	if !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("tampered socket pin: got %v; want errors.Is(lockfile.ErrLockfileDrift)", err)
	}
	if !strings.Contains(err.Error(), socketCoord) {
		t.Fatalf("drift error does not name the tampered socket %q: %v", socketCoord, err)
	}
}

// artifactsContain reports whether any artifact buffer contains needle.
func artifactsContain(arts map[string][]byte, needle string) bool {
	for _, b := range arts {
		if bytes.Contains(b, []byte(needle)) {
			return true
		}
	}
	return false
}
