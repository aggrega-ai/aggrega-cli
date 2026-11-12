// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Direct unit tests for mergeAnnotationValueMaps and
// flattenAnnotationValueMap. These pin the helpers' contracts
// independently of the cascade walker, so regressions surface here
// rather than as opaque cascade-output failures.

func scalarAV(s string) *types.AnnotationValue {
	return &types.AnnotationValue{Scalar: &s}
}

func structuredAV(pairs ...any) *types.AnnotationValue {
	av := &types.AnnotationValue{}
	for i := 0; i < len(pairs); i += 2 {
		av.Structured.Set(pairs[i].(string), pairs[i+1])
	}
	return av
}

// Scalar-path regression coverage (pre-structured-values behaviour preserved).

func TestMergeAnnotationValueMaps_NilFromOverlayOptsOut(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*types.AnnotationValue]{}
	base.Set("opt-out-me", scalarAV("inherited"))
	base.Set("keep-me", scalarAV("kept"))

	overlay := types.OrderedMap[*types.AnnotationValue]{}
	overlay.Set("opt-out-me", nil)

	result := mergeAnnotationValueMaps(base, overlay)
	if _, ok := result.Get("opt-out-me"); ok {
		t.Errorf("nil from overlay should opt out the inherited key from the merged map")
	}
	got, ok := result.Get("keep-me")
	if !ok || got == nil || !got.IsScalar() || *got.Scalar != "kept" {
		t.Errorf("keep-me should survive unchanged; got %+v", got)
	}
}

func TestMergeAnnotationValueMaps_NonNilFromOverlayReplacesNonNilFromBase(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*types.AnnotationValue]{}
	base.Set("key", scalarAV("base-value"))

	overlay := types.OrderedMap[*types.AnnotationValue]{}
	overlay.Set("key", scalarAV("overlay-value"))

	result := mergeAnnotationValueMaps(base, overlay)
	got, ok := result.Get("key")
	if !ok || got == nil || !got.IsScalar() || *got.Scalar != "overlay-value" {
		t.Errorf("last-write-wins broke; got %+v", got)
	}
}

func TestMergeAnnotationValueMaps_AbsentFromOverlayPreservesBase(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*types.AnnotationValue]{}
	base.Set("inherited-key", scalarAV("inherited-value"))

	overlay := types.OrderedMap[*types.AnnotationValue]{}
	overlay.Set("overlay-only-key", scalarAV("overlay-only-value"))

	result := mergeAnnotationValueMaps(base, overlay)
	got, ok := result.Get("inherited-key")
	if !ok || got == nil || !got.IsScalar() || *got.Scalar != "inherited-value" {
		t.Errorf("absent-from-overlay should preserve base; got %+v", got)
	}
	got2, ok := result.Get("overlay-only-key")
	if !ok || got2 == nil || !got2.IsScalar() || *got2.Scalar != "overlay-only-value" {
		t.Errorf("overlay-only entry missing; got %+v", got2)
	}
}

func TestFlattenAnnotationValueMap_FiltersNilsAndPreservesOrder(t *testing.T) {
	t.Parallel()
	m := types.OrderedMap[*types.AnnotationValue]{}
	m.Set("a", scalarAV("A"))
	m.Set("b", nil)
	m.Set("c", scalarAV("C"))

	result := flattenAnnotationValueMap(m)
	keys := result.Keys()
	wantKeys := []string{"a", "c"}
	if len(keys) != len(wantKeys) {
		t.Fatalf("keys = %v, want %v (b filtered)", keys, wantKeys)
	}
	for i, k := range wantKeys {
		if keys[i] != k {
			t.Errorf("key[%d] = %q, want %q (author order through filter)", i, keys[i], k)
		}
	}
	if got, _ := result.Get("a"); !got.IsScalar() || *got.Scalar != "A" {
		t.Errorf("a = %+v, want scalar A", got)
	}
	if got, _ := result.Get("c"); !got.IsScalar() || *got.Scalar != "C" {
		t.Errorf("c = %+v, want scalar C", got)
	}
}

func TestFlattenAnnotationValueMap_AllNil_YieldsEmpty(t *testing.T) {
	t.Parallel()
	m := types.OrderedMap[*types.AnnotationValue]{}
	m.Set("a", nil)
	m.Set("b", nil)

	result := flattenAnnotationValueMap(m)
	if result.Len() != 0 {
		t.Errorf("all-nil input should flatten to empty; got %d entries", result.Len())
	}
}

// Structured-value path coverage.

