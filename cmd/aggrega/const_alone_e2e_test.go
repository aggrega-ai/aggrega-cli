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

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// The test in this file drives a compact-form schema whose property is written
// as JSON Schema's `const` alone — `mode: { const: fast }`, with no `type:` beside
// it — through the real pull and build. It is valid JSON Schema and admits
// exactly its value, as written: the Component loads, a value equal to it builds,
// and any other value halts naming the property and the keyword.

// constAloneWorkspace lays down a workspace whose Component declares one values
// property as declaration (the text after the property's name) and whose
// Aggregate supplies value for it.
func constAloneWorkspace(t *testing.T, root, declaration, value string) (catalogDir, releasePath string) {
	t.Helper()
	// Offline, so every reference is answered by the local catalog alone and the
	// result cannot depend on a registry the machine may or may not reach.
	writeWorkspaceConfig(t, root, "workspace: {}\noffline: true")
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/acme/web/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/web
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      mode: `+declaration+`
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe/compose/web/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: web
  version: 1.0.0
spec:
  capability: compose
  implements: acme/web:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "web:1"
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: const-app
  version: 1.0.0
spec:
  components:
    web:
      type: acme/web:1.0.0
      values:
        mode: `+value+`
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: const-manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        acme/web:1.0.0: compose/web:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: const-release
  environment: local
  version: 1.0.0
spec:
  aggregate: const-app
  manifest: const-manifest
  boundaries:
    main:
      directory: out
      components:
        web: {}
`)
	return catalogDir, releasePath
}

// TestAConstWrittenAloneHoldsItsValue drives one row per value shape `const`
// carries — a string, a number, a mapping — each with the value it holds, which
// must pull and build, and a different value, which must halt as a value failing
// its schema and never as a schema that could not be compiled.
func TestAConstWrittenAloneHoldsItsValue(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	for _, tc := range []struct {
		name, declaration, equal, other string
	}{
		{name: "a string", declaration: "{ const: fast }", equal: "fast", other: "slow"},
		{name: "a number", declaration: "{ const: 3 }", equal: "3", other: "4"},
		{name: "a mapping", declaration: "{ const: { kind: fast, level: 2 } }",
			equal: "{ kind: fast, level: 2 }", other: "{ kind: fast, level: 3 }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Each run compiles a workspace of its own, so no lockfile the refused
			// one wrote is read by the other.
			run := func(value string) error {
				t.Helper()
				root := t.TempDir()
				catalogDir, rel := constAloneWorkspace(t, root, tc.declaration, value)
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

			if err := run(tc.equal); err != nil {
				t.Fatalf("the value the const holds must build:\n%s", formatCompilerError(err))
			}

			err := run(tc.other)
			if err == nil {
				t.Fatal("a value the const does not hold built")
			}
			printed := formatCompilerError(err)
			if errors.Is(err, resolver.ErrSchemaContractCompileFailure()) {
				t.Fatalf("the schema itself was refused, so the const was never applied:\n%s", printed)
			}
			if !errors.Is(err, resolver.ErrSchemaConformance()) {
				t.Fatalf("the halt must be the value failing its schema:\n%s", printed)
			}
			for _, w := range []string{"E0168", "values.mode", "const"} {
				if !strings.Contains(printed, w) {
					t.Errorf("the printed diagnostic does not carry %q:\n%s", w, printed)
				}
			}
		})
	}
}
