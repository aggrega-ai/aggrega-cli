// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof of the CloudFormation provider against the sibling catalog,
// building the fragment-form mariadb-rds Recipe and a db-reader consumer through
// the real compiled Wasm guest and inspecting the emitted stack templates
// firsthand.
//
// The single-stack build is fully exercised: the fragment carries only a
// Resources block plus a provides block, the provider stamps the stack envelope
// and projects provides into Outputs, and an intra-stack consumer receives the
// database host through the value pipe as a long-form Fn::GetAtt. The
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

// cfnCatalogPaths are the shipped hub entities the example consumes — the
// fragment-form mariadb-rds Recipe, the db-reader consumer Component and Recipe,
// the mariadb Component, and the cloudformation rendering Plugin whose guest is
// resolved from the binary's embed tier — so the example uses the catalog rather
// than duplicating any recipe body.
func cfnCatalogPaths() []string {
	return []string{
		cfnHubCatalog + "/component/core/datastore/mariadb/1.0.0/component.yaml",
		cfnHubCatalog + "/component/example/compute/db-reader/1.0.0/component.yaml",
		cfnHubCatalog + "/recipe/cloudformation/mariadb-rds/1.0.0/recipe.yaml",
		cfnHubCatalog + "/recipe/cloudformation/db-reader/1.0.0/recipe.yaml",
		cfnHubCatalog + "/plugin/cloudformation-renderer/1.0.0/plugin.yaml",
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

// TestCloudFormationFragment_IntraStackAndProjection builds the single-stack
// workspace end-to-end through the real cloudformation guest and asserts, from
// the emitted bytes, that the fragment-form Recipe assembled into a well-formed
// stack: the envelope is stamped, the provides block projects into long-form
// Outputs, merged fragments carry collision-safe logical ids, and an intra-stack
// consumer's parameter value is the database's own !GetAtt-valued provides,
// value-piped and normalized to a long-form Fn::GetAtt.
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
	hostOut := mustCfnMap(t, outputs, "host")
	hostVal, ok := hostOut["Value"].(map[string]any)
	if !ok || hostVal["Fn::GetAtt"] == nil {
		t.Fatalf("Outputs.host.Value is not an Fn::GetAtt intrinsic; got %#v", hostOut["Value"])
	}
	for _, want := range []string{"port", "protocol"} {
		if _, present := outputs[want]; !present {
			t.Fatalf("Outputs missing projected provides key %q; got %v", want, mapKeys(outputs))
		}
	}

	// Resources: the database and both consumers merged, with the colliding
	// second consumer logical id disambiguated by a node-id-derived prefix.
	resources := mustCfnMap(t, stack, "Resources")
	if _, ok := resources["MariaDB"]; !ok {
		t.Fatalf("Resources missing the database resource MariaDB; got %v", mapKeys(resources))
	}
	if _, ok := resources["AppDbHostParameter"]; !ok {
		t.Fatalf("Resources missing the first consumer resource AppDbHostParameter; got %v", mapKeys(resources))
	}
	if _, ok := resources["appbAppDbHostParameter"]; !ok {
		t.Fatalf("colliding second consumer logical id was not disambiguated; got %v", mapKeys(resources))
	}

	// Intra-stack reference: the consumer parameter value is the database's own
	// provides value, value-piped and normalized to a long-form Fn::GetAtt whose
	// first element is the producer logical id. No Export/import is involved —
	// the reference is same-stack.
	appA := mustCfnMap(t, resources, "AppDbHostParameter")
	appAProps := mustCfnMap(t, appA, "Properties")
	getAtt, ok := appAProps["Value"].(map[string]any)
	if !ok || getAtt["Fn::GetAtt"] == nil {
		t.Fatalf("consumer parameter Value is not an Fn::GetAtt intrinsic; got %#v", appAProps["Value"])
	}
	gaArr, ok := getAtt["Fn::GetAtt"].([]any)
	if !ok || len(gaArr) == 0 || gaArr[0] != "MariaDB" {
		t.Fatalf("intra-stack Fn::GetAtt = %#v, want a reference to the MariaDB logical id", getAtt["Fn::GetAtt"])
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

	// Producer: Outputs.host carries BOTH the intra-stack Fn::GetAtt projection as
	// its Value AND an Export.Name that the consumer stack can import.
	producerOutputs := mustCfnMap(t, producer, "Outputs")
	hostOut := mustCfnMap(t, producerOutputs, "host")
	hostVal, ok := hostOut["Value"].(map[string]any)
	if !ok || hostVal["Fn::GetAtt"] == nil {
		t.Fatalf("producer Outputs.host.Value is not an Fn::GetAtt intrinsic; got %#v", hostOut["Value"])
	}
	exportBlock := mustCfnMap(t, hostOut, "Export")
	exportName, ok := exportBlock["Name"].(string)
	if !ok || exportName == "" {
		t.Fatalf("producer Outputs.host.Export.Name is not a non-empty string; got %#v", exportBlock["Name"])
	}
	for _, seg := range []string{"aggrega-", "db", "host"} {
		if !strings.Contains(exportName, seg) {
			t.Fatalf("producer export name %q is missing the expected segment %q", exportName, seg)
		}
	}
	if !strings.HasPrefix(exportName, "aggrega-") {
		t.Fatalf("producer export name %q does not start with the aggrega- prefix", exportName)
	}
	// The export name carries the deployment identity from the link payload — the
	// Release's metadata.name — as its scope segment, so it is no longer the
	// empty-segment placeholder. Distinct deployments sharing an account and region
	// therefore produce distinct export names.
	if !strings.Contains(exportName, "cloudformation-two-boundary") {
		t.Fatalf("producer export name %q is missing the deployment segment %q", exportName, "cloudformation-two-boundary")
	}
	t.Logf("producer export name: %s", exportName)

	// Consumer: AppDbHostParameter's Value is an Fn::ImportValue object whose
	// imported name EQUALS the producer export name — the round-trip identity that
	// wires the two stacks together.
	consumerResources := mustCfnMap(t, consumer, "Resources")
	appDbHost := mustCfnMap(t, consumerResources, "AppDbHostParameter")
	appProps := mustCfnMap(t, appDbHost, "Properties")
	importVal, ok := appProps["Value"].(map[string]any)
	if !ok || importVal["Fn::ImportValue"] == nil {
		t.Fatalf("consumer AppDbHostParameter.Properties.Value is not an Fn::ImportValue intrinsic; got %#v", appProps["Value"])
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
