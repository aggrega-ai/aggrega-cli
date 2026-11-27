// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// cfnMiniatureHub is the hub this directory's testdata holds for the
// cloudformation format: Components; postgres-rds, a Recipe writing its
// fragment inline with an overrides header; sql-server-rds, naming its engine
// through a local; redis-elasticache, inlining a
// parameter group and reading a credential slot; s3-object-store-amazon-s3
// and function-lambda, each linking a child to the instance, the second
// reading its overrides through a variable and a with block; http-api,
// writing several resources outside every branch, its provides naming one,
// and one inside a branch no rule reads; sqs, keyed by a variable bound to the
// logical ID; and, importing the miniature EcsFargateFragment, server-ecs at
// 1.1.0 beside an older 1.0.0 written inline, worker-ecs, task-ecs and
// acme-board-ecs, a container product naming its default image.
const cfnMiniatureHub = "testdata/cloudformation/hub"

// cfnFaults holds Recipes the miniature hub lacks, each carrying one fault.
const cfnFaults = "testdata/cloudformation/faults"

// cfnFormatOrFail returns the cloudformation format.
func cfnFormatOrFail(t *testing.T) format {
	t.Helper()
	f, ok := formatNamed("cloudformation")
	if !ok {
		t.Fatal("no format is named cloudformation")
	}
	return f
}

