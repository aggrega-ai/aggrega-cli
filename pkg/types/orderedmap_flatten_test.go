// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "testing"

// FlattenLeaves coverage: the walker emits primitive leaves in author
// order through nested OrderedMap intermediates, joining segments with
// "." at every nesting depth. Both pointer-typed and value-typed
// intermediates dispatch through the same recursive walk; empty
// OrderedMaps at any level yield a single FlatLeaf at the current
// path so consumers can still attribute the empty container.

func TestFlattenLeaves_FlatScalar(t *testing.T) {
	t.Parallel()
	root := OrderedMap[any]{}
	root.Set("pool", "shared-pool")

	leaves := FlattenLeaves(root)
	if len(leaves) != 1 {
		t.Fatalf("expected 1 leaf, got %d", len(leaves))
	}
	if leaves[0].Path != "pool" || leaves[0].Value != "shared-pool" {
		t.Errorf("leaf = %+v, want {pool, shared-pool}", leaves[0])
	}
}

func TestFlattenLeaves_NestedPointerIntermediate(t *testing.T) {
	t.Parallel()
	// Pointer-typed intermediate (the shape leaf-writers produce).
	inner := OrderedMap[any]{}
	inner.Set("host", "api.example.com")
	inner.Set("port", 443)
	root := OrderedMap[any]{}
	root.Set("ingress", &inner)

	leaves := FlattenLeaves(root)
	want := []FlatLeaf{
		{Path: "ingress.host", Value: "api.example.com"},
		{Path: "ingress.port", Value: 443},
	}
	if len(leaves) != len(want) {
		t.Fatalf("len = %d, want %d", len(leaves), len(want))
	}
	for i, leaf := range leaves {
		if leaf.Path != want[i].Path || leaf.Value != want[i].Value {
			t.Errorf("leaves[%d] = %+v, want %+v", i, leaf, want[i])
		}
	}
}

func TestFlattenLeaves_NestedValueIntermediate(t *testing.T) {
	t.Parallel()
	// Value-typed intermediate (the shape YAML unmarshal may produce).
	inner := OrderedMap[any]{}
	inner.Set("host", "api.example.com")
	root := OrderedMap[any]{}
	root.Set("ingress", inner)

	leaves := FlattenLeaves(root)
	if len(leaves) != 1 {
		t.Fatalf("len = %d, want 1", len(leaves))
	}
	if leaves[0].Path != "ingress.host" {
		t.Errorf("leaf.Path = %q, want ingress.host", leaves[0].Path)
	}
}

func TestFlattenLeaves_MixedFlatAndNested_AuthorOrder(t *testing.T) {
	t.Parallel()
	// Mixed shape: top-level scalar, top-level nested OM, deeply
	// nested OM. The walker emits primitive leaves in author order
	// at every nesting depth.
	inner := OrderedMap[any]{}
	inner.Set("host", "api.example.com")
	inner.Set("port", 443)

	deepLeaf := OrderedMap[any]{}
	deepLeaf.Set("enabled", true)
	deep := OrderedMap[any]{}
	deep.Set("subkey", &deepLeaf)

	root := OrderedMap[any]{}
	root.Set("pool", "shared-pool")
	root.Set("ingress", &inner)
	root.Set("nested", &deep)

	leaves := FlattenLeaves(root)
	want := []FlatLeaf{
		{Path: "pool", Value: "shared-pool"},
		{Path: "ingress.host", Value: "api.example.com"},
		{Path: "ingress.port", Value: 443},
		{Path: "nested.subkey.enabled", Value: true},
	}
	if len(leaves) != len(want) {
		t.Fatalf("len = %d, want %d; got: %v", len(leaves), len(want), leaves)
	}
	for i, leaf := range leaves {
		if leaf.Path != want[i].Path {
			t.Errorf("leaves[%d].Path = %q, want %q", i, leaf.Path, want[i].Path)
		}
		if leaf.Value != want[i].Value {
			t.Errorf("leaves[%d].Value = %v, want %v", i, leaf.Value, want[i].Value)
		}
	}
}

func TestFlattenLeaves_EmptyIntermediate_YieldsAttributableLeaf(t *testing.T) {
	t.Parallel()
	// An empty nested OrderedMap at an intermediate position yields a
	// single FlatLeaf at the accumulated path. The discipline lets
	// consumers attribute the empty container — useful when a feature
	// declares the namespace without populating any leaves yet.
	emptyInner := OrderedMap[any]{}
	root := OrderedMap[any]{}
	root.Set("ingress", &emptyInner)
	root.Set("upstream", "api.example.com")

	leaves := FlattenLeaves(root)
	if len(leaves) != 2 {
		t.Fatalf("len = %d, want 2; got: %v", len(leaves), leaves)
	}
	if leaves[0].Path != "ingress" {
		t.Errorf("leaves[0].Path = %q, want ingress (empty intermediate attributed)", leaves[0].Path)
	}
	if leaves[1].Path != "upstream" {
		t.Errorf("leaves[1].Path = %q, want upstream", leaves[1].Path)
	}
}

func TestFlattenLeaves_NonOrderedMapLeaf_TreatedAsPrimitive(t *testing.T) {
	t.Parallel()
	// Non-OrderedMap values at intermediate positions — slices, plain
	// maps, structs — are emitted as primitive leaves rather than
	// recursed into. Callers consume the value as the leaf payload.
	root := OrderedMap[any]{}
	root.Set("ports", []int{8080, 8081})
	root.Set("metadata", map[string]string{"team": "infra"})

	leaves := FlattenLeaves(root)
	if len(leaves) != 2 {
		t.Fatalf("len = %d, want 2", len(leaves))
	}
	if leaves[0].Path != "ports" {
		t.Errorf("leaves[0].Path = %q, want ports", leaves[0].Path)
	}
	if leaves[1].Path != "metadata" {
		t.Errorf("leaves[1].Path = %q, want metadata", leaves[1].Path)
	}
}
