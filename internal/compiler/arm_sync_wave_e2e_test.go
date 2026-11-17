// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that a Release boundary's declared wave orders provisioning in
// the ARM template it assembles into, through dependsOn on symbolic names, and
// that a build declaring no wave assembles the same bytes it would without the
// ordering.

package compiler_test

import (
	"bytes"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const (
	armSyncWaveDir     = "testdata/arm-sync-wave/"
	armSyncWaveCatalog = armSyncWaveDir + "catalog"
)

// armSyncWaveInput is the build input for one of the fixture's Releases. Its
// three boundaries share one directory, so they collapse into one template.
func armSyncWaveInput(release string) pipelinetest.Input {
	return armInput(armSyncWaveDir+release, armSyncWaveCatalog)
}

// armDependsOn returns each resource's dependsOn entries, keyed by symbolic
// name, with a nil entry for a resource carrying none. It decodes the template
// rather than searching its text because a substring cannot say which resource an
// occurrence belongs to, and the claim is about which resource waits on which. It
// keeps the emitted order, because an authored entry keeps its place ahead of the
// wave's.
func armDependsOn(t *testing.T, template map[string]any) map[string][]string {
	t.Helper()
	resources := armSection(t, template, "resources")
	out := map[string][]string{}
	for symbol := range resources {
		body := armResourceBody(t, template, symbol)
		switch v := body["dependsOn"].(type) {
		case nil:
			out[symbol] = nil
		case []any:
			entries := make([]string, 0, len(v))
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					t.Fatalf("resource %q has a non-string dependsOn entry %#v", symbol, item)
				}
				entries = append(entries, s)
			}
			out[symbol] = entries
		default:
			t.Fatalf("resource %q dependsOn is not a list; got %#v", symbol, v)
		}
	}
	return out
}

// TestArmSyncWaveDependsOn compiles a Release whose three boundaries share one
// directory at waves -1, 0 and 5, and asserts the emitted dependsOn expresses
// exactly that provisioning order, then compares the whole template with the
// committed golden.
//
// The waves are neither contiguous nor all non-negative, so the assertions hold
// only if the chaining follows the populated waves in order:
//
//   - the network's two resources are in the lowest wave and add nothing, so the
//     one that authored a dependency keeps it and the other names nothing;
//   - the store's resources name both of the network's, alphabetically, which
//     shows the fan-out reaching every resource of the preceding wave;
//   - StorePolicy authored its own dependency on StorePrimary and keeps it ahead
//     of the wave's entries;
//   - the app names the store's two resources and not the network's, because the
//     ordering reaches the immediately preceding populated wave only and ARM
//     resolves the rest transitively.
func TestArmSyncWaveDependsOn(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armSyncWaveInput("release.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	deps := armDependsOn(t, template)

	for symbol, want := range map[string][]string{
		"NetworkPrimary": nil,
		"NetworkPolicy":  {"NetworkPrimary"},
		"StorePrimary":   {"NetworkPolicy", "NetworkPrimary"},
		"StorePolicy":    {"StorePrimary", "NetworkPolicy", "NetworkPrimary"},
		"AppPrimary":     {"StorePolicy", "StorePrimary"},
		"AppPolicy":      {"AppPrimary", "StorePolicy", "StorePrimary"},
	} {
		got, present := deps[symbol]
		if !present {
			t.Errorf("resource %q is missing from the assembled template\n--- template ---\n%s", symbol, raw)
			continue
		}
		if !equalStringSlice(got, want) {
			t.Errorf("%s's dependsOn = %v, want %v\n--- template ---\n%s", symbol, got, want, raw)
		}
	}

	armCompareGolden(t, armSyncWaveDir+"golden/wave-template.json", raw)
	pipelinetest.AssertDeterministic(t, armSyncWaveInput("release.yaml"))
}

// TestArmNoWaveIsByteIdentical compiles the same three boundaries with no wave
// declared and asserts the assembled template is byte for byte the committed
// golden, and that no resource gained a dependsOn the fragments did not author.
//
// The comparison is anti-needled: a byte comparison against a golden passes
// trivially if the build produces nothing, so the test first proves the same
// comparison fails for the wave-declaring build.
func TestArmNoWaveIsByteIdentical(t *testing.T) {
	t.Parallel()
	_, waved := findArmTemplate(t, pipelinetest.Build(t, armSyncWaveInput("release.yaml")).Artifacts, "stack")
	template, plain := findArmTemplate(t, pipelinetest.Build(t, armSyncWaveInput("release-no-wave.yaml")).Artifacts, "stack")

	if bytes.Equal(waved, plain) {
		t.Fatal("the wave-declaring build matches the no-wave build, so the comparison below cannot detect the ordering at all")
	}
	armCompareGolden(t, armSyncWaveDir+"golden/no-wave-template.json", plain)

	// Without a wave the ordering pass reaches nothing, so it must not reshape what
	// it did not add: only the authored dependency survives.
	for symbol, got := range armDependsOn(t, template) {
		want := []string(nil)
		if symbol == "NetworkPolicy" || symbol == "StorePolicy" || symbol == "AppPolicy" {
			want = []string{symbol[:len(symbol)-len("Policy")] + "Primary"}
		}
		if !equalStringSlice(got, want) {
			t.Errorf("%s's dependsOn = %v with no wave declared, want %v", symbol, got, want)
		}
	}
	pipelinetest.AssertDeterministic(t, armSyncWaveInput("release-no-wave.yaml"))
}