// cfnHubWith returns a copy of the miniature cloudformation hub holding each
// named Recipe of cfnFaults beside its own.
func cfnHubWith(t *testing.T, faults ...string) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, cfnMiniatureHub, root)
	for _, name := range faults {
		body, err := os.ReadFile(filepath.Join(cfnFaults, name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "aggrega-catalog", "recipe", "cloudformation", name, "1.0.0")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "recipe.yaml"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// cfnRowsOrFail generates the miniature hub's rows with an empty hand table,
// the format's hand Whens applied.
func cfnRowsOrFail(t *testing.T) ([]cloudimport.Row, []string) {
	t.Helper()
	rows, notes, err := generateFromHub(cfnMiniatureHub, cfnFormatOrFail(t), cloudimport.Table{})
	if err != nil {
		t.Fatalf("generate the miniature cloudformation hub's table: %v\n%s", err, strings.Join(notes, "\n"))
	}
	return rows, notes
}

// rowsOf returns the rows of the Recipe named recipe.
func rowsOf(rows []cloudimport.Row, recipe string) []cloudimport.Row {
	var out []cloudimport.Row
	for _, r := range rows {
		if r.Recipe == recipe {
			out = append(out, r)
		}
	}
	return out
}

// oneRowOf returns the one row of recipe.
func oneRowOf(t *testing.T, rows []cloudimport.Row, recipe string) cloudimport.Row {
	t.Helper()
	got := rowsOf(rows, recipe)
	if len(got) != 1 {
		t.Fatalf("the Recipe %s gives %d rows, want 1: %#v", recipe, len(got), got)
	}
	return got[0]
}

// placedAt is a placement of key from the property path from.
func placedAt(key string, from ...string) cloudimport.Placement {
	return cloudimport.Placement{From: from, Key: key}
}

// equal reports a field of a row that is not what it should be.
func equal[T any](t *testing.T, what string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s is\n%#v\nwant\n%#v", what, got, want)
	}
}

// TestCloudFormationExtractsAnInlineFragment reads the miniature postgres-rds
// and requires its row as its text writes it: the instance's type as the key;
// the engine, a discriminator written as a literal, as the When, and
// ManageMasterUserPassword, a literal setting, in no predicate; each property
// one action reads a value or an infra key at, bare or quoted, the quoted
// read as text; each override every branch of a property reads whole,
// DeletionPolicy among them; the address the provides publish; and no
// credential slot, since no property reads one.
func TestCloudFormationExtractsAnInlineFragment(t *testing.T) {
	t.Parallel()
	rows, _ := cfnRowsOrFail(t)
	r := oneRowOf(t, rows, "postgres-rds")
	equal(t, "the key", r.Key, "AWS::RDS::DBInstance")
	equal(t, "the Component", r.Component, "core/datastore/postgres")
	equal(t, "the form", r.Form, cloudimport.FormRaw)
	equal(t, "the When", r.When, []cloudimport.Pred{{Path: []string{"Properties", "Engine"}, Op: cloudimport.OpEquals,
		Value: "postgres"}})
	equal(t, "the values", r.Values, []cloudimport.Placement{
		{From: []string{"Properties", "DBName"}, Key: "database", Conv: "string"},
		{From: []string{"Properties", "MasterUsername"}, Key: "user", Conv: "string"},
	})
	equal(t, "the infra", r.Infra, []cloudimport.Placement{
		{From: []string{"Properties", "EngineVersion"}, Key: "engine_version", Conv: "string"},
		placedAt("port", "Properties", "Port"),
	})
	equal(t, "the overrides", r.Overrides, []cloudimport.Placement{
		placedAt("DeletionPolicy", "DeletionPolicy"),
		placedAt("DBInstanceClass", "Properties", "DBInstanceClass"),
		placedAt("MultiAZ", "Properties", "MultiAZ"),
		placedAt("DBSubnetGroupName", "Properties", "DBSubnetGroupName"),
		placedAt("VPCSecurityGroups", "Properties", "VPCSecurityGroups"),
	})
	equal(t, "the provides", r.Provides, []cloudimport.Placement{placedAt("host", "Endpoint", "Address")})
	equal(t, "the secrets", r.Secrets, []cloudimport.Placement(nil))
	equal(t, "the folds", r.Folds, []cloudimport.Fold(nil))
	equal(t, "the origin", r.Origin, "generated aggrega-catalog/recipe/cloudformation/postgres-rds/1.0.0/recipe.yaml")
}

// TestCloudFormationInlinesAndFolds requires the parameter group a replication
// group references to be read in place of the reference, its override placed
// beneath the referencing property, and the credential slot the AuthToken
// reads placed at that property; a bucket policy and a function version,
// each naming its parent, folded into it with what their lines place; and the
// task-ecs shape, whose service the fragment leaves out, to take its task
// definition as the instance and fold the schedule starting it.
func TestCloudFormationInlinesAndFolds(t *testing.T) {
	t.Parallel()
	rows, notes := cfnRowsOrFail(t)
	redis := oneRowOf(t, rows, "redis-elasticache")
	equal(t, "the redis overrides", redis.Overrides, []cloudimport.Placement{
		placedAt("CacheNodeType", "Properties", "CacheNodeType"),
		placedAt("CacheParameterGroupFamily", "Properties", "CacheParameterGroupName", "CacheParameterGroupFamily"),
	})
	equal(t, "the redis secrets", redis.Secrets, []cloudimport.Placement{placedAt("password", "Properties", "AuthToken")})
	equal(t, "the redis folds", redis.Folds, []cloudimport.Fold(nil))

	bucket := oneRowOf(t, rows, "s3-object-store-amazon-s3")
	equal(t, "the bucket's folds", bucket.Folds, []cloudimport.Fold{{Type: "AWS::S3::BucketPolicy",
		Key: []string{"infra", "overrides", "policy"}, From: []string{"Properties", "PolicyDocument"}}})
	equal(t, "the bucket's provides", bucket.Provides, []cloudimport.Placement{{Key: "bucket"}})

	function := oneRowOf(t, rows, "function-lambda")
	equal(t, "the function's folds", function.Folds, []cloudimport.Fold{{Type: "AWS::Lambda::Version",
		Key:  []string{"infra", "min_replicas"},
		From: []string{"Properties", "ProvisionedConcurrencyConfig", "ProvisionedConcurrentExecutions"}}})

	task := oneRowOf(t, rows, "task-ecs")
	equal(t, "the task's key", task.Key, "AWS::ECS::TaskDefinition")
	equal(t, "the task's folds", task.Folds, []cloudimport.Fold{{Type: "AWS::Scheduler::Schedule",
		Key: []string{"infra", "overrides", "Cluster"}, From: []string{"Properties", "Target", "Arn"}}})
	equal(t, "the task's overrides", task.Overrides, []cloudimport.Placement{
		placedAt("ExecutionRoleArn", "Properties", "ExecutionRoleArn")})
	for _, n := range notes {
		if strings.HasPrefix(n, "cloudformation/task-ecs:") && strings.Contains(n, "leaves it out") {
			t.Errorf("the task's schedule, linked to its instance, is left out: %s", n)
		}
	}
}

// TestCloudFormationReadsOverrideAliases requires an override read through a
// variable bound to infra.overrides — its member, toJson of it, optional of
// it with a default, and the dot of a with block bound to optional of it — to
// be placed at the property writing it, in a fragment a Recipe imports and in
// one it writes inline.
func TestCloudFormationReadsOverrideAliases(t *testing.T) {
	t.Parallel()
	rows, _ := cfnRowsOrFail(t)
	server := rowsOf(rows, "server-ecs")
	if len(server) == 0 {
		t.Fatal("server-ecs gives no row")
	}
	for _, r := range server {
		equal(t, "the server's overrides", r.Overrides, []cloudimport.Placement{
			placedAt("Cluster", "Properties", "Cluster"),
			placedAt("Subnets", "Properties", "NetworkConfiguration", "AwsvpcConfiguration", "Subnets"),
			placedAt("AssignPublicIp", "Properties", "NetworkConfiguration", "AwsvpcConfiguration", "AssignPublicIp"),
			placedAt("TargetGroupArn", "Properties", "LoadBalancers", "0", "TargetGroupArn"),
			placedAt("ExecutionRoleArn", "Properties", "TaskDefinition", "ExecutionRoleArn"),
		})
	}
	function := oneRowOf(t, rows, "function-lambda")
	equal(t, "the function's overrides", function.Overrides, []cloudimport.Placement{
		placedAt("Role", "Properties", "Role"),
		placedAt("Timeout", "Properties", "Timeout"),
	})
	equal(t, "the function's infra", function.Infra, []cloudimport.Placement{
		placedAt("image", "Properties", "Code", "ImageUri")})
}

// TestCloudFormationResolvesImportedFragments requires the Recipes importing
// the miniature EcsFargateFragment to give generated rows from the resolved
// fragment: the service as the instance, the task definition it references
// read in place of the reference, the provides each Recipe itself publishes
// of the service, and none where it publishes none. server-ecs and
// worker-ecs, which render one fragment, are told apart by the Whens the
// importer's table writes for them, and the container product, which names
// its default image, maps that image to itself.
func TestCloudFormationResolvesImportedFragments(t *testing.T) {
	t.Parallel()
	rows, _ := cfnRowsOrFail(t)
	server := rowsOf(rows, "server-ecs")
	var whens [][]cloudimport.Pred
	for _, r := range server {
		equal(t, "a server row's key", r.Key, "AWS::ECS::Service")
		equal(t, "a server row's origin", r.Origin,
			"generated aggrega-catalog/recipe/cloudformation/server-ecs/1.1.0/recipe.yaml")
		equal(t, "a server row's provides", r.Provides, []cloudimport.Placement{placedAt("name", "Name")})
		whens = append(whens, r.When)
	}
	loadBalancers := []string{"Properties", "LoadBalancers"}
	portMappings := []string{"Properties", "TaskDefinition", "ContainerDefinitions", "0", "PortMappings"}
	equal(t, "the server rows' Whens", whens, [][]cloudimport.Pred{
		{{Path: loadBalancers, Op: cloudimport.OpPresent}},
		{{Path: portMappings, Op: cloudimport.OpPresent}},
	})
	worker := oneRowOf(t, rows, "worker-ecs")
	equal(t, "the worker's When", worker.When, []cloudimport.Pred{{Path: loadBalancers, Op: cloudimport.OpAbsent},
		{Path: portMappings, Op: cloudimport.OpAbsent}})
	equal(t, "the worker's provides", worker.Provides, []cloudimport.Placement(nil))
	equal(t, "the worker's task definition override", worker.Overrides[len(worker.Overrides)-1],
		placedAt("ExecutionRoleArn", "Properties", "TaskDefinition", "ExecutionRoleArn"))

	product := oneRowOf(t, rows, "acme-board-ecs")
	equal(t, "the product's key", product.Key, "AWS::ECS::Service")
	equal(t, "the product's When", product.When, []cloudimport.Pred(nil))
	equal(t, "the product's Component", product.Component, "core/devtool/acme-board")
	images, err := generateImagesFromHub(cfnMiniatureHub, cfnFormatOrFail(t))
	if err != nil {
		t.Fatalf("generate the image rows: %v", err)
	}
	equal(t, "the image rows", images, []cloudimport.ImageRow{{Repository: "ghcr.io/acme/board",
		Component: "core/devtool/acme-board", Recipe: "acme-board-ecs", ImageKey: "image"}})

	f := cfnFormatOrFail(t)
	f.whens = nil
	_, _, err = generateFromHub(cfnMiniatureHub, f, cloudimport.Table{})
	var conflict *claimConflict
	if !errors.As(err, &conflict) || !strings.Contains(err.Error(), "cloudformation/server-ecs:1.1.0 implementing "+
		"core/compute/server") || !strings.Contains(err.Error(), "cloudformation/worker-ecs:1.0.0 implementing "+
		"core/compute/worker") {
		t.Errorf("without the Whens written by hand, server-ecs and worker-ecs are not refused as claiming one "+
			"key and When: %v", err)
	}
}

// TestCloudFormationPrimaryUnderTheExtendedRule requires the instance's
// resource to be the one keyed by a variable bound to the logical ID local or
// to capitalizedWords of the instance's name; a block a variable bound to true
// guards to be read as written and one bound to false as absent; of several
// resources written outside every branch, the one the provides reference to
// be the instance, the others it links folded; a resource inside a branch no
// rule reads to be left out of the row with a note naming it; and several the
// provides each reference to be refused, naming them.
func TestCloudFormationPrimaryUnderTheExtendedRule(t *testing.T) {
	t.Parallel()
	rows, notes := cfnRowsOrFail(t)
	equal(t, "the queue's key", oneRowOf(t, rows, "sqs").Key, "AWS::SQS::Queue")
	equal(t, "the worker's key", oneRowOf(t, rows, "worker-ecs").Key, "AWS::ECS::Service")
	equal(t, "the task's key", oneRowOf(t, rows, "task-ecs").Key, "AWS::ECS::TaskDefinition")

	api := oneRowOf(t, rows, "http-api")
	equal(t, "the API's key", api.Key, "AWS::ApiGatewayV2::Api")
	equal(t, "the API's When", api.When, []cloudimport.Pred{{Path: []string{"Properties", "ProtocolType"},
		Op: cloudimport.OpEquals, Value: "HTTP"}})
	equal(t, "the API's folds", api.Folds, []cloudimport.Fold{{Type: "AWS::ApiGatewayV2::Stage",
		From: []string{"Properties", "ApiId"}}})
	equal(t, "the API's provides", api.Provides, []cloudimport.Placement{placedAt("endpoint", "ApiEndpoint")})
	if !slices.ContainsFunc(notes, func(n string) bool {
		return strings.HasPrefix(n, "cloudformation/http-api:1.0.0: ") &&
			strings.Contains(n, "{{ $l }}DomainName (AWS::ApiGatewayV2::DomainName), written in a branch") &&
			strings.Contains(n, "the row leaves it out")
	}) {
		t.Errorf("no note names the domain name the API's row leaves out: %v", notes)
	}

	_, _, err := generateFromHub(cfnHubWith(t, "provides-name-two"), cfnFormatOrFail(t), cloudimport.Table{})
	var refused *refusedRecipes
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "the Recipe cloudformation/provides-name-two:1.0.0 "+
		"(aggrega-catalog/recipe/cloudformation/provides-name-two/1.0.0/recipe.yaml) writes {{ $l }}UserPool "+
		"(AWS::Cognito::UserPool) and {{ $l }}Client (AWS::Cognito::UserPoolClient) outside every branch") ||
		!strings.Contains(err.Error(), "its provides reference each of them") {
		t.Errorf("a fragment whose provides reference two resources is not refused naming them: %v", err)
	}
}

