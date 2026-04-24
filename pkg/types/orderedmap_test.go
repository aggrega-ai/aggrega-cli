// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// assertMarshalRoundTripIdempotent asserts that marshalling v, parsing the
// output back into a fresh OrderedMap[V], and marshalling that second copy
// produces byte-identical output. Stronger guarantee than byte-equality with
// a hand-written fixture: it exercises the full Marshal -> Unmarshal ->
// Marshal loop and doesn't depend on the yaml.v3 encoder's default indent
// (which is an implementation detail that can vary across versions).
//
// The caller verifies order preservation separately via Keys().
func assertMarshalRoundTripIdempotent[V any](t *testing.T, m *OrderedMap[V]) {
	t.Helper()
	first, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("first Marshal: %v", err)
	}
	var m2 OrderedMap[V]
	if err := yaml.Unmarshal(first, &m2); err != nil {
		t.Fatalf("round-trip Unmarshal: %v", err)
	}
	second, err := yaml.Marshal(&m2)
	if err != nil {
		t.Fatalf("second Marshal: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("Marshal->Unmarshal->Marshal not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !slices.Equal(m.Keys(), m2.Keys()) {
		t.Errorf("round-trip changed key order:\nfirst Keys:  %v\nsecond Keys: %v", m.Keys(), m2.Keys())
	}
}

func TestOrderedMap_ZeroValue(t *testing.T) {
	t.Parallel()

	var m OrderedMap[string]

	if got := m.Len(); got != 0 {
		t.Errorf("Len() on zero value = %d, want 0", got)
	}
	if keys := m.Keys(); keys == nil {
		t.Error("Keys() on zero value returned nil, want non-nil empty slice")
	} else if len(keys) != 0 {
		t.Errorf("Keys() on zero value = %v, want empty", keys)
	}
	if _, ok := m.Get("missing"); ok {
		t.Error("Get on zero value reported ok=true")
	}
	count := 0
	for range m.All() {
		count++
	}
	if count != 0 {
		t.Errorf("All() on zero value yielded %d entries, want 0", count)
	}
}

func TestOrderedMap_SetGetDeleteLen(t *testing.T) {
	t.Parallel()

	var m OrderedMap[int]
	m.Set("c", 3)
	m.Set("a", 1)
	m.Set("b", 2)

	if got := m.Len(); got != 3 {
		t.Errorf("Len = %d, want 3", got)
	}

	if v, ok := m.Get("a"); !ok || v != 1 {
		t.Errorf("Get(a) = (%d, %v), want (1, true)", v, ok)
	}
	if v, ok := m.Get("b"); !ok || v != 2 {
		t.Errorf("Get(b) = (%d, %v), want (2, true)", v, ok)
	}
	if v, ok := m.Get("c"); !ok || v != 3 {
		t.Errorf("Get(c) = (%d, %v), want (3, true)", v, ok)
	}

	wantKeys := []string{"c", "a", "b"}
	if got := m.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Keys = %v, want %v", got, wantKeys)
	}

	if !m.Delete("a") {
		t.Error("Delete(a) = false, want true")
	}
	if m.Len() != 2 {
		t.Errorf("Len after Delete(a) = %d, want 2", m.Len())
	}
	if _, ok := m.Get("a"); ok {
		t.Error("Get(a) after Delete reported ok=true")
	}
	wantKeys = []string{"c", "b"}
	if got := m.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Keys after Delete(a) = %v, want %v", got, wantKeys)
	}

	if m.Delete("nonexistent") {
		t.Error("Delete(nonexistent) = true, want false")
	}
}

func TestOrderedMap_SetExistingPreservesPosition(t *testing.T) {
	t.Parallel()

	var m OrderedMap[string]
	m.Set("first", "1")
	m.Set("second", "2")
	m.Set("third", "3")

	m.Set("second", "updated")

	wantKeys := []string{"first", "second", "third"}
	if got := m.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Keys after Set-existing = %v, want %v", got, wantKeys)
	}
	if v, _ := m.Get("second"); v != "updated" {
		t.Errorf("Get(second) after Set-existing = %q, want %q", v, "updated")
	}
}

