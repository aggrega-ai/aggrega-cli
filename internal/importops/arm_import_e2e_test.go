// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of the arm importer through the real Import, as the core
// importer the binary carries: the target an import for another target maps the
// same template to, a re-import and a sync of the drafts, a drafted name the
// catalog already holds, and the choice of the template an import starts from
// among several. Each case runs over a workspace whose catalog folder holds the
// standard catalog, and reads the templates committed under testdata/arm.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// armTestdata is where the cases' templates sit.
const armTestdata = "testdata/arm/"

// armWorkspace carries the arm importer and lays a workspace whose catalog
// folder holds every entity of the standard catalog but its Plugin entities,
// offline, with the arm importer attached with settings where they are given. It
// returns the workspace and its directory. The carried set is process-wide, so
// no case calling it is parallel.
func armWorkspace(t *testing.T, settings map[string]any) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	embeddedentities.Require(t)
	importopstest.CarryImporters(t, armImporter)
	wsDir := aWorkspaceDir(t)
	for _, kind := range hubCatalogKinds(t) {
		if err := os.CopyFS(filepath.Join(wsDir, "catalog", kind), os.DirFS(filepath.Join(platformHub(t), kind))); err != nil {
			t.Fatalf("copy the standard catalog's %s entities: %v", kind, err)
		}
	}
	var attachments []importopstest.Attachment
	if settings != nil {
		attachments = append(attachments, importopstest.SchemaImporterAttachment(t, armImporter, settings))
	}
	importopstest.WriteOfflineConfiguration(t, wsDir, attachments...)
	return resolveWorkspaceAt(t, wsDir), wsDir
}

// armSource copies the directory testdata/arm/<name> into a directory of its
// own, as a user's source sits beside no import, and returns the copy.
func armSource(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), filepath.Base(name))
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(armTestdata, filepath.FromSlash(name)))); err != nil {
		t.Fatalf("copy the template directory %s: %v", name, err)
	}
	return dir
}

