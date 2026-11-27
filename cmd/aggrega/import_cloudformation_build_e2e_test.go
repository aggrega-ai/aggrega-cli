// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end proof, through the real command tree, that what `aggrega import`
// drafts from a CloudFormation template builds back into one: the template is
// imported as the cloudformation core importer the binary carries, naming no
// target, over a workspace whose catalog folder holds the standard catalog, and
// `aggrega pull`, `aggrega validate` and `aggrega build --verify-determinism`
// run over the drafts, none of which is edited. Two runs over the same template
// write byte-identical drafts and output.

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
)

// cfnBuildBackFixture is the committed template the cases import: a PostgreSQL
// instance the catalog maps, reading two exported values, a log group the
// operator owns, and an MSK cluster no row maps.
const cfnBuildBackFixture = "../../internal/importops/testdata/cloudformation/build-back"

// cfnImporterURI is the core importer the cases carry.
const cfnImporterURI = "importer/cloudformation"

// cfnBuildBack imports the template directory source at a fresh workspace and
// builds the drafts, returning the workspace's root. The import names no target,
// so it drafts for the target the importer declares.
func cfnBuildBack(t *testing.T, hub, source string) string {
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

// cfnDrafts reads every file an import of the template wrote beneath root that
// is not a copy of the standard catalog, by path.
func cfnDrafts(t *testing.T, root string) map[string]string {
	t.Helper()
	drafts := map[string]string{}
	for _, name := range []string{"aggregate.yaml", "release.yaml", "manifest.yaml"} {
		drafts[name] = string(mustReadFile(t, filepath.Join(root, name)))
	}
	for _, path := range []string{
		"catalog/component/imported/messaging/aws-msk-cluster/0.1.0/component.yaml",
		"catalog/recipe/cloudformation/aws-msk-cluster/0.1.0/recipe.yaml",
	} {
		drafts[path] = string(mustReadFile(t, filepath.Join(root, path)))
	}
	return drafts
}

// TestCloudFormationImportBuildsBack imports the build-back template with no
// target and builds the drafts: the stack template holds the PostgreSQL instance
// as its Recipe renders it, as OrdersDb, with the exports the source reads
// written as imports, and the drafted MSK cluster as the source writes it, and
// holds no resource for the log group the operator owns. Two imports and two
// builds over the template write byte-identical drafts and output.
func TestCloudFormationImportBuildsBack(t *testing.T) {
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	importopstest.CarryImporters(t, cfnImporterURI)
	isolateFromThisMachine(t)
	source, err := filepath.Abs(cfnBuildBackFixture)
	if err != nil {
		t.Fatal(err)
	}

	hub := siblingCatalogRootForExamples(t)
	first := cfnBuildBack(t, hub, armTemplateSource(t, source))
	template := mustReadFile(t, filepath.Join(first, "dist/cloudformation/cloudformation-stack.json"))
	var doc struct {
		Resources map[string]map[string]any `json:"Resources"`
	}
	if err := json.Unmarshal(template, &doc); err != nil {
		t.Fatalf("the stack template is not JSON: %v\n%s", err, template)
	}
	if got, want := sortedKeys(doc.Resources), []string{"Events", "OrdersDb"}; !slices.Equal(got, want) {
		t.Fatalf("the stack template deploys %v, want %v", got, want)
	}
	for _, absent := range []string{"AppLogs", "AWS::Logs::LogGroup"} {
		if strings.Contains(string(template), absent) {
			t.Errorf("the stack template holds %q, which nothing the source declares asks for", absent)
		}
	}

	db := doc.Resources["OrdersDb"]
	if db["Type"] != "AWS::RDS::DBInstance" || db["DeletionPolicy"] != "Snapshot" {
		t.Errorf("the instance is\n%v", db)
	}
	wantProps := map[string]any{
		"AllocatedStorage":         float64(20),
		"BackupRetentionPeriod":    float64(7),
		"DBInstanceClass":          "db.t4g.micro",
		"DBName":                   "orders",
		"DBSubnetGroupName":        map[string]any{"Fn::ImportValue": "shared-db-subnets"},
		"Engine":                   "postgres",
		"EngineVersion":            "16.15",
		"ManageMasterUserPassword": true,
		"MasterUsername":           "orders_app",
		"MultiAZ":                  false,
		"Port":                     float64(5432),
		"StorageType":              "gp3",
		"VPCSecurityGroups":        []any{map[string]any{"Fn::ImportValue": "shared-db-security-group"}},
	}
	if got := objectAt(t, db, "Properties"); !reflect.DeepEqual(got, wantProps) {
		t.Errorf("the instance's properties are\n%v\nwant\n%v", got, wantProps)
	}

	wantEvents := map[string]any{
		"BrokerNodeGroupInfo": map[string]any{
			"ClientSubnets": []any{"subnet-0a1b2c3d", "subnet-4e5f6a7b", "subnet-8c9d0e1f"},
			"InstanceType":  "kafka.m5.large",
		},
		"ClusterName":         "orders-events",
		"KafkaVersion":        "3.6.0",
		"NumberOfBrokerNodes": float64(3),
	}
	events := doc.Resources["Events"]
	if events["Type"] != "AWS::MSK::Cluster" || !reflect.DeepEqual(objectAt(t, events, "Properties"), wantEvents) {
		t.Errorf("the drafted resource is\n%v\nwant the cluster as the source writes it:\n%v", events, wantEvents)
	}

	second := cfnBuildBack(t, hub, armTemplateSource(t, source))
	if a, b := cfnDrafts(t, first), cfnDrafts(t, second); !reflect.DeepEqual(a, b) {
		t.Errorf("two imports of one template drafted different files:\n%v\nagainst\n%v", a, b)
	}
	if a, b := treeFingerprint(t, filepath.Join(first, "dist")), treeFingerprint(t, filepath.Join(second, "dist")); !reflect.DeepEqual(a, b) {
		t.Errorf("two builds of one template wrote different files")
	}
}