// TestCloudFormationRefusesWhatItCannotRead adds to the miniature hub one
// Recipe carrying one fault each and requires the generator to write no
// table, naming the Recipe and what to fix: an import the hub does not
// resolve, naming the reference; resources none of which the text says is the
// instance, naming them; a resource outside every branch no rule reads as part
// of the instance, naming it and its type; and two Recipes claiming one key
// and When for different Components, naming both.
func TestCloudFormationRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	var (
		imports  *importError
		refused  *refusedRecipes
		conflict *claimConflict
	)
	for _, c := range []struct {
		fault string
		as    any
		want  []string
	}{
		{"unresolvable-import", &imports, []string{"the Recipe cloudformation/unresolvable-import:1.0.0",
			"imports MissingFragment#/artifacts/Service, which this checkout does not resolve"}},
		{"ambiguous-primary", &refused, []string{"the Recipe cloudformation/ambiguous-primary:1.0.0",
			"writes {{ .locals.logical_id }}Queue (AWS::SQS::Queue) and {{ .locals.logical_id }}DeadLetters " +
				"(AWS::SQS::Queue) outside every branch",
			"its provides reference none of them"}},
		{"unplaced-resource", &refused, []string{"the Recipe cloudformation/unplaced-resource:1.0.0",
			"writes the resource {{ .locals.logical_id }}Alarm (AWS::CloudWatch::Alarm) outside every branch",
			"add the rule to the cfnrules package"}},
		{"rival-postgres", &conflict, []string{
			"cloudformation/postgres-rds:1.0.0 implementing core/datastore/postgres",
			"cloudformation/rival-postgres:1.0.0 implementing core/datastore/acme-sql"}},
	} {
		t.Run(c.fault, func(t *testing.T) {
			rows, _, err := generateFromHub(cfnHubWith(t, c.fault), cfnFormatOrFail(t), cloudimport.Table{})
			if rows != nil || err == nil || !errors.As(err, c.as) {
				t.Fatalf("the hub with %s generates %d rows and %T %v", c.fault, len(rows), err, err)
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal does not say %q: %v", w, err)
				}
			}
		})
	}
}

