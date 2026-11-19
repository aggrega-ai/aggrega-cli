// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of the resource-schema importers through the real Import:
// the CloudFormation, Terraform and Azure Resource Manager importers, run as the
// core importers the binary carries, over the committed synthetic schema
// fixtures, in an offline workspace whose conventional catalog folder holds the
// JSON parser and nothing else.
//
// The import host's guard over an existing catalog entity, its layered view of
// the catalog and its record entry of one format each are held with these
// importers: a re-run of an import is stable, an import naming a Component a
// workspace's catalog folder holds drafts a Recipe alone for it, and two formats
// imported into one root keep each their own drafts and entry through a sync of
// each.

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	cloudFormationSchemaFormat = "cloudformation-schema"
	terraformSchemaFormat      = "terraform-schema"

	// schemaFixtures is where the importers' synthetic schema fixtures sit.
	schemaFixtures = "../../plugins/internal/schemadraft/"

	// Where the CloudFormation importer drafts its ledger fixture and where the
	// Terraform importer drafts its own.
	cfnLedgerComponentPath = "catalog/component/imported/component/aws-example-ledger/0.1.0/component.yaml"
	cfnLedgerRecipePath    = "catalog/recipe/cloudformation/aws-example-ledger/0.1.0/recipe.yaml"
	tfLedgerComponentPath  = "catalog/component/imported/component/ex-ledger/0.1.0/component.yaml"
	tfLedgerRecipePath     = "catalog/recipe/terraform/ex-ledger/0.1.0/recipe.yaml"
	tfLedgerModulePath     = "catalog/recipe/terraform/ex-ledger/0.1.0/module/main.tf.json"
)

// schemaSettings are the settings an attachment hands one schema importer, by
// the importer's identity.
type schemaSettings map[string]map[string]any

// onlyCloudFormation and onlyTerraform ask for the one resource type each
// fixture drafts.
func onlyCloudFormation() schemaSettings {
	return schemaSettings{"importer/cloudformation-schema": {"resource_types": []any{"AWS::Example::Ledger"}}}
}

func onlyTerraform() schemaSettings {
	return schemaSettings{"importer/terraform-schema": {"resource_types": []any{"ex_ledger"}}}
}

// bothSchemas asks for the one type of each of the CloudFormation and Terraform
// fixtures.
func bothSchemas() schemaSettings {
	s := onlyCloudFormation()
	s["importer/terraform-schema"] = onlyTerraform()["importer/terraform-schema"]
	return s
}

// schemaWorkspace carries the schema importers as a release does and resolves an
// offline workspace whose conventional catalog folder holds the JSON parser, with
// the attachments handing each importer in settings its own. Where hubComponent
// names a Component of the standard catalog, by its path beneath the hub's
// catalog, it is copied into the catalog folder.
//
// The carried set is process-wide, so a case calling this is not parallel.
func schemaWorkspace(t *testing.T, settings schemaSettings, hubComponent string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	embeddedentities.Require(t)
	importopstest.CarrySchemaImporters(t)
	wsDir := aWorkspaceDir(t)
	catalog := filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir)
	importopstest.LayJSONParser(t, wsDir, catalog)
	if hubComponent != "" {
		src := filepath.Join(platformHub(t), "component", filepath.FromSlash(hubComponent))
		if err := os.CopyFS(filepath.Join(catalog, "component", filepath.FromSlash(hubComponent)), os.DirFS(src)); err != nil {
			t.Fatalf("copy the standard catalog's %s into the catalog folder: %v", hubComponent, err)
		}
	}
	var attachments []importopstest.Attachment
	for _, uri := range importopstest.SchemaImporters {
		if s, ok := settings[uri]; ok {
			attachments = append(attachments, importopstest.SchemaImporterAttachment(t, uri, s))
		}
	}
	importopstest.WriteOfflineConfiguration(t, wsDir, attachments...)
	return resolveWorkspaceAt(t, wsDir)
}

// schemaSource copies the committed fixture beneath the schema fixtures into a
// directory of its own and returns the copy, so a case can edit what it imports.
func schemaSource(t *testing.T, fixture string) formatSource {
	t.Helper()
	body, err := os.ReadFile(schemaFixtures + fixture)
	if err != nil {
		t.Fatalf("read the schema fixture %s: %v", fixture, err)
	}
	return writeFormatSource(t, filepath.Base(fixture), string(body))
}

