// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// writeIsolationWorkspace lays down a minimal valid serve workspace under tmp: an
// Aggregate using core/server, its Release + Manifest, and a catalog with the
// core/server Component, its helm Recipe, and the helm Provider. The closure of
// the Release is {core/server, server-recipe, helm}. Returns the catalog dir.
func writeIsolationWorkspace(t *testing.T, tmp string) string {
	t.Helper()
	writeFixtureFile(t, filepath.Join(tmp, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: iso
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
      values:
        port: 8080
`)
	writeFixtureFile(t, filepath.Join(tmp, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: iso-release
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
	writeFixtureFile(t, filepath.Join(tmp, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: iso-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	catalogDir := filepath.Join(tmp, "catalog")
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
        image: test:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`)
	return catalogDir
}

// isolationServer builds a server over the isolation workspace with the active
// Release resolved and the lazy catalog wired to the catalog dir.
func isolationServer(t *testing.T, tmp, catalogDir string) *Server {
	t.Helper()
	releasePath := filepath.Join(tmp, "release.yaml")
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	srv := newTestServer(t, tmp, nil, nil)
	srv.activeRelease = rel
	srv.activeReleasePath = releasePath
	srv.wsConfig.Workspace.CatalogDirs = []string{catalogDir}
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{catalogDir}, nil)
	return srv
}

// closureDiagnostics counts the Tier-D closure-level diagnostics in a validate
// response (those carrying the "Dependencies: " prefix — closure resolution / pillar /
// extends failures).
func closureDiagnostics(t *testing.T, srv *Server) []Diagnostic {
	t.Helper()
	aggPath := filepath.Join(srv.workspaceRoot, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{File: aggPath, Content: string(aggBytes)}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("validate returned envelope error: %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal validate result: %v", err)
	}
	var closureDiags []Diagnostic
	for _, d := range result.Diagnostics {
		if strings.HasPrefix(d.Message, "Dependencies: ") {
			closureDiags = append(closureDiags, d)
		}
	}
	return closureDiags
}

// TestServeValidate_BrokenOutOfClosureIsolation is the serve-side blast-radius
// headline: a broken recipe, library, and component OUTSIDE the active Release's
// closure do not break aggrega/validate — the closure resolves and no closure-level
// diagnostic fires — mirroring the CLI build's isolation.
func TestServeValidate_BrokenOutOfClosureIsolation(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	catalogDir := writeIsolationWorkspace(t, tmp)

	// Baseline: clean workspace validates with no closure-level diagnostic.
	srv := isolationServer(t, tmp, catalogDir)
	if diags := closureDiagnostics(t, srv); len(diags) != 0 {
		t.Fatalf("clean workspace should produce no closure-level diagnostic; got %+v", diags)
	}

	// Add broken entities OUTSIDE the closure.
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "broken.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: broken-recipe
  version: "1.0.0"
spec:
  target: helm
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "libs", "broken-lib.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BrokenLib
  version: "1.0.0"
spec:
  schemas:
    Broken:
      $dynamicAnchor: forbidden
      type: string
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "broken.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/broken
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
`)

	// Fresh server (re-walks the now-dirty catalog) — validate must STILL succeed
	// with no closure-level diagnostic: the broken entities are out of closure.
	srv = isolationServer(t, tmp, catalogDir)
	if diags := closureDiagnostics(t, srv); len(diags) != 0 {
		t.Fatalf("broken out-of-closure entities must not produce a closure diagnostic (serve isolation); got %+v", diags)
	}
}

// TestServeValidate_InClosureBreakSurfacesDiagnostic proves closure-scoping does
// not weaken in-closure validation: an in-closure Component whose schema $ref is
// unresolvable surfaces a closure-level diagnostic (the daemon's early-warning
// Pass-2), exactly where the whole-catalog Pass-2 used to fire.
func TestServeValidate_InClosureBreakSurfacesDiagnostic(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	catalogDir := writeIsolationWorkspace(t, tmp)

	// Corrupt the IN-closure core/server component with a dangling $ref.
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
`)
	srv := isolationServer(t, tmp, catalogDir)
	if diags := closureDiagnostics(t, srv); len(diags) == 0 {
		t.Fatal("an in-closure unresolvable $ref must surface a closure-level diagnostic (Pass-2 must still reject)")
	}
}