func TestOrderedMap_KeysDefensiveCopy(t *testing.T) {
	t.Parallel()

	var m OrderedMap[int]
	m.Set("x", 1)
	m.Set("y", 2)

	keys := m.Keys()
	keys[0] = "mutated"
	keys = append(keys, "extra")

	freshKeys := m.Keys()
	want := []string{"x", "y"}
	if !slices.Equal(freshKeys, want) {
		t.Errorf("fresh Keys() = %v, want %v (mutation on earlier slice leaked)", freshKeys, want)
	}
}

func TestOrderedMap_AllYieldsInOrder(t *testing.T) {
	t.Parallel()

	var m OrderedMap[int]
	m.Set("gamma", 3)
	m.Set("alpha", 1)
	m.Set("beta", 2)

	var got []string
	var vals []int
	for k, v := range m.All() {
		got = append(got, k)
		vals = append(vals, v)
	}

	wantKeys := []string{"gamma", "alpha", "beta"}
	wantVals := []int{3, 1, 2}
	if !slices.Equal(got, wantKeys) {
		t.Errorf("All() keys = %v, want %v", got, wantKeys)
	}
	if !slices.Equal(vals, wantVals) {
		t.Errorf("All() values = %v, want %v", vals, wantVals)
	}
}

func TestOrderedMap_AllEarlyStop(t *testing.T) {
	t.Parallel()

	var m OrderedMap[int]
	m.Set("a", 1)
	m.Set("b", 2)
	m.Set("c", 3)

	count := 0
	for range m.All() {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("early-stop iteration count = %d, want 2", count)
	}
}

func TestOrderedMap_YAMLRoundTrip_FlatValues(t *testing.T) {
	t.Parallel()

	src := []byte("charlie: 3\nalpha: 1\nbravo: 2\n")

	var m OrderedMap[int]
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantKeys := []string{"charlie", "alpha", "bravo"}
	if got := m.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Keys after Unmarshal = %v, want %v (author order lost)", got, wantKeys)
	}

	out, err := yaml.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Errorf("round-trip mismatch:\ngot:\n%s\nwant:\n%s", out, src)
	}
}

func TestOrderedMap_YAMLRoundTrip_NestedStruct(t *testing.T) {
	t.Parallel()

	type entry struct {
		Name  string `yaml:"name"`
		Count int    `yaml:"count"`
	}

	src := []byte("zulu:\n  name: zz\n  count: 26\nalpha:\n  name: aa\n  count: 1\n")

	var m OrderedMap[entry]
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantKeys := []string{"zulu", "alpha"}
	if got := m.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Keys = %v, want %v (author order lost after Unmarshal)", got, wantKeys)
	}

	if v, _ := m.Get("zulu"); v != (entry{Name: "zz", Count: 26}) {
		t.Errorf("Get(zulu) = %+v, want {Name:zz Count:26}", v)
	}

	assertMarshalRoundTripIdempotent(t, &m)
}

func TestOrderedMap_YAMLRoundTrip_ListValues(t *testing.T) {
	t.Parallel()

	src := []byte("east:\n  - 1\n  - 2\nnorth:\n  - 3\n  - 4\nwest:\n  - 5\n  - 6\n")

	var m OrderedMap[[]int]
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantKeys := []string{"east", "north", "west"}
	if got := m.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Keys = %v, want %v (author order lost after Unmarshal)", got, wantKeys)
	}
	if v, _ := m.Get("east"); !slices.Equal(v, []int{1, 2}) {
		t.Errorf("Get(east) = %v, want [1 2]", v)
	}

	assertMarshalRoundTripIdempotent(t, &m)
}

func TestOrderedMap_YAMLRoundTrip_Nested(t *testing.T) {
	t.Parallel()

	src := []byte("outer_b:\n  inner_y: 2\n  inner_x: 1\nouter_a:\n  inner_z: 3\n")

	var m OrderedMap[OrderedMap[int]]
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantOuter := []string{"outer_b", "outer_a"}
	if got := m.Keys(); !slices.Equal(got, wantOuter) {
		t.Errorf("outer Keys = %v, want %v (author order lost at outer level)", got, wantOuter)
	}

	inner, ok := m.Get("outer_b")
	if !ok {
		t.Fatal("Get(outer_b) not present")
	}
	wantInner := []string{"inner_y", "inner_x"}
	if got := inner.Keys(); !slices.Equal(got, wantInner) {
		t.Errorf("outer_b inner Keys = %v, want %v (author order lost at inner level)", got, wantInner)
	}

	assertMarshalRoundTripIdempotent(t, &m)
}

