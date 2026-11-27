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
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore/tfconfig"
)

// tfMiniatureHub is the hub this directory's testdata holds for the terraform
// format: Components; the pass-through module; postgres-rds, writing its
// instance in an artifact with an overrides header, an attribute set in each
// branch of a decision and replicas a range repeats, and mysql-rds beside it
// on the same type and another engine; rabbitmq-amazon-mq, a local module
// writing the one Amazon MQ broker of the hub; redis-elasticache, sourcing a
// published module by its repository, and the Cloud SQL Recipes, each sourcing
// a subdirectory of one; server-cloud-run, passing its input through the
// pass-through module to an ArtifactLibrary body and reading its valuesMapping
// from a MappingLibrary; server-ecs and task-ecs, each writing a task
// definition its instance references; and the FeatureRecipe generated-secret.
const tfMiniatureHub = "testdata/terraform/hub"

// tfFaults holds Recipes the miniature hub lacks, each carrying one fault.
const tfFaults = "testdata/terraform/faults"

// tfFormatOrFail returns the terraform format.
func tfFormatOrFail(t *testing.T) format {
	t.Helper()
	f, ok := formatNamed("terraform")
	if !ok {
		t.Fatal("no format is named terraform")
	}
	return f
}

// tfHubWith returns a copy of the miniature terraform hub holding each named
// Recipe of tfFaults beside its own.
func tfHubWith(t *testing.T, faults ...string) string {
	t.Helper()
	return hubWithFaults(t, tfMiniatureHub, tfFaults, "terraform", faults...)
}

// tfRowsOrFail generates the rows of the terraform hub at hub with an empty
// hand table.
func tfRowsOrFail(t *testing.T, hub string) ([]cloudimport.Row, []string) {
	t.Helper()
	rows, notes, err := generateFromHub(hub, tfFormatOrFail(t), cloudimport.Table{})
	if err != nil {
		t.Fatalf("generate the terraform table of %s: %v\n%s", hub, err, strings.Join(notes, "\n"))
	}
	return rows, notes
}

// noteOf returns the note the generator writes of the Recipe named recipe.
func noteOf(t *testing.T, notes []string, recipe string) string {
	t.Helper()
	for _, n := range notes {
		if strings.HasPrefix(n, "terraform/"+recipe+":") {
			return n
		}
	}
	t.Fatalf("no note names the Recipe %s: %v", recipe, notes)
	return ""
}

