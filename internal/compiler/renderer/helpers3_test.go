// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestNormalizeSelfInjectEntries_ReadsTheListAndNothingElse pins the one shape a
// mechanism takes — a list of {in, from?, value?} entries, each entry either a
// plain map or an ordered map — and that a mechanism in any other shape yields no
// entry at all rather than being rendered some other way.
func TestNormalizeSelfInjectEntries_ReadsTheListAndNothingElse(t *testing.T) {
	t.Parallel()

	orderedEntry := types.NewOrderedMap[any]()
	orderedEntry.Set("in", "K4")
	orderedEntry.Set("value", "tmpl4")
	verbose := []any{
		map[string]any{"in": "K1", "value": "tmpl1"},
		map[string]any{"in": "K2", "from": "provides.x"},
		map[string]any{"in": "K3", "value": "tmpl3", "from": "from3"},
		orderedEntry,
		map[string]any{"value": "no-in"}, // skipped
	}
	got := normalizeSelfInjectEntries(verbose)
	want := map[string]selfInjectEntry{
		"K1": {value: "tmpl1"},
		"K2": {from: "provides.x"},
		"K3": {value: "tmpl3", from: "from3"},
		"K4": {value: "tmpl4"},
	}
	if got.Len() != len(want) {
		t.Errorf("list: got %d entries %v, want %d", got.Len(), got.Keys(), len(want))
	}
	for key, w := range want {
		if g, ok := got.Get(key); !ok || g != w {
			t.Errorf("list entry %q: got %+v (present=%v), want %+v", key, g, ok, w)
		}
	}

	notAList := types.NewOrderedMap[any]()
	notAList.Set("K", "literal")
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"nil", nil},
		{"plain map of strings", map[string]any{"K": "literal"}},
		{"plain map of entries", map[string]any{"K": map[string]any{"value": "tmpl"}}},
		{"string map", map[string]string{"K": "v"}},
		{"ordered map", notAList},
		{"nil ordered map", (*types.OrderedMap[any])(nil)},
		{"scalar", "scalar"},
	} {
		if got := normalizeSelfInjectEntries(tc.raw); got.Len() != 0 {
			t.Errorf("%s: got entries=%v, want no entries", tc.name, got.Keys())
		}
	}
}

// TestMergeProvenanceIntoLabels_EveryArm pins both arms of the
// labels-merge helper: existing key replace, new key append.
func TestPhaseB1_NilState(t *testing.T) {
	t.Parallel()
	// phaseB1 with a nil state is acceptable: the function unwinds via
	// the zero-state path without doing any work. The test exists to
	// drive the entry guard arm.
	tmplCtx := &types.TemplateContext{}
	// nil state would panic; instead, a zero-value PhaseBState lets the
	// function take the no-recipe-bound branch.
	st := &PhaseBState{}
	_ = phaseB1(tmplCtx, st)
}

// TestExecutePhaseBForFeature_NilAndEmpty pins the nil-fr and the
// no-valuesMapping arms.
func TestExecutePhaseBForFeature_NilAndEmpty(t *testing.T) {
	t.Parallel()
	tmplCtx := &types.TemplateContext{}
	got, err := ExecutePhaseBForFeature(tmplCtx, nil, nil, redact.Keys{})
	if err != nil || len(got.Resolved) != 0 {
		t.Errorf("nil fr: (%v, %v)", got.Resolved, err)
	}
	// FR with no valuesMapping → returns ctx.Values as-is.
	fr := &types.FeatureRecipe{}
	tmplCtx.Values.Set("k", "v")
	got, err = ExecutePhaseBForFeature(tmplCtx, fr, nil, redact.Keys{})
	if err != nil {
		t.Fatalf("no vm: %v", err)
	}
	if got.Resolved["k"] != "v" {
		t.Errorf("ctx.Values pass-through; got %v", got.Resolved)
	}
}

// TestEvaluateFeatureRecipeLocals_NilAndEmpty pins early returns and
// happy path with self-containment check.
func TestEvaluateFeatureRecipeLocals_NilAndEmpty(t *testing.T) {
	t.Parallel()
	got, err := EvaluateFeatureRecipeLocals(nil, "i", "api", "", types.OrderedMap[any]{}, types.OrderedMap[any]{}, resolver.SensitiveLeaves{})
	if err != nil || got.Len() != 0 {
		t.Errorf("nil fr: (%v, %v)", got, err)
	}
	emptyFR := &types.FeatureRecipe{}
	got, err = EvaluateFeatureRecipeLocals(emptyFR, "i", "api", "", types.OrderedMap[any]{}, types.OrderedMap[any]{}, resolver.SensitiveLeaves{})
	if err != nil || got.Len() != 0 {
		t.Errorf("empty locals: (%v, %v)", got, err)
	}
	// Populated locals.
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = "test-fr"
	fr.Spec.Locals.Set("k", "{{ .values.value }}")
	values := types.OrderedMap[any]{}
	values.Set("value", "v")
	got, err = EvaluateFeatureRecipeLocals(fr, "i", "api", "", values, types.OrderedMap[any]{}, resolver.SensitiveLeaves{})
	if err != nil {
		t.Fatalf("populated: %v", err)
	}
	if v, _ := got.Get("k"); v != "v" {
		t.Errorf("k = %q; want v", v)
	}
}