func TestOrderedMap_UnmarshalRejectsDuplicateKey(t *testing.T) {
	t.Parallel()

	src := []byte("a: 1\nb: 2\na: 3\n")

	var m OrderedMap[int]
	err := yaml.Unmarshal(src, &m)
	if err == nil {
		t.Fatal("Unmarshal of duplicate-key YAML succeeded, want error")
	}
	if !strings.Contains(err.Error(), "duplicate key") || !strings.Contains(err.Error(), `"a"`) {
		t.Errorf("error message = %q, want substring \"duplicate key\" and key name \"a\"", err.Error())
	}
}

func TestOrderedMap_UnmarshalRejectsNonMappingNode(t *testing.T) {
	t.Parallel()

	src := []byte("- a\n- b\n")

	var m OrderedMap[string]
	err := yaml.Unmarshal(src, &m)
	if err == nil {
		t.Fatal("Unmarshal of sequence node into OrderedMap succeeded, want error")
	}
	if !strings.Contains(err.Error(), "expected mapping node") {
		t.Errorf("error message = %q, want substring \"expected mapping node\"", err.Error())
	}
}

func TestOrderedMap_YAMLRoundTrip_EmptyMap(t *testing.T) {
	t.Parallel()

	src := []byte("{}\n")

	var m OrderedMap[int]
	if err := yaml.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("Len after Unmarshal({}) = %d, want 0", m.Len())
	}
	if keys := m.Keys(); keys == nil {
		t.Error("Keys() after Unmarshal({}) returned nil, want non-nil empty slice")
	}
}

func TestOrderedMap_EmbeddedAsStructField(t *testing.T) {
	t.Parallel()

	type wrapper struct {
		Name string             `yaml:"name"`
		Data OrderedMap[string] `yaml:"data"`
	}

	src := []byte("name: example\ndata:\n  zeta: z\n  alpha: a\n  mu: m\n")

	var w wrapper
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if w.Name != "example" {
		t.Errorf("Name = %q, want %q", w.Name, "example")
	}

	wantKeys := []string{"zeta", "alpha", "mu"}
	if got := w.Data.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Data.Keys = %v, want %v (author order lost when embedded as struct field)", got, wantKeys)
	}

	// Marshal -> Unmarshal -> Marshal should be byte-idempotent. We don't
	// assert against the hand-written src because yaml.v3's emission indent
	// for content produced via MarshalYAML (Node tree) may differ from its
	// emission indent for Go-struct content — both reach the top-level
	// Encoder, but through different code paths.
	first, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("first Marshal: %v", err)
	}
	var w2 wrapper
	if err := yaml.Unmarshal(first, &w2); err != nil {
		t.Fatalf("round-trip Unmarshal: %v", err)
	}
	second, err := yaml.Marshal(&w2)
	if err != nil {
		t.Fatalf("second Marshal: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("wrapper round-trip not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if got := w2.Data.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("after round-trip Data.Keys = %v, want %v", got, wantKeys)
	}
}

func TestOrderedMap_TenTimesOrderStability(t *testing.T) {
	t.Parallel()

	// Go's builtin map randomizes iteration order. If OrderedMap delegated to
	// map iteration for round-trip, some runs would produce a different byte
	// output. Ten iterations make spurious order-randomization failures
	// extremely unlikely to slip through.
	src := []byte("k3: v3\nk1: v1\nk5: v5\nk2: v2\nk4: v4\n")

	for i := 0; i < 10; i++ {
		var m OrderedMap[string]
		if err := yaml.Unmarshal(src, &m); err != nil {
			t.Fatalf("iteration %d Unmarshal: %v", i, err)
		}
		out, err := yaml.Marshal(&m)
		if err != nil {
			t.Fatalf("iteration %d Marshal: %v", i, err)
		}
		if !bytes.Equal(out, src) {
			t.Fatalf("iteration %d order drift:\ngot:\n%s\nwant:\n%s", i, out, src)
		}
	}
}
