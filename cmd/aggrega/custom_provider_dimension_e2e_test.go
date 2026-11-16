// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

// Package main's registry round-trip coverage for a CUSTOM provider render
// dimension. A provider socket influences the rendered OUTPUT BYTES, so the arc
// law requires it pinned in the lockfile at pull and reconstructed
// byte-identically at build (config-invariant — re-pull to change). This file
// proves that end to end against a LOCAL fixture registry, for a provider guest
// published to the registry declaring a CUSTOM render target (a dimension beyond
// the embedded helm/compose/terraform providers): pull pins both the provider's
// .wasm ARTIFACT (lf.Plugins) and the capability→plugin BINDING (lf.Providers);
// a hermetic build (nil puller) loads the PULLED guest and renders THROUGH it;
// a second build reuses the pin with no network, byte-identically; an explicit
// binding OVERRIDES an embedded core provider for its target; and an unknown
// target with no binding and no serving plugin halts loudly.
//
// The provider guest is the committed render-node-go PDK fixture — a provider
// plugin whose render_node export emits, for each IR node, a single file whose
// bytes are "rendered-node:<node-id>". That fixed, provider-distinctive content
// is the observable witness that the PULLED guest rendered, not an embedded
// provider (whose output is docker-compose / a helm chart, never that string).
// Render routing keys on the Plugin ENTITY's listed capabilities, and publish
// admits a list naming only capabilities describe() reports, so each test builds
// the guest reporting the capability its entity lists (custom, or a core one).
package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// renderedNodePrefix is the fixed content prefix the render-node-go provider
// fixture prepends to each node id. Its presence in the rendered tree is the
// provider-distinctive witness that the PULLED custom guest rendered a node.
const renderedNodePrefix = "rendered-node:"

