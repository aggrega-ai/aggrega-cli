// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// catalogLocalPluginWorkspace writes a workspace whose Recipe runs a pipeline step
// named by a Plugin entity authored in the workspace's own catalog directory, and
// returns the Release path and the directory the entity sits in. When guest is
// non-nil it is laid down beside the entity under the filename the plugin cascade
// reads it by. The workspace is offline, so nothing here can reach a registry.
func catalogLocalPluginWorkspace(t *testing.T, guest []byte) (releasePath, pluginDir string) {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")

	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\n")
	writeFixture(t, filepath.Join(catalogDir, "component", "test", "svc", "1.0.0", "component.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      replicas: { type: integer, default: 1 }
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe", "helm", "svc", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
  pipeline:
    - id: patch
      plugin: pipeline-step/patch:1.0.0
      function: step
`)
	pluginDir = filepath.Join(catalogDir, "plugin", "pipeline-step", "patch", "1.0.0")
	writeFixture(t, filepath.Join(pluginDir, "plugin.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: patch
  pluginType: pipeline-step
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`)
	if guest != nil {
		writeFixtureBytes(t, filepath.Join(pluginDir, pluginpath.PluginLeaf("pipeline-step/patch")), guest)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: helm/svc:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1alpha1
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
        app:
          infra:
            replicas: 1
`)
	return releasePath, pluginDir
}

// TestPullRefusesACatalogPluginEntityWhoseArtifactNoSourceHolds drives the real
// pull over a Plugin entity authored in a catalog directory.
//
// With the entity's .wasm absent from every source the build reads, the pull
// refuses before it writes anything, naming the entity, the file it expected and
// the directory it expected it in, and the two ways out. Such an entity is never
// pinned and its artifact is never fetched, so letting the pull complete would only
// move the failure to a build that can no longer name the fix.
//
// The control is the same workspace with the .wasm laid beside the entity: the pull
// completes and writes the lockfile, and — because the entity is local — records no
// plugin row for it. Without the control, a pull that refused every catalog Plugin
// entity would pass the first half.
func TestPullRefusesACatalogPluginEntityWhoseArtifactNoSourceHolds(t *testing.T) {
	t.Run("no source holds the artifact", func(t *testing.T) {
		releasePath, pluginDir := catalogLocalPluginWorkspace(t, nil)
		ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: filepath.Dir(releasePath)})

		err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath})
		if err == nil {
			t.Fatal("the pull completed over a catalog Plugin entity whose artifact no source holds; it must refuse")
		}
		msg := err.Error()
		for _, want := range []string{
			"pipeline-step/patch:1.0.0",
			pluginpath.PluginLeaf("pipeline-step/patch"),
			pluginDir,
			"Place the built .wasm beside the entity",
			"publish the plugin",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal does not name %q:\n%s", want, msg)
			}
		}
		if _, statErr := os.Stat(lockfile.PathFor(releasePath)); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("the refused pull wrote a lockfile (stat: %v); a refusal writes nothing", statErr)
		}
	})

	t.Run("the artifact sits beside the entity", func(t *testing.T) {
		guest, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm"))
		if err != nil {
			t.Fatalf("read the guest fixture: %v", err)
		}
		releasePath, _ := catalogLocalPluginWorkspace(t, guest)
		ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: filepath.Dir(releasePath)})

		if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath}); err != nil {
			t.Fatalf("the pull refused a catalog Plugin entity whose artifact sits beside it: %v", err)
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
		if err != nil {
			t.Fatalf("load the written lockfile: %v", err)
		}
		for _, p := range lf.Plugins {
			if strings.Contains(p.URI, "pipeline-step/patch") {
				t.Errorf("the lockfile pins the catalog-local plugin's artifact %q; a local entity is never pinned", p.URI)
			}
		}
		for _, e := range lf.Entities {
			if strings.Contains(e.URI, "pipeline-step/patch") {
				t.Errorf("the lockfile pins the catalog-local plugin entity %q; a local entity is never pinned", e.URI)
			}
		}
	})
}
