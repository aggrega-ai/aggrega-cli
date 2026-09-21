// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof of the CloudFormation provider against the sibling catalog,
// building the fragment-form mariadb-rds Recipe and a db-reader consumer through
// the real compiled Wasm guest and inspecting the emitted stack templates
// firsthand.
//
// The single-stack build is fully exercised: the fragment carries only a
// Resources block plus a provides block, the provider stamps the stack envelope
// and projects provides into Outputs under names scoped by the contributing
// node, and an intra-stack consumer receives the database host through the value
// pipe as a long-form Fn::GetAtt. The
// two-boundary build (an intra-stack edge in one stack, a cross-stack edge into
// another) is exercised end-to-end as well: the producer stack projects the
// database host into an Output carrying an Export.Name, and the consumer stack
// imports that same name via Fn::ImportValue — the export/import pair is a
// round-trip identity, proven against the real emitted bytes. Applying the
// producer Export and the consumer import onto the flow-style JSON stack is
// carried by the shared surgical codec the link pass uses.
//
// These tests depend on both aggrega-cli AND aggrega-hub submodules being
// initialized in the umbrella checkout (the catalog paths cross the submodule
// boundary into aggrega-hub). They skip when the sibling catalog is absent.

package compiler_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

const cfnHubCatalog = "../../../aggrega-hub/aggrega-catalog"

// cfnCatalogPaths are the entities the example consumes — the fragment-form
// mariadb-rds Recipe, the db-reader consumer Component and Recipe, the mariadb
// Component, and the engine-managed credential Feature plus its cloudformation
// FeatureRecipe that bind the mariadb Component's required slot all come from
// the sibling hub, so the example uses the catalog rather than duplicating any
// recipe body. That credential pair is the honest fill here: the mariadb-rds
// Recipe sets the provider's own managed-password directive, so RDS generates
// and rotates the password and the FeatureRecipe renders nothing.
//
// One entity is fixture-local: the cloudformation rendering Plugin, kept
// version-independent so the example needs no edit when a release tag changes
// (its guest still resolves from the binary's embed tier).
func cfnCatalogPaths() []string {
	return []string{
		cfnHubCatalog + "/component/core/datastore/mariadb/1.0.0/component.yaml",
		cfnHubCatalog + "/component/example/compute/db-reader/1.0.0/component.yaml",
		cfnHubCatalog + "/recipe/cloudformation/mariadb-rds/1.0.0/recipe.yaml",
		cfnHubCatalog + "/recipe/cloudformation/db-reader/1.0.0/recipe.yaml",
		cfnHubCatalog + "/feature/core/generated-secret/1.0.0/feature.yaml",
		cfnHubCatalog + "/feature-recipe/cloudformation/generated-secret/1.0.0/recipe.yaml",
		"testdata/cloudformation-two-boundary/catalog/cloudformation-renderer.plugin.yaml",
	}
}

func skipIfNoSiblingCatalog(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(cfnHubCatalog); err != nil {
		t.Skipf("sibling catalog not present at %s: %v", cfnHubCatalog, err)
	}
}

func cfnSingleStackInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-two-boundary/release-single-stack.yaml",
		CatalogPaths: cfnCatalogPaths(),
	}
}

func cfnTwoBoundaryInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-two-boundary/release.yaml",
		CatalogPaths: cfnCatalogPaths(),
	}
}

func cfnCrossAccountInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-two-boundary/release-cross-account.yaml",
		CatalogPaths: cfnCatalogPaths(),
	}
}

func cfnSameAccountDeclaredInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-two-boundary/release-same-account-declared.yaml",
		CatalogPaths: cfnCatalogPaths(),
	}
}

