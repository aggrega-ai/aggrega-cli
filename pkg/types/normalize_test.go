// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "testing"

func normComps(names ...string) OrderedMap[ComponentInstance] {
	var m OrderedMap[ComponentInstance]
	for _, n := range names {
		m.Set(n, ComponentInstance{Type: "x"})
	}
	return m
}

// TestNormalize pins the sole constructor: non-mutating, folds a top-level
// components surface into an ordered-first synthesized `default` subsystem, and
// is idempotent and nil-safe.
func TestNormalize(t *testing.T) {
	t.Parallel()

	t.Run("non-mutating: the caller's aggregate is untouched", func(t *testing.T) {
		agg := &Aggregate{}
		agg.Spec.Components = normComps("a", "b")
		na := Normalize(agg)

		if agg.Spec.Components.Len() != 2 || agg.Spec.Subsystems.Len() != 0 {
			t.Errorf("Normalize must not mutate its input; caller sees components=%d subsystems=%d", agg.Spec.Components.Len(), agg.Spec.Subsystems.Len())
		}
		n := na.Aggregate()
		if n.Spec.Components.Len() != 0 {
			t.Errorf("normalized view: root components must be folded away; got %d", n.Spec.Components.Len())
		}
		def, ok := n.Spec.Subsystems.Get("default")
		if !ok || !def.Synthesized || def.Components.Len() != 2 {
			t.Errorf("normalized view must carry a synthesized default holding the 2 components; ok=%v synthesized=%v len=%d", ok, def.Synthesized, def.Components.Len())
		}
		if keys := n.Spec.Subsystems.Keys(); len(keys) == 0 || keys[0] != "default" {
			t.Errorf("the synthesized default must be ordered first; keys=%v", keys)
		}
	})

	t.Run("a declared subsystem is a no-op (never synthesizes)", func(t *testing.T) {
		agg := &Aggregate{}
		var subs OrderedMap[Subsystem]
		subs.Set("svc", Subsystem{Components: normComps("a")})
		agg.Spec.Subsystems = subs
		if _, ok := Normalize(agg).Aggregate().Spec.Subsystems.Get("default"); ok {
			t.Error("must not synthesize a default when subsystems are declared")
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		agg := &Aggregate{}
		agg.Spec.Components = normComps("a")
		first := Normalize(agg).Aggregate()
		second := Normalize(first).Aggregate()
		if second.Spec.Subsystems.Len() != 1 {
			t.Errorf("a second Normalize must be a no-op; got %d subsystems", second.Spec.Subsystems.Len())
		}
	})

	t.Run("nil-safe", func(t *testing.T) {
		if Normalize(nil).Aggregate() != nil {
			t.Error("Normalize(nil) must wrap nil without panic")
		}
	})
}

func relComps(names ...string) OrderedMap[BoundaryComponent] {
	var m OrderedMap[BoundaryComponent]
	for _, n := range names {
		m.Set(n, BoundaryComponent{})
	}
	return m
}

// TestNormalizeRelease pins the Release sole constructor: non-mutating, folds a
// top-level components surface into an ordered-first synthesized `default`
// boundary, idempotent, nil-safe. Symmetric to TestNormalize.
func TestNormalizeRelease(t *testing.T) {
	t.Parallel()

	t.Run("non-mutating: the caller's release is untouched", func(t *testing.T) {
		rel := &Release{}
		rel.Spec.Components = relComps("api", "worker")
		nr := NormalizeRelease(rel)

		if rel.Spec.Components.Len() != 2 || rel.Spec.Boundaries.Len() != 0 {
			t.Errorf("NormalizeRelease must not mutate its input; caller sees components=%d boundaries=%d", rel.Spec.Components.Len(), rel.Spec.Boundaries.Len())
		}
		n := nr.Release()
		if n.Spec.Components.Len() != 0 {
			t.Errorf("normalized view: root components must be folded away; got %d", n.Spec.Components.Len())
		}
		def, ok := n.Spec.Boundaries.Get("default")
		if !ok || !def.Synthesized || def.Components.Len() != 2 {
			t.Errorf("normalized view must carry a synthesized default holding the 2 components; ok=%v synthesized=%v len=%d", ok, def.Synthesized, def.Components.Len())
		}
		if keys := n.Spec.Boundaries.Keys(); len(keys) == 0 || keys[0] != "default" {
			t.Errorf("the synthesized default must be ordered first; keys=%v", keys)
		}
	})

	t.Run("a declared boundary is a no-op (never synthesizes)", func(t *testing.T) {
		rel := &Release{}
		var b OrderedMap[Boundary]
		b.Set("main", Boundary{Components: relComps("api")})
		rel.Spec.Boundaries = b
		if _, ok := NormalizeRelease(rel).Release().Spec.Boundaries.Get("default"); ok {
			t.Error("must not synthesize a default when boundaries are declared")
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		rel := &Release{}
		rel.Spec.Components = relComps("api")
		first := NormalizeRelease(rel).Release()
		second := NormalizeRelease(first).Release()
		if second.Spec.Boundaries.Len() != 1 {
			t.Errorf("a second NormalizeRelease must be a no-op; got %d boundaries", second.Spec.Boundaries.Len())
		}
	})

	t.Run("nil-safe", func(t *testing.T) {
		if NormalizeRelease(nil).Release() != nil {
			t.Error("NormalizeRelease(nil) must wrap nil without panic")
		}
	})
}
