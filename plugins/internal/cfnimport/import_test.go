// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// goldenView is the catalog view the imports of these tests read: each
// Component a row of the cloudformation table names, labelled as its row
// labels it, so a resource a row maps references the Component the row
// names.
func goldenView(t *testing.T) *aggrega.CatalogProjection {
	t.Helper()
	rows, err := Table().Rows()
	if err != nil {
		t.Fatal(err)
	}
	var out []aggrega.CatalogComponent
	for _, r := range rows {
		if slices.ContainsFunc(out, func(c aggrega.CatalogComponent) bool { return c.Name == r.Component }) {
			continue
		}
		labels := map[string]string{"component.aggrega.ai/category": r.Category}
		for k, v := range r.Traits {
			labels[k] = v
		}
		out = append(out, aggrega.CatalogComponent{Name: r.Component, Version: "1.0.0", Labels: labels})
	}
	slices.SortFunc(out, func(a, b aggrega.CatalogComponent) int { return strings.Compare(a.Name, b.Name) })
	return &aggrega.CatalogProjection{Components: out}
}

// importOf imports sources for the cloudformation target against goldenView,
// with the settings given.
func importOf(t *testing.T, settings json.RawMessage, sources ...aggrega.ImportSource) aggrega.ImportOutput {
	t.Helper()
	return Import(aggrega.ImportInput{Format: formatName, Target: target, Catalog: goldenView(t), Sources: sources,
		Settings: settings}, parse)
}

// importText imports the template text for the cloudformation target as
// template.yaml.
func importText(t *testing.T, text string) aggrega.ImportOutput {
	t.Helper()
	return importOf(t, nil, src("template.yaml", []byte(text)))
}