// TestTerraformMaskKeepsTheResourceShape masks an artifact and requires a line
// holding only actions, a comment among them, to be empty; every other
// action, inside a string or outside one, to be the identifier its number
// names, remembered by that number; a comment written beside other text to
// write nothing; a newline inside an action to be kept after its line, so
// every line keeps its number; and each line to know the branch and the range
// it is written in. The masked text parses as the configuration's blocks, and
// of an attribute each branch of a decision sets, the first is read.
func TestTerraformMaskKeepsTheResourceShape(t *testing.T) {
	t.Parallel()
	text := "{{- /* a comment */ -}}\n" +
		"{{- $n := 3 }}\n" +
		"resource \"aws_sqs_queue\" \"{{ .context.instance_id }}\" {\n" +
		"  name  = \"{{ .context.instance_id }}-queue\"\n" +
		"  delay = {{ .infra.delay }} {{/* seconds */}}\n" +
		"  {{- if .values.fifo }}\n" +
		"  fifo_queue = true\n" +
		"  {{- end }}\n" +
		"  tags = {{ toJson\n    .infra.tags }}\n" +
		"}\n" +
		"{{- range $i := until 2 }}\n" +
		"resource \"aws_sqs_queue\" \"dlq_{{ $i }}\" {}\n" +
		"{{- end }}\n"
	m := maskTemplate(text)
	want := "\n\n" +
		"resource \"aws_sqs_queue\" \"__aggrega_action_0__\" {\n" +
		"  name  = \"__aggrega_action_1__-queue\"\n" +
		"  delay = __aggrega_action_2__ \n" +
		"\n" +
		"  fifo_queue = true\n" +
		"\n" +
		"  tags = __aggrega_action_3__\n\n" +
		"}\n" +
		"\n" +
		"resource \"aws_sqs_queue\" \"dlq___aggrega_action_4__\" {}\n" +
		"\n"
	equal(t, "the masked text", m.text, want)
	equal(t, "the masked text's lines", strings.Count(m.text, "\n"), strings.Count(text, "\n"))
	var inner []string
	for _, a := range m.actions {
		inner = append(inner, normalize(a.inner))
		if !strings.Contains(text[a.start:a.end], a.inner) {
			t.Errorf("the action remembered at %d-%d is %q, which the text there does not hold", a.start, a.end,
				a.inner)
		}
	}
	equal(t, "the actions remembered", inner, []string{".context.instance_id", ".context.instance_id",
		".infra.delay", "toJson .infra.tags", "$i"})
	equal(t, "the state of the name's line", m.lineAt(4), lineState{})
	equal(t, "the state of the branch's line", m.lineAt(7), lineState{conditional: true})
	equal(t, "the state of the repeated resource's line", m.lineAt(13), lineState{conditional: true, repeated: true})

	file, diags, _ := tfconfig.ParseConfigFile("main.tf", []byte(m.text))
	if diags.HasErrors() {
		t.Fatalf("the masked text does not parse: %v", diags)
	}
	body := file.Body.(*hclsyntax.Body)
	var labels [][]string
	for _, b := range body.Blocks {
		labels = append(labels, b.Labels)
	}
	equal(t, "the resources' labels", labels, [][]string{{"aws_sqs_queue", "__aggrega_action_0__"},
		{"aws_sqs_queue", "dlq___aggrega_action_4__"}})

	twice := "resource \"aws_db_instance\" \"this\" {\n" +
		"  {{- if .infra.overrides.size }}\n" +
		"  size = {{ .infra.overrides.size }}\n" +
		"  {{- else }}\n" +
		"  size = 10\n" +
		"  {{- end }}\n" +
		"}\n"
	f, why := parseTerraformFile(fragment{name: "main.tf", text: twice}, true)
	if f == nil {
		t.Fatalf("an attribute set in each branch of a decision does not parse: %s", why)
	}
	size := f.body.Blocks[0].Body.Attributes["size"]
	if a, _, ok := f.masked(size.Expr); !ok || a.inner != ".infra.overrides.size" {
		t.Errorf("of an attribute set twice, the read one is %#v, not the first", size.Expr)
	}
}

// TestTerraformRawResourceRow reads the miniature postgres-rds and requires its
// row as its artifact writes it: the instance's type as the key — the replicas
// a range repeats, which reference it, neither the instance nor making it
// referenced, and left out with a note; the engine written as a literal as the
// When; each attribute one action reads a value or an infra key whole at,
// quoted read as text, trimmed of Gi read as gibibytes, and handed to toJson
// read as it is, the first of an attribute each branch sets read, and none
// read inside a with; each override the header names a path for; and the
// output the provides publish followed to the instance's attribute. mysql-rds
// beside it maps the same type for another engine. The table lists the
// feature the Recipe admits at its password slot and no logical ID.
func TestTerraformRawResourceRow(t *testing.T) {
	t.Parallel()
	rows, notes := tfRowsOrFail(t, tfMiniatureHub)
	r := oneRowOf(t, rows, "postgres-rds")
	equal(t, "the key", r.Key, "aws_db_instance")
	equal(t, "the Component", r.Component, "core/datastore/postgres")
	equal(t, "the form", r.Form, cloudimport.FormRaw)
	equal(t, "the When", r.When, []cloudimport.Pred{{Path: []string{"engine"}, Op: cloudimport.OpEquals,
		Value: "postgres"}})
	equal(t, "the values", r.Values, []cloudimport.Placement{
		{From: []string{"db_name"}, Key: "database", Conv: "string"},
		{From: []string{"username"}, Key: "user", Conv: "string"},
	})
	equal(t, "the infra", r.Infra, []cloudimport.Placement{
		{From: []string{"engine_version"}, Key: "engine_version", Conv: "string"},
		placedAt("port", "port"),
		{From: []string{"allocated_storage"}, Key: "storage.size", Conv: "unit:Gi"},
		placedAt("tags", "tags"),
	})
	equal(t, "the overrides", r.Overrides, []cloudimport.Placement{
		placedAt("instance_class", "instance_class"),
		placedAt("subnet_group", "db_subnet_group_name"),
	})
	equal(t, "the provides", r.Provides, []cloudimport.Placement{placedAt("host", "address")})
	equal(t, "the origin", r.Origin, "generated aggrega-catalog/recipe/terraform/postgres-rds/1.0.0/recipe.yaml")
	if n := noteOf(t, notes, "postgres-rds"); !strings.Contains(n, "the resource aws_db_instance.{{ "+
		".locals.db_identifier }}_replica_{{ $i }} (main.tf) is read as part of the instance by no read-in rule, so "+
		"the row leaves it out") {
		t.Errorf("no note names the replicas the row leaves out: %s", n)
	}

	mysql := oneRowOf(t, rows, "mysql-rds")
	equal(t, "the MySQL row's key and When", []any{mysql.Key, mysql.When}, []any{"aws_db_instance",
		[]cloudimport.Pred{{Path: []string{"engine"}, Op: cloudimport.OpEquals, Value: "mysql"}}})

	f := tfFormatOrFail(t)
	facts, err := generateFactsFromHub(tfMiniatureHub, f, cloudimport.Table{})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "the slot features", facts.slots, []slotFeature{{recipe: "postgres-rds", slot: "password",
		feature: "core/generated-secret"}})
	equal(t, "the logical IDs", facts.ids, []logicalID(nil))
	table, err := renderTable(rows, nil, facts, "r", f)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "table_gen.go", table, parser.ParseComments)
	if err != nil {
		t.Fatalf("the rendered table does not parse: %v\n%s", err, table)
	}
	if parsed.Name.Name != "tfimport" {
		t.Errorf("the rendered table is of package %s", parsed.Name.Name)
	}
	if !bytes.Contains(table, []byte(`{Recipe: "postgres-rds", Slot: "password", Feature: "core/generated-secret"}`)) ||
		bytes.Contains(table, []byte("generatedLogicalIDs")) {
		t.Errorf("the rendered table does not list the slot feature alone:\n%s", table)
	}
}

