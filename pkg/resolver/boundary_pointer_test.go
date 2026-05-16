// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// Direct unit tests for mergePointerStringMaps and flattenPointerStringMap.
// These pin the helpers' contracts independently of the cascade walker,
// so regressions surface here rather than as opaque cascade-output
// failures.

func pstr(s string) *string { return &s }

func TestMergePointerStringMaps_NilFromOverlayWinsOverNonNilFromBase(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*string]{}
	base.Set("opt-out-me", pstr("inherited"))
	base.Set("keep-me", pstr("kept"))

	overlay := types.OrderedMap[*string]{}
	overlay.Set("opt-out-me", nil)

	result := mergePointerStringMaps(base, overlay)
	if got, ok := result.Get("opt-out-me"); !ok || got != nil {
		t.Errorf("nil from overlay should override non-nil from base; got ok=%v ptr=%v", ok, got)
	}
	got, ok := result.Get("keep-me")
	if !ok || got == nil || *got != "kept" {
		t.Errorf("keep-me should survive unchanged; got ok=%v ptr=%v", ok, got)
	}
}

func TestMergePointerStringMaps_NonNilFromOverlayReplacesNonNilFromBase(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*string]{}
	base.Set("key", pstr("base-value"))

	overlay := types.OrderedMap[*string]{}
	overlay.Set("key", pstr("overlay-value"))

	result := mergePointerStringMaps(base, overlay)
	got, ok := result.Get("key")
	if !ok || got == nil || *got != "overlay-value" {
		t.Errorf("last-write-wins broke; got ok=%v ptr=%v", ok, got)
	}
}

func TestMergePointerStringMaps_AbsentFromOverlayPreservesBase(t *testing.T) {
	t.Parallel()
	base := types.OrderedMap[*string]{}
	base.Set("inherited-key", pstr("inherited-value"))

	overlay := types.OrderedMap[*string]{}
	// overlay has no entry for inherited-key — must not affect it.
	overlay.Set("overlay-only-key", pstr("overlay-only-value"))

	result := mergePointerStringMaps(base, overlay)
	got, ok := result.Get("inherited-key")
	if !ok || got == nil || *got != "inherited-value" {
		t.Errorf("absent-from-overlay should preserve base; got ok=%v ptr=%v", ok, got)
	}
	got2, ok := result.Get("overlay-only-key")
	if !ok || got2 == nil || *got2 != "overlay-only-value" {
		t.Errorf("overlay-only entry missing; got ok=%v ptr=%v", ok, got2)
	}
}

func TestFlattenPointerStringMap_FiltersNilsAndPreservesOrder(t *testing.T) {
	t.Parallel()
	m := types.OrderedMap[*string]{}
	m.Set("a", pstr("A"))
	m.Set("b", nil)
	m.Set("c", pstr("C"))

	result := flattenPointerStringMap(m)
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
	if got, _ := result.Get("a"); got != "A" {
		t.Errorf("a = %q, want A", got)
	}
	if got, _ := result.Get("c"); got != "C" {
		t.Errorf("c = %q, want C", got)
	}
}

func TestFlattenPointerStringMap_AllNil_YieldsEmpty(t *testing.T) {
	t.Parallel()
	m := types.OrderedMap[*string]{}
	m.Set("a", nil)
	m.Set("b", nil)

	result := flattenPointerStringMap(m)
	if result.Len() != 0 {
		t.Errorf("all-nil input should flatten to empty; got %d entries", result.Len())
	}
}