// TestCloudFormationReadsTheHighestVersionAlone requires the version of a
// Recipe an unversioned reference resolves to, the highest, to be read alone:
// server-ecs 1.0.0, written inline, gives no row, and the coverage check
// accounts for the name with no notImported entry.
func TestCloudFormationReadsTheHighestVersionAlone(t *testing.T) {
	t.Parallel()
	rows, notes := cfnRowsOrFail(t)
	for _, r := range rows {
		if strings.Contains(r.Origin, "/server-ecs/1.0.0/") {
			t.Errorf("server-ecs 1.0.0 gives a row: %#v", r)
		}
	}
	for _, n := range notes {
		if strings.Contains(n, "server-ecs:1.0.0") {
			t.Errorf("server-ecs 1.0.0 is read: %s", n)
		}
	}
	h, err := loadHub(cfnMiniatureHub, "cloudformation")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := unaccounted(h, cloudimport.Table{Generated: rows}, cfnFormatOrFail(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Errorf("the miniature hub's rows leave Recipes unaccounted for: %v", findings)
	}
}

// TestCloudFormationWhensForNoRowAreRefused requires a When the importer's
// table writes for a Recipe that gives no row to be refused, naming it.
func TestCloudFormationWhensForNoRowAreRefused(t *testing.T) {
	t.Parallel()
	f := cfnFormatOrFail(t)
	hand := f.whens()
	f.whens = func() map[string][][]cloudimport.Pred {
		out := map[string][][]cloudimport.Pred{"planted-needle": {{{Path: []string{"Properties", "X"},
			Op: cloudimport.OpPresent}}}}
		for k, v := range hand {
			out[k] = v
		}
		return out
	}
	_, _, err := generateFromHub(cfnMiniatureHub, f, cloudimport.Table{})
	var refused *refusedRecipes
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "writes Whens for the Recipe "+
		"cloudformation/planted-needle, which gives no row") {
		t.Errorf("a When for a Recipe giving no row is not refused naming it: %v", err)
	}
}

