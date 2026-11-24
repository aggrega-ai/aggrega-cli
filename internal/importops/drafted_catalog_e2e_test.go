// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs, through the real Import, that the Components and Recipes an
// importer drafts reach the Manifest the import composes and the values it asks:
// an instance of a drafted Component maps to the drafted Recipe realizing it, a
// drafted Recipe implementing a Component nobody holds is reported and maps
// nothing, and the values a drafted Component declares that the drafted Release
// leaves empty are owed.
//
// Each case imports through the draft-set importer, which drafts exactly the
// files its source spells out, over a workspace whose catalog folder holds the
// draft-composition catalog and one Recipe binding to arm, so an import may name
// arm as its target.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// catalogArmRecipe is the catalog's one Recipe binding to arm, implementing the
// catalog's example/x.
const catalogArmRecipe = "apiVersion: " + apiversion.APIVersion + "\n" + `kind: Recipe
metadata:
  name: x-arm
  version: "1.0.0"
spec:
  capability: arm
  implements: example/x
  artifacts:
    template.yaml: |
      resources: {}
`

// draftedStoreComponent is the Component an importer drafts for a store: one
// required infra key, described.
var draftedStoreComponent = drafted("catalog/components/x.yaml", `kind: Component
metadata:
  name: ws/storage/x
  version: 0.1.0
  labels:
    component.aggrega.ai/type: "resource"
spec:
  schema:
    infra:
      sku: { type: string, required: true, description: "The SKU the store runs on." }
`)

// draftedStoreRecipe is the Recipe of arm, x, an importer drafts beside
// draftedStoreComponent, implementing it at its version.
var draftedStoreRecipe = drafted("catalog/recipes/arm/x.yaml", `kind: Recipe
metadata:
  name: x
  version: 0.1.0
spec:
  capability: arm
  implements: ws/storage/x:0.1.0
  artifacts:
    template.yaml: |
      resources: {}
`)

// storeAggregateOf is the Aggregate named drafted declaring the one instance
// store, of typ.
func storeAggregateOf(typ string) abi.ScaffoldFile {
	return drafted("aggregate.yaml", `kind: Aggregate
metadata:
  name: drafted
  version: 0.1.0
spec:
  components:
    store:
      type: `+typ+"\n")
}

// kitRelease is the Release, and the Manifest where it writes one, the plugin
// kit's Release writer drafts for the drafted Aggregate, for an import given
// target, with the one entry store realized by realization, none where it is
// empty.
func kitRelease(target, realization string) []abi.ScaffoldFile {
	return kitReleaseOf(target, importer.ReleaseEntry{Key: "store", Realization: realization})
}

// kitReleaseOf is kitRelease with the one entry the kit writes being entry.
func kitReleaseOf(target string, entry importer.ReleaseEntry) []abi.ScaffoldFile {
	in := aggrega.ImportInput{Format: importopstest.DraftSetFormat, Name: "drafts.txt", Target: target}
	entities := importer.DraftRelease(in, aggrega.EmittedEntity{Kind: "Aggregate", Name: "drafted", Mode: "draft"},
		importer.ReleaseDraft{Entries: []importer.ReleaseEntry{entry}})
	out := []abi.ScaffoldFile{{Path: "release.yaml", Content: entities[0].Body}}
	if len(entities) > 1 {
		out = append(out, abi.ScaffoldFile{Path: ManifestFileName, Content: entities[1].Body})
	}
	return out
}

// draftedCatalogWorkspace is a workspace whose catalog folder holds the
// draft-composition catalog and catalogArmRecipe, and which names no default
// registry.
func draftedCatalogWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	importopstest.WriteConfiguration(t, wsDir)
	catalogDir := filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir)
	importopstest.LayDraftComposerCatalog(t, wsDir, catalogDir)
	armDir := filepath.Join(catalogDir, "recipes", "arm")
	if err := os.MkdirAll(armDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(armDir, "x-arm.yaml"), []byte(catalogArmRecipe), 0o644); err != nil {
		t.Fatal(err)
	}
	return resolveWorkspaceAt(t, wsDir)
}

// draftedCatalogImport imports the draft set files for target over a
// draftedCatalogWorkspace, into a directory of its own under a reporting
// delivery, failing the test where the import is refused.
func draftedCatalogImport(t *testing.T, target string, files ...abi.ScaffoldFile) Result {
	t.Helper()
	rw := draftedCatalogWorkspace(t)
	source := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"),
		importopstest.DraftSetBody(files...))
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    source,
		SourceName:    source,
		Format:        importopstest.DraftSetFormat,
		Target:        target,
		OutputDir:     filepath.Join(t.TempDir(), "drafts"),
		Delivery:      authoredcontent.DeliveryReport,
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("import for the target %q: %v", target, err)
	}
	return res
}

