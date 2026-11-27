// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of the cloudformation importer through the real Import, as
// the core importer the binary carries: the three forms a template is written
// in, the target an import for another target maps the same template to, a
// re-import and a sync of the drafts, a drafted name the catalog already holds,
// and the choice of the template an import starts from among several. Each case
// runs over a workspace whose catalog folder holds the standard catalog, and
// reads the templates committed under testdata/cloudformation.

import (
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

// cfnTestdata is where the cases' templates sit.
const cfnTestdata = "testdata/cloudformation/"

// cfnWorkspace carries the cloudformation importer and lays a workspace whose
// catalog folder holds every entity of the standard catalog but its Plugin
// entities, offline, with the importer attached with settings where they are
// given. It returns the workspace and its directory. The carried set is
// process-wide, so no case calling it is parallel.
func cfnWorkspace(t *testing.T, settings map[string]any) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	embeddedentities.Require(t)
	importopstest.CarryImporters(t, cloudFormationImporter)
	wsDir := aWorkspaceDir(t)
	for _, kind := range hubCatalogKinds(t) {
		if err := os.CopyFS(filepath.Join(wsDir, "catalog", kind), os.DirFS(filepath.Join(platformHub(t), kind))); err != nil {
			t.Fatalf("copy the standard catalog's %s entities: %v", kind, err)
		}
	}
	var attachments []importopstest.Attachment
	if settings != nil {
		attachments = append(attachments, importopstest.SchemaImporterAttachment(t, cloudFormationImporter, settings))
	}
	importopstest.WriteOfflineConfiguration(t, wsDir, attachments...)
	return resolveWorkspaceAt(t, wsDir), wsDir
}

// cfnSource copies the directory testdata/cloudformation/<name> into a
// directory of its own, as a user's source sits beside no import, and returns
// the copy.
func cfnSource(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), filepath.Base(name))
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(cfnTestdata, filepath.FromSlash(name)))); err != nil {
		t.Fatalf("copy the template directory %s: %v", name, err)
	}
	return dir
}

// cfnImport imports the directory source for target into outputDir, with the
// delivery and the sync given.
func cfnImport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, source, target, outputDir string, sync bool,
	delivery authoredcontent.Delivery) (Result, error) {
	t.Helper()
	return armImport(t, rw, source, target, outputDir, sync, delivery)
}

// TestCloudFormationShortLongAndJsonDraftOneSequence imports one template
// written with the short intrinsic forms, with the long forms and as JSON: the
// three draft the same Aggregate, Release and Manifest, apart from the name of
// the source each says it read and the name each takes from its directory.
func TestCloudFormationShortLongAndJsonDraftOneSequence(t *testing.T) {
	rw, wsDir := cfnWorkspace(t, nil)
	var drafted []map[string]string
	for _, form := range []string{"short", "long", "json"} {
		res, err := cfnImport(t, rw, cfnSource(t, "forms/"+form), "", filepath.Join(wsDir, form), false,
			authoredcontent.DeliveryReport)
		if err != nil || res.Halted() {
			t.Fatalf("the import of the %s form: %v %+v", form, err, res.Diagnostics)
		}
		if res.Format != "cloudformation" {
			t.Errorf("the %s form was read as %q, want cloudformation", form, res.Format)
		}
		files := map[string]string{}
		for _, name := range []string{"aggregate.yaml", "release.yaml", ManifestFileName} {
			lines := slices.DeleteFunc(strings.Split(fileNamed(t, res, name), "\n"),
				func(line string) bool { return strings.HasPrefix(line, "# Sources read:") })
			files[name] = strings.ReplaceAll(strings.Join(lines, "\n"), "imported/"+form, "imported/flow")
		}
		drafted = append(drafted, files)
	}
	for name, want := range drafted[0] {
		for i, form := range []string{"long", "json"} {
			if got := drafted[i+1][name]; got != want {
				t.Errorf("the %s form's %s differs from the short form's:\n%s\nagainst\n%s", form, name, got, want)
			}
		}
	}
	if got := drafted[0]["aggregate.yaml"]; !strings.Contains(got, "    orders-db:\n      type: core/datastore/postgres\n") {
		t.Errorf("the Aggregate is\n%s\nwant the instance drafted as core/datastore/postgres", got)
	}
}

