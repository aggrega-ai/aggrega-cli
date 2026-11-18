// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// The resource-schema importers read exports at the scale real providers reach.
// The case here imports a Terraform providers schema export of about eighteen
// megabytes through the path every surface takes for a core importer: the binary
// carries the importer's entity and accepts its module by digest, the JSON parser
// is the binary's own, and the limits each runs under are the ones its entity
// records.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest/exportgen"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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

// withEmbeddedLimits is the cascade's resolution with the limits an embedded
// plugin's entity records attached to its module, as the command's own
// resolution attaches them: the parser the binary carries then runs under the
// limits its entity states, not the platform default.
func withEmbeddedLimits(t *testing.T, cascade *recordingCascade) PluginBytesResolver {
	t.Helper()
	return func(rw *workspaceconfig.ResolvedWorkspace, uri string,
		colocated map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error) {
		resolved, err := cascade.resolve(rw, uri, colocated)
		if err != nil {
			return resolved, err
		}
		if raw, ok := embedded.LookupEntity(uri); ok {
			var entity types.Plugin
			if err := yaml.Unmarshal(raw, &entity); err != nil {
				t.Fatalf("read the embedded entity of %s: %v", uri, err)
			}
			resolved.Limits = resolver.PluginRuntimeLimits(entity)
		}
		return resolved, nil
	}
}

// TestTerraformSchemaImportLargeExportWithinLimits imports an export of 1,200
// resource types, once naming the format with the resource type to draft in the
// importer's settings, and once naming no format, so the export is read by the
// host's recognition and again by the importer. Both draft the one type with no
// halt, under the limits the carried entities record: the importer's 8192 pages
// and 150 seconds, and the JSON parser's 16384 pages and 300 seconds.
//
// The carried set is process-wide, so the case is not parallel.
func TestTerraformSchemaImportLargeExportWithinLimits(t *testing.T) {
	embeddedentities.Require(t)
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
	parserEntity, ok := embedded.LookupEntity("parser/json")
	if !ok {
		t.Fatal("this binary embeds no entity for parser/json")
	}
	if got := runtimeOf(t, "parser/json", parserEntity); got.MaxMemoryPages != 16384 || got.TimeoutMs != 300000 {
		t.Fatalf("parser/json carries %d pages and %d ms; the case holds it to 16384 pages and 300000 ms",
			got.MaxMemoryPages, got.TimeoutMs)
	}

	modulePath := wasmtest.EnsureFixtureBuilt(t, "terraform-schema-importer.wasm")
	module, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatalf("read the importer's module: %v", err)
	}
	t.Cleanup(embedded.SeedForTest(uri, module))
	carry(t, embedded.PublishedPlugin{URI: uri, Entity: carriedEntity, Digest: digest.ContentDigest(module)})
	var entity types.Plugin
	if err := yaml.Unmarshal(carriedEntity, &entity); err != nil {
		t.Fatalf("read the carried entity: %v", err)
	}

	export := exportgen.Generate(exportgen.Spec{Providers: []string{"registry.terraform.io/example/big"},
		TypesPerProvider: 1200, AttributesPerType: 40, BlockDepth: 2})
	if len(export) < minimumExportBytes {
		t.Fatalf("the generated export is %d bytes, below the %d the case imports", len(export), minimumExportBytes)
	}
	const requested = "big_resource_0007"

	for _, named := range []bool{true, false} {
		label, format := "the format recognised", ""
		if named {
			label, format = "the format named", "terraform-schema"
		}
		t.Run(label, func(t *testing.T) {
			wsDir := aWorkspaceDir(t)
			source := filepath.Join(wsDir, "export.json")
			if err := os.WriteFile(source, export, 0o644); err != nil {
				t.Fatalf("write the export: %v", err)
			}
			// An empty catalog folder: the workspace then names one, so nothing is
			// fetched, and the JSON parser is the binary's own.
			if err := os.MkdirAll(filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir), 0o755); err != nil {
				t.Fatalf("create the catalog folder: %v", err)
			}
			importopstest.WriteOfflineConfiguration(t, wsDir, importopstest.Attachment{
				URI:      entity.Coordinate() + ":" + entity.Metadata.Version,
				Settings: map[string]any{"resource_types": []any{requested}},
			})
			rw := resolveWorkspaceAt(t, wsDir)
			cascade := &recordingCascade{workspaceCache: true}
			res, err := Import(context.Background(), rw, Options{
				SourcePath:    source,
				SourceName:    "export.json",
				Format:        format,
				Delivery:      authoredcontent.DeliveryFor(true),
				ResolvePlugin: withEmbeddedLimits(t, cascade),
			})
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			if res.Halted() {
				t.Fatalf("the importer halted: %+v", res.Diagnostics)
			}
			if res.Format != "terraform-schema" {
				t.Errorf("the export was read as the %q format, want terraform-schema", res.Format)
			}
			drafts := draftPaths(res)
			if len(drafts) == 0 {
				t.Fatalf("the import drafted nothing; diagnostics: %+v", res.Diagnostics)
			}
			if !slices.ContainsFunc(drafts, func(p string) bool { return filepath.Ext(p) == ".yaml" }) {
				t.Errorf("the import drafted no YAML document: %v", drafts)
			}
		})
	}
}