// ensureRenderProviderWasm builds a minimal custom provider render guest whose
// describe() reports exactly the given capabilities, and returns its .wasm path.
// The committed go render fixture (aggrega-pdk/go/examples/render) renders one
// node to a file whose bytes are "rendered-node:<node-id>" — the
// provider-distinctive witness this suite needs — but its describe() declares
// no ProviderContract, so it supports no recipe input kind and the
// orchestrator's input-kind gate rejects every recipe routed to it, and it
// reports only its own ["parity"] capability, which publish would refuse to
// list under any other name. This helper copies that example to a scratch
// module (leaving the original untouched) and patches its describe() to declare
// InputKinds.Synthesized, so the guest accepts the fixture's synthesized
// (workload-bearing) recipe, and to report the capabilities the caller's entity
// lists; then builds it with the go toolchain (GOOS=wasip1).
//
// The example is read from the aggrega-pdk repository checked out beside this
// one, and the test skips, naming that repository, when the checkout is absent:
// the example is a module of its own, not part of the released Go plugin kit.
// Paths resolve relative to the ORIGINAL cwd, so a caller invokes it BEFORE any
// t.Chdir into a temp workspace. A build-tool-absent failure fails loudly (the
// go toolchain runs the surrounding test, so it is present).
func ensureRenderProviderWasm(t *testing.T, capabilities ...string) string {
	t.Helper()
	if len(capabilities) == 0 {
		t.Fatal("ensureRenderProviderWasm: name the capabilities the guest reports; publish admits an entity listing only those")
	}
	exampleDir := wasmtest.SiblingPath(t, "aggrega-pdk", "go", "examples", "render")
	pdkDir := wasmtest.SiblingPath(t, "aggrega-pdk", "go")
	if _, err := os.Stat(filepath.Join(exampleDir, "main.go")); err != nil {
		t.Fatalf("provider render fixture source absent at %s: %v", exampleDir, err)
	}

	scratch := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum", "main.go"} {
		data, rerr := os.ReadFile(filepath.Join(exampleDir, name))
		if rerr != nil {
			t.Fatalf("read render example %s: %v", name, rerr)
		}
		out := string(data)
		switch name {
		case "go.mod":
			// The example's replace directive for the plugin kit is relative to its
			// own location; rewrite it to an absolute path that resolves from the
			// scratch module. The ABI is taken at the version its go.mod requires.
			out = strings.Replace(out, "=> ../../\n", "=> "+pdkDir+"\n", 1)
		case "main.go":
			// Declare a ProviderContract so the guest advertises support for the
			// synthesized (workload-bearing) recipe the fixture catalog authors.
			const anchor = "\t\tMetadata: &aggrega.PluginDescriptorMeta{"
			if !strings.Contains(out, anchor) {
				t.Fatalf("render example main.go descriptor shape changed; patch anchor %q absent", anchor)
			}
			out = strings.Replace(out, anchor,
				"\t\tContract:     &aggrega.ProviderContract{InputKinds: aggrega.InputKinds{Synthesized: true, ArtifactsStandalone: true}},\n"+anchor, 1)
			// Report the capabilities the published entity lists in place of the
			// example's own.
			const capsAnchor = "\t\tCapabilities: []string{\"parity\"},\n"
			if !strings.Contains(out, capsAnchor) {
				t.Fatalf("render example main.go capability shape changed; patch anchor %q absent", capsAnchor)
			}
			quoted := make([]string, len(capabilities))
			for i, c := range capabilities {
				quoted[i] = strconv.Quote(c)
			}
			out = strings.Replace(out, capsAnchor,
				"\t\tCapabilities: []string{"+strings.Join(quoted, ", ")+"},\n", 1)
		}
		if werr := os.WriteFile(filepath.Join(scratch, name), []byte(out), 0o644); werr != nil {
			t.Fatalf("write scratch %s: %v", name, werr)
		}
	}

	artifact := filepath.Join(scratch, "render-synth-provider-go.wasm")
	// Stamp a build version so the guest self-reports it through describe(); the
	// entities published against this guest author the same metadata.version, and
	// the publish cross-check halts on a mismatch. This build bypasses the
	// example's build.sh, so the stamp is applied here rather than inherited.
	cmd := exec.Command("go", "build", "-buildmode=c-shared",
		"-ldflags", "-X github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega.Version=1.0.0",
		"-o", artifact, "./")
	cmd.Dir = scratch
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build custom provider render fixture: %v\n%s", err, out)
	}
	return artifact
}

// publishProviderPluginEntity publishes the render-node-go provider guest to the
// in-process registry as a REAL kind:Plugin entity (ONE consolidated manifest:
// the entity YAML the config blob, the .wasm an inline layer) declaring the
// supplied served capabilities, each of which the guest's describe() must report
// (build it with ensureRenderProviderWasm naming the same list). metadata.name is
// the bare leaf of socketName (e.g. "render-custom" for "provider/render-custom");
// pluginType is provider; the sole authored export is render_node, which the
// guest's describe() self-reports, so the publish-time cross-check passes.
// Returns the .wasm content digest the consumer pins at pull.
func publishProviderPluginEntity(t *testing.T, host, socketName, version, pluginPath string, capabilities []string) string {
	t.Helper()
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read provider plugin fixture %s: %v", pluginPath, err)
	}
	contentDigest := digest.ContentDigest(wasmBytes)

	var caps strings.Builder
	for _, c := range capabilities {
		caps.WriteString("      - " + c + "\n")
	}

	entityDir := t.TempDir()
	entityPath := filepath.Join(entityDir, "plugin.yaml")
	bareName := filepath.Base(socketName)
	writeFixture(t, entityPath, `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: `+bareName+`
  version: "`+version+`"
  pluginType: provider
spec:
  abiVersion: v1beta1
  provider:
    capabilities:
`+caps.String()+`  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: entityDir}), EntityPath: entityPath, PluginPath: pluginPath, Registry: host}); err != nil {
		t.Fatalf("publish provider plugin entity %s:%s: %v", socketName, version, err)
	}
	return contentDigest
}

// customDimensionCatalog writes and pushes a fixture catalog whose Component is
// implemented by a Recipe rendering under the CUSTOM capability. The compute
// resolves purely over the registry. Returns an empty local catalog dir, which
// the caller supplies to pull and build through --catalog: the capability's
// render behaviour is carried entirely by the published provider Plugin, so the
// local dir contributes no entity and exists only to exercise the flag.
func customDimensionCatalog(t *testing.T, host, capability string) string {
	t.Helper()
	cat := t.TempDir()
	writeFixtureEntity(t, cat, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
	writeFixtureEntity(t, cat, "recipe/"+capability+"/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: `+capability+`
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: busybox:1
`)
	pushCatalog(t, host, cat)

	local := t.TempDir()
	return local
}