// goldenSources returns the sources of testdata/golden/<name>/source, each
// named by its path there.
func goldenSources(t *testing.T, name string) []aggrega.ImportSource {
	t.Helper()
	dir := filepath.Join("testdata", "golden", name, "source")
	var out []aggrega.ImportSource
	err := filepath.WalkDir(dir, func(file string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		out = append(out, src(filepath.ToSlash(rel), raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// goldenFiles returns what the golden of out holds: each file the import
// writes by its path, its diagnostics in diagnostics.txt and its decisions in
// decisions.txt.
func goldenFiles(out aggrega.ImportOutput) map[string]string {
	files := map[string]string{}
	for _, f := range out.Files {
		files[f.Path] = f.Content
	}
	var diags strings.Builder
	for _, d := range out.Diagnostics {
		fmt.Fprintf(&diags, "%s %s: %s\n", d.Severity, d.Code, d.Message)
	}
	files["diagnostics.txt"] = diags.String()
	var decisions strings.Builder
	for _, d := range out.Decisions {
		chosen := "nothing"
		if d.Chosen != nil {
			chosen = d.Chosen.Target + " (" + d.Chosen.Mode + ")"
		}
		fmt.Fprintf(&decisions, "%s (%s) -> %s: %s\n", d.SourceNode.ID, d.SourceNode.Type, chosen, d.Rationale)
	}
	files["decisions.txt"] = decisions.String()
	return files
}

// importGolden imports the sources of testdata/golden/<name>/source for the
// cloudformation target against goldenView, twice, failing unless both runs
// give one output, and compares each file the import writes, its diagnostics
// and its decisions with those under testdata/golden/<name>/want, no file
// missing and none left over. Run the tests with UPDATE_GOLDEN=1 to write
// them there instead.
func importGolden(t *testing.T, name string) aggrega.ImportOutput {
	t.Helper()
	sources := goldenSources(t, name)
	out := importOf(t, nil, sources...)
	if again := importOf(t, nil, sources...); fmt.Sprint(goldenFiles(again)) != fmt.Sprint(goldenFiles(out)) {
		t.Fatalf("two imports of %s differ", name)
	}
	compareGolden(t, name, goldenFiles(out))
	return out
}

// compareGolden compares got with the files under testdata/golden/<name>/want,
// or writes them there under UPDATE_GOLDEN=1.
func compareGolden(t *testing.T, name string, got map[string]string) {
	t.Helper()
	dir := filepath.Join("testdata", "golden", name, "want")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		for path, content := range got {
			file := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	held := map[string]bool{}
	err := filepath.WalkDir(dir, func(file string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}
		path := filepath.ToSlash(rel)
		held[path] = true
		want, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		content, written := got[path]
		switch {
		case !written:
			t.Errorf("%s holds %s, which the import does not write (run with UPDATE_GOLDEN=1 to rewrite it)", dir,
				path)
		case content != string(want):
			t.Errorf("%s differs from its golden in %s (run with UPDATE_GOLDEN=1 to rewrite it):\n%s", path, dir,
				content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read the golden %s: %v (run with UPDATE_GOLDEN=1 to write it)", dir, err)
	}
	for path := range got {
		if !held[path] {
			t.Errorf("the import writes %s, which %s does not hold (run with UPDATE_GOLDEN=1 to write it)", path, dir)
		}
	}
}

// fileText returns the content of the file of path out writes, failing where
// it writes none.
func fileText(t *testing.T, out aggrega.ImportOutput, path string) string {
	t.Helper()
	for _, f := range out.Files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("the import writes no %s; it reports %+v", path, out.Diagnostics)
	return ""
}

// holds fails unless text holds each of parts.
func holds(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("%s does not hold %q:\n%s", what, part, text)
		}
	}
}

// lacks fails where text holds any of parts.
func lacks(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if strings.Contains(text, part) {
			t.Errorf("%s holds %q:\n%s", what, part, text)
		}
	}
}

// coded returns the diagnostics of out of code.
func coded(out aggrega.ImportOutput, code cloudimport.Code) []aggrega.Diagnostic {
	var found []aggrega.Diagnostic
	for _, d := range out.Diagnostics {
		if d.Code == string(code) {
			found = append(found, d)
		}
	}
	return found
}

// decisionOn returns the decision on the node id, failing where out holds
// none.
func decisionOn(t *testing.T, out aggrega.ImportOutput, id string) aggrega.ImportDecision {
	t.Helper()
	for _, d := range out.Decisions {
		if d.SourceNode.ID == id {
			return d
		}
	}
	t.Fatalf("the import decides nothing on %s; it reports %+v", id, out.Diagnostics)
	return aggrega.ImportDecision{}
}

// TestCloudFormationImportGoldenRdsAndServer imports an RDS for PostgreSQL
// instance taking a parameter group and an Amazon ECS service whose task
// definition reads the instance's address into its environment, behind the
// operator's load balancer: the instance references the PostgreSQL Component,
// its storage in gibibytes and its deletion policy placed, its parameter group
// drafted from the source and the name it reads left for the operator; the
// service references the server Component, its task definition read in place
// of the reference, its main container's image, port and count placed, the
// address delivered on its connection, and the sidecar, the target group and
// the listener rule not carried; the cluster, log group, load balancer and
// listener are substrate.
func TestCloudFormationImportGoldenRdsAndServer(t *testing.T) {
	t.Parallel()
	out := importGolden(t, "rds-server")
	aggregate := fileText(t, out, "aggregate.yaml")
	holds(t, "the Aggregate", aggregate, "type: core/datastore/postgres", "type: core/compute/server",
		"type: imported/datastore/aws-rds-dbparametergroup", "- in: \"DB_HOST\"\n                  from: host")
	release := fileText(t, out, "release.yaml")
	holds(t, "the Release", release, "size: \"20Gi\"", "DeletionPolicy: \"Snapshot\"", "replicas: 2", "port: 8080",
		`image: "123456789012.dkr.ecr.us-east-1.amazonaws.com/orders-api:1.4.2"`, "UpdateReplacePolicy")
	if n := len(coded(out, cloudimport.CodeSubstrateLeftToOperator)); n != 1 {
		t.Errorf("the substrate is reported %d times: %+v", n, out.Diagnostics)
	}
}

// TestCloudFormationImportGoldenFormsAreOneTopology imports the topology of
// the RDS and server golden written in the YAML long form and in JSON, each
// handed under the golden source's name, and requires each import to write
// the golden's byte sequence.
func TestCloudFormationImportGoldenFormsAreOneTopology(t *testing.T) {
	t.Parallel()
	want := goldenFiles(importOf(t, nil, goldenSources(t, "rds-server")...))
	for _, form := range []string{"long.yaml", "template.json"} {
		got := goldenFiles(importOf(t, nil, src("template.yaml", fixture(t, "forms/"+form))))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			for path, content := range got {
				if content != want[path] {
					t.Errorf("the template written as %s imports %s otherwise:\n%s\nwant\n%s", form, path, content,
						want[path])
				}
			}
		}
	}
}

// TestCloudFormationImportGoldenSam imports an AWS SAM template: a function
// packaged as a .zip and one running an image, each mapped through the row
// derived for SAM's function, a simple table through the row derived for it,
// an HTTP API through the row derived from the HTTP API Recipe, its event
// drawn as a connection and not carried, and a REST API drafted from the
// source, as no Recipe deploys one.
func TestCloudFormationImportGoldenSam(t *testing.T) {
	t.Parallel()
	out := importGolden(t, "sam")
	aggregate := fileText(t, out, "aggregate.yaml")
	holds(t, "the Aggregate", aggregate, "type: core/compute/function", "type: core/datastore/dynamodb",
		"type: core/gateway/amazon-api-gateway", "type: imported/")
	holds(t, "the Release", fileText(t, out, "release.yaml"),
		`image: "123456789012.dkr.ecr.us-east-1.amazonaws.com/orders-report:2.0.1"`)
}

// TestCloudFormationImportGoldenDraftedFromSource imports a resource no row
// maps, drafted from the source, beside a function reading it.
func TestCloudFormationImportGoldenDraftedFromSource(t *testing.T) {
	t.Parallel()
	out := importGolden(t, "drafted-from-source")
	if n := len(coded(out, cloudimport.CodeResourceDraftedFromSource)); n != 1 {
		t.Errorf("%d resources are drafted from the source: %+v", n, out.Diagnostics)
	}
}

// TestCloudFormationImportGoldenNested imports a template deploying a local
// nested stack, whose queue and queue policy join the draft and whose output
// a function reads.
func TestCloudFormationImportGoldenNested(t *testing.T) {
	t.Parallel()
	out := importGolden(t, "nested")
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "type: core/messaging/sqs")
}

// TestCloudFormationImportGoldenCondition imports resources deployed under
// conditions: one on a boolean parameter, written as the instance's active,
// and one on another, not carried.
func TestCloudFormationImportGoldenCondition(t *testing.T) {
	t.Parallel()
	out := importGolden(t, "condition")
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "active: \"{{ .parameters.with_archive }}\"")
	if len(coded(out, cloudimport.CodeConditionNotCarried)) == 0 {
		t.Errorf("no condition is reported not carried: %+v", out.Diagnostics)
	}
}

// TestCloudFormationImportGoldenDynamicReference imports dynamic references:
// a Systems Manager parameter's, written whole as deploy-time text, and the
// secrets Secrets Manager and a SecureString parameter hold, each attached by
// reference at the slot reading it, never resolved.
func TestCloudFormationImportGoldenDynamicReference(t *testing.T) {
	t.Parallel()
	out := importGolden(t, "dynamic-reference")
	release := fileText(t, out, "release.yaml")
	holds(t, "the Release", release, "{{resolve:ssm:/orders/cache-node-type}}", `origin: "orders/cache"`,
		"type: core/referenced-secret")
}

// TestCloudFormationImportGoldenImportValue imports values another stack
// exports, each written whole as the one-line short form a catalog Recipe
// reads back as the function.
func TestCloudFormationImportGoldenImportValue(t *testing.T) {
	t.Parallel()
	out := importGolden(t, "import-value")
	holds(t, "the Release", fileText(t, out, "release.yaml"), `DBSubnetGroupName: "!ImportValue shared-db-subnets"`,
		`Cluster: "!ImportValue shared-cluster"`)
}

// generatedSecretText is a template whose secret Secrets Manager generates,
// read at the credential slot of each reader given, written as
// <logical ID>: <type> <engine property>: <engine>, <slot path>.
func generatedSecretText(name string, readers ...string) string {
	text := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Shared:
    Type: AWS::SecretsManager::Secret
    Properties:
` + name + `      GenerateSecretString:
        SecretStringTemplate: "{}"
        GenerateStringKey: password
        PasswordLength: 40
        ExcludePunctuation: true
`
	return text + strings.Join(readers, "")
}

// The readers of generatedSecretText: a RabbitMQ broker and a Redis
// replication group, each reading the secret at its credential slot.
const (
	brokerReader = `  Broker:
    Type: AWS::AmazonMQ::Broker
    Properties:
      EngineType: RABBITMQ
      Users: [{Username: app, Password: !Sub '{{resolve:secretsmanager:${Shared}:SecretString:password}}'}]
`
	cacheReader = `  Cache:
    Type: AWS::ElastiCache::ReplicationGroup
    DependsOn: Broker
    Properties:
      Engine: redis
      AuthToken: !Sub '{{resolve:secretsmanager:${Shared}:SecretString:password}}'
`
)

// TestGeneratedSecretBecomesRandomPassword attaches a secret Secrets Manager
// generates as core/random-password on the one instance reading it at a
// credential slot, its key, length, punctuation and name read from it; read
// by two, the first by instance order takes core/random-password, its name
// pinned, and the second the kit's referenced secret naming it, whose Recipe
// admits core/random-password alone at the slot, so its decision says the
// build halts there and names the fix. One core/random-password is drafted,
// and the secret is no instance of its own.
func TestGeneratedSecretBecomesRandomPassword(t *testing.T) {
	t.Parallel()
	t.Run("one reader", func(t *testing.T) {
		out := importText(t, generatedSecretText("      Name: orders/broker\n", brokerReader))
		holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), `    broker:
      type: core/messaging/rabbitmq
      features:
        password:
          type: core/random-password
          values:
            key: "password"
            length: 40
            symbols: false
            name: "orders/broker"
`)
		holds(t, "the decision on Shared", decisionOn(t, out, "Shared").Rationale, "attached: the format's attach "+
			"rule of AWS::SecretsManager::Secret when Properties.GenerateSecretString present writes it as "+
			"core/random-password on the instance broker at the slot password")
	})
	t.Run("two readers", func(t *testing.T) {
		out := importText(t, generatedSecretText("", brokerReader, cacheReader))
		aggregate, release := fileText(t, out, "aggregate.yaml"), fileText(t, out, "release.yaml")
		if n := strings.Count(aggregate, "type: core/random-password"); n != 1 {
			t.Errorf("the Aggregate attaches core/random-password %d times, want once:\n%s", n, aggregate)
		}
		holds(t, "the Aggregate", aggregate, `            name: "shared"`)
		holds(t, "the Release", release, `    cache:
      features:
        password:
          type: core/referenced-secret
          infra:
            # Attached by reference, never as a value: the secret Shared the source declares, which the instance broker attaches as core/random-password, at the locator the source states.
            origin: "shared"
`)
		holds(t, "the decision on Cache", decisionOn(t, out, "Cache").Rationale, "the Recipe redis-elasticache "+
			"admits only core/random-password at the slot password, where the instance takes core/referenced-secret, "+
			"so the build halts at that slot: give the instance a core/random-password of its own")
		lacks(t, "the decision on Broker", decisionOn(t, out, "Broker").Rationale, "halts")
		if strings.Contains(aggregate, "    shared:") || coded(out, cloudimport.CodeResourceDraftedFromSource) != nil {
			t.Errorf("the secret is drafted:\n%s", aggregate)
		}
	})
}

// TestSecretWithoutGeneratorIsReferenced attaches a secret holding a value
// the template writes as the kit's referenced secret on each reader, its
// origin the secret's name, or the kit's variable form where it names none;
// its SecretString is never written.
func TestSecretWithoutGeneratorIsReferenced(t *testing.T) {
	t.Parallel()
	const text = `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Kept:
    Type: AWS::SecretsManager::Secret
    Properties:
%s      SecretString: '{"password":"plain-text-value"}'
  Cache:
    Type: AWS::ElastiCache::ReplicationGroup
    Properties:
      Engine: valkey
      AuthToken: !Sub '{{resolve:secretsmanager:${Kept}:SecretString:password}}'
`
	for _, c := range []struct{ case_, name, origin string }{
		{"named", "      Name: orders/cache\n", `origin: "orders/cache"`},
		{"unnamed", "", `origin: "${Kept}"`},
	} {
		t.Run(c.case_, func(t *testing.T) {
			out := importText(t, strings.Replace(text, "%s", c.name, 1))
			release := fileText(t, out, "release.yaml")
			holds(t, "the Release", release, "    cache:\n      features:\n        password:\n          type: "+
				"core/referenced-secret\n", c.origin, "writes and the draft does not place: Properties.SecretString")
			for _, f := range goldenFiles(out) {
				lacks(t, "the import", f, "plain-text-value")
			}
		})
	}
}

// TestTargetGroupIsNotCarriedWhileNoPublicIngressIsHeld leaves a target group
// a service registers its tasks with, and the listener rule forwarding to it,
// not carried, as the hub holds no Feature realizing public ingress for
// Amazon ECS.
func TestTargetGroupIsNotCarriedWhileNoPublicIngressIsHeld(t *testing.T) {
	t.Parallel()
	out := importGolden(t, "rds-server")
	for _, id := range []string{"ApiTargets", "ApiRule"} {
		holds(t, "the decision on "+id, decisionOn(t, out, id).Rationale, "not carried: ", "the hub holds no "+
			"Feature realizing public ingress for Amazon ECS")
	}
	lacks(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "core/public-ingress")
}

// TestSsmParameterIsDraftedFromTheSource drafts a Systems Manager parameter a
// function reads from the source, its decision saying why: the catalog's
// shared configuration writes one parameter of the boundary and realizes
// none a template writes.
func TestSsmParameterIsDraftedFromTheSource(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Endpoint:
    Type: AWS::SSM::Parameter
    Properties: {Name: /orders/endpoint, Type: String, Value: https://orders.example}
  Reader:
    Type: AWS::Lambda::Function
    Properties:
      PackageType: Image
      Code: {ImageUri: 123456789012.dkr.ecr.us-east-1.amazonaws.com/reader:1}
      Environment: {Variables: {ENDPOINT_PARAMETER: !Ref Endpoint}}
`)
	d := decisionOn(t, out, "Endpoint")
	if d.Chosen == nil || d.Chosen.Mode != "draft" {
		t.Errorf("the parameter is decided %+v, want drafted from the source", d.Chosen)
	}
	holds(t, "the decision on Endpoint", d.Rationale, "core/shared-config on cloudformation writes one parameter "+
		"of the boundary, /<boundary>/shared-config holding tier=<infra.tier>, and realizes no parameter a "+
		"template writes")
}

// TestNoEchoFeedsParameterSecret attaches a NoEcho parameter read at a
// credential slot as the kit's form of a secret value for the cloudformation
// target, read from its variable, the drafted Manifest mapping no
// realization for it.
func TestNoEchoFeedsParameterSecret(t *testing.T) {
	t.Parallel()
	out := importText(t, noEchoCache)
	holds(t, "the Release", fileText(t, out, "release.yaml"), "    cache:\n      features:\n        password:\n"+
		"          type: core/secret-value\n", `origin: "${CachePassword}"`)
	lacks(t, "the Manifest", fileText(t, out, "manifest.yaml"), "secret")
}

// noEchoCache is a Redis replication group reading its token from a NoEcho
// parameter whose default the template writes.
const noEchoCache = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  CachePassword:
    Type: String
    NoEcho: true
    Default: noecho-default-value
Resources:
  Cache:
    Type: AWS::ElastiCache::ReplicationGroup
    Properties:
      Engine: redis
      AuthToken: !Ref CachePassword
`

// TestNoEchoDefaultIsNeverWritten writes a NoEcho parameter's default in no
// file, no decision and no diagnostic, a note saying so.
func TestNoEchoDefaultIsNeverWritten(t *testing.T) {
	t.Parallel()
	out := importText(t, noEchoCache)
	for path, content := range goldenFiles(out) {
		if strings.Contains(content, "noecho-default-value") {
			t.Errorf("%s writes the NoEcho default:\n%s", path, content)
		}
	}
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "# The template parameter CachePassword is "+
		"NoEcho, so the draft never writes its default.")
}

// TestSecretsManagerReferenceIsReferencedSecret attaches the secret a Secrets
// Manager dynamic reference reads at a credential slot as the kit's
// referenced secret, its origin the secret's name or ARN as the reference
// writes it; the secret an Amazon ECS container reads by ARN under Secrets,
// which no slot of the server takes, is named among what the instance's
// Release entry does not place, its ARN never resolved.
func TestSecretsManagerReferenceIsReferencedSecret(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:secretsmanager:eu-south-1:123456789012:secret:orders/search-AbCdEf"
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Search:
    Type: AWS::OpenSearchService::Domain
    Properties:
      AdvancedSecurityOptions:
        MasterUserOptions:
          MasterUserPassword: '{{resolve:secretsmanager:`+arn+`:SecretString:password}}'
  Api:
    Type: AWS::ECS::Service
    Properties:
      TaskDefinition: !Ref ApiTask
  ApiTask:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions:
        - Name: api
          Image: registry.example/api:1
          Secrets: [{Name: DB_PASSWORD, ValueFrom: `+arn+`}]
`)
	release := fileText(t, out, "release.yaml")
	holds(t, "the Release", release, "    search:\n      features:\n        password:\n          type: "+
		"core/referenced-secret\n", `origin: "`+arn+`"`, "Properties.TaskDefinition.ContainerDefinitions.0.Secrets")
}

