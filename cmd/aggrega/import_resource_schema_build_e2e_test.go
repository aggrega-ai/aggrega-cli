// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end proofs, through the real command tree, that what the resource-schema
// importers draft builds into the resource the schema describes: each format's
// synthetic fixture is imported as a core importer the binary carries, the
// Terraform Recipe's directory is pinned, a hand-written Aggregate, Manifest and
// Release name the drafted Component and Recipe, and `aggrega pull`,
// `aggrega validate --release` and `aggrega build` run over them. Two runs over
// the same source write byte-identical drafts and output.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// schemaFixtures is where the importers' synthetic schema fixtures sit, resolved
// before any case changes its working directory.
var schemaFixtures = func() string {
	dir, err := filepath.Abs("../../plugins/internal/schemadraft")
	if err != nil {
		panic(err)
	}
	return dir + string(filepath.Separator)
}()

// schemaBuildCase is one format's fixture, the hand-written entities that build
// it, and what the build is held to.
type schemaBuildCase struct {
	format, importer, fixture, resourceType string
	// recipe is the drafted Recipe's path, pinned where pin is set.
	recipe string
	pin    bool
	// aggregate, manifest and release are the hand-written entities.
	aggregate, manifest, release string
	check                        func(t *testing.T, root string)
}

const schemaManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        %s: %s
`

const schemaAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: probe
  version: 1.0.0
spec:
  components:
    %s:
      type: %s
      values:
%s`

const schemaRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: probe
  version: 1.0.0
  environment: probe
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    stack:
      directory: stack
      components:
        %s:
