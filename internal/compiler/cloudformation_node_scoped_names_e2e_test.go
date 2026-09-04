// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof that a CloudFormation stack keeps every node's names apart
// when several nodes of the same type assemble into one stack, built against the
// sibling catalog through the real compiled Wasm guest and asserted against the
// emitted bytes.
//
// Two shapes are covered. Three databases whose instance names differ only in
// punctuation prove that each declared database reaches the stack: a transform
// that merely drops the punctuation maps all three onto one name, so the stack
// would carry fewer resources than the deployment declared. Two databases of the
// same type read by two applications prove that each consumer's injected host
// references the database it names and that the stack outputs carry each
// database's connection identity separately.
//
// These tests depend on both aggrega-cli AND aggrega-hub submodules being
// initialized in the umbrella checkout (the catalog paths cross the submodule
// boundary into aggrega-hub). They skip when the sibling catalog is absent.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

func cfnPunctuationInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-node-scoped-names/release-punctuation.yaml",
		CatalogPaths: cfnCatalogPaths(),
	}
}

func cfnTwoProducersInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-node-scoped-names/release-two-producers.yaml",
		CatalogPaths: cfnCatalogPaths(),
	}
}

// cfnMixedEnginesCatalogPaths adds the postgres and mysql Components and their
// cloudformation Recipes to the shared set, so the two engines this workspace
// deploys resolve from the sibling catalog like every other entity here.
func cfnMixedEnginesCatalogPaths() []string {
	return append(cfnCatalogPaths(),
		cfnHubCatalog+"/component/core/datastore/postgres/1.0.0/component.yaml",
		cfnHubCatalog+"/component/core/datastore/mysql/1.0.0/component.yaml",
		cfnHubCatalog+"/recipe/cloudformation/postgres-rds/1.0.0/recipe.yaml",
		cfnHubCatalog+"/recipe/cloudformation/mysql-rds/1.0.0/recipe.yaml",
	)
}

func cfnMixedEnginesInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-node-scoped-names/release-mixed-engines.yaml",
		CatalogPaths: cfnMixedEnginesCatalogPaths(),
	}
}

// TestCloudFormationPunctuationOnlyNames_EveryDatabaseReachesTheStack builds
// three MariaDB databases whose instance names differ only in punctuation into
// one stack and asserts from the emitted bytes that all three are present under
// three distinct resource names. The count is the load-bearing assertion: three
// declared databases that render as two resources mean one deployment-declared
// database is absent from the template with nothing said about it.
func TestCloudFormationPunctuationOnlyNames_EveryDatabaseReachesTheStack(t *testing.T) {
	in := cfnPunctuationInput(t)

	result := pipelinetest.Build(t, in)

	stack, raw := findCfnStack(t, result.Artifacts, "stack-a")
	t.Logf("assembled stack template:\n%s", raw)

	resources := mustCfnMap(t, stack, "Resources")
	dbResources := make([]string, 0, len(resources))
	for name, res := range resources {
		entry, ok := res.(map[string]any)
		if !ok {
			t.Fatalf("Resources[%q] is not an object; got %#v", name, res)
		}
		if entry["Type"] == "AWS::RDS::DBInstance" {
			dbResources = append(dbResources, name)
		}
	}
	if len(dbResources) != 3 {
		t.Fatalf("three databases were declared but the stack carries %d database resource(s) %v; every resource names in full: %v",
			len(dbResources), dbResources, mapKeys(resources))
	}

	// Each database's own logical database name reaches the stack, so no
	// database's resource has been displaced by another's.
	for _, want := range []string{"aaadb", "payments", "paymentsflat"} {
		if !strings.Contains(string(raw), `"DBName": "`+want+`"`) {
			t.Fatalf("the stack carries no DBName %q, so that database's resource is missing from the template", want)
		}
	}
	t.Logf("three punctuation-only-distinct databases render as three resources: %v", dbResources)
}

// TestCloudFormationPunctuationOnlyNames_Deterministic asserts the
// punctuation-only build is byte-identical across repeated runs.
func TestCloudFormationPunctuationOnlyNames_Deterministic(t *testing.T) {
	in := cfnPunctuationInput(t)
	pipelinetest.AssertDeterministic(t, in)
}