// TestLiteralPasswordGoesThroughCarryCredentials judges a password the
// template writes as a literal at a credential slot by the empty_credentials
// setting, as the importer kit judges every credential, naming it and never
// quoting it.
func TestLiteralPasswordGoesThroughCarryCredentials(t *testing.T) {
	t.Parallel()
	text := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Broker:
    Type: AWS::AmazonMQ::Broker
    Properties:
      EngineType: RABBITMQ
      Users: [{Username: app, Password: literal-broker-password}]
`
	for _, c := range []struct {
		case_    string
		settings json.RawMessage
		code     cloudimport.Code
	}{
		{"carried", nil, cloudimport.Code("credential-carried")},
		{"emptied", json.RawMessage(`{"empty_credentials": "marked"}`), cloudimport.Code("credential-emptied")},
	} {
		t.Run(c.case_, func(t *testing.T) {
			out := importOf(t, c.settings, src("template.yaml", []byte(text)))
			if n := len(coded(out, c.code)); n != 1 {
				t.Errorf("the literal password is reported %s %d times: %+v", c.code, n, out.Diagnostics)
			}
			for path, content := range goldenFiles(out) {
				lacks(t, path, content, "literal-broker-password")
			}
		})
	}
}

// TestDeployTimeTextBuildsThroughADoubleQuotedPlacement draws, for an RDS
// instance mapped to postgres-rds whose template writes its subnet group as
// !Select [0, !GetAZs ""], a Release entry holding the one-line short form
// with its strings single-quoted, which the provider reads back as the
// intrinsic where the Recipe places it inside a double-quoted scalar; a value
// whose one-line form would need a double quote, a backslash or a line break
// is not carried, naming the property.
func TestDeployTimeTextBuildsThroughADoubleQuotedPlacement(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Db:
    Type: AWS::RDS::DBInstance
    Properties:
      Engine: postgres
      DBSubnetGroupName: !Select [0, !GetAZs ""]
      DBInstanceClass: !Select [0, !Split ["\\", !ImportValue shared-classes]]
`)
	release := fileText(t, out, "release.yaml")
	m := regexp.MustCompile(`(?m)^ +DBSubnetGroupName: (".*")$`).FindStringSubmatch(release)
	if m == nil {
		t.Fatalf("the Release holds no subnet group:\n%s", release)
	}
	text, err := strconv.Unquote(m[1])
	if err != nil || text != "!Select [0, !GetAZs '']" {
		t.Fatalf("the Release holds the subnet group %s (%v), want !Select [0, !GetAZs '']", m[1], err)
	}
	if got, want := readBack(t, `"`+text+`"`), map[string]any{"Fn::Select": []any{float64(0),
		map[string]any{"Fn::GetAZs": ""}}}; !reflect.DeepEqual(got, want) {
		t.Errorf("placed as postgres-rds places it, the subnet group reads back as %#v, want %#v", got, want)
	}
	at := 0
	for _, d := range coded(out, cloudimport.CodeValueNotCarried) {
		if d.SourceRef == "template.yaml#/Resources/Db/Properties/DBInstanceClass" {
			at++
		}
		if !strings.Contains(d.Message, "DBInstanceClass") {
			t.Errorf("a value-not-carried names no DBInstanceClass: %s", d.Message)
		}
	}
	if at != 1 {
		t.Errorf("the value no one line can hold is reported at its property %d times: %+v", at, out.Diagnostics)
	}
	lacks(t, "the Release", release, "DBInstanceClass")
}