// TestTerraformGitModuleRow reads the miniature redis-elasticache, sourcing a
// published module by its repository, and requires a row of the module form
// keyed by the module's registry address: the engine its valuesMapping writes
// as a literal as the When, and another literal as none; each input one
// action reads a value or an infra key whole at, a nested input by its path;
// each override the header names an input for; and the output the provides
// publish in a branch of a decision.
func TestTerraformGitModuleRow(t *testing.T) {
	t.Parallel()
	rows, _ := tfRowsOrFail(t, tfMiniatureHub)
	r := oneRowOf(t, rows, "redis-elasticache")
	equal(t, "the key", r.Key, "terraform-aws-modules/elasticache/aws")
	equal(t, "the form", r.Form, cloudimport.FormModule)
	equal(t, "the When", r.When, []cloudimport.Pred{{Path: []string{"engine"}, Op: cloudimport.OpEquals,
		Value: "redis"}})
	equal(t, "the values", r.Values, []cloudimport.Placement{{From: []string{"maintenance", "window"},
		Key: "maintenance_window", Conv: "string"}})
	equal(t, "the infra", r.Infra, []cloudimport.Placement{
		{From: []string{"engine_version"}, Key: "engine_version", Conv: "string"},
		placedAt("port", "port"),
	})
	equal(t, "the overrides", r.Overrides, []cloudimport.Placement{
		placedAt("node_type", "node_type"),
		placedAt("subnet_group", "subnet_group_name"),
	})
	equal(t, "the provides", r.Provides, []cloudimport.Placement{placedAt("host", "primary_endpoint_address")})
}

// TestTerraformSubdirModuleRows reads the miniature Cloud SQL Recipes, each
// sourcing a subdirectory of one repository, and requires one row for each,
// keyed by the module's address and its subdirectory, the database version
// each writes as a literal giving no When.
func TestTerraformSubdirModuleRows(t *testing.T) {
	t.Parallel()
	rows, _ := tfRowsOrFail(t, tfMiniatureHub)
	for recipe, key := range map[string]string{
		"postgres-cloud-sql": "terraform-google-modules/sql-db/google//modules/postgresql",
		"mysql-cloud-sql":    "terraform-google-modules/sql-db/google//modules/mysql",
	} {
		r := oneRowOf(t, rows, recipe)
		equal(t, recipe+"'s key", r.Key, key)
		equal(t, recipe+"'s form", r.Form, cloudimport.FormModule)
		equal(t, recipe+"'s When", r.When, []cloudimport.Pred(nil))
		equal(t, recipe+"'s values", r.Values, []cloudimport.Placement{{From: []string{"db_name"}, Key: "database",
			Conv: "string"}})
		equal(t, recipe+"'s infra", r.Infra, []cloudimport.Placement{{From: []string{"disk_size"},
			Key: "storage.size", Conv: "unit:Gi"}})
		equal(t, recipe+"'s provides", r.Provides, []cloudimport.Placement{placedAt("host", "private_ip_address")})
	}
}