// TestCloudFormationTwoProducers_EachConsumerAndOutputTracksItsOwnDatabase
// builds two same-type databases and two applications that each read one of
// them into a single stack, and asserts from the emitted bytes that both the
// injected host references and the stack outputs stay attached to the database
// each belongs to. Two properties are proven together because they share one
// cause: a consumer's Fn::GetAtt names its own database's resource, and each
// database's connection identity reaches the outputs under a name of its own
// rather than one output per provides key shared between the two.
func TestCloudFormationTwoProducers_EachConsumerAndOutputTracksItsOwnDatabase(t *testing.T) {
	in := cfnTwoProducersInput(t)

	result := pipelinetest.Build(t, in)

	stack, raw := findCfnStack(t, result.Artifacts, "stack-a")
	t.Logf("assembled stack template:\n%s", raw)

	resources := mustCfnMap(t, stack, "Resources")

	// Map each database resource name to the logical database name it creates,
	// so a consumer's reference can be traced back to a specific database.
	dbNameByResource := map[string]string{}
	for name, res := range resources {
		entry, ok := res.(map[string]any)
		if !ok || entry["Type"] != "AWS::RDS::DBInstance" {
			continue
		}
		props, ok := entry["Properties"].(map[string]any)
		if !ok {
			t.Fatalf("database resource %q carries no Properties object; got %#v", name, entry["Properties"])
		}
		dbName, ok := props["DBName"].(string)
		if !ok {
			t.Fatalf("database resource %q carries no DBName string; got %#v", name, props["DBName"])
		}
		dbNameByResource[name] = dbName
	}
	if len(dbNameByResource) != 2 {
		t.Fatalf("two databases were declared but the stack carries %d database resource(s) %v", len(dbNameByResource), mapKeys(resources))
	}

	// Each consumer's parameter value is an Fn::GetAtt naming a database
	// resource; the database it names must be the one the consumer targets.
	wantDBForParameter := map[string]string{
		"/orders-app/db-host":   "orders",
		"/payments-app/db-host": "payments",
	}
	seen := map[string]string{}
	for name, res := range resources {
		entry, ok := res.(map[string]any)
		if !ok || entry["Type"] != "AWS::SSM::Parameter" {
			continue
		}
		props, ok := entry["Properties"].(map[string]any)
		if !ok {
			t.Fatalf("consumer resource %q carries no Properties object; got %#v", name, entry["Properties"])
		}
		paramName, ok := props["Name"].(string)
		if !ok {
			t.Fatalf("consumer resource %q carries no Name string; got %#v", name, props["Name"])
		}
		value, ok := props["Value"].(map[string]any)
		if !ok {
			t.Fatalf("consumer resource %q value is not an intrinsic object; got %#v", name, props["Value"])
		}
		getAtt, ok := value["Fn::GetAtt"].([]any)
		if !ok || len(getAtt) == 0 {
			t.Fatalf("consumer resource %q value is not an Fn::GetAtt intrinsic; got %#v", name, props["Value"])
		}
		referenced, ok := getAtt[0].(string)
		if !ok {
			t.Fatalf("consumer resource %q Fn::GetAtt does not lead with a resource name; got %#v", name, getAtt)
		}
		seen[paramName] = dbNameByResource[referenced]
		t.Logf("consumer parameter %s references resource %s (database %q)", paramName, referenced, dbNameByResource[referenced])
	}
	if len(seen) != 2 {
		t.Fatalf("two consumers were declared but %d consumer resource(s) reached the stack: %v", len(seen), mapKeys(resources))
	}
	for paramName, wantDB := range wantDBForParameter {
		if got := seen[paramName]; got != wantDB {
			t.Fatalf("consumer %s reads database %q, but it targets the database named %q", paramName, got, wantDB)
		}
	}

	// Outputs: each database contributes its own connection identity. With two
	// same-type databases, one output per provides key can only describe one of
	// them, so the count of database-name outputs discriminates.
	outputs := mustCfnMap(t, stack, "Outputs")
	databaseValues := map[string]bool{}
	for name, out := range outputs {
		entry, ok := out.(map[string]any)
		if !ok {
			t.Fatalf("Outputs[%q] is not an object; got %#v", name, out)
		}
		if v, ok := entry["Value"].(string); ok {
			databaseValues[v] = true
		}
	}
	for _, want := range []string{"orders", "payments"} {
		if !databaseValues[want] {
			t.Fatalf("the stack outputs carry no entry whose value is the database name %q, so that database's identity is absent from the outputs; outputs present: %v",
				want, mapKeys(outputs))
		}
	}
	t.Logf("stack outputs: %v", mapKeys(outputs))
}