// TestDraftedRecipeMapsDraftedInstance imports, for the arm target, a drafted
// Component ws/storage/x, the drafted Recipe x of arm implementing it, an
// Aggregate instance store of it, and the Release and Manifest the plugin kit
// drafts naming store's realization x. The composed Manifest keeps the
// importer's mapping of store to arm/x, maps the type no further, and the import
// reports nothing beside what the drafts owe — no dropped mapping, no unmapped
// type.
//
// 🔴 PLANTED DEFECT: hand composeDraftSet seam.Recipes rather than the view
// overlayDrafts returns (internal/importops/import.go): the mapping is dropped
// with a warning and the type left unmapped with another, and both rows redden.
func TestDraftedRecipeMapsDraftedInstance(t *testing.T) {
	t.Parallel()
	files := append([]abi.ScaffoldFile{draftedStoreComponent, draftedStoreRecipe,
		storeAggregateOf("ws/storage/x:0.1.0")}, kitRelease("arm", "x")...)
	res := draftedCatalogImport(t, "arm", files...)

	want := "apiVersion: " + apiversion.APIVersion + "\n" + `kind: Manifest
metadata:
  name: drafted
  version: 0.1.0
spec:
  mappings:
    components:
      type: {}
      name:
        store: arm/x
`
	if got := fileNamed(t, res, ManifestFileName); got != want {
		t.Errorf("the drafted Manifest is\n%s\nwant\n%s", got, want)
	}
	if got := diagnosticsBesideTheOwed(t, res); len(got) != 0 {
		t.Errorf("the import reported %+v; want nothing beside the owed values", got)
	}
}

// TestDraftedRecipeOfUnknownComponentReported imports, for the arm target, a
// drafted Recipe y of arm implementing ws/none/y, which neither the drafts nor
// the catalog hold, and an Aggregate instance of that type. The import reports
// the Recipe and the type it implements, and the composition leaves the type
// unmapped with its own warning: the Recipe is not read as a realization.
//
// 🔴 PLANTED DEFECT: consult a drafted Recipe whatever it implements (drop the
// implements check in overlayDrafts): the report row reddens, and the type maps
// to arm/y, so the unmapped row reddens too.
func TestDraftedRecipeOfUnknownComponentReported(t *testing.T) {
	t.Parallel()
	recipe := drafted("catalog/recipes/arm/y.yaml", `kind: Recipe
metadata:
  name: y
  version: 0.1.0
spec:
  capability: arm
  implements: ws/none/y:0.1.0
  artifacts:
    template.yaml: |
      resources: {}
`)
	files := append([]abi.ScaffoldFile{recipe, storeAggregateOf("ws/none/y:0.1.0")}, kitRelease("arm", "")...)
	res := draftedCatalogImport(t, "arm", files...)

	want := []abi.Diagnostic{
		{Severity: abi.SeverityWarn, Message: `the import does not read the drafted Recipe "arm/y:0.1.0" in ` +
			`"catalog/recipes/arm/y.yaml": it implements the component type "ws/none/y:0.1.0", which neither the ` +
			`drafts nor the workspace's catalog hold`},
		{Severity: abi.SeverityWarn, Message: `the drafted Manifest leaves the component type "ws/none/y:0.1.0" ` +
			`unmapped: no Recipe in the workspace's catalog binds to the target "arm" and implements it, so a build ` +
			`of the drafts halts naming each instance of it until a mapping is added`},
	}
	if got := diagnosticsBesideTheOwed(t, res); !slices.Equal(got, want) {
		t.Errorf("the import reported\n%+v\nwant\n%+v", got, want)
	}
}

// TestOwedValuesOfADraftedComponentAreAsked imports, with no target, a drafted
// Component ws/storage/x declaring the required infra key sku, an Aggregate
// instance store of it, and a Release whose entry writes sku empty. The import
// owes sku, described by the drafted Component's schema, after the Release's own
// spec.manifest.
//
// 🔴 PLANTED DEFECT: hand answerDrafts seam.Recipes rather than the view
// overlayDrafts returns (internal/importops/import.go): sku is no longer owed and
// the key row reddens.
func TestOwedValuesOfADraftedComponentAreAsked(t *testing.T) {
	t.Parallel()
	release := drafted("release.yaml", `kind: Release
metadata:
  name: drafted
  environment: development
  version: 0.1.0
spec:
  aggregate: drafted
  manifest: ""
  components:
    store:
      infra:
        sku: ""
`)
	res := draftedCatalogImport(t, "", draftedStoreComponent, storeAggregateOf("ws/storage/x:0.1.0"), release)

	want := []string{"release.yaml spec.manifest", "release.yaml spec.components.store.infra.sku"}
	if got := owedKeys(res.Owed); !slices.Equal(got, want) {
		t.Fatalf("the drafts owe\n  %v\nwant\n  %v", got, want)
	}
	if got := res.Owed[1].Question(); got != "The SKU the store runs on." {
		t.Errorf("sku is described as %q; want the drafted Component's own description", got)
	}
	if !slices.Contains(res.Diagnostics, owedDiagnostic(res.Owed[1])) {
		t.Errorf("the diagnostics do not name the owed sku: %+v", res.Diagnostics)
	}
}
