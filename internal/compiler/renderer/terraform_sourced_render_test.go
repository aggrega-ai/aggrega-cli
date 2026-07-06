// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// terraformModuleFixture is a minimal local Terraform module: input variables,
// a resource wiring them, and a nested-directory file proving the host projection
// walks recursively and slash-joins relative paths. The bytes are the ground
// truth the guest must vendor verbatim.
func terraformModuleFixture() map[string]string {
	return map[string]string{
		"variables.tf": `variable "db_name" {
  type = string
}

variable "rds_endpoint" {
  type = string
}

variable "allocated_storage" {
  type    = number
  default = 10
}

variable "multi_az" {
  type    = bool
  default = false
}
`,
		"main.tf": `resource "aws_db_instance" "app" {
  identifier        = var.db_name
  allocated_storage = var.allocated_storage
  multi_az          = var.multi_az
}

output "endpoint" {
  value = var.rds_endpoint
}
`,
		"modules/net/net.tf": `resource "aws_security_group" "db" {
  name = "db-sg"
}
`,
	}
}

// writeTerraformModule materializes the fixture under a fresh temp directory and
// returns the module root path (the local source: the recipe points at).
func writeTerraformModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
	return root
}

// terraformLocalInput builds a RendererInput for one terraform node whose recipe
// sources a local module directory verbatim (no lockfile — a local source
// forwards as-authored). valuesPillar is the materialized .values pillar the
// recipe's valuesMapping reads; pass "" for a source-only node with no values.
func terraformLocalInput(nodeID, valuesPillar string) renderer.RendererInput {
	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "resource")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(nodeID, types.IRNode{
		Component:       "core/storage/tf-module",
		Labels:          labels,
		ResolvedVersion: "1.0.0",
		BoundaryPath:    "local/default",
	})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	if valuesPillar != "" {
		valuesMapping.Set(nodeID, json.RawMessage(valuesPillar))
	}
	return renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test-app"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				ValuesMapping: valuesMapping,
				InfraMapping:  types.OrderedMap[json.RawMessage]{},
				Provenance:    types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
}

// terraformLocalRecipeLookup serves a terraform recipe that vendors moduleDir and,
// when valuesMapping is non-empty, overlays the module's input variables from the
// node's .values pillar.
func terraformLocalRecipeLookup(moduleDir, valuesMapping string) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/storage/tf-module" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "tf-module-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Provider:      "terraform",
					Implements:    "core/storage/tf-module",
					Source:        moduleDir,
					ValuesMapping: types.ValuesMapping{Inline: valuesMapping},
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
}

// findBySuffix returns the single result-file whose key ends in suffix. A flat
// terraform node's output lands under a node/boundary prefix, so tests match by
// the module-relative tail rather than the full emitted key.
func findBySuffix(t *testing.T, files map[string][]byte, suffix string) []byte {
	t.Helper()
	var hit []byte
	var matched string
	for k, v := range files {
		if strings.HasSuffix(k, suffix) {
			if matched != "" {
				t.Fatalf("two output files end in %q: %q and %q", suffix, matched, k)
			}
			hit = v
			matched = k
		}
	}
	if matched == "" {
		keys := make([]string, 0, len(files))
		for k := range files {
			keys = append(keys, k)
		}
		t.Fatalf("no output file ends in %q; keys: %v", suffix, keys)
	}
	return hit
}

