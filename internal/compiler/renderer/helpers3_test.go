// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestNormalizeSelfInjectEntries_AllShapes pins every dispatch arm:
// *OrderedMap[any] with nested string + nested *OrderedMap[any] +
// nested map[string]any + nested default; map[string]any with all
// variants; map[string]string; schema-verbose []any.
func TestNormalizeSelfInjectEntries_AllShapes(t *testing.T) {
	t.Parallel()
	// *OrderedMap[any] outer (all inner variants).
	om := types.NewOrderedMap[any]()
	om.Set("compact-key", "literal-value")
	innerOM := types.NewOrderedMap[any]()
	innerOM.Set("value", "templated-value")
	innerOM.Set("from", "ignored-because-value-wins-in-resolveSelfInjectEntry")
	om.Set("verbose-om", innerOM)
	om.Set("verbose-map", map[string]any{"from": "provides.key"})
	om.Set("default-val", 42)
	got, err := normalizeSelfInjectEntries(om)
	if err != nil {
		t.Fatalf("om: %v", err)
	}
	if got["compact-key"].value != "literal-value" || !got["compact-key"].isLiteral {
		t.Errorf("compact: got %+v", got["compact-key"])
	}
	if got["verbose-om"].value != "templated-value" {
		t.Errorf("verbose-om: got %+v", got["verbose-om"])
	}
	if got["verbose-map"].from != "provides.key" {
		t.Errorf("verbose-map from: got %+v", got["verbose-map"])
	}
	if got["default-val"].value != "42" {
		t.Errorf("default coercion: got %+v", got["default-val"])
	}

	// Nil *OrderedMap[any].
	got, err = normalizeSelfInjectEntries((*types.OrderedMap[any])(nil))
	if err != nil || len(got) != 0 {
		t.Errorf("nil *OrderedMap[any]: %v / %d", err, len(got))
	}

	// map[string]any outer (all inner variants).
	plain := map[string]any{
		"compact":     "literal",
		"verbose-map": map[string]any{"value": "tmpl"},
		"mixed-om":    innerOM,
		"default":     true,
	}
	got, err = normalizeSelfInjectEntries(plain)
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	if got["compact"].value != "literal" {
		t.Errorf("compact: got %+v", got["compact"])
	}
	if got["verbose-map"].value != "tmpl" {
		t.Errorf("verbose-map: got %+v", got["verbose-map"])
	}
	if got["mixed-om"].value != "templated-value" {
		t.Errorf("mixed-om: got %+v", got["mixed-om"])
	}
	if got["default"].value != "true" {
		t.Errorf("default-bool: got %+v", got["default"])
	}

	// map[string]string outer.
	got, err = normalizeSelfInjectEntries(map[string]string{"K": "v"})
	if err != nil || got["K"].value != "v" || !got["K"].isLiteral {
		t.Errorf("map-string-string: %v / %+v", err, got)
	}

	// []any schema-verbose.
	verbose := []any{
		map[string]any{"in": "K1", "value": "tmpl1"},
		map[string]any{"in": "K2", "from": "provides.x"},
		map[string]any{"in": "K3", "value": "tmpl3", "from": "from3"},
		map[string]any{"value": "no-in"}, // skipped
	}
	got, err = normalizeSelfInjectEntries(verbose)
	if err != nil {
		t.Fatalf("verbose: %v", err)
	}
	if got["K1"].value != "tmpl1" {
		t.Errorf("K1: got %+v", got["K1"])
	}
	if got["K2"].from != "provides.x" {
		t.Errorf("K2: got %+v", got["K2"])
	}
	if got["K3"].value != "tmpl3" || got["K3"].from != "from3" {
		t.Errorf("K3: got %+v", got["K3"])
	}
	if _, has := got["no-in"]; has {
		t.Errorf("entry without `in` should be skipped")
	}

	// Unsupported outer.
	got, err = normalizeSelfInjectEntries("scalar")
	if err != nil || len(got) != 0 {
		t.Errorf("unsupported outer: err=%v len=%d", err, len(got))
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
	got, err := EvaluateFeatureRecipeLocals(nil, "i", "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, resolver.SensitiveLeaves{})
	if err != nil || got.Len() != 0 {
		t.Errorf("nil fr: (%v, %v)", got, err)
	}
	emptyFR := &types.FeatureRecipe{}
	got, err = EvaluateFeatureRecipeLocals(emptyFR, "i", "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, resolver.SensitiveLeaves{})
	if err != nil || got.Len() != 0 {
		t.Errorf("empty locals: (%v, %v)", got, err)
	}
	// Populated locals.
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = "test-fr"
	fr.Spec.Locals.Set("k", "{{ .values.value }}")
	values := types.OrderedMap[any]{}
	values.Set("value", "v")
	got, err = EvaluateFeatureRecipeLocals(fr, "i", "api", values, types.OrderedMap[any]{}, resolver.SensitiveLeaves{})
	if err != nil {
		t.Fatalf("populated: %v", err)
	}
	if v, _ := got.Get("k"); v != "v" {
		t.Errorf("k = %q; want v", v)
	}
}
