// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// probeSuccessThresholdWorkspace lays down a single-node workspace whose compose
// Recipe's Workload declares a live probe, carrying the probe line extra beneath
// its other settings (empty for none).
func probeSuccessThresholdWorkspace(t *testing.T, root, extra string) (releaseFile, catalogRoot string) {
	t.Helper()
	// Offline, so every reference is answered by the local catalog alone and the
	// result cannot depend on a registry the machine may or may not reach.
	writeWorkspaceConfig(t, root, "workspace: {}\noffline: true")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: probe-app
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: probe-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        only: {}
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: probe-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-compose-recipe:1.0.0
`)
	catalogRoot = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values: {}
`)
	seedEmbeddedPlugins(t, filepath.Join(catalogRoot, "providers"), "provider/compose")
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "compose", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-compose-recipe
  version: "1.0.0"
spec:
  capability: compose
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      server:
        image: test-server:latest
        ports:
          web: 8080
        health:
          live:
            http: /healthz
            periodSeconds: 10
            failureThreshold: 4
`+extra)
	return filepath.Join(root, "release.yaml"), catalogRoot
}

// TestBuildCmd_ProbeSuccessThresholdDrawsTheUnsupportedFieldWarning drives two
// real builds through the command tree, one of a live probe carrying
// successThreshold and one of the same probe without it, and reads the warning
// surface a user reads: the field is reported with the unsupported-field warning,
// named by its path in the Workload, only where it is authored, and both builds
// render the same compose file, since a compose healthcheck has no success
// threshold to carry it into.
func TestBuildCmd_ProbeSuccessThresholdDrawsTheUnsupportedFieldWarning(t *testing.T) {
	// Not parallel: the warning surface under assertion is the process stderr, and
	// the test moves the process working directory.
	marker := unsupportedFieldMarker(t, "compose", "containers.server.health.live.successThreshold")

	build := func(extra string) (string, map[string]string) {
		t.Helper()
		work := t.TempDir()
		t.Chdir(work)
		releaseFile, catalogRoot := probeSuccessThresholdWorkspace(t, work, extra)
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: work}), ReleasePath: releaseFile, CatalogDirs: []string{catalogRoot}}); err != nil {
			t.Fatalf("prime lockfile via pull: %v", err)
		}
		var out string
		var err error
		shown := captureStderr(t, func() {
			out, err = runAggrega(t, "build", "--release", releaseFile, "--catalog", catalogRoot)
		})
		if err != nil {
			t.Fatalf("build failed: %v\ncobra output:\n%s\nstderr:\n%s", err, out, shown)
		}
		return shown, renderedTree(t, filepath.Join(work, "dist"))
	}

	authoredShown, authoredRender := build("            successThreshold: 1\n")
	if !strings.Contains(authoredShown, marker) {
		t.Errorf("an authored successThreshold drew no unsupported-field warning.\nwant to contain: %s\ngot:\n%s", marker, authoredShown)
	}
	if n := strings.Count(authoredShown, "successThreshold"); n != 1 {
		t.Errorf("successThreshold is named %d times on the warning surface, want once:\n%s", n, authoredShown)
	}

	plainShown, plainRender := build("")
	if strings.Contains(plainShown, "successThreshold") {
		t.Errorf("a probe without successThreshold drew a warning about it:\n%s", plainShown)
	}

	if len(authoredRender) != len(plainRender) {
		t.Fatalf("the two builds rendered %d and %d file(s); successThreshold must not change what is emitted",
			len(authoredRender), len(plainRender))
	}
	for rel, want := range plainRender {
		if got := authoredRender[rel]; got != want {
			t.Errorf("%s differs between the two builds; successThreshold must not change the rendered output", rel)
		}
	}
	healthchecked := false
	for _, body := range plainRender {
		if strings.Contains(body, "healthcheck:") {
			healthchecked = true
		}
	}
	if !healthchecked {
		t.Fatal("neither build rendered a healthcheck, so the probe never reached the compose translation")
	}
}