// TestCloudFormationImportForTerraformMapsTheSameNamedRecipe imports one
// template for the default target and for terraform: both draft the same
// Aggregate, byte for byte, and each Manifest maps the PostgreSQL instance to
// the Recipe named postgres-rds of its own target.
func TestCloudFormationImportForTerraformMapsTheSameNamedRecipe(t *testing.T) {
	rw, wsDir := cfnWorkspace(t, nil)
	source := cfnSource(t, "build-back")

	byDefault, err := cfnImport(t, rw, source, "", filepath.Join(wsDir, "default"), false, authoredcontent.DeliveryReport)
	if err != nil || byDefault.Halted() {
		t.Fatalf("the import for the default target: %v %+v", err, byDefault.Diagnostics)
	}
	forTerraform, err := cfnImport(t, rw, source, "terraform", filepath.Join(wsDir, "terraform"), false,
		authoredcontent.DeliveryReport)
	if err != nil || forTerraform.Halted() {
		t.Fatalf("the import for terraform: %v %+v", err, forTerraform.Diagnostics)
	}

	if a, b := fileNamed(t, byDefault, "aggregate.yaml"), fileNamed(t, forTerraform, "aggregate.yaml"); a != b {
		t.Errorf("the Aggregate differs by target:\n%s\nagainst\n%s", a, b)
	}
	if got := fileNamed(t, byDefault, ManifestFileName); !strings.Contains(got, "orders-db: cloudformation/postgres-rds\n") {
		t.Errorf("the default target's Manifest is\n%s\nwant it to map the instance to cloudformation/postgres-rds", got)
	}
	if got := fileNamed(t, forTerraform, ManifestFileName); !strings.Contains(got, "orders-db: terraform/postgres-rds\n") {
		t.Errorf("terraform's Manifest is\n%s\nwant it to map the instance to terraform/postgres-rds", got)
	}
}

// TestCloudFormationReimportDraftsTheSameSet imports a template that drafts a
// Component of its own, imports it again over the drafts, and syncs: the sync
// finds the drafts in step and removes nothing, a plain second import is refused
// over the Aggregate it would overwrite, and the Component the Aggregate
// references is still on disk.
func TestCloudFormationReimportDraftsTheSameSet(t *testing.T) {
	rw, wsDir := cfnWorkspace(t, nil)
	source := cfnSource(t, "build-back")

	first, err := cfnImport(t, rw, source, "", wsDir, false, authoredcontent.DeliveryWrite)
	if err != nil || first.Halted() {
		t.Fatalf("the import: %v %+v", err, first.Diagnostics)
	}
	again, err := cfnImport(t, rw, source, "", wsDir, true, authoredcontent.DeliveryReport)
	if err != nil || again.Halted() {
		t.Fatalf("the sync: %v %+v", err, again.Diagnostics)
	}
	if len(again.Drift) != 0 || len(again.Files) != 0 || len(again.Removed) != 0 {
		t.Errorf("the sync drifts %+v, writes %v and removes %v; want it in step", again.Drift, draftPaths(again),
			again.Removed)
	}
	reimport, err := cfnImport(t, rw, source, "", wsDir, false, authoredcontent.DeliveryReport)
	if err == nil && !reimport.Halted() {
		t.Errorf("a second import over the drafts wrote %v; want it refused over the Aggregate it would overwrite",
			draftPaths(reimport))
	}

	synced, err := cfnImport(t, rw, source, "", wsDir, true, authoredcontent.DeliveryWrite)
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
	if !slices.ContainsFunc(first.Files, func(f abi.ScaffoldFile) bool { return f.Path == mskComponent }) {
		t.Errorf("the import drafted no Component for the MSK cluster: %v", draftPaths(first))
	}
}

