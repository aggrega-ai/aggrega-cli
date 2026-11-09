// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// twoKeyEnvInject builds an IR attachment inject of shape
// {env: {a: fromA, b: fromB}} in that author order.
func twoKeyEnvInject(a, fromA, b, fromB string) types.OrderedMap[types.OrderedMap[string]] {
	m := types.OrderedMap[string]{}
	m.Set(a, fromA)
	m.Set(b, fromB)
	out := types.OrderedMap[types.OrderedMap[string]]{}
	out.Set("env", m)
	return out
}

// twoProvidesFR is a FeatureRecipe publishing two provides keys.
func twoProvidesFR(implements, k1, t1, k2, t2 string) *types.FeatureRecipe {
	pv := types.OrderedMap[string]{}
	pv.Set(k1, t1)
	pv.Set(k2, t2)
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "provides-fr"},
		Spec:     types.FeatureRecipeSpec{Capability: "compose", Implements: implements, Provides: pv},
	}
}

// sensitiveByValueRun resolves one attachment whose inject carries two delivery
// keys — one the merge marked sensitive, one it did not — and returns the
// delivery keys the run reported, in report order.
func sensitiveByValueRun(t *testing.T, marked types.OrderedMap[types.OrderedMap[string]], provided ...string) []string {
	t.Helper()
	fr := twoProvidesFR("core/db-creds:1.0.0", "password", "{{ .values.generated }}", "locator", "a-locator")
	if len(provided) == 1 && provided[0] == "locator-only" {
		pv := types.OrderedMap[string]{}
		pv.Set("locator", "a-locator")
		fr.Spec.Provides = pv
	}
	entry := types.IRFeatureEntry{
		Type:            "core/db-creds",
		Values:          attachValues("generated", "s3cr3t"),
		Inject:          twoKeyEnvInject("DB_PASSWORD", "password", "DB_LOCATOR", "locator"),
		SensitiveInject: marked,
	}
	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("db-creds", entry)
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", inner)
	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: fvm}}
	lookup := RecipeLookup{FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return fr, true }}

	var reported []string
	inject, _ := BuildInjectPipe(nil)
	if _, err := ResolveAttachmentInject(&inject, graph, "api", "", lookup, func(attachment, mechanism, deliveryKey string) {
		if attachment != "db-creds" || mechanism != "env" {
			t.Errorf("report identified attachment %q mechanism %q, want %q / %q", attachment, mechanism, "db-creds", "env")
		}
		reported = append(reported, deliveryKey)
	}, nil, nil, nil); err != nil {
		t.Fatalf("ResolveAttachmentInject: %v", err)
	}
	return reported
}

// TestAttachmentInject_ReportsOnlyTheMarkedDeliveryKey is the false-positive
// guard. Both delivery keys travel the identical by-value channel on the
// identical attachment and both land in the pipe; only the one the merge marked
// is reported. A warning that fired on the unmarked sibling would be a defect,
// because a published locator is not secret material and a noisy
// security-adjacent advisory gets silenced wholesale.
func TestAttachmentInject_ReportsOnlyTheMarkedDeliveryKey(t *testing.T) {
	t.Parallel()
	marked := types.OrderedMap[string]{}
	marked.Set("DB_PASSWORD", "password")
	byMech := types.OrderedMap[types.OrderedMap[string]]{}
	byMech.Set("env", marked)

	got := sensitiveByValueRun(t, byMech)
	if len(got) != 1 || got[0] != "DB_PASSWORD" {
		t.Errorf("reported %v, want exactly [DB_PASSWORD]", got)
	}
}

// TestAttachmentInject_ReportsNothingWhenNothingIsMarked pins the negative half
// of the same guard: with no marking, the same two deliveries report nothing.
func TestAttachmentInject_ReportsNothingWhenNothingIsMarked(t *testing.T) {
	t.Parallel()
	got := sensitiveByValueRun(t, types.OrderedMap[types.OrderedMap[string]]{})
	if len(got) != 0 {
		t.Errorf("reported %v, want nothing when no delivery key is marked", got)
	}
}

// TestAttachmentInject_ReportsNothingWhenNoValueWasDelivered proves the report
// tracks a delivery that happened rather than an authored intention. The key is
// marked, but the attachment's provides publishes nothing under the referenced
// name, so no literal enters the pipe and nothing is reported. Reporting here
// would tell an author a value is travelling when none is.
func TestAttachmentInject_ReportsNothingWhenNoValueWasDelivered(t *testing.T) {
	t.Parallel()
	marked := types.OrderedMap[string]{}
	marked.Set("DB_PASSWORD", "password")
	byMech := types.OrderedMap[types.OrderedMap[string]]{}
	byMech.Set("env", marked)

	got := sensitiveByValueRun(t, byMech, "locator-only")
	if len(got) != 0 {
		t.Errorf("reported %v, want nothing when the referenced provides key resolves to nothing", got)
	}
}
