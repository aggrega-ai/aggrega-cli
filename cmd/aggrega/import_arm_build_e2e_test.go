// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end proof, through the real command tree, that what `aggrega import`
// drafts from an Azure Resource Manager template builds back into one: the
// template is imported as the arm core importer the binary carries, naming no
// target, over a workspace whose catalog folder holds the standard catalog, and
// `aggrega pull`, `aggrega validate` and `aggrega build --verify-determinism`
// run over the drafts, none of which is edited. Two runs over the same template
// write byte-identical drafts and output.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
)

// armBuildBackFixture is the committed template the case imports: a storage
// account and an Event Hubs namespace the catalog maps, a virtual network the
// operator owns, and a static web app no row maps.
const armBuildBackFixture = "../../internal/importops/testdata/arm/build-back"

// armImporterURI is the core importer the case carries.
const armImporterURI = "importer/arm"

// armBuildWorkspace lays an offline workspace at a fresh root whose catalog
// folder holds every entity of the standard catalog at hub but its Plugin
// entities, and returns the root. hub is resolved before a case changes its
// working directory.
func armBuildWorkspace(t *testing.T, hub string) string {
	t.Helper()
	root := t.TempDir()
	entries, err := os.ReadDir(hub)
	if err != nil {
		t.Fatalf("read the standard catalog: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "plugin" {
			continue
		}
		if err := os.CopyFS(filepath.Join(root, "catalog", e.Name()), os.DirFS(filepath.Join(hub, e.Name()))); err != nil {
			t.Fatalf("copy the standard catalog's %s entities: %v", e.Name(), err)
		}
	}
	importopstest.WriteOfflineConfiguration(t, root)
	return root
}

// armTemplateSource copies the committed template directory named by fixture
// into a directory of its own, so no file an import writes beside its source
// reaches the repository, and returns the copy's absolute path.
func armTemplateSource(t *testing.T, fixture string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "stack")
	if err := os.CopyFS(dir, os.DirFS(fixture)); err != nil {
		t.Fatalf("copy the template directory: %v", err)
	}
	return dir
}

// armBuildBack imports the template directory source at a fresh workspace and
// builds the drafts, returning the workspace's root. The import names no target,
// so it drafts for the target the importer declares.
func armBuildBack(t *testing.T, hub, source string) string {
	t.Helper()
	root := armBuildWorkspace(t, hub)
	if code, stdout, commentary, process := runImportAtTheRoot(t, root, source); code != 0 {
		t.Fatalf("the import exited %d:\n%s\n%s\n%s", code, stdout, commentary, process)
	}
	t.Chdir(root)
	mustRunDriftCommand(t, "the pull", "pull", "--release", "release.yaml")
	mustRunDriftCommand(t, "the validation of the catalog", "validate")
	mustRunDriftCommand(t, "the validation of the Release", "validate", "--release", "release.yaml")
	mustRunDriftCommand(t, "the build", "build", "--release", "release.yaml", "--verify-determinism")
	return root
}

// armDrafts reads every file an import of the template wrote beneath root that
// is not a copy of the standard catalog: the Aggregate, the Release, the
// Manifest and the entities it drafted, by path.
func armDrafts(t *testing.T, root string) map[string]string {
	t.Helper()
	drafts := map[string]string{}
	for _, name := range []string{"aggregate.yaml", "release.yaml", "manifest.yaml"} {
		drafts[name] = string(mustReadFile(t, filepath.Join(root, name)))
	}
	for _, path := range []string{
		"catalog/component/imported/compute/microsoft-web-staticsites/0.1.0/component.yaml",
		"catalog/recipe/arm/microsoft-web-staticsites/0.1.0/recipe.yaml",
	} {
		drafts[path] = string(mustReadFile(t, filepath.Join(root, path)))
	}
	return drafts
}