// TestTerraformLibraryBackedRow reads the miniature server-cloud-run, whose
// local module is the pass-through module and whose body and valuesMapping
// come from libraries, and requires the body's service as the instance, the
// secret it references left out with a note; the image the MappingLibrary
// entry reads placed where the body reads that member of the pass-through's
// input; each override the header passes a member of that input, placed
// where the body reads it whole, and the one it reads only inside an
// expression left out with a note; and each output of the body the provides
// read through the pass-through placed at the instance's attribute. Where the
// hub holds no pass-through module, or the Recipe's module differs from it,
// the body's outputs and its input are not followed. No container product
// row is written, as no Recipe names an image default.
func TestTerraformLibraryBackedRow(t *testing.T) {
	t.Parallel()
	rows, notes := tfRowsOrFail(t, tfMiniatureHub)
	r := oneRowOf(t, rows, "server-cloud-run")
	equal(t, "the key", r.Key, "google_cloud_run_v2_service")
	equal(t, "the form", r.Form, cloudimport.FormRaw)
	equal(t, "the infra", r.Infra, []cloudimport.Placement{{From: []string{"template", "0", "containers", "0",
		"image"}, Key: "image", Conv: "string"}})
	equal(t, "the overrides", r.Overrides, []cloudimport.Placement{
		placedAt("region", "location"),
		placedAt("service_account", "template", "0", "service_account"),
	})
	equal(t, "the provides", r.Provides, []cloudimport.Placement{placedAt("url", "uri"), placedAt("name", "name")})
	n := noteOf(t, notes, "server-cloud-run")
	for _, want := range []string{
		"the resource google_secret_manager_secret.file (service/main.tf) is read as part of the instance by no " +
			"read-in rule, so the row leaves it out",
		"the override ingress passes the module input config.ingress, which no attribute of the instance reads " +
			"whole, so the row leaves it out",
	} {
		if !strings.Contains(n, want) {
			t.Errorf("the note does not say %q: %s", want, n)
		}
	}
	images, err := generateImagesFromHub(tfMiniatureHub, tfFormatOrFail(t))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "the image rows", images, []cloudimport.ImageRow(nil))

	for _, c := range []struct {
		name  string
		edit  func(root string) error
		infra []cloudimport.Placement
	}{
		{"no pass-through module in the hub", func(root string) error {
			return os.Remove(filepath.Join(root, filepath.FromSlash(passThroughFile)))
		}, nil},
		{"a module other than the pass-through", func(root string) error {
			path := filepath.Join(root, "aggrega-catalog", "recipe", "terraform", "server-cloud-run", "1.0.0",
				"module", "main.tf")
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(path, append(body, '\n'), 0o644)
		}, nil},
	} {
		root := t.TempDir()
		copyTree(t, tfMiniatureHub, root)
		if err := c.edit(root); err != nil {
			t.Fatal(err)
		}
		rows, _ := tfRowsOrFail(t, root)
		r := oneRowOf(t, rows, "server-cloud-run")
		equal(t, c.name+": the provides", r.Provides, []cloudimport.Placement(nil))
		equal(t, c.name+": the infra", r.Infra, c.infra)
	}
}