// importSchema imports src as format into dir with opts, failing the test where
// the import is refused or the importer halts.
func importSchema(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, src formatSource, format, dir string,
	opts Options) Result {
	t.Helper()
	res, err := schemaImport(rw, src, format, dir, opts)
	if err != nil {
		t.Fatalf("the import of %s as %s was refused: %v", src.name, format, err)
	}
	if res.Halted() {
		t.Fatalf("the importer of %s halted: %+v", format, res.Diagnostics)
	}
	return res
}

// schemaImport is the import importSchema runs, returning its refusal.
func schemaImport(rw *workspaceconfig.ResolvedWorkspace, src formatSource, format, dir string, opts Options) (Result, error) {
	opts.SourcePath, opts.SourceName, opts.Format, opts.OutputDir = src.path, src.name, format, dir
	opts.ResolvePlugin = (&recordingCascade{}).resolve
	return Import(context.Background(), rw, opts)
}

// treeBytes reads every file beneath dir into a map keyed by its slash path.
func treeBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		out[filepath.ToSlash(rel)] = string(body)
		return err
	})
	if err != nil {
		t.Fatalf("read the tree beneath %s: %v", dir, err)
	}
	return out
}

// diagnosticsMention reports whether any diagnostic of res carries text.
func diagnosticsMention(res Result, text string) bool {
	return slices.ContainsFunc(res.Diagnostics, func(d abi.Diagnostic) bool { return strings.Contains(d.Message, text) })
}

// TestResourceSchemaRerunIsStable imports the CloudFormation fixture at the
// workspace's root, where the first run creates the catalog folder's drafts, and
// again into a directory of its own: the two draft the same files byte for byte.
// A plain import at the root over its drafts is refused as one over the
// Component, naming --sync, and changes nothing; a sync over them reports no
// drift, writes and removes nothing, and leaves the drafts as the first run wrote
// them.
func TestResourceSchemaRerunIsStable(t *testing.T) {
	rw := schemaWorkspace(t, onlyCloudFormation(), "")
	src := schemaSource(t, "cloudformation/testdata/ledger.json")
	root, other := rw.Root(), filepath.Join(t.TempDir(), "other")

	res := importSchema(t, rw, src, cloudFormationSchemaFormat, root, Options{})
	importSchema(t, rw, src, cloudFormationSchemaFormat, other, Options{})
	if res.Format != cloudFormationSchemaFormat {
		t.Errorf("the import read the source as %q", res.Format)
	}
	elsewhere := treeBytes(t, other)
	whole := treeBytes(t, root)
	for _, want := range []string{cfnLedgerComponentPath, cfnLedgerRecipePath, RecordFileName} {
		if whole[want] == "" || whole[want] != elsewhere[want] {
			t.Errorf("%s at the root is not the file the import into a directory of its own wrote", want)
		}
	}
	if len(elsewhere) != 3 {
		t.Errorf("the import into a directory of its own wrote %v", slices.Sorted(maps.Keys(elsewhere)))
	}

	_, err := schemaImport(rw, src, cloudFormationSchemaFormat, root, Options{})
	var existing *ExistingEntityError
	if !errors.As(err, &existing) {
		t.Fatalf("a plain import over the drafts was not refused as one over an existing entity: %v", err)
	}
	if existing.Kind != entitykind.Component || existing.Path != cfnLedgerComponentPath {
		t.Errorf("the refusal names the %s at %s, want the Component at %s", existing.Kind, existing.Path,
			cfnLedgerComponentPath)
	}
	if !strings.Contains(err.Error(), "--sync") {
		t.Errorf("the refusal does not name --sync: %v", err)
	}
	if !reflect.DeepEqual(whole, treeBytes(t, root)) {
		t.Errorf("the refused import changed the workspace")
	}

	synced := importSchema(t, rw, src, cloudFormationSchemaFormat, root, Options{Sync: true})
	if len(synced.Drift) != 0 || len(synced.Files) != 0 || len(synced.Written) != 0 || len(synced.Removed) != 0 {
		t.Errorf("a sync of an unchanged source drifted %+v, wrote %v and removed %v", synced.Drift, synced.Written,
			synced.Removed)
	}
	if !reflect.DeepEqual(whole, treeBytes(t, root)) {
		t.Errorf("a sync of an unchanged source changed the workspace")
	}
}

