// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntegration_LintRelease_NarrowingAdvisory proves `aggrega lint --release`
// surfaces the closure narrowing advisory (a child widening an inherited
// constraint) as informational output, and that narrowing NEVER changes the exit
// code (Invariant 1 — the compiler does not police narrow-vs-relax). A synthetic
// local catalog supplies an extends-ing component that widens its parent; the
// standard catalog supplies the compose provider the recipe targets.
func TestIntegration_LintRelease_NarrowingAdvisory(t *testing.T) {
	std := standardCatalogDir(t)
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")

	writeFixture(t, filepath.Join(cd, "parent.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/lint-parent
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      x: { type: integer, minimum: 5 }
`)
	// Child widens the inherited minimum (1 < 5) → a narrowing advisory.
	writeFixture(t, filepath.Join(cd, "child.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/lint-child
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: test/lint-parent
  schema:
    values:
      x: { type: integer, minimum: 1 }
`)
	writeFixture(t, filepath.Join(cd, "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: lint-child
  version: "1.0.0"
spec:
  provider: compose
  implements: test/lint-child
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
`)
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/lint-child
      values:
        x: 3
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
        test/lint-child:1.0.0: compose/lint-child:1.0.0
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
        app:
          infra:
            replicas: 1
`)
	// catalog_dirs: the local extends catalog (first) + the standard catalog for
	// the compose provider the recipe targets.
	writeWorkspaceConfig(t, ws, "workspace:\n  catalog_dirs:\n    - "+cd+"\n    - "+std+"\n")
	t.Chdir(ws)

	var buf bytes.Buffer
	err := lintRelease(nil, "release.yaml", &buf, io.Discard)
	out := buf.String()

	// Invariant 1 / exit invariance: narrowing is advisory — lint --release still
	// passes (the value x=3 conforms to the composed minimum 1).
	if err != nil {
		t.Fatalf("narrowing is advisory and the value conforms; lint --release must pass (exit 0); got %v", err)
	}
	// The narrowing advisory is surfaced, naming the widening child.
	if !strings.Contains(out, "test/lint-child") || !strings.Contains(out, "notice:") {
		t.Errorf("expected a narrowing notice naming the widening child; got diagnostics:\n%s", out)
	}
}