// writeCustomProviderWorkspace writes a consumer workspace (aggregate / manifest
// / release / config) wiring the test/svc Component through the CUSTOM capability
// recipe, with the supplied plugins block. autoPull selects the pull-vs-frozen
// posture. Returns the workspace path (not yet chdir'd into).
func writeCustomProviderWorkspace(t *testing.T, host, capability, pluginsBlock string, autoPull bool) string {
	t.Helper()
	ws := t.TempDir()
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: `+capability+`/svc:1.0.0
`)
	writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	autoPullVal := "false"
	if autoPull {
		autoPullVal = "true"
	}
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: "+autoPullVal+"\noffline: false\n"+pluginsBlock)
	return ws
}

// assertProviderPinnedInLock asserts the lockfile carries BOTH byte-determining
// pin axes for a registry provider socket: the capability→plugin BINDING (in
// lf.Providers) and the provider's .wasm ARTIFACT (in lf.Plugins, digest = the
// published content digest). The provider plugin ENTITY is also present in
// lf.Entities (referenced from the config socket and/or the recipe).
func assertProviderPinnedInLock(t *testing.T, lf *lockfile.Lockfile, capability, pluginCoord, wantWasmDigest string) {
	t.Helper()

	var binding *lockfile.ProviderBinding
	for i := range lf.Providers {
		if lf.Providers[i].Capability == capability {
			binding = &lf.Providers[i]
			break
		}
	}
	if binding == nil {
		t.Fatalf("provider BINDING for capability %q absent from lockfile providers (have %+v)", capability, lf.Providers)
	}
	if binding.Plugin != pluginCoord {
		t.Errorf("provider binding[%q].plugin = %q; want the pulled guest coordinate %q", capability, binding.Plugin, pluginCoord)
	}

	var art *lockfile.Plugin
	for i := range lf.Plugins {
		if lf.Plugins[i].URI == pluginCoord {
			art = &lf.Plugins[i]
			break
		}
	}
	if art == nil {
		t.Fatalf("provider .wasm ARTIFACT %q absent from lockfile plugins (have %v)", pluginCoord, lf.Plugins)
	}
	if art.Digest != wantWasmDigest {
		t.Errorf("provider artifact %q digest = %q; want the published content digest %q", pluginCoord, art.Digest, wantWasmDigest)
	}

	entityURI := pluginCoord + ":1.0.0"
	found := false
	for i := range lf.Entities {
		if lf.Entities[i].URI == entityURI {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("provider plugin ENTITY %q absent from lockfile entities (have %v)", entityURI, lf.Entities)
	}
}

// renderedSnippet returns the path and content of each file under dir, for a
// diagnostic that shows the REAL rendered bytes a provider guest produced.
func renderedSnippet(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, path)
		b.WriteString("  " + rel + ": " + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatalf("read rendered tree %s: %v", dir, err)
	}
	return b.String()
}

// TestIntegration_CustomProviderDimension_RoundTrip proves the 6-point custom
// provider-dimension round-trip against a local fixture registry. Each point
// runs from real invocations (pull + hermetic build) and inspects the REAL
// rendered output.
func TestIntegration_CustomProviderDimension_RoundTrip(t *testing.T) {
	// Each sub-test builds its provider .wasm fixture BEFORE any chdir (paths walk
	// up from cwd), reporting the capability its entity lists.

	// ── Points 1-3: a CUSTOM dimension no embedded provider serves ──────────────
	t.Run("points_1_3_pull_pin_render", func(t *testing.T) {
		const (
			capability  = "stellar"
			pluginCoord = "provider/render-custom"
			pluginURI   = pluginCoord + ":1.0.0"
		)
		providerWasm := ensureRenderProviderWasm(t, capability)
		host := startRegistry(t, nil)
		localProvider := customDimensionCatalog(t, host, capability)
		catDirs := []string{localProvider}

		// Point 1: push the custom provider guest declaring the custom render
		// dimension, bound via plugins.providers (capability + entity uri).
		wasmDigest := publishProviderPluginEntity(t, host, pluginCoord, "1.0.0", providerWasm, []string{capability})
		pluginsBlock := "plugins:\n  providers:\n    - capability: " + capability + "\n      uri: " + pluginURI + "\n"
		ws := writeCustomProviderWorkspace(t, host, capability, pluginsBlock, true)
		t.Chdir(ws)

		// Point 2: pull pins BOTH the plugins ARTIFACT and the providers BINDING.
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
			t.Fatalf("registry pull: %v", err)
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if err != nil {
			t.Fatalf("load lock: %v", err)
		}
		assertProviderPinnedInLock(t, lf, capability, pluginCoord, wasmDigest)

		// Point 3: a build loads the PULLED guest and renders THROUGH it. The
		// provider-distinctive "rendered-node:" content proves the pulled guest
		// rendered, not an embedded provider.
		if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", catDirs, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("build through the pulled custom provider: %v", err)
		}
		found, tree := distTreeContains(t, "dist", renderedNodePrefix)
		if !found {
			t.Fatalf("rendered tree does not carry the custom provider's %q content; the pulled guest did not render:\n%s", renderedNodePrefix, tree)
		}
		t.Logf("point 3 — rendered THROUGH the pulled custom provider guest:\n%s", renderedSnippet(t, "dist"))
		firstTree := hashDistTree(t, "dist")

		// Point 4 (reproducibility from the pin): a second pull + build reproduces
		// the rendered tree byte-for-byte. This proves the pinned .wasm + binding
		// deterministically reproduce output across pull/build cycles.
		_ = os.RemoveAll("dist")
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
			t.Fatalf("second registry pull: %v", err)
		}
		if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", catDirs, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("second build: %v", err)
		}
		if second := hashDistTree(t, "dist"); second != firstTree {
			t.Fatalf("second build not byte-identical: %s != %s", second, firstTree)
		}
	})

	// Point 4 (genuine offline rebuild). The arc-law target: a frozen (auto_pull
	// off, no network) rebuild reproduces the rendered tree byte-for-byte from the
	// lockfile alone. The registry provider socket's entity + .wasm are pinned at
	// pull AND reconstructed into the build's drift-verify closure from the
	// lockfile, so the frozen rebuild renders through the pulled guest offline and
	// its output is byte-identical to the pulled build's.
	t.Run("point4_offline_rebuild_byte_identical", func(t *testing.T) {
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
		// A first build (auto_pull on) establishes the rendered baseline.
		_ = os.RemoveAll("dist")
		if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", catDirs, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("baseline build: %v", err)
		}
		baseline := hashDistTree(t, "dist")

		// Freeze the workspace (no auto-pull heal), then rebuild with no network.
		writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: false\noffline: false\n"+pinned)
		_ = os.RemoveAll("dist")
		if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", catDirs, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("frozen offline rebuild halted; the workspace provider socket axis must reconstruct from the lockfile: %v", err)
		}
		if found, tree := distTreeContains(t, "dist", renderedNodePrefix); !found {
			t.Fatalf("frozen rebuild produced no %q witness; the pinned provider did not render:\n%s", renderedNodePrefix, tree)
		}
		if frozen := hashDistTree(t, "dist"); frozen != baseline {
			t.Fatalf("frozen offline rebuild not byte-identical to the baseline: %s != %s", frozen, baseline)
		}
	})

	// ── Point 5: override an embedded CORE provider (helm) with a pulled guest ──
	t.Run("override_core_provider", func(t *testing.T) {
		std := standardCatalogDir(t)
		const (
			capability  = "helm"
			pluginCoord = "provider/render-helm-override"
			pluginURI   = pluginCoord + ":1.0.0"
		)
		providerWasm := ensureRenderProviderWasm(t, capability)
		host := startRegistry(t, nil)

		// A fixture Component + helm Recipe pushed to the registry. Nothing local
		// declares the helm provider: render-time resolution overlays the binary's
		// embedded core provider entities, so helm resolves from the embed.
		cat := t.TempDir()
		writeFixtureEntity(t, cat, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
		writeFixtureEntity(t, cat, "recipe/helm/svc/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: busybox:1
`)
		pushCatalog(t, host, cat)

		// Publish a custom guest ALSO serving the core helm capability, and bind
		// helm to it. The binding SELECTS the pulled guest over the embedded helm
		// provider — so a helm node renders THROUGH the pulled guest.
		wasmDigest := publishProviderPluginEntity(t, host, pluginCoord, "1.0.0", providerWasm, []string{capability})
		pluginsBlock := "plugins:\n  providers:\n    - capability: " + capability + "\n      uri: " + pluginURI + "\n"
		ws := writeCustomProviderWorkspace(t, host, capability, pluginsBlock, true)
		t.Chdir(ws)
		catDirs := []string{std}

		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
			t.Fatalf("registry pull (override-core): %v", err)
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
		if err != nil {
			t.Fatalf("load lock: %v", err)
		}
		assertProviderPinnedInLock(t, lf, capability, pluginCoord, wasmDigest)

		if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", catDirs, false, os.Stderr, os.Stdout); err != nil {
			t.Fatalf("build with a core-target override binding: %v", err)
		}
		// The pulled guest rendered the helm node — its provider-distinctive content
		// is present, proving the override selected the pulled guest over the
		// embedded helm provider (whose output would be a helm chart, never this).
		found, tree := distTreeContains(t, "dist", renderedNodePrefix)
		if !found {
			t.Fatalf("override-core build: rendered tree does not carry the pulled guest's %q content; the binding did not override the embedded helm provider:\n%s", renderedNodePrefix, tree)
		}
		t.Logf("point 5 — helm target rendered THROUGH the pulled guest (override), not the embedded helm provider:\n%s", renderedSnippet(t, "dist"))
	})

	// ── Point 6: an unknown target with no binding + no serving plugin halts ────
	t.Run("unknown_target_halts_loudly", func(t *testing.T) {
		const capability = "voidtarget"
		host := startRegistry(t, nil)
		// Closure/workload resolution reaches render on the published Component and
		// Recipe alone; NO provider plugin serves the capability and NO binding is
		// declared, so render halts loudly.
		localProvider := customDimensionCatalog(t, host, capability)
		catDirs := []string{localProvider}
		ws := writeCustomProviderWorkspace(t, host, capability, "", true)
		t.Chdir(ws)

		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), ReleasePath: "release.yaml", CatalogDirs: catDirs}); err != nil {
			t.Fatalf("registry pull (unknown target): %v", err)
		}
		err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", catDirs, false, os.Stderr, os.Stdout)
		if err == nil {
			t.Fatal("build SUCCEEDED for a capability no provider plugin serves and no binding selects; expected a loud halt")
		}
		if !errors.Is(err, renderer.ErrUnknownProvider()) && !errors.Is(err, catalog.ErrProviderBindingUnsatisfied()) {
			t.Fatalf("unknown-target halt = %v; want errors.Is renderer.ErrUnknownProvider or catalog.ErrProviderBindingUnsatisfied", err)
		}
	})
}
