// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The resource-schema importers read exports at the scale real providers reach.
// The cases here run `aggrega import` itself, through the command's own plugin
// resolution, over a Terraform providers schema export of about eighteen
// megabytes: the binary carries the importer's entity and accepts its module by
// digest, the JSON parser is the binary's own, and each runs under the limits its
// entity records.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest/exportgen"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// minimumExportBytes is the smallest export the case imports.
const minimumExportBytes = 17 << 20

// runtimeOf reads the limits a Plugin entity records.
func runtimeOf(t *testing.T, what string, entity []byte) types.PluginRuntime {
	t.Helper()
	var p types.Plugin
	if err := yaml.Unmarshal(entity, &p); err != nil {
		t.Fatalf("read the entity of %s: %v", what, err)
	}
	if p.Spec.Runtime == nil {
		t.Fatalf("the entity of %s records no runtime limits", what)
	}
	return *p.Spec.Runtime
}

// TestTheJSONParserTheImportResolvesRunsUnderItsEntitysLimits holds the command's
// own resolution of the parser an import reads a source through — the one a
// workspace with an empty catalog folder gets from the binary — to the limits
// the binary's entity for parser/json records.
func TestTheJSONParserTheImportResolvesRunsUnderItsEntitysLimits(t *testing.T) {
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	isolateFromThisMachine(t)

	entity, ok := embedded.LookupEntity("parser/json")
	if !ok {
		t.Fatal("this binary embeds no entity for parser/json")
	}
	want := runtimeOf(t, "parser/json", entity)
	if want.MaxMemoryPages != 16384 || want.TimeoutMs != 300000 {
		t.Fatalf("parser/json records %d pages and %d ms; the import is held to 16384 pages and 300000 ms",
			want.MaxMemoryPages, want.TimeoutMs)
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, workspaceconfig.ConventionalCatalogDir), 0o755); err != nil {
		t.Fatalf("create the catalog folder: %v", err)
	}
	importopstest.WriteOfflineConfiguration(t, root)
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	resolved, err := importPluginBytes(rw, "parser/json", nil)
	if err != nil {
		t.Fatalf("resolve parser/json: %v", err)
	}
	if resolved.Limits == nil || resolved.Limits.MaxMemoryPages != want.MaxMemoryPages ||
		resolved.Limits.TimeoutMs != want.TimeoutMs {
		t.Errorf("the import resolves parser/json under %+v; its entity records %d pages and %d ms",
			resolved.Limits, want.MaxMemoryPages, want.TimeoutMs)
	}
}

// TestTerraformSchemaImportLargeExportWithinLimits imports an export of 1,200
// resource types with `aggrega import`, once naming the format and once naming
// none, so the export is read by the host's recognition and again by the
// importer, with the resource type to draft in the importer's attachment. Both
// draft with no halt, under the limits the carried entities record: the
// importer's 8192 pages and 150 seconds, and the JSON parser's 16384 pages and
// 300 seconds.
//
// The carried set is process-wide, so the case is not parallel.
func TestTerraformSchemaImportLargeExportWithinLimits(t *testing.T) {
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	isolateFromThisMachine(t)
	const uri = "importer/terraform-schema"

	var carriedEntity []byte
	for _, p := range embedded.PublishedPlugins() {
		if p.URI == uri {
			carriedEntity = p.Entity
		}
	}
	if len(carriedEntity) == 0 {
		t.Fatalf("this binary carries no entity for %s: build the core plugins with the repository's build script", uri)
	}
	if got := runtimeOf(t, uri, carriedEntity); got.MaxMemoryPages != 8192 || got.TimeoutMs != 150000 {
		t.Fatalf("%s carries %d pages and %d ms; the case holds it to 8192 pages and 150000 ms",
			uri, got.MaxMemoryPages, got.TimeoutMs)
	}

	module, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "terraform-schema-importer.wasm"))
	if err != nil {
		t.Fatalf("read the importer's module: %v", err)
	}
	t.Cleanup(embedded.SeedForTest(uri, module))
	t.Cleanup(embedded.SeedPublishedForTest([]embedded.PublishedPlugin{
		{URI: uri, Entity: carriedEntity, Digest: digest.ContentDigest(module)}}))
	var entity types.Plugin
	if err := yaml.Unmarshal(carriedEntity, &entity); err != nil {
		t.Fatalf("read the carried entity: %v", err)
	}

	export := exportgen.Generate(exportgen.Spec{Providers: []string{"registry.terraform.io/example/big"},
		TypesPerProvider: 1200, AttributesPerType: 40, BlockDepth: 2})
	if len(export) < minimumExportBytes {
		t.Fatalf("the generated export is %d bytes, below the %d the case imports", len(export), minimumExportBytes)
	}

	for _, named := range []bool{true, false} {
		label, args := "the format recognised", []string{"--dry-run"}
		if named {
			label, args = "the format named", []string{"--dry-run", "--format", "terraform-schema"}
		}
		t.Run(label, func(t *testing.T) {
			root := t.TempDir()
			// An empty catalog folder: the workspace then names one, so nothing is
			// fetched, and the JSON parser is the binary's own.
			if err := os.MkdirAll(filepath.Join(root, workspaceconfig.ConventionalCatalogDir), 0o755); err != nil {
				t.Fatalf("create the catalog folder: %v", err)
			}
			importopstest.WriteOfflineConfiguration(t, root, importopstest.Attachment{
				URI:      entity.Coordinate() + ":" + entity.Metadata.Version,
				Settings: map[string]any{"resource_types": []any{"big_resource_0007"}},
			})
			seedSource(t, root, "export.json", export)

			code, stdout, commentary, process := runImportAtTheRoot(t, root, append([]string{filepath.Join(root, "export.json")}, args...)...)
			if code != 0 {
				t.Fatalf("the import exited %d:\n%s\ncommentary:\n%s", code, process, commentary)
			}
			if got := commentaryFormat(commentary); got != "terraform-schema" {
				t.Errorf("the export was read as %q, want terraform-schema\ncommentary:\n%s", got, commentary)
			}
			if !strings.Contains(stdout, "kind: Component") || !strings.Contains(stdout, "kind: Recipe") {
				t.Errorf("the import drafted no Component and Recipe:\n%.2000s", stdout)
			}
		})
	}
}