// TestDeployTimeTextReadingAResourceTheBuildKeepsIsWritten writes deploy-time
// text reading a resource the stack the build writes declares under the
// logical ID the template gives it — a bucket its Recipe names by the
// capitalised words of its key, which give the ID back — and leaves text
// reading one the build names otherwise, naming what it owes.
func TestDeployTimeTextReadingAResourceTheBuildKeepsIsWritten(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Uploads:
    Type: AWS::S3::Bucket
  UploadsPolicy:
    Type: AWS::S3::BucketPolicy
    Properties:
      Bucket: !Ref Uploads
      PolicyDocument: {Statement: [{Effect: Deny, Action: s3:*, Resource: !Sub 'arn:${AWS::Partition}:s3:::${Uploads}/*'}]}
  DBArchive:
    Type: AWS::S3::Bucket
  DBArchivePolicy:
    Type: AWS::S3::BucketPolicy
    Properties:
      Bucket: !Ref DBArchive
      PolicyDocument: {Statement: [{Effect: Deny, Action: s3:*, Resource: !Sub 'arn:${AWS::Partition}:s3:::${DBArchive}/*'}]}
`)
	release := fileText(t, out, "release.yaml")
	holds(t, "the Release", release, `"Resource": "!Sub 'arn:${AWS::Partition}:s3:::${Uploads}/*'"`)
	lacks(t, "the Release", release, "${DBArchive}")
	if !slices.ContainsFunc(coded(out, cloudimport.CodeValueNotCarried), func(d aggrega.Diagnostic) bool {
		return strings.Contains(d.Message, "the resource DBArchive, which the drafted stack may declare under "+
			"another logical ID")
	}) {
		t.Errorf("the text reading DBArchive is not reported owed: %+v", out.Diagnostics)
	}
}

// TestInstanceCommentSaysHowTheBuildNamesTheResource writes, above an
// instance whose resource the stack the build writes names otherwise than
// the template does, the name it takes, as its Recipe names it, and why
// updating a deployed stack replaces it; above one keeping its name, nothing.
func TestInstanceCommentSaysHowTheBuildNamesTheResource(t *testing.T) {
	t.Parallel()
	aggregate := fileText(t, importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Orders:
    Type: AWS::SQS::Queue
  Ledger:
    Type: AWS::DynamoDB::Table
  DBStream:
    Type: AWS::MSK::Cluster
    Properties: {ClusterName: stream}
`), "aggregate.yaml")
	holds(t, "the Aggregate", aggregate, "    # The resource Orders.\n    # The stack the build writes names it "+
		"OrdersQueue, the capitalised words of its key followed by Queue, as the Recipe sqs names it, where the "+
		"template names it Orders, so updating a stack the template deployed replaces the resource.\n    orders:\n",
		"    # The resource DBStream.\n    # The stack the build writes names it DbStream, the capitalised words of "+
			"its key, where the template names it DBStream, so updating a stack the template deployed replaces the "+
			"resource.\n    db-stream:\n", "    # The resource Ledger.\n    ledger:\n")
}
