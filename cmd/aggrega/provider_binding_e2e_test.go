// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// providerBindingFixture lays a workload-synthesis Release down at root with a
// catalog that serves the "helm" render target through the two named provider
// plugins. Each plugin routes to a distinct embedded provider .wasm identity, so
// which one renders a node is observable in the emitted bytes. Returns the
// release path and the catalog root.
//
// altPlugin controls whether a SECOND provider plugin is written (serving the
// same target through the compose-provider .wasm), which is what makes the
// target ambiguous and the binding load-bearing.
func providerBindingFixture(t *testing.T, root string, altPlugin bool) (releasePath, catalogRoot string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: binding-smoke
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: binding-smoke-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        only:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: binding-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)

	catalogRoot = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	// The primary provider plugin (routes to the embedded helm .wasm).
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))
	if altPlugin {
		// A second publisher serving the SAME target through a different provider
		// .wasm identity — makes "helm" ambiguous, so a binding must select one.
		writeFixture(t, filepath.Join(catalogRoot, "base", "providers", "helm-renderer-alt.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-renderer-alt
  version: "1.0.0"
spec:
  plugin: oci://hub.aggrega.ai/plugin/provider/compose
  pluginType: provider
  abiVersion: v1alpha1
  targets:
    - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)
	}
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  target: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`)
	return filepath.Join(root, "release.yaml"), catalogRoot
}

// writeWorkspaceConfig writes an .aggrega-config.yaml at root and resolves the
// workspace from it. The workspace:{} block anchors the root at root so
// resolution never climbs to an ambient ancestor config.
func resolveWorkspaceWithConfig(t *testing.T, root, body string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), body)
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve workspace: %v", err)
	}
	return rw
}

// snapshotDir walks dir and returns a map of relative-path → file content, for
// byte-identity comparison of two build outputs.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return out
}

func snapshotEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// nonInteractive forces the pull binding-helper's TTY gate off for the duration
// of a test, so a run in a real terminal never tries to prompt on stdin.
func nonInteractive(t *testing.T) {
	t.Helper()
	prev := isTerminal
	isTerminal = func() bool { return false }
	t.Cleanup(func() { isTerminal = prev })
}

// TestProviderBinding_BuildIsInvariantToConfigAfterPull is the headline
// determinism proof: after a pull resolves the render engine for a target and
// records it in the lockfile, changing the workspace-config plugins.providers and
// rebuilding WITHOUT a re-pull produces byte-identical output — the build reads
// the lockfile, not the config. A re-pull then adopts the new binding into the
// lockfile.
func TestProviderBinding_BuildIsInvariantToConfigAfterPull(t *testing.T) {
	nonInteractive(t)
	root := t.TempDir()
	releasePath, catalogRoot := providerBindingFixture(t, root, true)
	catDirs := []string{catalogRoot}

	// Pull with the target bound to the helm provider.
	rwHelm := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  providers:
    - target: helm
      uri: oci://hub.aggrega.ai/plugin/provider/helm
`)
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwHelm, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (helm binding): %v", err)
	}

	// First build under the helm binding.
	out1 := filepath.Join(root, "out1")
	if err := dispatchBuildWithDiag(rwHelm, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out1}, io.Discard, io.Discard); err != nil {
		t.Fatalf("build 1: %v", err)
	}
	snap1 := snapshotDir(t, out1)
	if len(snap1) == 0 {
		t.Fatal("build 1 produced no artifacts")
	}

	// Change the config binding to the OTHER provider, then rebuild WITHOUT a
	// re-pull. The lockfile still records the helm binding, so the output must be
	// byte-identical — build never reads the config for engine selection.
	rwCompose := resolveWorkspaceWithConfig(t, root, `workspace: {}
offline: true
plugins:
  providers:
    - target: helm
      uri: oci://hub.aggrega.ai/plugin/provider/compose
`)
	out2 := filepath.Join(root, "out2")
	if err := dispatchBuildWithDiag(rwCompose, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out2}, io.Discard, io.Discard); err != nil {
		t.Fatalf("build 2 (config changed, no re-pull): %v", err)
	}
	snap2 := snapshotDir(t, out2)
	if !snapshotEqual(snap1, snap2) {
		t.Fatalf("build output changed after a plugins.providers config change with no re-pull; build must read the lockfile, not the config")
	}

	// A re-pull adopts the new binding into the lockfile.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rwCompose, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("re-pull (compose binding): %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	got := ""
	for _, b := range lf.Providers {
		if b.Target == "helm" {
			got = b.Plugin
		}
	}
	if got != "oci://hub.aggrega.ai/plugin/provider/compose" {
		t.Fatalf("re-pull did not adopt the new binding; lockfile providers[helm]=%q, want the compose-provider identity", got)
	}
}

// TestProviderBinding_AmbiguousTargetHaltsLoudly proves that when two provider
// plugins serve one target and no binding disambiguates, a non-interactive pull
// halts loudly with ErrAmbiguousProviderPlugin, naming both candidate plugins.
func TestProviderBinding_AmbiguousTargetHaltsLoudly(t *testing.T) {
	nonInteractive(t)
	root := t.TempDir()
	releasePath, catalogRoot := providerBindingFixture(t, root, true)
	rw := resolveWorkspaceWithConfig(t, root, "workspace: {}\noffline: true\n")

	err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}, ws: rw, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}})
	if err == nil {
		t.Fatal("pull over an ambiguous target with no binding returned nil; expected a loud halt")
	}
	if !errors.Is(err, catalog.ErrAmbiguousProviderPlugin) {
		t.Fatalf("ambiguity halt must be ErrAmbiguousProviderPlugin; got: %v", err)
	}
	if !chainContains(err, "helm-renderer") || !chainContains(err, "helm-renderer-alt") {
		t.Fatalf("ambiguity halt must name both candidate plugins; got: %v", err)
	}
}

// TestProviderBinding_UnambiguousTargetNeedsNoBinding proves the zero-config path:
// a target served by exactly one provider plugin resolves and builds with no
// plugins.providers entry, and pull records the resolved binding into the
// lockfile.
func TestProviderBinding_UnambiguousTargetNeedsNoBinding(t *testing.T) {
	nonInteractive(t)
	root := t.TempDir()
	releasePath, catalogRoot := providerBindingFixture(t, root, false)
	catDirs := []string{catalogRoot}
	rw := resolveWorkspaceWithConfig(t, root, "workspace: {}\noffline: true\n")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catDirs, ws: rw, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("pull (unambiguous, no binding): %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	found := false
	for _, b := range lf.Providers {
		if b.Target == "helm" {
			found = true
			if b.Plugin != "oci://hub.aggrega.ai/plugin/provider/helm" {
				t.Errorf("recorded binding plugin=%q, want the helm-provider identity", b.Plugin)
			}
		}
	}
	if !found {
		t.Fatalf("pull did not record a providers entry for the resolved target; got %+v", lf.Providers)
	}

	out := filepath.Join(root, "out")
	if err := dispatchBuildWithDiag(rw, releasePath, catDirs, false, buildDiagOptions{quiet: true}, buildRunOptions{outDir: out}, io.Discard, io.Discard); err != nil {
		t.Fatalf("build (zero-config): %v", err)
	}
	if len(snapshotDir(t, out)) == 0 {
		t.Fatal("zero-config build produced no artifacts")
	}
}