// TestResourceSchemaRecipeOnlyForPublishedComponent imports the Terraform fixture
// asking for a resource type under a Component the workspace's catalog folder
// holds, which the standard catalog publishes as core/datastore/postgres. The
// import drafts a Recipe alone, implementing the Component at the version the
// folder holds, and writes no Component. The control asks for the same type under
// a Component no folder holds: it is drafted new, with the warning that says so.
func TestResourceSchemaRecipeOnlyForPublishedComponent(t *testing.T) {
	const component = "core/datastore/postgres"
	request := func(component string) schemaSettings {
		return schemaSettings{"importer/terraform-schema": {"resource_types": []any{
			map[string]any{"type": "ex_ledger", "component": component}}}}
	}
	src := schemaSource(t, "terraform/testdata/ledger.json")

	t.Run("a Component the catalog folder holds", func(t *testing.T) {
		rw := schemaWorkspace(t, request(component), component)
		dir := filepath.Join(t.TempDir(), "drafts")
		res := importSchema(t, rw, src, terraformSchemaFormat, dir, Options{})
		drafted := treeBytes(t, dir)
		const recipe = "catalog/recipe/terraform/postgres-ex-ledger/0.1.0/recipe.yaml"
		if body, ok := drafted[recipe]; !ok {
			t.Fatalf("the import drafted %v, which lacks the Recipe %s", slices.Sorted(maps.Keys(drafted)), recipe)
		} else if !strings.Contains(body, "implements: "+component+":1.0.0") {
			t.Errorf("the Recipe does not implement %s at the version the folder holds:\n%.1500s", component, body)
		}
		for path := range drafted {
			if strings.HasPrefix(path, "catalog/component/") {
				t.Errorf("the import drafted the Component %s, though the folder holds %s", path, component)
			}
		}
		if diagnosticsMention(res, "holds no Component "+component) {
			t.Errorf("the import warned that the catalog holds no %s, though the folder holds it", component)
		}
	})

	t.Run("a Component no folder holds", func(t *testing.T) {
		rw := schemaWorkspace(t, request(component), "")
		dir := filepath.Join(t.TempDir(), "drafts")
		res := importSchema(t, rw, src, terraformSchemaFormat, dir, Options{})
		if _, ok := treeBytes(t, dir)["catalog/component/"+component+"/0.1.0/component.yaml"]; !ok {
			t.Errorf("the Component was not drafted new where no folder holds it")
		}
		if !diagnosticsMention(res, "holds no Component "+component) {
			t.Errorf("the import did not warn that the Component is drafted new: %+v", res.Diagnostics)
		}
	})
}