// armImport imports the directory source for target into outputDir, with the
// delivery and the sync given.
func armImport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, source, target, outputDir string, sync bool,
	delivery authoredcontent.Delivery) (Result, error) {
	t.Helper()
	return Import(context.Background(), rw, Options{
		SourcePath:    source,
		SourceName:    filepath.Base(source),
		Target:        target,
		OutputDir:     outputDir,
		Sync:          sync,
		Delivery:      delivery,
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
}

// diagnosticsCodedAs returns the diagnostics res carries of code.
func diagnosticsCodedAs(res Result, code string) []abi.Diagnostic {
	var out []abi.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

// TestArmImportForTerraformMapsTheSameNamedRecipe imports one template for the
// default target and for terraform: both draft the same Aggregate, byte for
// byte, and each Manifest maps the PostgreSQL flexible server to the Recipe
// named postgres-flexible-server of its own target.
func TestArmImportForTerraformMapsTheSameNamedRecipe(t *testing.T) {
	rw, wsDir := armWorkspace(t, nil)
	source := armSource(t, "topology")

	byDefault, err := armImport(t, rw, source, "", filepath.Join(wsDir, "default"), false, authoredcontent.DeliveryReport)
	if err != nil || byDefault.Halted() {
		t.Fatalf("the import for the default target: %v %+v", err, byDefault.Diagnostics)
	}
	forTerraform, err := armImport(t, rw, source, "terraform", filepath.Join(wsDir, "terraform"), false,
		authoredcontent.DeliveryReport)
	if err != nil || forTerraform.Halted() {
		t.Fatalf("the import for terraform: %v %+v", err, forTerraform.Diagnostics)
	}

	if a, b := fileNamed(t, byDefault, "aggregate.yaml"), fileNamed(t, forTerraform, "aggregate.yaml"); a != b {
		t.Errorf("the Aggregate differs by target:\n%s\nagainst\n%s", a, b)
	}
	if got := fileNamed(t, byDefault, ManifestFileName); !strings.Contains(got, "flexibleservers: arm/postgres-flexible-server") {
		t.Errorf("the default target's Manifest is\n%s\nwant it to map the server to arm/postgres-flexible-server", got)
	}
	if got := fileNamed(t, forTerraform, ManifestFileName); !strings.Contains(got, "flexibleservers: terraform/postgres-flexible-server") {
		t.Errorf("terraform's Manifest is\n%s\nwant it to map the server to terraform/postgres-flexible-server", got)
	}
}

// TestArmReimportDraftsTheSameSet imports a template that drafts a Component of
// its own, imports it again over the drafts, and syncs: the second import draws
// the same files, the sync finds the drafts in step and removes nothing, and the
// Component the Aggregate references is still on disk.
func TestArmReimportDraftsTheSameSet(t *testing.T) {
	rw, wsDir := armWorkspace(t, nil)
	source := armSource(t, "build-back")

	first, err := armImport(t, rw, source, "", wsDir, false, authoredcontent.DeliveryWrite)
	if err != nil || first.Halted() {
		t.Fatalf("the import: %v %+v", err, first.Diagnostics)
	}
	again, err := armImport(t, rw, source, "", wsDir, true, authoredcontent.DeliveryReport)
	if err != nil || again.Halted() {
		t.Fatalf("the sync: %v %+v", err, again.Diagnostics)
	}
	if len(again.Drift) != 0 || len(again.Files) != 0 || len(again.Removed) != 0 {
		t.Errorf("the sync drifts %+v, writes %v and removes %v; want it in step", again.Drift, draftPaths(again),
			again.Removed)
	}
	reimport, err := armImport(t, rw, source, "", wsDir, false, authoredcontent.DeliveryReport)
	if err == nil && !reimport.Halted() {
		t.Errorf("a second import over the drafts wrote %v; want it refused over the Aggregate it would overwrite",
			draftPaths(reimport))
	}

	synced, err := armImport(t, rw, source, "", wsDir, true, authoredcontent.DeliveryWrite)
	if err != nil || synced.Halted() {
		t.Fatalf("the written sync: %v %+v", err, synced.Diagnostics)
	}
	if len(synced.Removed) != 0 {
		t.Errorf("the sync removed %v", synced.Removed)
	}
	for _, f := range first.Files {
		if f.Path == RecordFileName {
			continue
		}
		body, err := os.ReadFile(filepath.Join(wsDir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Errorf("the drafted %s is gone after the sync: %v", f.Path, err)
		} else if string(body) != f.Content {
			t.Errorf("the drafted %s changed through the sync", f.Path)
		}
	}
	if !slices.ContainsFunc(first.Files, func(f abi.ScaffoldFile) bool {
		return strings.HasSuffix(f.Path, "microsoft-web-staticsites/0.1.0/component.yaml")
	}) {
		t.Errorf("the import drafted no Component for the static web app: %v", draftPaths(first))
	}
}

// The paths of the Component and the Recipe the build-back template's static web
// app drafts, beneath the output directory.
const (
	staticSiteComponent = "catalog/component/imported/compute/microsoft-web-staticsites/0.1.0/component.yaml"
	staticSiteRecipe    = "catalog/recipe/arm/microsoft-web-staticsites/0.1.0/recipe.yaml"
)

// takenNameTree lays the Component and the Recipe the static web app drafts, as
// a catalog author holds them, in a fresh directory and returns it.
func takenNameTree(t *testing.T, drafts Result) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "catalog")
	for _, path := range []string{staticSiteComponent, staticSiteRecipe} {
		full := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(path, "catalog/")))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(fileNamed(t, drafts, path)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// heldBackEntry is the mapping_overrides setting the warning of a held-back
// static web app prints.
var heldBackEntry = map[string]any{"arm": map[string]any{"Microsoft.Web/staticSites": map[string]any{
	"component": "imported/compute/microsoft-web-staticsites", "category": "compute",
	"traits": map[string]any{"component.aggrega.ai/hosting": "managed", "component.aggrega.ai/type": "resource"},
	"vendor": "staticsites"}}}

// takenNamePlace is where a case lays the catalog entities the draft's name is
// taken by: it lays the workspace for the settings given and returns it with its
// directory.
type takenNamePlace func(t *testing.T, tree string, settings map[string]any) (*workspaceconfig.ResolvedWorkspace, string)

// inCatalogFolder lays the entities of tree in the workspace's catalog folder.
func inCatalogFolder(t *testing.T, tree string, settings map[string]any) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	rw, ws := armWorkspace(t, settings)
	if err := os.CopyFS(filepath.Join(ws, "catalog"), os.DirFS(tree)); err != nil {
		t.Fatal(err)
	}
	return rw, ws
}

// inTheHubView publishes the entities of tree as the catalog snapshot the
// workspace reads, which names no catalog folder.
func inTheHubView(t *testing.T, tree string, settings map[string]any) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	embeddedentities.Require(t)
	importopstest.CarryImporters(t, armImporter)
	ws := filepath.Join(t.TempDir(), "ws")
	holdSnapshotOf(t, ws, tree)
	var attachments []importopstest.Attachment
	if settings != nil {
		attachments = append(attachments, importopstest.SchemaImporterAttachment(t, armImporter, settings))
	}
	importopstest.WriteOfflineConfiguration(t, ws, attachments...)
	return resolveWorkspaceAt(t, ws), ws
}

// TestArmTakenNameHeldBack imports a template whose static web app no row maps
// where the catalog already holds the Component that draft would take, once in
// the workspace's catalog folder and once in the catalog the hub publishes: the
// resource is held back, warned component-exists, and drafts no Component of its
// own. Handed the mapping_overrides entry the warning prints, the import
// references the Component, the Manifest maps it to the Recipe the catalog holds,
// and the instance's properties are in its Release entry's comment.
func TestArmTakenNameHeldBack(t *testing.T) {
	rw, wsDir := armWorkspace(t, nil)
	drafted, err := armImport(t, rw, armSource(t, "build-back"), "", filepath.Join(wsDir, "first"), false,
		authoredcontent.DeliveryReport)
	if err != nil || drafted.Halted() {
		t.Fatalf("the first import: %v %+v", err, drafted.Diagnostics)
	}
	tree := takenNameTree(t, drafted)

	for _, place := range []struct {
		name string
		lay  takenNamePlace
	}{{"the catalog folder", inCatalogFolder}, {"the hub view", inTheHubView}} {
		t.Run(place.name+" holds it", func(t *testing.T) {
			rw, ws := place.lay(t, tree, nil)
			res, err := armImport(t, rw, armSource(t, "build-back"), "", filepath.Join(ws, "out"), false,
				authoredcontent.DeliveryReport)
			if err != nil || res.Halted() {
				t.Fatalf("the import: %v %+v", err, res.Diagnostics)
			}
			held := diagnosticsCodedAs(res, "component-exists")
			if len(held) != 1 || held[0].Severity != abi.SeverityWarn ||
				!strings.HasPrefix(held[0].Message, "site-docs (Microsoft.Web/staticSites) is held back: the catalog holds the "+
					"Component imported/compute/microsoft-web-staticsites") ||
				!strings.Contains(held[0].Message, `"mapping_overrides"`) {
				t.Fatalf("component-exists diagnostics are %+v", held)
			}
			for _, path := range draftPaths(res) {
				if strings.Contains(path, "staticsites") {
					t.Errorf("the import drafted %s for a name the catalog holds", path)
				}
			}
			if got := fileNamed(t, res, "aggregate.yaml"); strings.Contains(got, "site-docs") {
				t.Errorf("the Aggregate holds the held-back resource:\n%s", got)
			}
		})

		t.Run(place.name+" holds it, and the setting maps the type", func(t *testing.T) {
			rw, ws := place.lay(t, tree, map[string]any{"mapping_overrides": heldBackEntry})
			res, err := armImport(t, rw, armSource(t, "build-back"), "", filepath.Join(ws, "out"), false,
				authoredcontent.DeliveryReport)
			if err != nil || res.Halted() {
				t.Fatalf("the import: %v %+v", err, res.Diagnostics)
			}
			if held := diagnosticsCodedAs(res, "component-exists"); len(held) != 0 {
				t.Errorf("the import held back with the setting given: %+v", held)
			}
			for _, path := range draftPaths(res) {
				if strings.Contains(path, "staticsites") {
					t.Errorf("the import drafted %s though it references the Component the catalog holds", path)
				}
			}
			if got := fileNamed(t, res, "aggregate.yaml"); !strings.Contains(got,
				"    site-docs:\n      type: imported/compute/microsoft-web-staticsites\n") {
				t.Errorf("the Aggregate does not reference the Component the catalog holds:\n%s", got)
			}
			if got := fileNamed(t, res, ManifestFileName); !strings.Contains(got,
				"imported/compute/microsoft-web-staticsites: arm/microsoft-web-staticsites\n") {
				t.Errorf("the Manifest does not map the Component to the Recipe the catalog holds:\n%s", got)
			}
			decision := decisionNamed(t, res.Decisions, "site-docs")
			for _, want := range []string{"mapping_overrides", "appLocation", "repositoryUrl", "repositoryToken"} {
				if !strings.Contains(decision.Rationale, want) {
					t.Errorf("the decision on site-docs does not name %s: %s", want, decision.Rationale)
				}
			}
		})
	}
}

// importForRoots imports the directory testdata/arm/<name> under the settings
// given, reporting rather than writing.
func importForRoots(t *testing.T, name string, settings map[string]any) Result {
	t.Helper()
	rw, ws := armWorkspace(t, settings)
	res, err := armImport(t, rw, armSource(t, name), "", filepath.Join(ws, "out"), false, authoredcontent.DeliveryReport)
	if err != nil {
		t.Fatalf("the import of %s was refused: %v", name, err)
	}
	return res
}

// TestArmTwoRootsRefused imports a directory holding two templates, neither
// linking the other: the importer halts source-refused naming both and the root
// setting, and drafts nothing.
func TestArmTwoRootsRefused(t *testing.T) {
	res := importForRoots(t, "roots/two", nil)
	halts := diagnosticsCodedAs(res, "source-refused")
	if !res.Halted() || len(halts) != 1 || !strings.Contains(halts[0].Message, "a.json, b.json") ||
		!strings.Contains(halts[0].Message, "root") {
		t.Fatalf("the import of two roots reports %+v", res.Diagnostics)
	}
	if got := draftPaths(res); len(got) != 0 {
		t.Errorf("the refused import drafted %v", got)
	}
}

// TestArmRootSettingChoosesOne names one of the two templates in the root
// setting: the import starts from it alone, drafting its storage account and
// none of the other's, and names the other among the sources it did not read.
func TestArmRootSettingChoosesOne(t *testing.T) {
	res := importForRoots(t, "roots/two", map[string]any{"root": "b.json"})
	if res.Halted() {
		t.Fatalf("the import halted: %+v", res.Diagnostics)
	}
	got := fileNamed(t, res, "aggregate.yaml")
	if !strings.Contains(got, "# Sources read: b.json.") || !strings.Contains(got, "stbeta:") || strings.Contains(got, "stalpha") {
		t.Errorf("the Aggregate is\n%s\nwant b.json's storage account alone, b.json the one source read", got)
	}
}

// TestArmSharedLinkFromTheCommonAncestor imports the directory above two
// templates that link one shared template by a relative path leaving their own
// directory, starting from one of them: the shared template is read with it, so
// its storage account is drafted, and the other template is not read.
func TestArmSharedLinkFromTheCommonAncestor(t *testing.T) {
	res := importForRoots(t, "roots/shared", map[string]any{"root": "a/main.json"})
	if res.Halted() {
		t.Fatalf("the import halted: %+v", res.Diagnostics)
	}
	got := fileNamed(t, res, "aggregate.yaml")
	if !strings.Contains(got, "# Sources read: a/main.json, shared/net.json.") || !strings.Contains(got, "stalpha:") ||
		!strings.Contains(got, "stshared:") || strings.Contains(got, "stbeta") {
		t.Errorf("the Aggregate is\n%s\nwant a's and the shared template's storage accounts, from a/main.json and shared/net.json", got)
	}
	if links := diagnosticsCodedAs(res, "linked-source-not-read"); len(links) != 0 {
		t.Errorf("the shared template was not read: %+v", links)
	}
}

// TestArmLinkOutsideTheDirectoryNotRead imports one template's own directory,
// whose link leaves it for a template that sits beside it on disk: the import
// reads nothing outside the directory it is handed, warns linked-source-not-read
// and drafts the storage account of the template it holds alone.
func TestArmLinkOutsideTheDirectoryNotRead(t *testing.T) {
	res := importForRoots(t, "roots/shared/a", nil)
	if res.Halted() {
		t.Fatalf("the import halted: %+v", res.Diagnostics)
	}
	links := diagnosticsCodedAs(res, "linked-source-not-read")
	if len(links) != 1 || links[0].Severity != abi.SeverityWarn ||
		!strings.Contains(links[0].Message, "a relative path leaving the imported directory") {
		t.Fatalf("linked-source-not-read diagnostics are %+v", links)
	}
	if got := fileNamed(t, res, "aggregate.yaml"); !strings.Contains(got, "stalpha:") || strings.Contains(got, "stshared") {
		t.Errorf("the Aggregate is\n%s\nwant a's storage account alone", got)
	}
}