// TestCloudFormationListsTheFeatureEachSlotAdmits requires the feature type a
// Recipe's assertions admit at a credential slot alone — read off the type as
// written or before its first colon — to be listed by Recipe and slot.
func TestCloudFormationListsTheFeatureEachSlotAdmits(t *testing.T) {
	t.Parallel()
	slots, err := generateSlotFeaturesFromHub(cfnMiniatureHub, cfnFormatOrFail(t), cloudimport.Table{})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "the slot features", slots, []slotFeature{
		{recipe: "postgres-rds", slot: "password", feature: "core/generated-secret"},
		{recipe: "redis-elasticache", slot: "password", feature: "core/random-password"},
	})
	listed, err := generateSlotFeaturesFromHub(cfnMiniatureHub, cfnFormatOrFail(t), cloudimport.Table{
		NotImported: []cloudimport.NotImported{{Recipe: "postgres-rds", Reason: "planted"}}})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "the slot features beside a notImported entry", listed, slots[1:])
}

// TestTheCloudFormationTableRendersFoldsAndSlots renders the miniature hub's
// table twice and requires one byte sequence, Go of the cfnimport package
// that parses, writing each fold and each slot feature.
func TestTheCloudFormationTableRendersFoldsAndSlots(t *testing.T) {
	t.Parallel()
	f := cfnFormatOrFail(t)
	rows, _ := cfnRowsOrFail(t)
	facts, err := generateFactsFromHub(cfnMiniatureHub, f, cloudimport.Table{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := renderTable(rows, nil, facts, "r", f)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	again, _ := cfnRowsOrFail(t)
	second, err := renderTable(again, nil, facts, "r", f)
	if err != nil {
		t.Fatalf("render again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two generations from one hub differ")
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "table_gen.go", first, parser.ParseComments)
	if err != nil {
		t.Fatalf("the rendered table does not parse: %v\n%s", err, first)
	}
	if parsed.Name.Name != "cfnimport" {
		t.Errorf("the rendered table is of package %s", parsed.Name.Name)
	}
	for _, want := range []string{
		`{Type: "AWS::S3::BucketPolicy", Key: []string{"infra", "overrides", "policy"}, ` +
			`From: []string{"Properties", "PolicyDocument"}}`,
		`{Recipe: "redis-elasticache", Slot: "password", Feature: "core/random-password"}`,
	} {
		if !bytes.Contains(first, []byte(want)) {
			t.Errorf("the rendered table writes no %s:\n%s", want, first)
		}
	}
}

// TestCloudFormationListsHowEachRecipeNamesItsInstance lists, of each Recipe
// keying the resource that is its instance by the instance's logical ID — the
// capitalised words of its key, written as that action, as the local
// logical_id holding it alone or through a variable bound to either — the
// literal text after it, and leaves out a Recipe whose local logical_id holds
// anything else; the Recipe of SQL Server takes the hand-written When of an
// engine of any edition.
func TestCloudFormationListsHowEachRecipeNamesItsInstance(t *testing.T) {
	t.Parallel()
	f := cfnFormatOrFail(t)
	facts, err := generateFactsFromHub(cfnMiniatureHub, f, cloudimport.Table{})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "the logical IDs", facts.ids, []logicalID{
		{recipe: "acme-board-ecs", suffix: ""},
		{recipe: "function-lambda", suffix: ""},
		{recipe: "http-api", suffix: "Api"},
		{recipe: "postgres-rds", suffix: ""},
		{recipe: "redis-elasticache", suffix: ""},
		{recipe: "s3-object-store-amazon-s3", suffix: ""},
		{recipe: "server-ecs", suffix: ""},
		{recipe: "sql-server-rds", suffix: ""},
		{recipe: "sqs", suffix: ""},
		{recipe: "task-ecs", suffix: "TaskDefinition"},
		{recipe: "worker-ecs", suffix: ""},
	})
	planted, err := generateFactsFromHub(cfnHubWith(t, "renamed-logical-id"), f, cloudimport.Table{})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "the logical IDs beside a Recipe renaming its resource", planted.ids, facts.ids)

	rows, _ := cfnRowsOrFail(t)
	equal(t, "the SQL Server row's When", oneRowOf(t, rows, "sql-server-rds").When, []cloudimport.Pred{
		{Path: []string{"Properties", "Engine"}, Op: cloudimport.OpPrefix, Value: "sqlserver-"}})
}