// armResources decodes the arm template a build wrote.
func armResources(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Resources map[string]map[string]any `json:"resources"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the template is not JSON: %v\n%s", err, body)
	}
	return doc.Resources
}

// innerResource returns the one resource of the template a nested template
// resource carries.
func innerResource(t *testing.T, wrapper map[string]any) map[string]any {
	t.Helper()
	inner := objectAt(t, wrapper, "properties", "template")
	resources, _ := inner["resources"].([]any)
	if len(resources) == 0 {
		t.Fatalf("the nested template resource carries no resource: %v", wrapper)
	}
	first, _ := resources[0].(map[string]any)
	return first
}

// TestArmImportBuildsBack imports the build-back template with no target and
// builds the drafts: the template holds each mapped resource as its Recipe
// renders it, in the nested template resource the Recipe writes, the drafted resource as
// the source writes it with its pointer whole and its string written with an
// escaped bracket as written, and holds no telemetry resource and nothing for
// the virtual network the operator owns. Two imports and two builds over the
// template write byte-identical drafts and output.
func TestArmImportBuildsBack(t *testing.T) {
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	importopstest.CarryImporters(t, armImporterURI)
	isolateFromThisMachine(t)
	source, err := filepath.Abs(armBuildBackFixture)
	if err != nil {
		t.Fatal(err)
	}

	hub := siblingCatalogRootForExamples(t)
	first := armBuildBack(t, hub, armTemplateSource(t, source))
	template := mustReadFile(t, filepath.Join(first, "dist/arm/arm-template.json"))
	resources := armResources(t, template)
	if got, want := slices.Sorted(func(yield func(string) bool) {
		for k := range resources {
			if !yield(k) {
				return
			}
		}
	}), []string{"EventsMain", "SiteDocs", "Stdocs"}; !slices.Equal(got, want) {
		t.Fatalf("the template deploys %v, want %v", got, want)
	}
	for _, absent := range []string{"virtualNetworks", "vnet-core", "elemetry", "46d3xbcp"} {
		if strings.Contains(string(template), absent) {
			t.Errorf("the template holds %q, which nothing the source declares asks for", absent)
		}
	}

	storage := innerResource(t, resources["Stdocs"])
	if storage["type"] != "Microsoft.Storage/storageAccounts" || storage["name"] != "stdocs" ||
		storage["location"] != "northeurope" || storage["kind"] != "StorageV2" ||
		!reflect.DeepEqual(storage["sku"], map[string]any{"name": "Standard_LRS"}) {
		t.Errorf("the storage account is\n%v", storage)
	}
	hubs := innerResource(t, resources["EventsMain"])
	if hubs["type"] != "Microsoft.EventHub/namespaces" || hubs["name"] != "events-main" ||
		hubs["location"] != "northeurope" ||
		!reflect.DeepEqual(hubs["sku"], map[string]any{"name": "Standard", "tier": "Standard", "capacity": float64(1)}) {
		t.Errorf("the Event Hubs namespace is\n%v", hubs)
	}

	want := map[string]any{
		"apiVersion": "2023-01-01",
		"location":   "northeurope",
		"name":       "site-docs",
		"properties": map[string]any{
			"buildProperties": map[string]any{"appLocation": "/", "outputLocation": "dist"},
			"repositoryUrl":   "[[not-an-expression]",
			"repositoryToken": "[resourceId('Microsoft.KeyVault/vaults/secrets', 'kv-shared', 'swa-token')]",
		},
		"sku":  map[string]any{"name": "Free", "tier": "Free"},
		"type": "Microsoft.Web/staticSites",
	}
	if got := without(resources["SiteDocs"], "metadata"); !reflect.DeepEqual(got, want) {
		t.Errorf("the drafted resource is\n%v\nwant\n%v", got, want)
	}

	second := armBuildBack(t, hub, armTemplateSource(t, source))
	if a, b := armDrafts(t, first), armDrafts(t, second); !reflect.DeepEqual(a, b) {
		t.Errorf("two imports of one template drafted different files:\n%v\nagainst\n%v", a, b)
	}
	if a, b := treeFingerprint(t, filepath.Join(first, "dist")), treeFingerprint(t, filepath.Join(second, "dist")); !reflect.DeepEqual(a, b) {
		t.Errorf("two builds of one template wrote different files")
	}
}
