// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// lintOnceWorkspace lays down a workspace whose one Component carries a Feature
// declaring the property `origin` in both its values and its infra pillars — the
// overlap the pillar-overlap lint reports — and whose Aggregate and Manifest reach
// that Feature through two references: the Aggregate by its bare name, the
// Manifest's mapping by name and version. Both resolve to the one Feature file.
// The catalog sits at the workspace's conventional `catalog` directory, with the
// helm provider beside it, so the workspace builds with no flag.
func lintOnceWorkspace(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: lint-once
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
      features:
        secret:
          type: test/overlap
          values:
            origin: inline-value
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: lint-once-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: helm/svc:1.0.0
    features:
      type:
        test/overlap:1.0.0: helm/overlap:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: lint-once-release
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
	catalog := filepath.Join(root, workspaceconfig.ConventionalCatalogDir)
	writeFixture(t, filepath.Join(catalog, "component", "test", "svc", "1.0.0", "component.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      greeting: { type: string, default: "hello" }
    infra:
      replicas: { type: integer, default: 1 }
`)
	writeFixture(t, filepath.Join(catalog, "feature", "test", "overlap", "1.0.0", "feature.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: test/overlap
  version: "1.0.0"
spec:
  schema:
    values:
      origin: { type: string }
    infra:
      origin: { type: string }
`)
	writeFixture(t, filepath.Join(catalog, "recipe", "helm", "svc", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        variables:
          GREETING: "{{ .values.greeting }}"
`)
	writeFixture(t, filepath.Join(catalog, "feature-recipe", "helm", "overlap", "1.0.0", "feature-recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: overlap
  version: "1.0.0"
spec:
  capability: helm
  implements: test/overlap
  artifacts:
    overlap.yaml: |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: overlap
      data:
        origin: "{{ .values.origin }}"
`)
	writeHelmRendererPlugin(t, filepath.Join(catalog, "providers"))
}

// TestBuildPrintsEachLintNoticeOncePerEntity drives `aggrega pull` and then
// `aggrega build` through the real command tree, and counts the pillar-overlap
// notice the build prints for the one Feature that carries the overlap.
//
// The Feature is reached through two references — the bare name the Aggregate
// writes and the versioned one the Manifest maps — which is the shape a workspace
// takes as soon as its Manifest names versions. Both are the same Feature, so the
// notice is printed once, naming the Feature by its name and version. The
// assertion requires the notice to be printed at all, so a build that had stopped
// linting would fail here rather than pass as "printed no more than once".
func TestBuildPrintsEachLintNoticeOncePerEntity(t *testing.T) {
	// Not parallel: the notices are read from the process stderr, and the test
	// moves the process working directory.
	root := t.TempDir()
	lintOnceWorkspace(t, root)
	t.Chdir(root)

	pullStderr := captureStderr(t, func() {
		if out, err := runAggrega(t, "--workspace-root", root, "pull", "--release", "release.yaml"); err != nil {
			t.Fatalf("pull: %v\n%s", err, out)
		}
	})

	var out string
	var err error
	stderr := captureStderr(t, func() {
		out, err = runAggrega(t, "--workspace-root", root, "build", "--release", "release.yaml")
	})
	if err != nil {
		t.Fatalf("build: %v\noutput:\n%s\nstderr:\n%s\npull stderr:\n%s", err, out, stderr, pullStderr)
	}
	// The build must have rendered for what it printed to mean anything; renderedTree
	// fails on an output tree holding no file.
	renderedTree(t, filepath.Join(root, "dist"))

	var notices []string
	for _, line := range strings.Split(out+"\n"+stderr, "\n") {
		if strings.Contains(line, `Feature "test/overlap"`) && strings.Contains(line, "in both values: and infra: pillars") {
			notices = append(notices, line)
		}
	}
	if len(notices) != 1 {
		t.Fatalf("the overlap notice for test/overlap was printed %d time(s); want once\n%s\noutput:\n%s\nstderr:\n%s",
			len(notices), strings.Join(notices, "\n"), out, stderr)
	}
	if !strings.Contains(notices[0], " at test/overlap:1.0.0 ") {
		t.Errorf("the notice does not name the Feature by its name and version: %s", notices[0])
	}
}
