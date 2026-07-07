// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestRunBuild_Step4bAssertionFailureHaltsCLI pins the CLI-level
// wire-in for Step 4b (Component/Feature assertion evaluation). The
// test invokes runBuild with a minimal fixture whose Component
// carries an always-firing failing assertion and asserts the build
// halts with ErrAssertionFailed in the surfaced error chain. A
// regression that deletes the EvaluateComponentAndFeatureAssertions
// call from build.go would let runBuild complete past Step 4b into
// Step 5 (Render); this test fails such a regression because the
// expected sentinel and message stop being present in the chain.
func TestRunBuild_Step4bAssertionFailureHaltsCLI(t *testing.T) {
	root := t.TempDir()

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: cli-smoke
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
  name: cli-smoke-release
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
  name: cli-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)

	catalogRoot := filepath.Join(root, "catalog")
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
  assertions:
    smoke_assert: '{{ fail "cli-step4b-smoke-fired" }}'
`)

	writeFixture(t, filepath.Join(catalogRoot, "base", "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))

	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  provider: helm
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

	prevRelease, prevCatalog, prevTrace := releasePath, catalogDirs, traceEnabled
	t.Cleanup(func() {
		releasePath, catalogDirs, traceEnabled = prevRelease, prevCatalog, prevTrace
	})
	releasePath = filepath.Join(root, "release.yaml")
	catalogDirs = []string{catalogRoot}
	traceEnabled = false

	err := dispatchBuild(resolveTempWorkspace(t), releasePath, catalogDirs, traceEnabled, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("build returned nil; expected assertion failure")
	}
	if !errors.Is(err, renderer.ErrAssertionFailed) {
		t.Errorf("expected ErrAssertionFailed in chain; got %v", err)
	}
	if !chainContains(err, "cli-step4b-smoke-fired") {
		t.Errorf("expected fail-call message in chain; got %v", err)
	}
}

// writeHelmRendererPlugin writes the helm-renderer provider Plugin entity into
// providersDir, binding the helm provider to the embedded helm rendering guest.
// Its content-addressed spec.plugin resolves through the plugin-resolution
// cascade's embed tier (the exact bytes compiled into the binary), so the build's
// render step routes the helm provider to the shipped guest with no sidecar,
// cache, or lockfile.
func writeHelmRendererPlugin(t *testing.T, providersDir string) {
	t.Helper()
	writeFixture(t, filepath.Join(providersDir, "helm-renderer.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-renderer
  version: "1.0.0"
spec:
  plugin: oci://hub.aggrega.ai/plugins/helm-provider@sha256:143a1a2a66a2d970258033f3d7158d06e6e6ef6daa3bbea16d1886c93de36594
  pluginType: provider
  abiVersion: v1alpha1
  compatibleProviders:
    - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)
}

// writeFixture writes content to path, creating parent directories
// as needed. Fails the test on any I/O error.
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// chainContains walks err via errors.Unwrap and returns true when any
// link's Error() contains needle. cerrors.Wrap flattens the surface
// presentation, so verifying inner-cause text requires chain traversal.
func chainContains(err error, needle string) bool {
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(cause.Error(), needle) {
			return true
		}
	}
	return false
}