// TestCloudFormationFragment_IntraStackAndProjection builds the single-stack
// workspace end-to-end through the real cloudformation guest and asserts, from
// the emitted bytes, that the fragment-form Recipe assembled into a well-formed
// stack: the envelope is stamped, the provides block projects into long-form
// Outputs named for the contributing node and the entry, each node's resources
// carry the logical ids its Recipe derived from its instance name, and an
// intra-stack consumer's parameter value is the database's own !GetAtt-valued
// provides, value-piped and normalized to a long-form Fn::GetAtt.
func TestCloudFormationFragment_IntraStackAndProjection(t *testing.T) {
	in := cfnSingleStackInput(t)

	result := pipelinetest.Build(t, in)

	stack, raw := findCfnStack(t, result.Artifacts, "stack-a")
	t.Logf("assembled single stack template:\n%s", raw)

	// No unresolved cross-boundary link placeholder survives — none is stamped
	// on an intra-stack edge, so none may appear.
	for k, v := range result.Artifacts {
		if strings.Contains(string(v), rendercore.CrossBoundaryLinkSentinelPrefix) {
			t.Fatalf("artifact %q retains a cross-boundary link placeholder:\n%s", k, v)
		}
	}

	// Envelope: assemble stamped the format version even though the fragment
	// carried no envelope of its own.
	if stack["AWSTemplateFormatVersion"] != "2010-09-09" {
		t.Fatalf("AWSTemplateFormatVersion = %#v, want 2010-09-09", stack["AWSTemplateFormatVersion"])
	}

	// provides → Outputs projection: the fragment's provides block became stack
	// Outputs, with the short-form intrinsics expanded to long form.
	outputs := mustCfnMap(t, stack, "Outputs")
	hostOut := mustCfnMap(t, outputs, "dbXXhost")
	hostVal, ok := hostOut["Value"].(map[string]any)
	if !ok || hostVal["Fn::GetAtt"] == nil {
		t.Fatalf("Outputs.dbXXhost.Value is not an Fn::GetAtt intrinsic; got %#v", hostOut["Value"])
	}
	for _, want := range []string{"dbXXport", "dbXXprotocol"} {
		if _, present := outputs[want]; !present {
			t.Fatalf("Outputs missing projected provides entry %q; got %v", want, mapKeys(outputs))
		}
	}

	// Resources: the database and both consumers merged, each under the logical id
	// its Recipe derived from its own instance name — so the two consumers, which
	// share a Recipe, still describe two resources.
	resources := mustCfnMap(t, stack, "Resources")
	if _, ok := resources["Db"]; !ok {
		t.Fatalf("Resources missing the database resource Db; got %v", mapKeys(resources))
	}
	if _, ok := resources["AppA"]; !ok {
		t.Fatalf("Resources missing the first consumer resource AppA; got %v", mapKeys(resources))
	}
	if _, ok := resources["AppB"]; !ok {
		t.Fatalf("Resources missing the second consumer resource AppB; got %v", mapKeys(resources))
	}

	// Intra-stack reference: the consumer parameter value is the database's own
	// provides value, value-piped and normalized to a long-form Fn::GetAtt whose
	// first element is the producer logical id. No Export/import is involved —
	// the reference is same-stack.
	appA := mustCfnMap(t, resources, "AppA")
	appAProps := mustCfnMap(t, appA, "Properties")
	getAtt, ok := appAProps["Value"].(map[string]any)
	if !ok || getAtt["Fn::GetAtt"] == nil {
		t.Fatalf("consumer parameter Value is not an Fn::GetAtt intrinsic; got %#v", appAProps["Value"])
	}
	gaArr, ok := getAtt["Fn::GetAtt"].([]any)
	if !ok || len(gaArr) == 0 || gaArr[0] != "Db" {
		t.Fatalf("intra-stack Fn::GetAtt = %#v, want a reference to the Db logical id", getAtt["Fn::GetAtt"])
	}
	t.Logf("intra-stack consumer reference: Fn::GetAtt %v", gaArr)

	// Long-form intrinsics only — no short-form YAML tag survives.
	for _, tag := range []string{"!GetAtt", "!Ref", "!Sub", "!ImportValue"} {
		if strings.Contains(string(raw), tag) {
			t.Fatalf("a short-form intrinsic tag %q survived in the emitted output", tag)
		}
	}
}

// TestCloudFormationFragment_Deterministic asserts the single-stack build is
// byte-identical across repeated runs (the Deterministic Output invariant) — the
// always-on double-run guard for this end-to-end proof.
func TestCloudFormationFragment_Deterministic(t *testing.T) {
	in := cfnSingleStackInput(t)
	pipelinetest.AssertDeterministic(t, in)
}