func TestMergeAnnotationValueMaps_StructuredParentStructuredChild_DeepMerge(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*types.AnnotationValue]{}
	base.Set("feature.myco/db-pool", structuredAV(
		"pool_size", 10,
		"max_connections", 50,
	))

	overlay := types.OrderedMap[*types.AnnotationValue]{}
	overlay.Set("feature.myco/db-pool", structuredAV(
		"pool_size", 20, // overrides parent
		// max_connections absent — inherits from parent
		"timeout", "30s", // introduced by child
	))

	result := mergeAnnotationValueMaps(base, overlay)
	got, ok := result.Get("feature.myco/db-pool")
	if !ok || got == nil || !got.IsStructured() {
		t.Fatalf("expected merged structured value; got %+v", got)
	}
	checkSubkey := func(name string, want any) {
		t.Helper()
		v, present := got.Structured.Get(name)
		if !present {
			t.Errorf("subkey %q missing from merged value", name)
			return
		}
		if v != want {
			t.Errorf("subkey %q = %v, want %v", name, v, want)
		}
	}
	checkSubkey("pool_size", 20)
	checkSubkey("max_connections", 50)
	checkSubkey("timeout", "30s")
}

func TestMergeAnnotationValueMaps_PerSubkeyNullOptOut(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*types.AnnotationValue]{}
	base.Set("feature.myco/db-pool", structuredAV(
		"pool_size", 10,
		"max_connections", 50,
	))

	overlay := types.OrderedMap[*types.AnnotationValue]{}
	overlay.Set("feature.myco/db-pool", structuredAV(
		"max_connections", nil,
	))

	result := mergeAnnotationValueMaps(base, overlay)
	got, _ := result.Get("feature.myco/db-pool")
	if got == nil || !got.IsStructured() {
		t.Fatalf("expected merged structured value; got %+v", got)
	}
	if _, present := got.Structured.Get("max_connections"); present {
		t.Error("max_connections should be opted out per-subkey; child nil should remove it")
	}
	poolSize, _ := got.Structured.Get("pool_size")
	if poolSize != 10 {
		t.Errorf("pool_size = %v, want 10 (sibling subkey survives per-subkey opt-out)", poolSize)
	}
}

func TestMergeAnnotationValueMaps_ScalarParentStructuredChild_ChildWinsNoMerge(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*types.AnnotationValue]{}
	base.Set("k", scalarAV("base-scalar"))

	overlay := types.OrderedMap[*types.AnnotationValue]{}
	overlay.Set("k", structuredAV("sub", "val"))

	result := mergeAnnotationValueMaps(base, overlay)
	got, _ := result.Get("k")
	if got == nil || !got.IsStructured() {
		t.Fatalf("shape mismatch should let child win (structured); got %+v", got)
	}
}

func TestMergeAnnotationValueMaps_StructuredParentScalarChild_ChildWinsNoMerge(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*types.AnnotationValue]{}
	base.Set("k", structuredAV("sub", "val"))

	overlay := types.OrderedMap[*types.AnnotationValue]{}
	overlay.Set("k", scalarAV("child-scalar"))

	result := mergeAnnotationValueMaps(base, overlay)
	got, _ := result.Get("k")
	if got == nil || !got.IsScalar() || *got.Scalar != "child-scalar" {
		t.Errorf("shape mismatch should let child win (scalar); got %+v", got)
	}
}

func TestMergeAnnotationValueMaps_EmptyAnnotationValue_TreatedAsOptOut(t *testing.T) {
	t.Parallel()
	// Defensive: the third state (non-nil pointer to empty
	// AnnotationValue) can arise from the UnmarshalYAML defensive
	// branch. The belt-and-suspenders predicate IsEmpty() catches it
	// as a whole-value opt-out signal regardless of pointer nil-ness.
	base := types.OrderedMap[*types.AnnotationValue]{}
	base.Set("k", scalarAV("inherited"))

	overlay := types.OrderedMap[*types.AnnotationValue]{}
	overlay.Set("k", &types.AnnotationValue{})

	result := mergeAnnotationValueMaps(base, overlay)
	if _, ok := result.Get("k"); ok {
		t.Error("empty AnnotationValue (third state) should opt out via the IsEmpty predicate")
	}
}

func TestMergeAnnotationValueMaps_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	build := func() types.OrderedMap[*types.AnnotationValue] {
		base := types.OrderedMap[*types.AnnotationValue]{}
		base.Set("alpha", scalarAV("a"))
		base.Set("bravo", structuredAV("p", 1, "q", 2))
		overlay := types.OrderedMap[*types.AnnotationValue]{}
		overlay.Set("bravo", structuredAV("q", 99, "r", 3))
		overlay.Set("charlie", scalarAV("c"))
		return mergeAnnotationValueMaps(base, overlay)
	}
	first := build()
	second := build()
	if first.Len() != second.Len() {
		t.Fatalf("len differs: first=%d second=%d", first.Len(), second.Len())
	}
	firstKeys := first.Keys()
	secondKeys := second.Keys()
	for i := range firstKeys {
		if firstKeys[i] != secondKeys[i] {
			t.Errorf("key order differs at index %d: first=%q second=%q", i, firstKeys[i], secondKeys[i])
		}
	}
}