// TestCloudFormationTwoProducers_Deterministic asserts the two-producer build is
// byte-identical across repeated runs.
func TestCloudFormationTwoProducers_Deterministic(t *testing.T) {
	in := cfnTwoProducersInput(t)
	pipelinetest.AssertDeterministic(t, in)
}

// TestCloudFormationMixedEngines_EachEngineCarriesItsInstance builds a
// PostgreSQL database and a MySQL database into one stack and asserts from the
// emitted bytes that each engine's Recipe names its resource for its own
// instance and that each database's outputs carry that instance too. It covers
// the two remaining CloudFormation datastore Recipes, which no other end-to-end
// build reaches, so their rendering is proven rather than inferred from the
// Recipes reading alike.
func TestCloudFormationMixedEngines_EachEngineCarriesItsInstance(t *testing.T) {
	in := cfnMixedEnginesInput(t)

	result := pipelinetest.Build(t, in)

	stack, raw := findCfnStack(t, result.Artifacts, "stack-a")
	t.Logf("assembled stack template:\n%s", raw)

	resources := mustCfnMap(t, stack, "Resources")
	for name, wantEngine := range map[string]string{"ReportingPg": "postgres", "LedgerMy": "mysql"} {
		entry, ok := resources[name].(map[string]any)
		if !ok {
			t.Fatalf("Resources missing the resource %q its instance name derives; got %v", name, mapKeys(resources))
		}
		props, ok := entry["Properties"].(map[string]any)
		if !ok {
			t.Fatalf("resource %q carries no Properties object; got %#v", name, entry["Properties"])
		}
		if props["Engine"] != wantEngine {
			t.Fatalf("resource %q has Engine %#v, want %q — the two engines' Recipes have crossed", name, props["Engine"], wantEngine)
		}
	}

	outputs := mustCfnMap(t, stack, "Outputs")
	for _, want := range []string{"reportingXzhpgXXhost", "reportingXzhpgXXprotocol", "ledgerXzhmyXXhost", "ledgerXzhmyXXprotocol"} {
		if _, present := outputs[want]; !present {
			t.Fatalf("Outputs missing %q; got %v", want, mapKeys(outputs))
		}
	}
	t.Logf("mixed-engine stack resources %v, outputs %v", mapKeys(resources), mapKeys(outputs))
}

// TestCloudFormationMixedEngines_Deterministic asserts the mixed-engine build is
// byte-identical across repeated runs.
func TestCloudFormationMixedEngines_Deterministic(t *testing.T) {
	in := cfnMixedEnginesInput(t)
	pipelinetest.AssertDeterministic(t, in)
}

// TestCloudFormationClashingResourceNames_Halts builds two databases whose
// instance names differ only in which separator they use — payments-db and
// payments_db — into one stack. A CloudFormation resource name admits neither
// separator, so both instances name their resource identically, and the build has
// to refuse rather than emit one resource where two databases were declared.
//
// The refusal comes from the host comparing the names each provider declared it
// derived, so the assertion is against that halt specifically: a build that
// merely failed for some other reason would not prove the names were what
// stopped it. The message has to name the deployment's own words — both
// instances and the name they landed on — because renaming one of them is the
// author's remedy.
func TestCloudFormationClashingResourceNames_Halts(t *testing.T) {
	skipIfNoSiblingCatalog(t)
	in := pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-node-scoped-names/release-name-clash.yaml",
		CatalogPaths: cfnCatalogPaths(),
	}

	_, err := pipelinetest.BuildE(t, in)
	if err == nil {
		t.Fatal("two instances naming one resource built cleanly; want a halt naming the clash")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("build failed for a different reason than the name clash: %v", err)
	}
	// The halt carries one finding per contributor per colliding name, wrapped in
	// a report naming what they have in common. The report's own text is its
	// message alone, so the findings are read through the set it wraps rather than
	// searched for in the halt as a string.
	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	joined, isMulti := envelope.Wrap.(interface{ Unwrap() []error })
	if !isMulti {
		t.Fatalf("the report wraps a single error rather than the set the rule raised: %T", envelope.Wrap)
	}
	texts := make([]string, 0, len(joined.Unwrap()))
	for _, member := range joined.Unwrap() {
		texts = append(texts, member.Error())
	}
	whole := strings.Join(texts, "\n")
	for _, want := range []string{"PaymentsDb", "payments-db", "payments_db"} {
		if !strings.Contains(whole, want) {
			t.Fatalf("the halt does not name %q, so the author cannot tell which instances to rename: %v", want, texts)
		}
	}
	t.Logf("clashing resource names halt the build with %d finding(s): %v", len(texts), texts)
}