// TestCloudFormationTwoBoundary_CrossStackWiring builds the two-boundary
// workspace — an intra-stack edge in the producer stack and a cross-stack edge
// into the consumer stack — end-to-end through the real cloudformation guest and
// proves the cross-stack wiring from the emitted bytes. The producer stack
// projects the database host into an Output that carries both its intra-stack
// Fn::GetAtt Value and an Export.Name; the consumer stack imports that exact name
// through Fn::ImportValue. The load-bearing assertion is the round-trip identity:
// the name the consumer imports equals the name the producer exports. The export
// name is checked structurally (a well-formed aggrega- name carrying the component
// segment and the provides-key segment) rather than pinned to a literal, so the
// proof survives later additions to the name's composition.
//
// No Recipe this build reaches authors an Outputs entry of its own, so the only
// export name in the emitted stacks is the one the link derives. That makes this
// the build with no authored export name at all, against which the authored-export
// cases next door vary exactly one thing.
func TestCloudFormationTwoBoundary_CrossStackWiring(t *testing.T) {
	in := cfnTwoBoundaryInput(t)

	result := pipelinetest.Build(t, in)

	producer, producerRaw := findCfnStack(t, result.Artifacts, "stack-a")
	consumer, consumerRaw := findCfnStack(t, result.Artifacts, "stack-b")
	t.Logf("producer stack template (stack-a):\n%s", producerRaw)
	t.Logf("consumer stack template (stack-b):\n%s", consumerRaw)

	// No cross-boundary link placeholder survives in any emitted artifact — every
	// stamped placeholder must have been resolved into a real Export/import pair.
	for k, v := range result.Artifacts {
		if strings.Contains(string(v), rendercore.CrossBoundaryLinkSentinelPrefix) {
			t.Fatalf("artifact %q retains a cross-boundary link placeholder:\n%s", k, v)
		}
	}

	// Producer: the database's host output carries BOTH the intra-stack Fn::GetAtt
	// projection as its Value AND an Export.Name that the consumer stack can
	// import. The output is named for the producing node and the entry together.
	producerOutputs := mustCfnMap(t, producer, "Outputs")
	hostOut := mustCfnMap(t, producerOutputs, "dbXXhost")
	hostVal, ok := hostOut["Value"].(map[string]any)
	if !ok || hostVal["Fn::GetAtt"] == nil {
		t.Fatalf("producer Outputs.dbXXhost.Value is not an Fn::GetAtt intrinsic; got %#v", hostOut["Value"])
	}
	exportBlock := mustCfnMap(t, hostOut, "Export")
	exportName, ok := exportBlock["Name"].(string)
	if !ok || exportName == "" {
		t.Fatalf("producer Outputs.dbXXhost.Export.Name is not a non-empty string; got %#v", exportBlock["Name"])
	}
	for _, seg := range []string{"aggrega-", "db", "host"} {
		if !strings.Contains(exportName, seg) {
			t.Fatalf("producer export name %q is missing the expected segment %q", exportName, seg)
		}
	}
	if !strings.HasPrefix(exportName, "aggrega-") {
		t.Fatalf("producer export name %q does not start with the aggrega- prefix", exportName)
	}
	// The export name carries the Release identity from the link payload — the
	// Release's metadata.name — as its scope segment, so it is no longer the
	// empty-segment placeholder. Distinct Releases sharing an account and region
	// therefore produce distinct export names.
	if !strings.Contains(exportName, "cloudformation-two-boundary") {
		t.Fatalf("producer export name %q is missing the Release identity segment %q", exportName, "cloudformation-two-boundary")
	}
	t.Logf("producer export name: %s", exportName)

	// Consumer: the consuming application's parameter Value is an Fn::ImportValue
	// object whose imported name EQUALS the producer export name — the round-trip
	// identity that wires the two stacks together.
	consumerResources := mustCfnMap(t, consumer, "Resources")
	appDbHost := mustCfnMap(t, consumerResources, "AppB")
	appProps := mustCfnMap(t, appDbHost, "Properties")
	importVal, ok := appProps["Value"].(map[string]any)
	if !ok || importVal["Fn::ImportValue"] == nil {
		t.Fatalf("consumer AppB.Properties.Value is not an Fn::ImportValue intrinsic; got %#v", appProps["Value"])
	}
	importName, ok := importVal["Fn::ImportValue"].(string)
	if !ok {
		t.Fatalf("consumer Fn::ImportValue is not a string; got %#v", importVal["Fn::ImportValue"])
	}
	if importName != exportName {
		t.Fatalf("cross-stack round-trip identity broken: consumer imports %q but producer exports %q", importName, exportName)
	}
	t.Logf("cross-stack round-trip identity holds: consumer Fn::ImportValue == producer Export.Name == %s", importName)

	// Long-form intrinsics only — no short-form YAML tag survives in either stack.
	for _, raw := range [][]byte{producerRaw, consumerRaw} {
		for _, tag := range []string{"!GetAtt", "!Ref", "!Sub", "!ImportValue"} {
			if strings.Contains(string(raw), tag) {
				t.Fatalf("a short-form intrinsic tag %q survived in the emitted output", tag)
			}
		}
	}
}