// TestResourceSchemaTwoFormatsShareRoot imports the CloudFormation fixture and
// then the Terraform fixture into the workspace's root, over the catalog folder
// at its root. The record beside the drafts then holds one entry for each format,
// each listing its own files and no other's, and the second import leaves the
// first's drafts as they were. A sync of each format over a source edited to
// change its draft writes only the files its own entry lists and the record, and
// leaves the other format's files and entry untouched.
func TestResourceSchemaTwoFormatsShareRoot(t *testing.T) {
	rw := schemaWorkspace(t, bothSchemas(), "")
	root := rw.Root()
	cfn := schemaSource(t, "cloudformation/testdata/ledger.json")
	tf := schemaSource(t, "terraform/testdata/ledger.json")

	importSchema(t, rw, cfn, cloudFormationSchemaFormat, root, Options{})
	rec, _ := recordBeside(t, root)
	cfnEntry := entryIn(t, rec, cloudFormationSchemaFormat)
	cfnFiles := filesOf(t, root, cfnEntry)
	importSchema(t, rw, tf, terraformSchemaFormat, root, Options{})

	rec, _ = recordBeside(t, root)
	if got := recordFormats(rec); !slices.Equal(got, []string{cloudFormationSchemaFormat, terraformSchemaFormat}) {
		t.Fatalf("the record holds the entries %q, want one for each format", got)
	}
	if got := entryIn(t, rec, cloudFormationSchemaFormat); !reflect.DeepEqual(got, cfnEntry) {
		t.Errorf("the Terraform import changed the CloudFormation entry:\n%+v\nwant\n%+v", got, cfnEntry)
	}
	tfEntry := entryIn(t, rec, terraformSchemaFormat)
	if got := entryPaths(cfnEntry); !slices.Equal(slices.Sorted(slices.Values(got)),
		[]string{cfnLedgerComponentPath, cfnLedgerRecipePath}) {
		t.Errorf("the CloudFormation entry lists %v, want its Component and its Recipe", got)
	}
	if got := entryPaths(tfEntry); !slices.Equal(slices.Sorted(slices.Values(got)),
		[]string{tfLedgerComponentPath, tfLedgerModulePath, tfLedgerRecipePath}) {
		t.Errorf("the Terraform entry lists %v, want its Component, its Recipe and its module", got)
	}
	if got := filesOf(t, root, cfnEntry); !reflect.DeepEqual(got, cfnFiles) {
		t.Errorf("the Terraform import changed the CloudFormation drafts")
	}
	tfFiles := filesOf(t, root, tfEntry)

	t.Run("the CloudFormation sync", func(t *testing.T) {
		cfn.rewrite(t, strings.Replace(readFile(t, cfn.path), "How much the ledger serves.", "How much the ledger carries.", 1))
		synced := importSchema(t, rw, cfn, cloudFormationSchemaFormat, root, Options{Sync: true})
		if len(synced.Drift) == 0 {
			t.Fatalf("the sync found the edited source in step")
		}
		for _, p := range synced.Written {
			if p != RecordFileName && !recordLists(cfnEntry, p) {
				t.Errorf("the CloudFormation sync wrote %s, which its entry does not list", p)
			}
		}
		if len(synced.Removed) != 0 {
			t.Errorf("the CloudFormation sync removed %v", synced.Removed)
		}
		after, _ := recordBeside(t, root)
		if got := entryIn(t, after, terraformSchemaFormat); !reflect.DeepEqual(got, tfEntry) {
			t.Errorf("the CloudFormation sync changed the Terraform entry")
		}
		if got := filesOf(t, root, tfEntry); !reflect.DeepEqual(got, tfFiles) {
			t.Errorf("the CloudFormation sync changed the Terraform drafts")
		}
		if got := entryIn(t, after, cloudFormationSchemaFormat); reflect.DeepEqual(got, cfnEntry) {
			t.Errorf("the CloudFormation sync left its entry as it was, though its draft changed")
		}
		cfnEntry = entryIn(t, after, cloudFormationSchemaFormat)
		cfnFiles = filesOf(t, root, cfnEntry)
	})

	t.Run("the Terraform sync", func(t *testing.T) {
		tf.rewrite(t, strings.Replace(readFile(t, tf.path), "The ledger's own label.", "The ledger's own tag.", 1))
		synced := importSchema(t, rw, tf, terraformSchemaFormat, root, Options{Sync: true})
		if len(synced.Drift) == 0 {
			t.Fatalf("the sync found the edited source in step")
		}
		for _, p := range synced.Written {
			if p != RecordFileName && !recordLists(tfEntry, p) {
				t.Errorf("the Terraform sync wrote %s, which its entry does not list", p)
			}
		}
		if len(synced.Removed) != 0 {
			t.Errorf("the Terraform sync removed %v", synced.Removed)
		}
		after, _ := recordBeside(t, root)
		if got := entryIn(t, after, cloudFormationSchemaFormat); !reflect.DeepEqual(got, cfnEntry) {
			t.Errorf("the Terraform sync changed the CloudFormation entry")
		}
		if got := filesOf(t, root, cfnEntry); !reflect.DeepEqual(got, cfnFiles) {
			t.Errorf("the Terraform sync changed the CloudFormation drafts")
		}
		if got := entryIn(t, after, terraformSchemaFormat); reflect.DeepEqual(got, tfEntry) {
			t.Errorf("the Terraform sync left its entry as it was, though its draft changed")
		}
	})
}

