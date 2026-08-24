// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
// when valuesMapping is non-empty, resolves the module's input variables from the
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
					Capability:    "terraform",
					Implements:    "core/storage/tf-module",
					Source:        moduleDir,
					ValuesMapping: types.ValuesMapping{Inline: valuesMapping},
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
}

// terraformArtifactsRecipeLookup serves a terraform recipe that authors its
// configuration body itself and names no source. The body is a Go template, so
// the node's values are resolved into the emitted text as it renders — which is
// what makes this the render path with no inputs left to supply from outside.
func terraformArtifactsRecipeLookup(body string) renderer.RecipeLookup {
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("main.tf", types.ArtifactEntry{Inline: body})
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/storage/tf-module" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "tf-artifacts-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability: "terraform",
					Implements: "core/storage/tf-module",
					Artifacts:  artifacts,
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
}

// findBySuffix returns the single result-file whose key ends in suffix. A flat
// terraform node's output lands under a node/boundary prefix, so tests match by
// the module-relative tail rather than the full emitted key.
func findBySuffix(t *testing.T, files map[string]renderer.AssembledFile, suffix string) []byte {
	t.Helper()
	var hit []byte
	var matched string
	for k, v := range files {
		if strings.HasSuffix(k, suffix) {
			if matched != "" {
				t.Fatalf("two output files end in %q: %q and %q", suffix, matched, k)
			}
			hit = v.Content
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

// TestRenderAll_TerraformSourcedModuleWithValues drives a local terraform module
// through the real embedded provider guest and proves the sourced render shape end
// to end: the module files vendor byte-for-byte, the node's values land as the
// arguments of the boundary root's module block with a Terraform ${...} pointer
// passing through untouched, no variables file is written beside the module's own
// files, and the whole render is byte-identical across runs.
func TestRenderAll_TerraformSourcedModuleWithValues(t *testing.T) {
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
		t.Fatal("RenderAll produced no files; expected the vendored module plus the boundary root configuration")
	}

	// (a) module files vendored byte-for-byte, including the nested-directory file.
	for rel, want := range module {
		got := findBySuffix(t, result.Files, "/"+rel)
		if !bytes.Equal(got, []byte(want)) {
			t.Errorf("module file %q not vendored verbatim:\n got: %q\nwant: %q", rel, got, want)
		}
	}

	// (b) the boundary root's module block carries the resolved values as its
	// arguments, in the wire's deterministic order, with the ${...} interpolation
	// pointer passing through verbatim. The position is the property: an argument
	// is an expression the engine evaluates, so a pointer written here reaches
	// somewhere the engine can resolve it.
	wantBlock := `module "db" {
  source = "./modules/db"

  allocated_storage = 20
  db_name           = "appdb"
  multi_az          = true
  rds_endpoint      = "${aws_db_instance.main.endpoint}"
}
`
	gotRoot := findBySuffix(t, result.Files, "aggrega.modules.tf")
	if !strings.Contains(string(gotRoot), wantBlock) {
		t.Errorf("the root configuration's module block is not the expected bytes:\n got:\n%s\nwant:\n%s", gotRoot, wantBlock)
	}
	t.Logf("emitted aggrega.modules.tf:\n%s", gotRoot)

	// (b1) and nothing writes the values a second time beside the module's own
	// files. Two carriers for one value would give two answers for a pointer,
	// because a file loaded from the module's own directory is read as data
	// rather than evaluated as an expression.
	for k := range result.Files {
		if strings.HasSuffix(k, ".tfvars.json") || strings.HasSuffix(k, ".tfvars") {
			t.Errorf("a variables file %q was emitted; the node's values belong to the root configuration's module block and nowhere else", k)
		}
	}

	// (b2) build provenance rides in a standalone comment-only .tf file, because
	// the vendored module's own files stay byte-for-byte verbatim. The block
	// carries the same
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
		if !bytes.Equal(result2.Files[k].Content, v.Content) {
			t.Errorf("non-deterministic output for %q across two runs", k)
		}
	}
}

// TestRenderAll_TerraformMalformedModuleHalts proves the provider input-validation
// guardrail: a terraform node whose sourced module carries structurally invalid
// HCL (or invalid .tf.json) halts the build loudly at the input-kind gate — before
// any emission — with the ErrProviderNodeInputInvalid sentinel, and the halt names
// the offending node and the provider. The check runs in-guest through the
// embedded terraform provider's validate_node_input export (an in-guest HCL parse,
// no Terraform toolchain), so this is the real end-to-end guardrail, not a stub.
func TestRenderAll_TerraformMalformedModuleHalts(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{
			// A resource block with no closing brace — a native-HCL syntax error.
			name: "malformed_hcl",
			files: map[string]string{
				"main.tf": `resource "aws_db_instance" "app" {
  identifier = var.db_name
`,
			},
		},
		{
			// A .tf.json body that is not well-formed JSON — the JSON-HCL variant
			// parse fails structurally.
			name: "malformed_tf_json",
			files: map[string]string{
				"main.tf.json": `{ "resource": { "aws_db_instance": `,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			moduleDir := writeTerraformModule(t, tc.files)
			input := terraformLocalInput("db", "")
			lookup := terraformLocalRecipeLookup(moduleDir, "")

			_, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
			if err == nil {
				t.Fatal("expected RenderAll to halt on the malformed terraform module input; got nil")
			}
			if !errors.Is(err, renderer.ErrProviderNodeInputInvalid()) {
				t.Fatalf("halt is not the ErrProviderNodeInputInvalid guardrail: %v", err)
			}
			msg := err.Error()
			for _, want := range []string{`"db"`, "terraform"} {
				if !strings.Contains(msg, want) {
					t.Errorf("halt message must name %q; got: %s", want, msg)
				}
			}
			t.Logf("halt: %s", msg)
		})
	}
}