// TestCloudFormationTwoBoundary_Deterministic asserts the two-boundary build —
// including cross-stack Export/import wiring — is byte-identical across repeated
// runs (the Deterministic Output invariant).
func TestCloudFormationTwoBoundary_Deterministic(t *testing.T) {
	in := cfnTwoBoundaryInput(t)
	pipelinetest.AssertDeterministic(t, in)
}

// TestCloudFormationTwoBoundary_CrossAccountGetStackOutput builds the
// cross-account variant of the two-boundary workspace — the producer stack
// carries AWS placement annotations (account, region, assume-role) and the
// consuming stack sits in another account — end-to-end through the real
// cloudformation guest, and proves the cross-account wiring from the emitted
// bytes. Because the producer's placement marks a different account, the
// consumer's reference resolves through a cross-account stack-output read
// (Fn::GetStackOutput carrying the producer stack name, output name, region,
// and assume-role) rather than the same-account stack-export import
// (Fn::ImportValue) the sibling same-account build produces. The producer stack
// still projects the database host into an Output carrying an Export.Name — the
// publish adds the Export unconditionally — even though the cross-account
// consumer reads it by stack-output rather than by import. The stack name the
// consumer reads is checked for round-trip identity against the producer's
// export name structurally, so the proof survives later additions to the name's
// composition.
func TestCloudFormationTwoBoundary_CrossAccountGetStackOutput(t *testing.T) {
	in := cfnCrossAccountInput(t)

	result := pipelinetest.Build(t, in)

	producer, producerRaw := findCfnStack(t, result.Artifacts, "stack-a")
	consumer, consumerRaw := findCfnStack(t, result.Artifacts, "stack-b")
	t.Logf("producer stack template (stack-a):\n%s", producerRaw)
	t.Logf("consumer stack template (stack-b):\n%s", consumerRaw)

	// No cross-boundary link placeholder survives in any emitted artifact — every
	// stamped placeholder must have been resolved into a real cross-stack read.
	for k, v := range result.Artifacts {
		if strings.Contains(string(v), rendercore.CrossBoundaryLinkSentinelPrefix) {
			t.Fatalf("artifact %q retains a cross-boundary link placeholder:\n%s", k, v)
		}
	}

	// Consumer: the consuming application's parameter Value is an Fn::GetStackOutput
	// object — the cross-account read shape — and NOT an Fn::ImportValue import.
	consumerResources := mustCfnMap(t, consumer, "Resources")
	appDbHost := mustCfnMap(t, consumerResources, "AppB")
	appProps := mustCfnMap(t, appDbHost, "Properties")
	value, ok := appProps["Value"].(map[string]any)
	if !ok {
		t.Fatalf("consumer AppB.Properties.Value is not an object; got %#v", appProps["Value"])
	}
	if _, present := value["Fn::ImportValue"]; present {
		t.Fatalf("cross-account consumer value carries an Fn::ImportValue import; want Fn::GetStackOutput. got %#v", value)
	}
	getStackOutput, ok := value["Fn::GetStackOutput"].(map[string]any)
	if !ok {
		t.Fatalf("consumer AppB.Properties.Value is not an Fn::GetStackOutput intrinsic; got %#v", appProps["Value"])
	}

	// The read targets the injected provides key, in the producer's region, under
	// the producer's assume-role — all carried from the producer's placement
	// annotations, all deterministic pointer strings the runtime resolves.
	if getStackOutput["OutputName"] != "dbXXhost" {
		t.Fatalf("Fn::GetStackOutput.OutputName = %#v, want \"dbXXhost\"", getStackOutput["OutputName"])
	}
	if getStackOutput["Region"] != "eu-west-1" {
		t.Fatalf("Fn::GetStackOutput.Region = %#v, want \"eu-west-1\"", getStackOutput["Region"])
	}
	if getStackOutput["RoleArn"] != "arn:aws:iam::111111111111:role/cross-stack-reader" {
		t.Fatalf("Fn::GetStackOutput.RoleArn = %#v, want the producer assume-role arn", getStackOutput["RoleArn"])
	}
	stackName, ok := getStackOutput["StackName"].(string)
	if !ok || stackName == "" {
		t.Fatalf("Fn::GetStackOutput.StackName is not a non-empty string; got %#v", getStackOutput["StackName"])
	}
	t.Logf("cross-account read: Fn::GetStackOutput %#v", getStackOutput)

	// Producer: the database's host output STILL carries an Export.Name even on the
	// cross-account path — the publish adds the Export unconditionally.
	producerOutputs := mustCfnMap(t, producer, "Outputs")
	hostOut := mustCfnMap(t, producerOutputs, "dbXXhost")
	exportBlock := mustCfnMap(t, hostOut, "Export")
	exportName, ok := exportBlock["Name"].(string)
	if !ok || exportName == "" {
		t.Fatalf("producer Outputs.dbXXhost.Export.Name is not a non-empty string; got %#v", exportBlock["Name"])
	}
	t.Logf("producer export name (present even on the cross-account path): %s", exportName)

	// StackName round-trip identity: the export name the producer publishes and
	// the stack name the consumer reads share the producer stack's scope — the
	// export name is the stack name plus the component and provides-key segments.
	// The stack name is a well-formed aggrega- name carrying the Release
	// identity and the producer boundary segment.
	if !strings.HasPrefix(exportName, stackName+"-") {
		t.Fatalf("producer export name %q is not the consumer stack name %q plus the component and key segments", exportName, stackName)
	}
	if !strings.HasPrefix(stackName, "aggrega-") {
		t.Fatalf("consumer stack name %q does not start with the aggrega- prefix", stackName)
	}
	for _, seg := range []string{"cloudformation-cross-account", "stack-a"} {
		if !strings.Contains(stackName, seg) {
			t.Fatalf("consumer stack name %q is missing the expected segment %q", stackName, seg)
		}
	}
	t.Logf("cross-account round-trip identity holds: consumer reads stack %q; producer exports %q", stackName, exportName)

	// Long-form intrinsics only — no short-form YAML tag survives in either stack.
	for _, raw := range [][]byte{producerRaw, consumerRaw} {
		for _, tag := range []string{"!GetAtt", "!Ref", "!Sub", "!ImportValue"} {
			if strings.Contains(string(raw), tag) {
				t.Fatalf("a short-form intrinsic tag %q survived in the emitted output", tag)
			}
		}
	}
}