// TestRenderAll_TerraformSourcedModuleWithOverlay drives a local terraform module
// through the real embedded provider guest and proves the sourced+overlay render
// shape end-to-end: the module files vendor byte-for-byte, the node's values land
// as a deterministic aggrega.auto.tfvars.json overlay with a Terraform ${...}
// pointer passing through untouched, and the whole render is byte-identical across
// runs.
func TestRenderAll_TerraformSourcedModuleWithOverlay(t *testing.T) {
	module := terraformModuleFixture()
	moduleDir := writeTerraformModule(t, module)

	valuesPillar := `{"db_name":"appdb","upstream_endpoint":"${aws_db_instance.main.endpoint}","allocated_storage":20,"multi_az":true}`
	valuesMapping := `allocated_storage: {{ .values.allocated_storage }}
db_name: "{{ .values.db_name }}"
multi_az: {{ .values.multi_az }}
rds_endpoint: "{{ .values.upstream_endpoint }}"
`

	input := terraformLocalInput("db", valuesPillar)
	lookup := terraformLocalRecipeLookup(moduleDir, valuesMapping)
	factory := wasmEngineFactory(t)

	result, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if len(result.Files) == 0 {
		t.Fatal("RenderAll produced no files; expected the vendored module plus the tfvars overlay")
	}

	// (a) module files vendored byte-for-byte, including the nested-directory file.
	for rel, want := range module {
		got := findBySuffix(t, result.Files, "/"+rel)
		if !bytes.Equal(got, []byte(want)) {
			t.Errorf("module file %q not vendored verbatim:\n got: %q\nwant: %q", rel, got, want)
		}
	}

	// (b) the overlay carries the resolved values in the wire's deterministic
	// order, with the ${...} interpolation pointer passing through as a string.
	wantOverlay := `{
  "allocated_storage": 20,
  "db_name": "appdb",
  "multi_az": true,
  "rds_endpoint": "${aws_db_instance.main.endpoint}"
}
`
	gotOverlay := findBySuffix(t, result.Files, "aggrega.auto.tfvars.json")
	if string(gotOverlay) != wantOverlay {
		t.Errorf("tfvars overlay mismatch:\n got:\n%s\nwant:\n%s", gotOverlay, wantOverlay)
	}
	t.Logf("emitted aggrega.auto.tfvars.json:\n%s", gotOverlay)

	// (b2) build provenance rides in a standalone comment-only .tf file (the
	// JSON tfvars overlay cannot carry a comment). The block carries the same
	// aggrega.ai/* identity the host artifacts path stamps, in deterministic
	// alphabetical key order, with the Kubernetes-only keys excluded. This node
	// carries no provider-version and sync-wave 0, so those lines elide.
	wantProvenance := `# Aggrega build provenance
# aggrega.ai/aggregate-name: test-app
# aggrega.ai/boundary-path: local/default
# aggrega.ai/compiler-version: 0.1.0
# aggrega.ai/component-id: db
# aggrega.ai/environment: local
# aggrega.ai/provider: terraform
# aggrega.ai/recipe-uri: tf-module-recipe:1.0.0
`
	gotProvenance := findBySuffix(t, result.Files, "aggrega.provenance.tf")
	if string(gotProvenance) != wantProvenance {
		t.Errorf("aggrega.provenance.tf mismatch:\n got:\n%s\nwant:\n%s", gotProvenance, wantProvenance)
	}
	for _, banned := range []string{"app.kubernetes.io/name", "argocd.argoproj.io"} {
		if strings.Contains(string(gotProvenance), banned) {
			t.Errorf("provenance file contains Kubernetes-only key %q; it must not appear in Terraform output:\n%s", banned, gotProvenance)
		}
	}
	t.Logf("emitted aggrega.provenance.tf:\n%s", gotProvenance)

	// (c) render twice → byte-identical (Deterministic Output invariant).
	result2, err := renderer.RenderAll(context.Background(), terraformLocalInput("db", valuesPillar),
		terraformLocalRecipeLookup(moduleDir, valuesMapping), wasmEngineFactory(t))
	if err != nil {
		t.Fatalf("RenderAll (second run): %v", err)
	}
	if len(result2.Files) != len(result.Files) {
		t.Fatalf("second run emitted %d files, first emitted %d", len(result2.Files), len(result.Files))
	}
	for k, v := range result.Files {
		if !bytes.Equal(result2.Files[k], v) {
			t.Errorf("non-deterministic output for %q across two runs", k)
		}
	}
}

// TestRenderAll_TerraformSourcedModuleNoValues proves the source-only shape: a
// terraform recipe with a source but no valuesMapping (and no values pillar)
// vendors the module files alone and emits no tfvars overlay — the module renders
// with its own defaults, and no input file is invented.
func TestRenderAll_TerraformSourcedModuleNoValues(t *testing.T) {
	module := terraformModuleFixture()
	moduleDir := writeTerraformModule(t, module)

	input := terraformLocalInput("db", "")
	lookup := terraformLocalRecipeLookup(moduleDir, "")
	factory := wasmEngineFactory(t)

	result, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	for rel, want := range module {
		got := findBySuffix(t, result.Files, "/"+rel)
		if !bytes.Equal(got, []byte(want)) {
			t.Errorf("module file %q not vendored verbatim in the source-only case", rel)
		}
	}
	for k := range result.Files {
		if strings.HasSuffix(k, "aggrega.auto.tfvars.json") {
			t.Errorf("source-only render emitted a tfvars overlay %q; none expected when the node carries no values", k)
		}
	}
	// Build provenance is emitted regardless of whether the node carries values —
	// it identifies the render, not the inputs.
	gotProvenance := findBySuffix(t, result.Files, "aggrega.provenance.tf")
	if !strings.HasPrefix(string(gotProvenance), "# Aggrega build provenance\n") {
		t.Errorf("source-only render should still emit an aggrega.provenance.tf provenance block; got:\n%s", gotProvenance)
	}
}
