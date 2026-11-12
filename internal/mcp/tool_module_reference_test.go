// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// moduleReferenceWorkspace writes a workspace whose Release imports one module
// from the workspace's catalog, with the module's one component type left to the
// row, and no lockfile: the state in which the tools that compile a Release have
// no recorded module axis to reconstruct, and must resolve the module's
// references some other way.
func moduleReferenceWorkspace(t *testing.T, moduleType string) (root, catalogDir string) {
	t.Helper()
	root = t.TempDir()
	catalogDir = filepath.Join(root, "catalog")
	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("create the fixture directory: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write the fixture: %v", err)
		}
	}
	const component = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: demo/web
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
`
	write("catalog/components/web.yaml", component)
	// The file a path-spelled type names exists, so a refusal of that type cannot
	// be the file's absence.
	write("components/web.yaml", component)
	write("catalog/recipes/web.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: demo-web-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/web
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-web:latest
`)
	write("catalog/plugins/helm-renderer.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: helm
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1beta1
  provider:
    capabilities:
      - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)
	write("catalog/aggregates/store.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo/store
  version: "1.0.0"
spec:
  components:
    store:
      type: `+moduleType+`
`)
	write("aggregate.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: module-reference-app
  version: "1.0.0"
spec:
  components:
    web:
      type: demo/web
  aggregates:
    mod:
      type: demo/store
`)
	write("manifest.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: module-reference-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/web: demo-web-synthesized
`)
	write("release.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: module-reference-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        web: {}
      aggregates:
        mod: {}
`)
	return root, catalogDir
}

// TestTheToolsThatCompileOrListAReleaseRefuseAModuleReferenceNothingResolves
// drives every assistant tool that compiles a Release, or lists what one depends
// on, over a Release with no lockfile whose imported module names a component
// type no catalog holds — spelled as a catalog identity, and spelled as a path.
//
// Without a lockfile there is no recorded module axis for a tool to reconstruct,
// so the module's references are resolved by the resolution a validation runs.
// Each tool therefore refuses with the unresolvable-reference condition, naming
// the reference — the author's fault, the answer `aggrega validate --release`
// gives — and never with the disagreement between the compiler's own views of the
// catalog, which is what the checks that compile the Release report when nothing
// resolved the module's references before them. The listing tool refuses too,
// because a Release one of whose modules names nothing is not built from anything.
//
// The first row is the positive control: the same workspace with the module's
// type resolvable draws neither condition from any tool, so a refusal below is the
// row's fault and not the fixture's.
func TestTheToolsThatCompileOrListAReleaseRefuseAModuleReferenceNothingResolves(t *testing.T) {
	t.Parallel()
	unresolvable, _ := cerrors.CodeOf(closure.ErrUnresolvableURI())
	disagreement, _ := cerrors.CodeOf(resolver.ErrGuardEntityViewInconsistency())
	if unresolvable == "" || disagreement == "" {
		t.Fatal("a condition this test matches on registers no descriptive code")
	}

	for _, row := range []struct {
		name       string
		moduleType string
		refused    bool
	}{
		{"control: the module's type is held by the catalog", "demo/web", false},
		{"the module names a catalog identity nothing holds", "demo/absent", true},
		{"the module names its type by a path", "./components/web.yaml", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, catalogDir := moduleReferenceWorkspace(t, row.moduleType)
			s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", launchRoot: root}
			base := func(extra map[string]any) map[string]any {
				args := map[string]any{"workspace_root": root, "catalog_dirs": []string{catalogDir}, "release": "release.yaml"}
				for k, v := range extra {
					args[k] = v
				}
				return args
			}
			for _, call := range []struct {
				tool string
				args map[string]any
			}{
				{"attachment-container", base(map[string]any{"graph_node": "mod.store"})},
				{"attach-candidates", base(map[string]any{"graph_node": "mod.store"})},
				{"connection-at-position", base(map[string]any{
					"file": "aggregate.yaml", "position": map[string]any{"line": 0, "character": 0}})},
				{"build", base(nil)},
				{"describe", base(nil)},
			} {
				res := callDiscoveryTool(t, s, call.tool, call.args)
				text := res.Content[0].Text
				if strings.Contains(text, disagreement) {
					t.Errorf("%s reported the disagreement between the compiler's own views:\n%s", call.tool, text)
				}
				if !row.refused {
					if strings.Contains(text, unresolvable) {
						t.Errorf("%s refused a Release whose every reference resolves:\n%s", call.tool, text)
					}
					continue
				}
				if !res.IsError {
					t.Errorf("%s answered for a Release whose module names %q, which nothing resolves:\n%s",
						call.tool, row.moduleType, text)
					continue
				}
				if !strings.Contains(text, unresolvable) {
					t.Errorf("%s refused without the %s condition:\n%s", call.tool, unresolvable, text)
				}
				if !strings.Contains(text, `"`+row.moduleType+`"`) {
					t.Errorf("%s does not name the reference %q:\n%s", call.tool, row.moduleType, text)
				}
			}
		})
	}
}