// TestCloudFormationTwoBoundary_CrossAccountDeterministic asserts the
// cross-account build — including the cross-account stack-output read wiring —
// is byte-identical across repeated runs (the Deterministic Output invariant).
func TestCloudFormationTwoBoundary_CrossAccountDeterministic(t *testing.T) {
	in := cfnCrossAccountInput(t)
	pipelinetest.AssertDeterministic(t, in)
}

// TestCloudFormationTwoBoundary_SameAccountDeclaredImportValue builds the
// same-account-declared variant of the two-boundary workspace — both stacks
// carry AWS placement annotations naming the same account and region —
// end-to-end through the real cloudformation guest, and proves the reference
// resolves to a same-account stack-export import from the emitted bytes. The
// producer declaring its placement does not on its own force a cross-account
// read: the two ends must declare placements that differ. Because both ends
// declare the same account and region, the consumer's reference is an
// Fn::ImportValue against the producer's Export.Name — the same round-trip
// identity the no-annotation same-account build produces, here reached from
// explicitly-declared matching placements rather than from absent ones.
func TestCloudFormationTwoBoundary_SameAccountDeclaredImportValue(t *testing.T) {
	in := cfnSameAccountDeclaredInput(t)

	result := pipelinetest.Build(t, in)

	producer, producerRaw := findCfnStack(t, result.Artifacts, "stack-a")
	consumer, consumerRaw := findCfnStack(t, result.Artifacts, "stack-b")
	t.Logf("producer stack template (stack-a):\n%s", producerRaw)
	t.Logf("consumer stack template (stack-b):\n%s", consumerRaw)

	// No cross-boundary link placeholder survives in any emitted artifact — every
	// stamped placeholder must have been resolved into a real Export/import pair.
	for k, v := range result.Artifacts {
		if strings.Contains(string(v), rendercore.CrossBoundaryLinkSentinelPrefix) {
			t.Fatalf("artifact %q retains a cross-boundary link placeholder:\n%s", k, v)
		}
	}

	// Producer: the database's host output carries an Export.Name the consumer
	// stack can import.
	producerOutputs := mustCfnMap(t, producer, "Outputs")
	hostOut := mustCfnMap(t, producerOutputs, "dbXXhost")
	exportBlock := mustCfnMap(t, hostOut, "Export")
	exportName, ok := exportBlock["Name"].(string)
	if !ok || exportName == "" {
		t.Fatalf("producer Outputs.dbXXhost.Export.Name is not a non-empty string; got %#v", exportBlock["Name"])
	}
	t.Logf("producer export name: %s", exportName)

	// Consumer: the consuming application's parameter Value is an Fn::ImportValue
	// import — the same-account shape — and NOT an Fn::GetStackOutput cross-account
	// read, even though the producer declares its placement. The imported name
	// equals the producer's Export.Name: the round-trip identity that wires the two
	// stacks.
	consumerResources := mustCfnMap(t, consumer, "Resources")
	appDbHost := mustCfnMap(t, consumerResources, "AppB")
	appProps := mustCfnMap(t, appDbHost, "Properties")
	value, ok := appProps["Value"].(map[string]any)
	if !ok {
		t.Fatalf("consumer AppB.Properties.Value is not an object; got %#v", appProps["Value"])
	}
	if _, present := value["Fn::GetStackOutput"]; present {
		t.Fatalf("same-account-declared consumer value carries an Fn::GetStackOutput read; want Fn::ImportValue. got %#v", value)
	}
	importName, ok := value["Fn::ImportValue"].(string)
	if !ok {
		t.Fatalf("consumer AppB.Properties.Value is not an Fn::ImportValue intrinsic; got %#v", appProps["Value"])
	}
	if importName != exportName {
		t.Fatalf("cross-stack round-trip identity broken: consumer imports %q but producer exports %q", importName, exportName)
	}
	t.Logf("same-account-declared round-trip identity holds: consumer Fn::ImportValue == producer Export.Name == %s", importName)

	// Long-form intrinsics only — no short-form YAML tag survives in either stack.
	for _, raw := range [][]byte{producerRaw, consumerRaw} {
		for _, tag := range []string{"!GetAtt", "!Ref", "!Sub", "!ImportValue"} {
			if strings.Contains(string(raw), tag) {
				t.Fatalf("a short-form intrinsic tag %q survived in the emitted output", tag)
			}
		}
	}
}

