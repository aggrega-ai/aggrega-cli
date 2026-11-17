// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that an ARM template keeps the output names of different
// nodes apart, built through the real compiled Wasm guest and asserted against
// the emitted bytes.
//
// Three datastores whose instance names differ only in punctuation each declare
// an output named host. The provider derives every output name from the
// contributing node and the entry together, writing both into the alphabet an
// output name admits, so a hyphen, an underscore and no separator at all arrive
// as three different names. A derivation that merely drops the punctuation maps
// all three onto one name and the template would carry fewer outputs than the
// Aggregate declared.

package compiler_test

import (
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const armNamesRelease = "testdata/arm-names/release.yaml"

// TestArmOutputNamesStayApartAcrossNodes builds three datastores into one
// template and asserts the exact set of output names. Each name is written out
// in full rather than composed here from the node and the entry: a name composed
// in the test would follow the derivation wherever it moved, and the assertion
// would keep passing through a rename that changed every author's output names.
//
// The encoding writes an authored hyphen as `_zh` and keeps an authored
// underscore as it is when no `z` follows it, so the three instances end in
// three different names.
func TestArmOutputNamesStayApartAcrossNodes(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armInput(armNamesRelease))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	t.Logf("assembled template:\n%s", raw)

	want := []string{"payments_db__host", "payments_zhdb__host", "paymentsdb__host"}
	outputs := armSection(t, template, "outputs")
	got := sortedAnyKeys(outputs)
	if !equalStringSlice(got, want) {
		t.Fatalf("the template declares outputs %v, want exactly %v\n--- template ---\n%s", got, want, raw)
	}

	// Each output carries the expression of the server its own node declared, so
	// three names that stayed apart still point at three different servers.
	for name, symbol := range map[string]string{
		"payments_zhdb__host": "PaymentsHyphenDb",
		"payments_db__host":   "PaymentsUnderscoreDb",
		"paymentsdb__host":    "PaymentsFlatDb",
	} {
		body := armSection(t, outputs, name)
		wantValue := "[reference('" + symbol + "').fullyQualifiedDomainName]"
		if body["value"] != wantValue {
			t.Errorf("output %s = %#v, want %q: the name must stay attached to the node that declared it", name, body["value"], wantValue)
		}
	}
}

// TestArmOutputNamesAreDeterministic asserts the three-node build is
// byte-identical across repeated runs.
func TestArmOutputNamesAreDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, armInput(armNamesRelease))
}

// sortedAnyKeys returns the sorted keys of a decoded JSON object.
func sortedAnyKeys(m map[string]any) []string {
	keys := mapKeys(m)
	sort.Strings(keys)
	return keys
}