// The paths of the Component and the Recipe the build-back template's MSK
// cluster drafts, beneath the output directory.
const (
	mskComponent = "catalog/component/imported/messaging/aws-msk-cluster/0.1.0/component.yaml"
	mskRecipe    = "catalog/recipe/cloudformation/aws-msk-cluster/0.1.0/recipe.yaml"
)

// cfnTakenNameTree lays the Component and the Recipe the MSK cluster drafts, as
// a catalog author holds them, in a fresh directory and returns it.
func cfnTakenNameTree(t *testing.T, drafts Result) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "catalog")
	for _, path := range []string{mskComponent, mskRecipe} {
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

// cfnHeldBackEntry is the mapping_overrides setting the warning of a held-back
// MSK cluster prints.
var cfnHeldBackEntry = map[string]any{"cloudformation": map[string]any{"AWS::MSK::Cluster": map[string]any{
	"component": "imported/messaging/aws-msk-cluster", "category": "messaging",
	"traits": map[string]any{"component.aggrega.ai/hosting": "managed", "component.aggrega.ai/type": "resource"},
	"vendor": "cluster"}}}

// TestCloudFormationTakenNameHeldBack imports a template whose MSK cluster no
// row maps where the catalog already holds the Component that draft would take:
// the resource is held back, warned component-exists, and drafts no Component of
// its own. Handed the mapping_overrides entry the warning prints, the import
// references the Component and the Manifest maps it to the Recipe the catalog
// holds.
func TestCloudFormationTakenNameHeldBack(t *testing.T) {
	rw, wsDir := cfnWorkspace(t, nil)
	drafted, err := cfnImport(t, rw, cfnSource(t, "build-back"), "", filepath.Join(wsDir, "first"), false,
		authoredcontent.DeliveryReport)
	if err != nil || drafted.Halted() {
		t.Fatalf("the first import: %v %+v", err, drafted.Diagnostics)
	}
	tree := cfnTakenNameTree(t, drafted)

	lay := func(t *testing.T, settings map[string]any) (*workspaceconfig.ResolvedWorkspace, string) {
		t.Helper()
		rw, ws := cfnWorkspace(t, settings)
		if err := os.CopyFS(filepath.Join(ws, "catalog"), os.DirFS(tree)); err != nil {
			t.Fatal(err)
		}
		return rw, ws
	}

	t.Run("the catalog holds it", func(t *testing.T) {
		rw, ws := lay(t, nil)
		res, err := cfnImport(t, rw, cfnSource(t, "build-back"), "", filepath.Join(ws, "out"), false,
			authoredcontent.DeliveryReport)
		if err != nil || res.Halted() {
			t.Fatalf("the import: %v %+v", err, res.Diagnostics)
		}
		held := diagnosticsCodedAs(res, "component-exists")
		if len(held) != 1 || held[0].Severity != abi.SeverityWarn ||
			!strings.HasPrefix(held[0].Message, "Events (AWS::MSK::Cluster) is held back: the catalog holds the "+
				"Component imported/messaging/aws-msk-cluster") ||
			!strings.Contains(held[0].Message, `"mapping_overrides"`) {
			t.Fatalf("component-exists diagnostics are %+v", held)
		}
		for _, path := range draftPaths(res) {
			if strings.Contains(path, "msk") {
				t.Errorf("the import drafted %s for a name the catalog holds", path)
			}
		}
		if got := fileNamed(t, res, "aggregate.yaml"); strings.Contains(got, "    events:") {
			t.Errorf("the Aggregate holds the held-back resource:\n%s", got)
		}
	})

	t.Run("the catalog holds it, and the setting maps the type", func(t *testing.T) {
		rw, ws := lay(t, map[string]any{"mapping_overrides": cfnHeldBackEntry})
		res, err := cfnImport(t, rw, cfnSource(t, "build-back"), "", filepath.Join(ws, "out"), false,
			authoredcontent.DeliveryReport)
		if err != nil || res.Halted() {
			t.Fatalf("the import: %v %+v", err, res.Diagnostics)
		}
		if held := diagnosticsCodedAs(res, "component-exists"); len(held) != 0 {
			t.Errorf("the import held back with the setting given: %+v", held)
		}
		for _, path := range draftPaths(res) {
			if strings.Contains(path, "msk") {
				t.Errorf("the import drafted %s though it references the Component the catalog holds", path)
			}
		}
		if got := fileNamed(t, res, "aggregate.yaml"); !strings.Contains(got,
			"    events:\n      type: imported/messaging/aws-msk-cluster\n") {
			t.Errorf("the Aggregate does not reference the Component the catalog holds:\n%s", got)
		}
		if got := fileNamed(t, res, ManifestFileName); !strings.Contains(got,
			"imported/messaging/aws-msk-cluster: cloudformation/aws-msk-cluster\n") {
			t.Errorf("the Manifest does not map the Component to the Recipe the catalog holds:\n%s", got)
		}
	})
}

// importForCfnRoots imports the directory testdata/cloudformation/<name> under
// the settings given, reporting rather than writing.
func importForCfnRoots(t *testing.T, name string, settings map[string]any) Result {
	t.Helper()
	rw, ws := cfnWorkspace(t, settings)
	res, err := cfnImport(t, rw, cfnSource(t, name), "", filepath.Join(ws, "out"), false, authoredcontent.DeliveryReport)
	if err != nil {
		t.Fatalf("the import of %s was refused: %v", name, err)
	}
	return res
}

// TestCloudFormationTwoRootsRefused imports a directory holding two templates,
// neither linking the other: the importer halts source-refused naming both and
// the root setting, and drafts nothing.
func TestCloudFormationTwoRootsRefused(t *testing.T) {
	res := importForCfnRoots(t, "roots/two", nil)
	halts := diagnosticsCodedAs(res, "source-refused")
	if !res.Halted() || len(halts) != 1 || !strings.Contains(halts[0].Message, "a.yaml, b.yaml") ||
		!strings.Contains(halts[0].Message, "root") {
		t.Fatalf("the import of two roots reports %+v", res.Diagnostics)
	}
	if got := draftPaths(res); len(got) != 0 {
		t.Errorf("the refused import drafted %v", got)
	}
}

// TestCloudFormationRootSettingChoosesOne names one of the two templates in the
// root setting: the import starts from it alone, drafting its queue and none of
// the other's.
func TestCloudFormationRootSettingChoosesOne(t *testing.T) {
	res := importForCfnRoots(t, "roots/two", map[string]any{"root": "b.yaml"})
	if res.Halted() {
		t.Fatalf("the import halted: %+v", res.Diagnostics)
	}
	got := fileNamed(t, res, "aggregate.yaml")
	if !strings.Contains(got, "# Sources read: b.yaml.") || !strings.Contains(got, "BetaQueue") || strings.Contains(got, "AlphaQueue") {
		t.Errorf("the Aggregate is\n%s\nwant b.yaml's queue alone, b.yaml the one source read", got)
	}
}

// TestCloudFormationSharedChildFromTheCommonAncestor imports the directory above
// two templates that link one shared template by a relative path leaving their
// own directory, starting from one of them: the shared template is read with it,
// so its topic is drafted, and the other template is not read.
func TestCloudFormationSharedChildFromTheCommonAncestor(t *testing.T) {
	res := importForCfnRoots(t, "roots/shared", map[string]any{"root": "a/main.yaml"})
	if res.Halted() {
		t.Fatalf("the import halted: %+v", res.Diagnostics)
	}
	got := fileNamed(t, res, "aggregate.yaml")
	if !strings.Contains(got, "# Sources read: a/main.yaml, shared/net.yaml.") || !strings.Contains(got, "AQueue") ||
		!strings.Contains(got, "SharedTopic") || strings.Contains(got, "BQueue") {
		t.Errorf("the Aggregate is\n%s\nwant a's queue and the shared template's topic, from a/main.yaml and shared/net.yaml", got)
	}
	if links := diagnosticsCodedAs(res, "linked-source-not-read"); len(links) != 0 {
		t.Errorf("the shared template was not read: %+v", links)
	}
}

// TestCloudFormationChildOutsideTheDirectoryNotRead imports one template's own
// directory, whose link leaves it for a template that sits beside it on disk:
// the import reads nothing outside the directory it is handed, warns
// linked-source-not-read and drafts the queue of the template it holds alone.
func TestCloudFormationChildOutsideTheDirectoryNotRead(t *testing.T) {
	res := importForCfnRoots(t, "roots/shared/a", nil)
	if res.Halted() {
		t.Fatalf("the import halted: %+v", res.Diagnostics)
	}
	links := diagnosticsCodedAs(res, "linked-source-not-read")
	if len(links) != 1 || links[0].Severity != abi.SeverityWarn ||
		!strings.Contains(links[0].Message, "a path leaving the imported directory") {
		t.Fatalf("linked-source-not-read diagnostics are %+v", links)
	}
	if got := fileNamed(t, res, "aggregate.yaml"); !strings.Contains(got, "AQueue") || strings.Contains(got, "SharedTopic") {
		t.Errorf("the Aggregate is\n%s\nwant a's queue alone", got)
	}
}

// TestCloudFormationRemoteChildNotRead imports a template linking a second by an
// https address: nothing is fetched, the importer warns linked-source-not-read
// naming the link a URL, and the template's own queue is drafted.
func TestCloudFormationRemoteChildNotRead(t *testing.T) {
	res := importForCfnRoots(t, "remote", nil)
	if res.Halted() {
		t.Fatalf("the import halted: %+v", res.Diagnostics)
	}
	links := diagnosticsCodedAs(res, "linked-source-not-read")
	if len(links) != 1 || links[0].Severity != abi.SeverityWarn ||
		!strings.Contains(links[0].Message, "deploys a template linked by a URL, which the import does not read") ||
		!strings.Contains(links[0].Message, "Net is drafted as written") {
		t.Fatalf("linked-source-not-read diagnostics are %+v", links)
	}
	if got := fileNamed(t, res, "aggregate.yaml"); !strings.Contains(got, "LocalQueue") {
		t.Errorf("the Aggregate is\n%s\nwant the template's own queue", got)
	}
}

// TestCloudFormationParametersFileBesideTheTemplateIsRead imports, naming no
// format, a directory holding a template beside a parameters file of
// Key=Value text, and one beside a parameters file of an object at
// Parameters: neither form is recognised on its own, so the directory is
// recognised by its template, and the importer reads the parameters file
// beside it, the drafts carrying the value it supplies in place of the
// template's default.
func TestCloudFormationParametersFileBesideTheTemplateIsRead(t *testing.T) {
	const template = "AWSTemplateFormatVersion: \"2010-09-09\"\nParameters:\n  DbName:\n    Type: String\n" +
		"    Default: orders_dev\nResources:\n  Db:\n    Type: AWS::RDS::DBInstance\n    Properties:\n" +
		"      Engine: postgres\n      DBName: !Ref DbName\n"
	rw, ws := cfnWorkspace(t, nil)
	for form, params := range map[string]string{
		"pairs":  `["DbName=orders_prod"]`,
		"object": `{"Parameters": {"DbName": "orders_prod"}}`,
	} {
		t.Run(form, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "orders")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"template.yaml": template, "parameters.json": params} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			res, err := cfnImport(t, rw, dir, "", filepath.Join(ws, form), false, authoredcontent.DeliveryReport)
			if err != nil || res.Halted() {
				t.Fatalf("the import: %v %+v", err, res.Diagnostics)
			}
			if res.Format != "cloudformation" {
				t.Errorf("the directory was read as %q, want cloudformation", res.Format)
			}
			drafts := fileNamed(t, res, "aggregate.yaml") + fileNamed(t, res, "release.yaml")
			if !strings.Contains(drafts, "# Sources read: parameters.json, template.yaml.") ||
				!strings.Contains(drafts, `default: "orders_prod"`) || strings.Contains(drafts, "orders_dev") {
				t.Errorf("the drafts are\n%s\nwant parameters.json read and its orders_prod in place of orders_dev",
					drafts)
			}
		})
	}
}