// TestCloudFormationTwoBoundary_SameAccountDeclaredDeterministic asserts the
// same-account-declared build is byte-identical across repeated runs (the
// Deterministic Output invariant).
func TestCloudFormationTwoBoundary_SameAccountDeclaredDeterministic(t *testing.T) {
	in := cfnSameAccountDeclaredInput(t)
	pipelinetest.AssertDeterministic(t, in)
}

// findCfnStack returns the assembled CloudFormation stack template for the
// boundary whose dist directory contains dirSegment, decoded as a generic JSON
// tree. It fails the test when no such artifact is present.
func findCfnStack(t *testing.T, artifacts map[string][]byte, dirSegment string) (map[string]any, []byte) {
	t.Helper()
	for k, v := range artifacts {
		if strings.Contains(k, dirSegment) && strings.HasSuffix(k, "cloudformation-stack.json") {
			var doc map[string]any
			if err := json.Unmarshal(v, &doc); err != nil {
				t.Fatalf("stack %q is not valid JSON: %v", k, err)
			}
			return doc, v
		}
	}
	keys := make([]string, 0, len(artifacts))
	for k := range artifacts {
		keys = append(keys, k)
	}
	t.Fatalf("no cloudformation stack template for boundary %q; got artifact keys %v", dirSegment, keys)
	return nil, nil
}

// mustCfnMap fetches key from m and asserts it is a JSON object.
func mustCfnMap(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("expected object at key %q; got %#v", key, m[key])
	}
	return v
}

// mapKeys returns the keys of a decoded JSON object for diagnostics.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