// TestTerraformSeveralResourcesKeyedByTheUnreferencedOne requires, of the
// resources a Recipe writes, the one no other references to be the instance:
// the schedule task-ecs writes beside the task definition it starts, and the
// service server-ecs writes beside its task definition and log group; and a
// Recipe writing two resources of two types neither of which another
// references to give no row, the note naming both, and the coverage check to
// ask for it.
func TestTerraformSeveralResourcesKeyedByTheUnreferencedOne(t *testing.T) {
	t.Parallel()
	hub := tfHubWith(t, "two-unreferenced")
	rows, notes := tfRowsOrFail(t, hub)
	task := oneRowOf(t, rows, "task-ecs")
	equal(t, "the task's key", task.Key, "aws_scheduler_schedule")
	equal(t, "the task's values", task.Values, []cloudimport.Placement{{From: []string{"schedule_expression"},
		Key: "schedule", Conv: "string"}})
	equal(t, "the task's infra", task.Infra, []cloudimport.Placement{{From: []string{"target", "0",
		"ecs_parameters", "0", "task_definition_arn", "container_definitions", "0", "image"}, Key: "image",
		Conv: "string"}})
	equal(t, "the task's provides", task.Provides, []cloudimport.Placement(nil))
	equal(t, "the server's key", oneRowOf(t, rows, "server-ecs").Key, "aws_ecs_service")

	equal(t, "the rows of two-unreferenced", rowsOf(rows, "two-unreferenced"), []cloudimport.Row(nil))
	if n := noteOf(t, notes, "two-unreferenced"); !strings.Contains(n, "no row, since it writes "+
		"aws_apigatewayv2_route.this (main.tf) and aws_apigatewayv2_stage.this (main.tf), of 2 types no other "+
		"resource of the Recipe references") {
		t.Errorf("the note does not name the two resources: %s", n)
	}
	h, err := loadHub(hub, "terraform")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := unaccounted(h, cloudimport.Table{Generated: rows}, tfFormatOrFail(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(findings, "the Recipe terraform/two-unreferenced is reached by no row and listed in no "+
		"notImported entry") {
		t.Errorf("the coverage check does not ask for two-unreferenced: %v", findings)
	}
}

// TestTerraformDiscriminatorOnASingleRecipeKey requires the engine type the
// one Amazon MQ Recipe writes as a literal to be its When though no other
// Recipe maps the type, read through a local module, and a literal its
// valuesMapping passes to another attribute to be none; a Recipe the
// importer's table writes Whens for to give a row for each, after its own
// When; two Recipes left on one type and When to be refused, naming both,
// and the generator to write the table once notImported lists one of them.
func TestTerraformDiscriminatorOnASingleRecipeKey(t *testing.T) {
	t.Parallel()
	rows, _ := tfRowsOrFail(t, tfMiniatureHub)
	r := oneRowOf(t, rows, "rabbitmq-amazon-mq")
	equal(t, "the key", r.Key, "aws_mq_broker")
	equal(t, "the form", r.Form, cloudimport.FormRaw)
	equal(t, "the When", r.When, []cloudimport.Pred{{Path: []string{"engine_type"}, Op: cloudimport.OpEquals,
		Value: "RabbitMQ"}})
	equal(t, "the values", r.Values, []cloudimport.Placement{{From: []string{"user", "0", "username"},
		Key: "user", Conv: "string"}})
	equal(t, "the infra", r.Infra, []cloudimport.Placement{{From: []string{"engine_version"},
		Key: "engine_version", Conv: "string"}})
	equal(t, "the overrides", r.Overrides, []cloudimport.Placement{
		placedAt("instance_type", "host_instance_type"),
		placedAt("subnet_ids", "subnet_ids"),
		placedAt("deployment_mode", "deployment_mode"),
	})
	equal(t, "the provides", r.Provides, []cloudimport.Placement{placedAt("endpoint", "instances", "0",
		"endpoints", "0")})

	f := tfFormatOrFail(t)
	present := cloudimport.Pred{Path: []string{"storage_encrypted"}, Op: cloudimport.OpPresent}
	f.whens = func() map[string][][]cloudimport.Pred {
		return map[string][][]cloudimport.Pred{"mysql-rds": {{present}}}
	}
	told, _, err := generateFromHub(tfMiniatureHub, f, cloudimport.Table{})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "the When of mysql-rds with a When written for it", oneRowOf(t, told, "mysql-rds").When,
		[]cloudimport.Pred{{Path: []string{"engine"}, Op: cloudimport.OpEquals, Value: "mysql"}, present})

	hub := tfHubWith(t, "rival-postgres")
	_, _, err = generateFromHub(hub, tfFormatOrFail(t), cloudimport.Table{})
	var conflict *claimConflict
	if !errors.As(err, &conflict) || !strings.Contains(err.Error(), "terraform/postgres-rds:1.0.0 implementing "+
		"core/datastore/postgres") || !strings.Contains(err.Error(), "terraform/rival-postgres:1.0.0 implementing "+
		"core/datastore/acme-sql") {
		t.Errorf("two Recipes on aws_db_instance and the engine postgres are not refused naming both: %v", err)
	}
	listed, _, err := generateFromHub(hub, tfFormatOrFail(t), cloudimport.Table{NotImported: []cloudimport.NotImported{
		{Recipe: "rival-postgres", Reason: "planted"}}})
	if err != nil {
		t.Fatalf("with the rival listed in notImported the table is refused: %v", err)
	}
	equal(t, "the rows of the listed rival", rowsOf(listed, "rival-postgres"), []cloudimport.Row(nil))
}

// TestTerraformTemplateThatDoesNotParseContributesNoRow adds Recipes whose
// artifact does not parse — as a template, and as a configuration once its
// actions are masked — and requires the generator to write the table without
// a row of either, a note naming each and why, the coverage check asking
// for each; and the FeatureRecipe to be asked for alike.
func TestTerraformTemplateThatDoesNotParseContributesNoRow(t *testing.T) {
	t.Parallel()
	hub := tfHubWith(t, "unparsable-configuration", "unparsable-template")
	rows, notes := tfRowsOrFail(t, hub)
	for recipe, want := range map[string]string{
		"unparsable-configuration": "no row, since its file main.tf does not parse as a Terraform configuration " +
			"once its template actions are masked (main.tf:2,",
		"unparsable-template": "no row, since its file main.tf does not parse as a template",
	} {
		equal(t, "the rows of "+recipe, rowsOf(rows, recipe), []cloudimport.Row(nil))
		if n := noteOf(t, notes, recipe); !strings.Contains(n, want) {
			t.Errorf("the note of %s does not say %q: %s", recipe, want, n)
		}
	}
	h, err := loadHub(hub, "terraform")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := unaccounted(h, cloudimport.Table{Generated: rows}, tfFormatOrFail(t))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "the coverage findings", findings, []string{
		"the Recipe terraform/unparsable-configuration is reached by no row and listed in no notImported entry",
		"the Recipe terraform/unparsable-template is reached by no row and listed in no notImported entry",
		"the FeatureRecipe terraform/generated-secret is listed in no notImported entry",
	})
}