%s`

func aggregateOf(id, typ, values string) string {
	return fmt.Sprintf(schemaAggregate, id, typ, values)
}

func releaseOf(id, infra string) string { return fmt.Sprintf(schemaRelease, id, infra) }

var schemaBuildCases = []schemaBuildCase{
	{
		format: "cloudformation-schema", importer: "importer/cloudformation-schema",
		fixture: "cloudformation/testdata/ledger.json", resourceType: "AWS::Example::Ledger",
		recipe:    "catalog/recipe/cloudformation/aws-example-ledger/0.1.0/recipe.yaml",
		aggregate: aggregateOf("ledger", "imported/component/aws-example-ledger:0.1.0", "        identifier: orders-ledger\n        ledger_name: orders\n"),
		manifest: fmt.Sprintf(schemaManifest, "imported/component/aws-example-ledger:0.1.0",
			"cloudformation/aws-example-ledger:0.1.0"),
		release: releaseOf("ledger", "          infra:\n            capacity:\n              mode: STANDARD\n"+
			"              read_units: 5\n            subnet_group: subnets-a\n            overrides:\n              KmsKeyId: key-1\n"),
		check: checkCloudFormationLedger,
	},
	{
		format: "terraform-schema", importer: "importer/terraform-schema",
		fixture: "terraform/testdata/ledger.json", resourceType: "ex_ledger",
		recipe: "catalog/recipe/terraform/ex-ledger/0.1.0/recipe.yaml", pin: true,
		aggregate: aggregateOf("ledger", "imported/component/ex-ledger:0.1.0", "        name: orders\n"),
		manifest:  fmt.Sprintf(schemaManifest, "imported/component/ex-ledger:0.1.0", "terraform/ex-ledger:0.1.0"),
		release: releaseOf("ledger", "          infra:\n            retention:\n              - days: 30\n"+
			"                archive:\n                  target: cold\n            overrides:\n              enabled: true\n"),
		check: checkTerraformLedger,
	},
	{
		format: "arm-schema", importer: "importer/arm-schema",
		fixture: "arm/testdata/vault.json", resourceType: "Microsoft.Example/vaults@2025-01-01",
		recipe:    "catalog/recipe/arm/microsoft-example-vaults/0.1.0/recipe.yaml",
		aggregate: aggregateOf("vault", "imported/component/microsoft-example-vaults:0.1.0", "        name: orders-vault\n"),
		manifest: fmt.Sprintf(schemaManifest, "imported/component/microsoft-example-vaults:0.1.0",
			"arm/microsoft-example-vaults:0.1.0"),
		release: releaseOf("vault", "          infra:\n            location: westeurope\n"+
			"            tenant_id: 11111111-2222-3333-4444-555555555555\n            sku:\n              name: Standard\n"+
			"              max_capacity: 3\n            overrides:\n              enabledForDeployment: true\n"),
		check: checkArmVault,
	},
}

// schemaWorkspaceForCase carries the schema importers, then lays an offline
// workspace at a fresh root whose catalog folder is empty and whose one attachment
// hands the case's importer the resource type to draft. The importers are built
// before the machine's own configuration is set aside, since building them reads
// it.
func schemaWorkspaceForCase(t *testing.T, c schemaBuildCase) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	importopstest.WriteOfflineConfiguration(t, root, importopstest.SchemaImporterAttachment(t, c.importer,
		map[string]any{"resource_types": []any{c.resourceType}}))
	return root
}

// importSchemaFixture imports the case's fixture, which sits outside the
// workspace as an exported file does, naming its format, and fails the test
// where the import is refused.
func importSchemaFixture(t *testing.T, root string, c schemaBuildCase) {
	t.Helper()
	body, err := os.ReadFile(schemaFixtures + c.fixture)
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	external := filepath.Join(t.TempDir(), filepath.Base(c.fixture))
	if err := os.WriteFile(external, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, commentary, process := runImportAtTheRoot(t, root, external, "--format", c.format); code != 0 {
		t.Fatalf("the import exited %d:\n%s\n%s\n%s", code, stdout, commentary, process)
	}
	for name, body := range map[string]string{"aggregate.yaml": c.aggregate, "manifest.yaml": c.manifest,
		"release.yaml": c.release} {
		writeFixture(t, filepath.Join(root, name), body)
	}
}

// buildSchemaCase runs the import, the pin where the case pins, the pull, the
// validation and the build at a fresh root, and returns the root.
func buildSchemaCase(t *testing.T, c schemaBuildCase) string {
	t.Helper()
	root := schemaWorkspaceForCase(t, c)
	importSchemaFixture(t, root, c)
	t.Chdir(root)
	if c.pin {
		mustRunDriftCommand(t, "the pin of the drafted Recipe", "pin", c.recipe)
	}
	mustRunDriftCommand(t, "the pull", "pull", "--release", "release.yaml")
	mustRunDriftCommand(t, "the validation of the catalog", "validate")
	mustRunDriftCommand(t, "the validation of the Release", "validate", "--release", "release.yaml")
	mustRunDriftCommand(t, "the build", "build", "--release", "release.yaml")
	return root
}

// carrySchemaImportersIsolated carries the schema importers and then sets the
// machine's configuration aside.
func carrySchemaImportersIsolated(t *testing.T) {
	t.Helper()
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	importopstest.CarrySchemaImporters(t)
	isolateFromThisMachine(t)
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(mustReadFile(t, path), &doc); err != nil {
		t.Fatalf("%s is not JSON: %v", path, err)
	}
	return doc
}

// without returns m less the keys named, a copy.
func without(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if !slices.Contains(keys, k) {
			out[k] = v
		}
	}
	return out
}

func objectAt(t *testing.T, doc map[string]any, path ...string) map[string]any {
	t.Helper()
	var v any = doc
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok || m[k] == nil {
			t.Fatalf("no %s in\n%v", strings.Join(path, "."), doc)
		}
		v = m[k]
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %v", strings.Join(path, "."), v)
	}
	return m
}

func checkCloudFormationLedger(t *testing.T, root string) {
	stack := readJSON(t, filepath.Join(root, "dist/stack/cloudformation/cloudformation-stack.json"))
	ledger := objectAt(t, stack, "Resources", "Ledger")
	if ledger["Type"] != "AWS::Example::Ledger" {
		t.Errorf("the resource is of the type %v", ledger["Type"])
	}
	want := map[string]any{
		"Capacity":       map[string]any{"Mode": "STANDARD", "ReadUnits": float64(5)},
		"Identifier":     "orders-ledger",
		"KmsKeyId":       "key-1",
		"LedgerName":     "orders",
		"MasterPassword": map[string]any{"Ref": "LedgerMasterPassword"},
		"SubnetGroup":    "subnets-a",
	}
	if got := objectAt(t, ledger, "Properties"); !reflect.DeepEqual(got, want) {
		t.Errorf("the resource's properties are\n%v\nwant\n%v", got, want)
	}
	if got := objectAt(t, stack, "Parameters", "LedgerMasterPassword"); !reflect.DeepEqual(got,
		map[string]any{"NoEcho": true, "Type": "String"}) {
		t.Errorf("the secret's parameter is %v", got)
	}
	outputs := objectAt(t, stack, "Outputs")
	for _, name := range []string{"ledgerXXarn", "ledgerXXendpointXzuaddress", "ledgerXXendpointXzuport"} {
		if _, ok := outputs[name]; !ok {
			t.Errorf("the stack publishes no output %s; it publishes %v", name, outputs)
		}
	}
	if got := objectAt(t, outputs, "ledgerXXarn", "Value"); !reflect.DeepEqual(got,
		map[string]any{"Fn::GetAtt": []any{"Ledger", "Arn"}}) {
		t.Errorf("the arn output reads %v", got)
	}
}

var moduleArgument = regexp.MustCompile(`(?m)^\s+(\w+)\s+=`)

func checkTerraformLedger(t *testing.T, root string) {
	config := string(mustReadFile(t, filepath.Join(root, "dist/stack/terraform/aggrega.modules.tf")))
	_, rest, ok := strings.Cut(config, `module "ledger" {`)
	if !ok {
		t.Fatalf("the root configuration holds no module block of the component:\n%s", config)
	}
	block, _, _ := strings.Cut(rest, "\n}")
	var args []string
	for _, m := range moduleArgument.FindAllStringSubmatch(block, -1) {
		args = append(args, m[1])
	}
	if want := []string{"source", "enabled", "name", "retention"}; !slices.Equal(args, want) {
		t.Errorf("the module block sets %v, want %v:\n%s", args, want, block)
	}
	for _, line := range []string{`name      = "orders"`, `enabled   = true`,
		`retention = [{"archive":{"target":"cold"},"days":30}]`} {
		if !strings.Contains(block, line) {
			t.Errorf("the module block lacks %q:\n%s", line, block)
		}
	}
	drafted := mustReadFile(t, filepath.Join(root, "catalog/recipe/terraform/ex-ledger/0.1.0/module/main.tf.json"))
	if vendored := mustReadFile(t, filepath.Join(root, "dist/stack/terraform/modules/ledger/main.tf.json")); string(vendored) != string(drafted) {
		t.Errorf("the vendored module differs from the drafted one")
	}
}

func checkArmVault(t *testing.T, root string) {
	template := readJSON(t, filepath.Join(root, "dist/stack/arm/"+"arm-template.json"))
	vault := objectAt(t, template, "resources", "Vault")
	if vault["type"] != "Microsoft.Example/vaults" || vault["apiVersion"] != "2025-01-01" {
		t.Errorf("the resource is %v at %v", vault["type"], vault["apiVersion"])
	}
	want := map[string]any{
		"apiVersion": "2025-01-01",
		"location":   "westeurope",
		"name":       "orders-vault",
		"properties": map[string]any{
			"adminPassword":        "[parameters('VaultAdminPassword')]",
			"enabledForDeployment": true,
			"tenantId":             "11111111-2222-3333-4444-555555555555",
		},
		"sku":  map[string]any{"maxCapacity": float64(3), "name": "Standard"},
		"type": "Microsoft.Example/vaults",
	}
	if got := without(vault, "metadata"); !reflect.DeepEqual(got, want) {
		t.Errorf("the resource is\n%v\nwant\n%v", got, want)
	}
	if got := objectAt(t, template, "parameters", "VaultAdminPassword"); !reflect.DeepEqual(got,
		map[string]any{"type": "securestring"}) {
		t.Errorf("the secret's parameter is %v", got)
	}
}

// TestResourceSchemaDraftBuildsCloudFormation, TestResourceSchemaDraftBuildsTerraform
// and TestResourceSchemaDraftBuildsArm import each format's fixture, build what is
// drafted with a hand-written Aggregate, Manifest and Release, and read the
// resource the build renders. Each runs the import and the build twice over fresh
// workspaces, which write the same drafts and the same output byte for byte.
func TestResourceSchemaDraftBuildsCloudFormation(t *testing.T) {
	runSchemaBuildCase(t, schemaBuildCases[0])
}
func TestResourceSchemaDraftBuildsTerraform(t *testing.T) { runSchemaBuildCase(t, schemaBuildCases[1]) }
func TestResourceSchemaDraftBuildsArm(t *testing.T)       { runSchemaBuildCase(t, schemaBuildCases[2]) }

func runSchemaBuildCase(t *testing.T, c schemaBuildCase) {
	carrySchemaImportersIsolated(t)
	// Not parallel: the carried set and captureStderr are process-wide, and the
	// build runs from the workspace's root.
	var trees []map[string]string
	for run := 0; run < 2; run++ {
		root := buildSchemaCase(t, c)
		c.check(t, root)
		tree := map[string]string{}
		for _, dir := range []string{"catalog", "dist"} {
			for path, body := range renderedTree(t, filepath.Join(root, dir)) {
				tree[dir+"/"+path] = body
			}
		}
		trees = append(trees, tree)
	}
	if !reflect.DeepEqual(trees[0], trees[1]) {
		for path, body := range trees[0] {
			if trees[1][path] != body {
				t.Errorf("%s differs between the two runs", path)
			}
		}
		t.Errorf("two imports and builds of one source wrote different files")
	}
}

// TestDraftedTerraformRecipeUnpinnedHaltsPull imports the Terraform fixture and
// pulls a Release building it without pinning the drafted Recipe: the pull halts
// under the unpinned-entity condition and its message names aggrega pin, the
// module's file, and the Recipe to run it on.
func TestDraftedTerraformRecipeUnpinnedHaltsPull(t *testing.T) {
	carrySchemaImportersIsolated(t)
	c := schemaBuildCases[1]
	root := schemaWorkspaceForCase(t, c)
	importSchemaFixture(t, root, c)
	t.Chdir(root)

	err := runDriftCommand(t, "pull", "--release", "release.yaml")
	if !errors.Is(err, recipepin.ErrEntityNotPinned()) {
		t.Fatalf("the pull of an unpinned drafted Recipe did not halt as an unpinned entity: %v", err)
	}
	shown := formatCompilerError(err)
	for _, want := range []string{"aggrega pin " + c.recipe, "module/main.tf.json"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the pull's refusal does not carry %q:\n%s", want, shown)
		}
	}
	mustRunDriftCommand(t, "the pin the pull names", "pin", c.recipe)
	mustRunDriftCommand(t, "the pull after it", "pull", "--release", "release.yaml")
}