// TestRenderAll_TerraformSourcedModuleNoValues proves the source-only shape: a
// terraform recipe with a source but no valuesMapping (and no values pillar)
// vendors the module files alone, and its module block in the boundary root
// carries a source and no arguments — the module renders with its own declared
// defaults, and no input is invented.
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
		if strings.HasSuffix(k, ".tfvars.json") || strings.HasSuffix(k, ".tfvars") {
			t.Errorf("source-only render emitted a variables file %q; none expected when the node carries no values", k)
		}
	}
	wantBareBlock := "module \"db\" {\n  source = \"./modules/db\"\n}\n"
	gotRoot := findBySuffix(t, result.Files, "aggrega.modules.tf")
	if !strings.Contains(string(gotRoot), wantBareBlock) {
		t.Errorf("a valueless component's module block must carry only its source:\n got:\n%s\nwant:\n%s", gotRoot, wantBareBlock)
	}
	// Build provenance is emitted regardless of whether the node carries values —
	// it identifies the render, not the inputs.
	gotProvenance := findBySuffix(t, result.Files, "aggrega.provenance.tf")
	if !strings.HasPrefix(string(gotProvenance), "# Aggrega build provenance\n") {
		t.Errorf("source-only render should still emit an aggrega.provenance.tf provenance block; got:\n%s", gotProvenance)
	}
}

// TestRenderAll_TerraformAuthoredBodyCarriesNoModuleArguments proves the other
// terraform render path. A recipe that authors the configuration body itself
// resolves the node's values into that body as it renders, so the boundary root's
// module block for that component carries a source and nothing else.
//
// The empty argument list is a correctness property of the emitted tree rather
// than a cosmetic one: the emitted configuration declares no input variables, and
// an engine refuses a module block that passes an argument the configuration it
// points at does not declare. The node here carries the same values the sourced
// case above does, and they are asserted present in the emitted body, so the
// empty argument list cannot pass merely because nothing was resolved.
func TestRenderAll_TerraformAuthoredBodyCarriesNoModuleArguments(t *testing.T) {
	valuesPillar := `{"db_name":"appdb","allocated_storage":20,"multi_az":true}`
	body := `resource "aws_db_instance" "app" {
  identifier        = "{{ .values.db_name }}"
  allocated_storage = {{ .values.allocated_storage }}
  multi_az          = {{ .values.multi_az }}
}
`

	result, err := renderer.RenderAll(context.Background(),
		terraformLocalInput("db", valuesPillar),
		terraformArtifactsRecipeLookup(body), wasmEngineFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// (a) the values reached the authored body — the needle that makes (b) mean
	// something. Each one is resolved into the emitted text, so nothing is left
	// for an outer position to supply.
	gotBody := findBySuffix(t, result.Files, "/main.tf")
	for _, want := range []string{
		`identifier        = "appdb"`,
		`allocated_storage = 20`,
		`multi_az          = true`,
	} {
		if !strings.Contains(string(gotBody), want) {
			t.Errorf("the authored body must carry the resolved value %q; got:\n%s", want, gotBody)
		}
	}

	// (b) and the emitted configuration declares no input variables, so the module
	// block must pass none.
	if strings.Contains(string(gotBody), "variable \"") {
		t.Errorf("this fixture's authored body declares an input variable; the case it pins is a configuration that declares none:\n%s", gotBody)
	}
	wantBareBlock := "module \"db\" {\n  source = \"./modules/db\"\n}\n"
	gotRoot := findBySuffix(t, result.Files, "aggrega.modules.tf")
	if !strings.Contains(string(gotRoot), wantBareBlock) {
		t.Errorf("a component whose configuration is authored rather than sourced must get a module block carrying only its source:\n got:\n%s\nwant:\n%s", gotRoot, wantBareBlock)
	}
	for _, key := range []string{"db_name", "allocated_storage", "multi_az"} {
		if strings.Contains(string(gotRoot), key) {
			t.Errorf("the root configuration names %q; an argument by that name is an input the emitted configuration never declares:\n%s", key, gotRoot)
		}
	}
	t.Logf("emitted aggrega.modules.tf:\n%s", gotRoot)
}