// TestTerraformUnresolvableImportRefused adds a Recipe importing a body no
// library of the hub holds and requires the generator to write no table,
// naming the Recipe and the reference.
func TestTerraformUnresolvableImportRefused(t *testing.T) {
	t.Parallel()
	rows, _, err := generateFromHub(tfHubWith(t, "unresolvable-import"), tfFormatOrFail(t), cloudimport.Table{})
	var imports *importError
	if rows != nil || !errors.As(err, &imports) {
		t.Fatalf("the hub with an unresolvable import generates %d rows and %T %v", len(rows), err, err)
	}
	for _, want := range []string{"the Recipe terraform/unresolvable-import:1.0.0",
		"imports MissingModule#/artifacts/Service, which this checkout does not resolve"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestTerraformPlacementReadInPlaceOfAReference reads the miniature
// server-ecs and requires the task definition its service references to be
// read in place of the reference: the image its encoded container
// definitions read, and the infra key its CPU reads, placed beneath the
// service's task_definition; the log group the task definition references,
// which no read-in rule reads, left out with a note naming it; and the
// service's own values and output placed as written.
func TestTerraformPlacementReadInPlaceOfAReference(t *testing.T) {
	t.Parallel()
	rows, notes := tfRowsOrFail(t, tfMiniatureHub)
	r := oneRowOf(t, rows, "server-ecs")
	equal(t, "the key", r.Key, "aws_ecs_service")
	equal(t, "the infra", r.Infra, []cloudimport.Placement{
		placedAt("replicas", "desired_count"),
		{From: []string{"task_definition", "cpu"}, Key: "compute.cpu", Conv: "string"},
		{From: []string{"task_definition", "container_definitions", "0", "image"}, Key: "image", Conv: "string"},
	})
	equal(t, "the provides", r.Provides, []cloudimport.Placement{placedAt("name", "name")})
	n := noteOf(t, notes, "server-ecs")
	if !strings.Contains(n, "the resource aws_cloudwatch_log_group.logs (main.tf) is read as part of the "+
		"instance by no read-in rule, so the row leaves it out") ||
		strings.Contains(n, "aws_ecs_task_definition") {
		t.Errorf("the note does not name the log group alone: %s", n)
	}
}