// readFile reads path, failing the test where it cannot.
func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// ledgerWithoutName is the CloudFormation ledger fixture with its Name property
// removed, one of the identity properties its Component's values hold.
func ledgerWithoutName(t *testing.T, src formatSource) string {
	t.Helper()
	const name = `    "Name": {
      "type": "string",
      "description": "A display name for the ledger."
    },
`
	body := readFile(t, src.path)
	if !strings.Contains(body, name) {
		t.Fatalf("the ledger fixture holds no Name property to remove")
	}
	return strings.Replace(body, name, "", 1)
}

// componentExistsHalts returns the component-exists halts among diags.
func componentExistsHalts(diags []abi.Diagnostic) []abi.Diagnostic {
	var out []abi.Diagnostic
	for _, d := range diags {
		if d.Code == "component-exists" && d.Severity == abi.SeverityHalt {
			out = append(out, d)
		}
	}
	return out
}

// TestResourceSchemaSyncRedraftsItsOwnComponent imports the CloudFormation
// fixture at the workspace's root, where its Component lands in the catalog
// folder the import reads, and removes an identity property from the source. A
// sync then reads the Component the import drafted as its own draft rather than
// as a catalog Component: it does not halt component-exists, and brings the
// Component and the Recipe up to date with the source, the Component's values no
// longer holding the removed key.
//
// The control holds the first import's Component in the catalog folder with no
// record entry listing it: an import of the edited source into a directory of
// its own reads it as the catalog's, whose values keys differ from the draft's,
// and halts component-exists naming it.
func TestResourceSchemaSyncRedraftsItsOwnComponent(t *testing.T) {
	t.Run("its own drafts", func(t *testing.T) {
		rw := schemaWorkspace(t, onlyCloudFormation(), "")
		src := schemaSource(t, "cloudformation/testdata/ledger.json")
		root := rw.Root()
		importSchema(t, rw, src, cloudFormationSchemaFormat, root, Options{})
		before := treeBytes(t, root)

		src.rewrite(t, ledgerWithoutName(t, src))
		res, err := schemaImport(rw, src, cloudFormationSchemaFormat, root, Options{Sync: true})
		if err != nil {
			t.Fatalf("the sync was refused: %v", err)
		}
		if halts := componentExistsHalts(res.Diagnostics); len(halts) != 0 {
			t.Fatalf("the sync halted component-exists over its own drafted Component: %+v", halts)
		}
		changed := map[string]bool{}
		for _, d := range res.Drift {
			changed[d.File] = true
		}
		for _, want := range []string{cfnLedgerComponentPath, cfnLedgerRecipePath} {
			if !changed[want] {
				t.Errorf("the sync brought %s up to date with nothing; drift %+v", want, res.Drift)
			}
		}
		after := treeBytes(t, root)
		component := after[cfnLedgerComponentPath]
		if strings.Contains(component, "A display name for the ledger.") || !strings.Contains(component, "ledger_name:") {
			t.Errorf("the synced Component does not hold the values the edited source drafts:\n%.2500s", component)
		}
		if after[cfnLedgerRecipePath] == before[cfnLedgerRecipePath] {
			t.Errorf("the sync left the Recipe as it was, though the Component's values changed")
		}
	})

	t.Run("a Component no record entry lists", func(t *testing.T) {
		rw := schemaWorkspace(t, onlyCloudFormation(), "")
		src := schemaSource(t, "cloudformation/testdata/ledger.json")
		other := filepath.Join(t.TempDir(), "other")
		importSchema(t, rw, src, cloudFormationSchemaFormat, other, Options{})
		held := filepath.Join(rw.Root(), filepath.FromSlash(cfnLedgerComponentPath))
		importopstest.WriteSource(t, held, readFile(t, filepath.Join(other, filepath.FromSlash(cfnLedgerComponentPath))))

		src.rewrite(t, ledgerWithoutName(t, src))
		res, err := schemaImport(rw, src, cloudFormationSchemaFormat, filepath.Join(t.TempDir(), "drafts"), Options{})
		if err != nil {
			t.Fatalf("the import was refused: %v", err)
		}
		halts := componentExistsHalts(res.Diagnostics)
		if len(halts) != 1 || !strings.Contains(halts[0].Message, "imported/component/aws-example-ledger") {
			t.Errorf("the import did not halt component-exists naming the catalog folder's Component: %+v",
				res.Diagnostics)
		}
	})
}
