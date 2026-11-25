// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// The arm importer reads templates at the scale Azure Verified Modules reach:
// one module's compiled template is more than a hundred kilobytes, and a
// template composing several is several times that. The case here runs the
// real Import over such a template, as the core importer the binary carries,
// under the limits the importer's Plugin entity records.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	armImporter = "importer/arm"

	// armTemplates is where the arm importer's templates sit.
	armTemplates = "../../plugins/internal/armimport/testdata/"

	// minimumArmTemplateBytes is the smallest template the case imports.
	minimumArmTemplateBytes = 700 << 10
)

// avmModules are the compiled templates of the Azure Verified Modules the case
// composes, beneath the arm importer's test data.
var avmModules = []string{
	"db-for-postgre-sql/flexible-server/0.16.2",
	"kusto/cluster/0.11.1",
	"maps/account/0.2.2",
	"db-for-postgre-sql/flexible-server/0.9.1",
}

// largeArmTemplate composes a template of at least minimumArmTemplateBytes from
// the committed modules: each is deployed under a name of its own, three to a
// template, and each such template is itself deployed inline from the one
// above it, so the modules nest three deep and repeat. It fails the test where
// a module cannot be read.
func largeArmTemplate(t *testing.T) []byte {
	t.Helper()
	modules := make([]json.RawMessage, len(avmModules))
	for i, m := range avmModules {
		body, err := os.ReadFile(filepath.Join(armTemplates, "avm", filepath.FromSlash(m), "main.json"))
		if err != nil {
			t.Fatalf("read the module %s: %v", m, err)
		}
		modules[i] = body
	}
	moduleResource := func(name string, template any) map[string]any {
		return map[string]any{
			"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": name,
			"properties": map[string]any{
				"mode":                        "Incremental",
				"expressionEvaluationOptions": map[string]any{"scope": "inner"},
				"parameters":                  map[string]any{"name": map[string]any{"value": name}},
				"template":                    template,
			},
		}
	}
	template := func(resources []any) map[string]any {
		return map[string]any{
			"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
			"contentVersion": "1.0.0.0",
			"resources":      resources,
		}
	}
	const perTemplate = 3
	for n := 1; ; n++ {
		// The deepest template holds the last modules; each template above it
		// deploys the one below it after its own modules.
		var below map[string]any
		for first := (n - 1) / perTemplate * perTemplate; first >= 0; first -= perTemplate {
			var resources []any
			for i := first; i < min(first+perTemplate, n); i++ {
				resources = append(resources, moduleResource("module"+strconv.Itoa(i), modules[i%len(modules)]))
			}
			if below != nil {
				resources = append(resources, moduleResource("nested"+strconv.Itoa(first), below))
			}
			below = template(resources)
		}
		body, err := json.Marshal(below)
		if err != nil {
			t.Fatalf("encode the template: %v", err)
		}
		if len(body) >= minimumArmTemplateBytes {
			return body
		}
	}
}

// TestArmImportLargeTemplateWithinLimits imports a template of at least 700
// kilobytes — Azure Verified Modules nested and repeated — through the real
// Import as the arm core importer, naming no format, so the template is read
// by the host's recognition and again by the importer. It halts nowhere and
// drafts an Aggregate, under the limits the carried entity records, which are
// the ones the hub's overlay for the importer states.
//
// The carried set is process-wide, so the case is not parallel.
func TestArmImportLargeTemplateWithinLimits(t *testing.T) {
	embeddedentities.Require(t)
	importopstest.CarryImporters(t, armImporter)

	var carried []byte
	for _, p := range embedded.PublishedPlugins() {
		if p.URI == armImporter {
			carried = p.Entity
		}
	}
	var entity types.Plugin
	if err := yaml.Unmarshal(carried, &entity); err != nil || entity.Spec.Runtime == nil {
		t.Fatalf("the entity %s carries records no runtime limits (%v)", armImporter, err)
	}
	var overlay types.Plugin
	raw, err := os.ReadFile(wasmtest.SiblingPath(t, "aggrega-hub", "plugin-overlays", "importer", "arm.yaml"))
	if err != nil {
		t.Fatalf("read the overlay: %v", err)
	}
	if err := yaml.Unmarshal(raw, &overlay); err != nil || overlay.Spec.Runtime == nil {
		t.Fatalf("the overlay records no runtime limits (%v)", err)
	}
	if got, want := *entity.Spec.Runtime, *overlay.Spec.Runtime; got.MaxMemoryPages != want.MaxMemoryPages ||
		got.TimeoutMs != want.TimeoutMs {
		t.Fatalf("the carried entity records %d pages and %d ms; the overlay states %d pages and %d ms",
			got.MaxMemoryPages, got.TimeoutMs, want.MaxMemoryPages, want.TimeoutMs)
	}

	template := largeArmTemplate(t)
	if len(template) < minimumArmTemplateBytes {
		t.Fatalf("the template is %d bytes, below the %d the case imports", len(template), minimumArmTemplateBytes)
	}

	wsDir := aWorkspaceDir(t)
	if err := os.MkdirAll(filepath.Join(wsDir, "catalog"), 0o755); err != nil {
		t.Fatalf("create the catalog folder: %v", err)
	}
	importopstest.WriteOfflineConfiguration(t, wsDir)
	rw := resolveWorkspaceAt(t, wsDir)
	src := writeFormatSource(t, "main.json", string(template))

	res, err := Import(context.Background(), rw, Options{
		SourcePath: src.path, SourceName: src.name, OutputDir: filepath.Join(t.TempDir(), "drafts"),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if res.Halted() {
		t.Fatalf("the importer halted: %+v", res.Diagnostics)
	}
	if res.Format != "arm" {
		t.Errorf("the template was read as %q, want arm", res.Format)
	}
	if !slices.ContainsFunc(res.Files, func(f abi.ScaffoldFile) bool { return filepath.Base(f.Path) == "aggregate.yaml" }) {
		t.Errorf("the import drafted no Aggregate; its files are %d, its diagnostics %+v", len(res.Files), res.Diagnostics)
	}
}
