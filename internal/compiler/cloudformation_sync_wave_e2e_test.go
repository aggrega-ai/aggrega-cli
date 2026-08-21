// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a Release boundary's declared
// deployment wave orders provisioning in the CloudFormation stack template it
// assembles into, and that a build declaring no wave assembles the same bytes it
// assembled before the ordering existed.

package compiler_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// cfnSyncWaveGolden is the assembled stack a build declaring no wave produces. It
// is committed so the inert-when-unused claim is checked against bytes rather than
// against the same build's own output.
const cfnSyncWaveGolden = "testdata/cloudformation-sync-wave/golden/no-wave-stack.json"

// cfnSyncWaveBuild compiles one of the cloudformation-sync-wave fixture's Releases
// and returns the single assembled stack template its three boundaries collapse
// into, decoded and raw.
func cfnSyncWaveBuild(t *testing.T, releasePath string) (map[string]any, []byte) {
	t.Helper()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  releasePath,
		CatalogPaths: []string{"testdata/cloudformation-sync-wave/catalog/base"},
	})
	return findCfnStack(t, result.Artifacts, "stack")
}

// cfnDependsOn returns each resource's DependsOn entries, keyed by logical id,
// with a nil entry for a resource carrying none.
//
// It decodes the template rather than searching its text because a substring
// search cannot say which resource an occurrence belongs to, and the whole claim
// here is about which resource waits on which. It also preserves the emitted
// ORDER, because the merge's contract is that an authored entry keeps its place
// ahead of the wave's.
func cfnDependsOn(t *testing.T, stack map[string]any) map[string][]string {
	t.Helper()
	resources := mustCfnMap(t, stack, "Resources")
	out := map[string][]string{}
	for id, raw := range resources {
		body, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("resource %q is not an object; got %#v", id, raw)
		}
		switch v := body["DependsOn"].(type) {
		case nil:
			out[id] = nil
		case string:
			out[id] = []string{v}
		case []any:
			entries := make([]string, 0, len(v))
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					t.Fatalf("resource %q has a non-string DependsOn entry %#v", id, item)
				}
				entries = append(entries, s)
			}
			out[id] = entries
		default:
			t.Fatalf("resource %q DependsOn is neither a string nor a list; got %#v", id, v)
		}
	}
	return out
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCloudFormationSyncWave_DeclaredWaveOrdersProvisioning compiles a Release
// whose three boundaries share one directory — so their fragments assemble into a
// single stack template — at waves -1, 0 and 5, and asserts the emitted DependsOn
// expresses exactly that provisioning order.
//
// The waves are deliberately neither contiguous nor all non-negative, so the
// assertions below only hold if the chaining follows the POPULATED waves in order
// rather than counting from zero.
//
//   - the network's two resources are in the lowest wave and name nothing;
//   - the store's resources name BOTH of the network's, alphabetically — which is
//     what shows the fan-out reaching every resource of the preceding wave rather
//     than one of them;
//   - StorePolicy authored its own DependsOn on StoreBucket, and keeps it, ahead of
//     the wave's entries;
//   - the app names the store's two resources and NOT the network's, because the
//     ordering reaches the immediately-preceding populated wave only and
//     CloudFormation resolves the rest transitively.
func TestCloudFormationSyncWave_DeclaredWaveOrdersProvisioning(t *testing.T) {
	t.Parallel()
	stack, raw := cfnSyncWaveBuild(t, "testdata/cloudformation-sync-wave/release.yaml")
	deps := cfnDependsOn(t, stack)

	for _, id := range []string{"NetworkVpc", "NetworkSubnet", "StoreBucket", "StorePolicy", "AppParameter"} {
		if _, ok := deps[id]; !ok {
			t.Fatalf("resource %q missing from the assembled stack\n--- stack ---\n%s", id, raw)
		}
	}

	for _, id := range []string{"NetworkVpc", "NetworkSubnet"} {
		if len(deps[id]) != 0 {
			t.Errorf("the lowest wave must name nothing; %s's DependsOn = %v", id, deps[id])
		}
	}

	if got, want := deps["StoreBucket"], []string{"NetworkSubnet", "NetworkVpc"}; !equalStringSlice(got, want) {
		t.Errorf("StoreBucket's DependsOn = %v, want %v\n--- stack ---\n%s", got, want, raw)
	}
	if got, want := deps["StorePolicy"], []string{"StoreBucket", "NetworkSubnet", "NetworkVpc"}; !equalStringSlice(got, want) {
		t.Errorf("StorePolicy's DependsOn = %v, want the authored entry first then the wave's %v\n--- stack ---\n%s", got, want, raw)
	}
	if got, want := deps["AppParameter"], []string{"StoreBucket", "StorePolicy"}; !equalStringSlice(got, want) {
		t.Errorf("AppParameter's DependsOn = %v, want %v\n--- stack ---\n%s", got, want, raw)
	}
	for _, unwanted := range []string{"NetworkVpc", "NetworkSubnet"} {
		for _, got := range deps["AppParameter"] {
			if got == unwanted {
				t.Errorf("AppParameter names %s from two waves back; the ordering must chain through the wave between them", unwanted)
			}
		}
	}
}

// TestCloudFormationSyncWave_NoDeclaredWaveIsByteIdentical compiles the same three
// boundaries with no wave declared anywhere and asserts the assembled template is
// byte-for-byte the committed golden — the shape this fixture had before any wave
// ordering existed.
//
// 🔴 The golden comparison is ANTI-NEEDLED. A byte comparison against a golden
// passes trivially if the build produces nothing, or if the golden happens to
// already contain whatever the build emits, so the test first proves the SAME
// comparison FAILS for the wave-declaring build. Without that leg, a parity of two
// empty sets would pass and certify nothing.
func TestCloudFormationSyncWave_NoDeclaredWaveIsByteIdentical(t *testing.T) {
	t.Parallel()
	golden, err := os.ReadFile(cfnSyncWaveGolden)
	if err != nil {
		t.Fatalf("reading the golden: %v", err)
	}
	if len(golden) == 0 {
		t.Fatal("the golden is empty, so a comparison against it certifies nothing")
	}

	// The discriminating leg: the wave-declaring build must NOT match the golden.
	// If it did, the golden comparison below could not tell the ordering apart from
	// its absence.
	_, waved := cfnSyncWaveBuild(t, "testdata/cloudformation-sync-wave/release.yaml")
	if string(waved) == string(golden) {
		t.Fatal("the wave-declaring build matches the no-wave golden, so the comparison below cannot detect the ordering at all")
	}
	if !strings.Contains(string(waved), `"DependsOn"`) {
		t.Fatalf("the wave-declaring build emitted no DependsOn at all\n--- stack ---\n%s", waved)
	}

	_, plain := cfnSyncWaveBuild(t, "testdata/cloudformation-sync-wave/release-no-wave.yaml")
	if string(plain) != string(golden) {
		t.Errorf("a build declaring no wave changed the assembled stack\n--- got ---\n%s\n--- want ---\n%s", plain, golden)
	}

	// And the one DependsOn the fixture authors itself survives untouched, in the
	// authored single-id spelling rather than normalized into a list: with one wave
	// the ordering pass reaches nothing, so it must not reshape what it did not add.
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatalf("the no-wave stack is not valid JSON: %v", err)
	}
	policy := mustCfnMap(t, mustCfnMap(t, doc, "Resources"), "StorePolicy")
	if got, ok := policy["DependsOn"].(string); !ok || got != "StoreBucket" {
		t.Errorf("the authored DependsOn was reshaped with no wave declared; got %#v, want the string %q", policy["DependsOn"], "StoreBucket")
	}
}
