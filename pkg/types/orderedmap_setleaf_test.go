// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import "testing"

// SetLeafAtPath coverage: top-level leaf (no intermediate), pointer-
// typed intermediate (the shape SetLeafAtPath produces on first
// creation), value-typed intermediate (the shape YAML unmarshalling
// produces by default), and tree-shape collision fallback.

func TestSetLeafAtPath_TopLevelLeaf(t *testing.T) {
	t.Parallel()
	tree := OrderedMap[any]{}
	SetLeafAtPath(&tree, "upstream", "api.example.com")

	got, ok := tree.Get("upstream")
	if !ok {
		t.Fatal("upstream missing")
	}
	if got != "api.example.com" {
		t.Errorf("upstream = %v, want api.example.com", got)
	}
}

func TestSetLeafAtPath_NavigatesPointerTypedIntermediate(t *testing.T) {
	t.Parallel()
	// Subsequent SetLeafAtPath calls into the same tree reuse the
	// pointer-typed intermediate created by the first call rather
	// than creating a fresh one. Both leaves land under the same
	// intermediate.
	tree := OrderedMap[any]{}
	SetLeafAtPath(&tree, "ingress.enabled", true)
	SetLeafAtPath(&tree, "ingress.host", "shared-host")

	got, _ := tree.Get("ingress")
	ingress, ok := got.(*OrderedMap[any])
	if !ok {
		t.Fatalf("ingress should be *OrderedMap after first SetLeafAtPath; got %T", got)
	}
	enabled, _ := ingress.Get("enabled")
	host, _ := ingress.Get("host")
	if enabled != true {
		t.Errorf("ingress.enabled = %v, want true", enabled)
	}
	if host != "shared-host" {
		t.Errorf("ingress.host = %v, want shared-host", host)
	}
}

func TestSetLeafAtPath_NavigatesValueTypedIntermediate(t *testing.T) {
	t.Parallel()
	// Value-typed intermediates (the shape YAML unmarshalling
	// produces, or programmatic OrderedMap construction without
	// explicit pointers) land in the value-typed branch of the
	// descent switch. The leaf write MUST propagate to the parent
	// tree; a self-referential or local-copy write would silently
	// drop the contribution. This is the load-bearing pin test.
	tree := OrderedMap[any]{}
	intermediate := OrderedMap[any]{}
	intermediate.Set("existing", "preserved")
	tree.Set("ingress", intermediate) // value-typed, not &intermediate

	SetLeafAtPath(&tree, "ingress.host", "new-host")

	got, ok := tree.Get("ingress")
	if !ok {
		t.Fatal("ingress missing from tree after SetLeafAtPath")
	}
	var ingressMap OrderedMap[any]
	switch n := got.(type) {
	case *OrderedMap[any]:
		ingressMap = *n
	case OrderedMap[any]:
		ingressMap = n
	default:
		t.Fatalf("ingress entry has unexpected type %T", got)
	}
	host, ok := ingressMap.Get("host")
	if !ok {
		t.Fatal("ingress.host not propagated to parent — value-typed-intermediate descent dropped the write")
	}
	if host != "new-host" {
		t.Errorf("host = %v, want new-host", host)
	}
	existing, ok := ingressMap.Get("existing")
	if !ok {
		t.Fatal("pre-existing sibling entry lost during value-typed-intermediate descent")
	}
	if existing != "preserved" {
		t.Errorf("existing = %v, want preserved", existing)
	}
}

func TestSetLeafAtPath_TreeShapeCollision_OverwritesIntermediate(t *testing.T) {
	t.Parallel()
	// A path's intermediate node currently holds a scalar (e.g.,
	// from a prior write that landed at the wrong depth). The walker
	// overwrites the offending intermediate with a fresh OrderedMap
	// and continues the descent. Strict-no-collision semantics live
	// upstream at schema validation; this helper trusts validated
	// input and prefers progress over halting on shape mismatch.
	tree := OrderedMap[any]{}
	tree.Set("ingress", "literal-scalar")

	SetLeafAtPath(&tree, "ingress.host", "api.example.com")

	got, _ := tree.Get("ingress")
	ingress, ok := got.(*OrderedMap[any])
	if !ok {
		t.Fatalf("ingress should be *OrderedMap after collision overwrite; got %T", got)
	}
	host, found := ingress.Get("host")
	if !found {
		t.Fatal("ingress.host missing after collision overwrite")
	}
	if host != "api.example.com" {
		t.Errorf("host = %v, want api.example.com", host)
	}
}

func TestSetLeafAtPath_PairedWithFlattenLeaves(t *testing.T) {
	t.Parallel()
	// SetLeafAtPath and FlattenLeaves are paired primitives — writes
	// through the former are readable via the latter in author order.
	// This test pins the round-trip discipline so future refactors
	// that touch one but not the other surface a regression.
	tree := OrderedMap[any]{}
	SetLeafAtPath(&tree, "pool", "shared-pool")
	SetLeafAtPath(&tree, "ingress.host", "api.example.com")
	SetLeafAtPath(&tree, "ingress.port", 443)

	leaves := FlattenLeaves(tree)
	want := []FlatLeaf{
		{Path: "pool", Value: "shared-pool"},
		{Path: "ingress.host", Value: "api.example.com"},
		{Path: "ingress.port", Value: 443},
	}
	if len(leaves) != len(want) {
		t.Fatalf("FlattenLeaves len = %d, want %d; got: %v", len(leaves), len(want), leaves)
	}
	for i, leaf := range leaves {
		if leaf.Path != want[i].Path || leaf.Value != want[i].Value {
			t.Errorf("leaves[%d] = %+v, want %+v", i, leaf, want[i])
		}
	}
}
