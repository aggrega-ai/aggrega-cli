// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// The test in this file drives what an author meets whose Aggregate, or whose
// Release, wires a connection to a component inside a module by a name the
// module does not export — through the real pull and build. The module exports
// its database as `primary_db` and keeps its internal id `internal-pg-primary`
// and a second component `internal-cache` to itself. Each refused row halts
// naming the target as written, the module and what the module exports; the
// same workspace wired through the exported alias builds.

// exportWorkspace lays down the workspace: a consumer component the root declares,
// the module it imports, and the Recipes both bind. app is the consumer's block
// beneath its type; releaseApp the Release's entry for it.
func exportWorkspace(t *testing.T, root, app, releaseApp string) (catalogDir, releasePath string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/acme/db/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/db
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
	writeFixture(t, filepath.Join(catalogDir, "component/acme/consumer/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/consumer
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    databases:
      required: false
      selector: "component.aggrega.ai/type=server"
  schema:
    values: {}
`)
	for _, r := range []struct{ name, implements string }{
		{"db", "acme/db:1.0.0"},
		{"consumer", "acme/consumer:1.0.0"},
	} {
		writeFixture(t, filepath.Join(catalogDir, "recipe/compose/"+r.name+"/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: `+r.name+`
  version: 1.0.0
spec:
  capability: compose
  implements: `+r.implements+`
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "`+r.name+`:1"
`)
	}
	writeFixture(t, filepath.Join(catalogDir, "aggregate/finance-db/1.0.0/aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: finance-db
  version: 1.0.0
spec:
  components:
    internal-pg-primary:
      type: acme/db:1.0.0
    internal-cache:
      type: acme/db:1.0.0
  exportedComponents:
    primary_db: internal-pg-primary
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: billing
  version: 1.0.0
spec:
  aggregates:
    finance-db-subsystem:
      type: finance-db:1.0.0
  components:
    billing-api:
      type: acme/consumer:1.0.0
`+app)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: billing-manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        acme/db:1.0.0: compose/db:1.0.0
        acme/consumer:1.0.0: compose/consumer:1.0.0
`)
	entry := "        billing-api: {}\n"
	if releaseApp != "" {
		entry = "        billing-api:\n" + releaseApp
	}
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: billing
  environment: local
  version: 1.0.0
spec:
  aggregate: billing
  manifest: billing-manifest
  boundaries:
    main:
      directory: out
      aggregates:
        finance-db-subsystem: {}
      components:
`+entry)
	return catalogDir, releasePath
}

// TestAConnectionToAModuleComponentItDoesNotExportHaltsTheBuild drives one row
// per authoring surface and per kind of unexported name, each against a control
// that wires the same connection through the module's exported alias.
func TestAConnectionToAModuleComponentItDoesNotExportHaltsTheBuild(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	aggregateWiring := func(target string) (string, string) {
		return "      uses:\n        databases:\n          - " + target + "\n", ""
	}
	releaseWiring := func(target string) (string, string) {
		return "", "          uses:\n            databases:\n              db:\n                target: " + target + "\n"
	}
	for _, tc := range []struct {
		name   string
		wiring func(string) (string, string)
		target string
		want   []string
	}{
		{
			name: "the Aggregate names the module component's internal id", wiring: aggregateWiring,
			target: "finance-db-subsystem.internal-pg-primary",
			want:   []string{"aggregate.yaml", ".spec.components.billing-api.uses.databases"},
		},
		{
			name: "the Aggregate names a module component the module does not export", wiring: aggregateWiring,
			target: "finance-db-subsystem.internal-cache",
			want:   []string{"aggregate.yaml", ".spec.components.billing-api.uses.databases"},
		},
		{
			name: "the Release wires the module component's internal id", wiring: releaseWiring,
			target: "finance-db-subsystem.internal-pg-primary",
			want:   []string{"release.yaml", ".spec.boundaries.main.components.billing-api.uses.databases.db"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Each wiring compiles a workspace of its own, so no lockfile the refused
			// one wrote is read by the control.
			var root string
			run := func(target string) error {
				t.Helper()
				root = t.TempDir()
				app, releaseApp := tc.wiring(target)
				catalogDir, rel := exportWorkspace(t, root, app, releaseApp)
				if err := pullRun(context.Background(), pullConfig{
					ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					ReleasePath: rel,
					CatalogDirs: []string{catalogDir},
					DiagOut:     io.Discard,
					Out:         io.Discard,
					diag:        buildDiagOptions{quiet: true},
				}); err != nil {
					return err
				}
				return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					rel, []string{catalogDir}, false, io.Discard, io.Discard)
			}

			err := run(tc.target)
			if err == nil {
				t.Fatal("a connection to a module component the module does not export built")
			}
			printed := formatCompilerError(err)
			if !errors.Is(err, topology.ErrUsesTargetNotExported()) {
				t.Fatalf("the halt must be the unexported-target condition:\n%s", printed)
			}
			for _, w := range append([]string{
				"E0358",
				`"` + tc.target + `"`,
				`module "finance-db-subsystem"`,
				`which exports "primary_db"`,
			}, tc.want...) {
				if !strings.Contains(printed, w) {
					t.Errorf("the printed diagnostic does not carry %q:\n%s", w, printed)
				}
			}

			if err := run("finance-db-subsystem.primary_db"); err != nil {
				t.Fatalf("the same connection through the exported alias must build:\n%s", formatCompilerError(err))
			}
			compose := string(mustReadFile(t, filepath.Join(root, "dist", "out", "compose", "docker-compose.yaml")))
			if !strings.Contains(compose, "aggrega.ai/component-id: billing-api") {
				t.Errorf("the consumer did not render:\n%s", compose)
			}
		})
	}
}
