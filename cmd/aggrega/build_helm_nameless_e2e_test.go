// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// namelessDocChartFixture authors a buildable workspace whose `server` Recipe
// sources a LOCAL Helm chart that renders two documents carrying no resource
// identity: a ConfigMap declared with metadata.generateName (no name) and a
// v1.List wrapper (kind but no metadata.name). The build renders through the
// embedded helm provider guest, exercising the real binary render path.
func namelessDocChartFixture(t *testing.T, root string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: server
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: nameless-rel
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
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server
`)
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values: {}
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))

	// A local Helm chart rendering nameless documents. It sits in the Recipe's own
	// directory and is named relative to it, because content an entity reads lives
	// beside the entity; the catalog walk passes over its templates, whose kinds are
	// manifests rather than catalog entities.
	chartDir := filepath.Join(catalogRoot, "recipes", "charts", "nameless")
	writeNamelessDocChart(t, chartDir)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source: ./charts/nameless
`)
	return filepath.Join(root, "release.yaml")
}

// writeNamelessDocChart authors a minimal Helm chart whose templates render a
// generateName resource and a v1.List wrapper — neither carrying a
// metadata.name.
func writeNamelessDocChart(t *testing.T, dir string) {
	t.Helper()
	writeFixture(t, filepath.Join(dir, "Chart.yaml"), "apiVersion: v2\nname: nameless\nversion: 0.1.0\n")
	writeFixture(t, filepath.Join(dir, "templates", "generatename.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  generateName: gen-config-
data:
  key: value
`)
	writeFixture(t, filepath.Join(dir, "templates", "list.yaml"), `apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: item-one
    data:
      key: one
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: item-two
    data:
      key: two
`)
}

// distFileList returns the sorted set of output file paths (relative to dir) a
// build emitted, so the derived nameless-doc filenames are observable.
func distFileList(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk dist %s: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

// TestBuild_HelmNamelessDocsRenderThroughGuest is the end-to-end proof of the
// path-based fallback filename: a build sourcing a Helm chart that renders a
// generateName resource and a v1.List wrapper succeeds through the embedded helm
// guest (no halt), emits each nameless document under a deterministic
// path-derived filename carrying provenance labels, and rebuilds byte-identically.
func TestBuild_HelmNamelessDocsRenderThroughGuest(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := namelessDocChartFixture(t, root)
	catalogDir := filepath.Join(root, "catalog")

	// Build is read-only: prime the lockfile with an explicit pull, then build reads it.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}

	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("nameless-doc build should succeed through the guest: %v", err)
	}

	distMain := filepath.Join(root, "dist")
	firstFiles := distFileList(t, distMain)
	t.Logf("emitted output files: %v", firstFiles)

	var haveGen, haveList bool
	for _, f := range firstFiles {
		if strings.Contains(f, "generatename") {
			haveGen = true
		}
		if strings.Contains(f, "list") {
			haveList = true
		}
	}
	if !haveGen {
		t.Errorf("no generateName output file among: %v", firstFiles)
	}
	if !haveList {
		t.Errorf("no List output file among: %v", firstFiles)
	}

	first := readTree(t, distMain)
	joined := strings.Join(mapValues(first), "\n")
	for _, want := range []string{"generateName: gen-config-", "kind: List", "item-one", "item-two", "aggrega.ai/component-id"} {
		if !strings.Contains(joined, want) {
			t.Errorf("build output missing %q; got:\n%s", want, joined)
		}
	}

	// Deterministic rebuild — byte-identical output.
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("second build: %v", err)
	}
	second := readTree(t, distMain)
	if len(first) != len(second) {
		t.Fatalf("file set differs across builds:\nfirst: %v\nsecond: %v", firstFiles, distFileList(t, distMain))
	}
	for k, v := range first {
		if second[k] != v {
			t.Errorf("file %q not byte-identical across builds:\nfirst:\n%s\nsecond:\n%s", k, v, second[k])
		}
	}
}

// mapValues returns the values of a string map (order-independent; used only for
// substring presence checks).
func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
