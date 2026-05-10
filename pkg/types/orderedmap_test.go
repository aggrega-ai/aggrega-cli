// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
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

func TestOrderedMap_JSONRoundTrip_FlatValues(t *testing.T) {
	t.Parallel()

	src := []byte(`{"charlie":3,"alpha":1,"bravo":2}`)

	var m OrderedMap[int]
	if err := json.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantKeys := []string{"charlie", "alpha", "bravo"}
	if got := m.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Keys after JSON Unmarshal = %v, want %v (author order lost)", got, wantKeys)
	}

	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Errorf("JSON round-trip mismatch:\ngot:  %s\nwant: %s", out, src)
	}
}

func TestOrderedMap_JSONRoundTrip_NestedStruct(t *testing.T) {
	t.Parallel()

	type entry struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	src := []byte(`{"zulu":{"name":"zz","count":26},"alpha":{"name":"aa","count":1}}`)

	var m OrderedMap[entry]
	if err := json.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	wantKeys := []string{"zulu", "alpha"}
	if got := m.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Keys = %v, want %v (author order lost at outer level)", got, wantKeys)
	}

	if v, _ := m.Get("zulu"); v != (entry{Name: "zz", Count: 26}) {
		t.Errorf("Get(zulu) = %+v, want {Name:zz Count:26}", v)
	}

	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Errorf("JSON round-trip mismatch:\ngot:  %s\nwant: %s", out, src)
	}
}

func TestOrderedMap_JSONRoundTrip_Nested(t *testing.T) {
	t.Parallel()

	src := []byte(`{"outer_b":{"inner_y":2,"inner_x":1},"outer_a":{"inner_z":3}}`)

	var m OrderedMap[OrderedMap[int]]
	if err := json.Unmarshal(src, &m); err != nil {
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

	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Errorf("JSON round-trip mismatch:\ngot:  %s\nwant: %s", out, src)
	}
}

func TestOrderedMap_JSONRejectsDuplicateKey(t *testing.T) {
	t.Parallel()

	src := []byte(`{"a":1,"b":2,"a":3}`)

	var m OrderedMap[int]
	err := json.Unmarshal(src, &m)
	if err == nil {
		t.Fatal("Unmarshal of duplicate-key JSON succeeded, want error")
	}
	if !strings.Contains(err.Error(), "duplicate key") || !strings.Contains(err.Error(), `"a"`) {
		t.Errorf("error message = %q, want substring \"duplicate key\" and key name \"a\"", err.Error())
	}
}

func TestOrderedMap_JSONRejectsNonObject(t *testing.T) {
	t.Parallel()

	src := []byte(`[1,2,3]`)

	var m OrderedMap[int]
	err := json.Unmarshal(src, &m)
	if err == nil {
		t.Fatal("Unmarshal of array into OrderedMap succeeded, want error")
	}
	if !strings.Contains(err.Error(), "expected JSON object") {
		t.Errorf("error message = %q, want substring \"expected JSON object\"", err.Error())
	}
}

func TestOrderedMap_JSONEmptyObject(t *testing.T) {
	t.Parallel()

	src := []byte(`{}`)

	var m OrderedMap[int]
	if err := json.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("Len after Unmarshal({}) = %d, want 0", m.Len())
	}

	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Errorf("empty round-trip mismatch:\ngot:  %s\nwant: %s", out, src)
	}
}

func TestOrderedMap_JSONNullDecodesAsEmpty(t *testing.T) {
	t.Parallel()

	src := []byte(`null`)

	var m OrderedMap[int]
	if err := json.Unmarshal(src, &m); err != nil {
		t.Fatalf("Unmarshal(null): %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("Len after Unmarshal(null) = %d, want 0", m.Len())
	}
}

func TestOrderedMap_JSONEmbeddedAsStructField(t *testing.T) {
	t.Parallel()

	type wrapper struct {
		Name string             `json:"name"`
		Data OrderedMap[string] `json:"data"`
	}

	src := []byte(`{"name":"example","data":{"zeta":"z","alpha":"a","mu":"m"}}`)

	var w wrapper
	if err := json.Unmarshal(src, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if w.Name != "example" {
		t.Errorf("Name = %q, want %q", w.Name, "example")
	}
	wantKeys := []string{"zeta", "alpha", "mu"}
	if got := w.Data.Keys(); !slices.Equal(got, wantKeys) {
		t.Errorf("Data.Keys = %v, want %v", got, wantKeys)
	}

	out, err := json.Marshal(&w)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Errorf("wrapper JSON round-trip mismatch:\ngot:  %s\nwant: %s", out, src)
	}
}

func TestOrderedMap_JSONTenTimesOrderStability(t *testing.T) {
	t.Parallel()

	src := []byte(`{"k3":"v3","k1":"v1","k5":"v5","k2":"v2","k4":"v4"}`)

	for i := 0; i < 10; i++ {
		var m OrderedMap[string]
		if err := json.Unmarshal(src, &m); err != nil {
			t.Fatalf("iteration %d Unmarshal: %v", i, err)
		}
		out, err := json.Marshal(&m)
		if err != nil {
			t.Fatalf("iteration %d Marshal: %v", i, err)
		}
		if !bytes.Equal(out, src) {
			t.Fatalf("iteration %d JSON order drift:\ngot:  %s\nwant: %s", i, out, src)
		}
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

// TestOrderedMap_SetOnNilReceiverPanicsWithTypedMessage pins the
// type-level contract that Set on a nil *OrderedMap[V] panics with a
// typed message identifying the call site, mirroring the builtin map's
// `m[k] = v` panic on nil map. Regression coverage for the
// stdlib-mirroring convention documented on Set.
func TestOrderedMap_SetOnNilReceiverPanicsWithTypedMessage(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected Set on nil receiver to panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T", r)
		}
		if !strings.Contains(msg, "OrderedMap.Set") {
			t.Errorf("panic message lacks typed prefix: %q", msg)
		}
	}()

	var m *OrderedMap[string]
	m.Set("k", "v")
}

// TestOrderedMap_DeleteOnNilReceiverNoOps pins the contract that Delete
// on a nil *OrderedMap[V] returns false without panicking, mirroring the
// builtin map's `delete(m, k)` no-op on nil map.
func TestOrderedMap_DeleteOnNilReceiverNoOps(t *testing.T) {
	t.Parallel()

	var m *OrderedMap[string]
	if m.Delete("k") {
		t.Error("expected Delete on nil receiver to return false")
	}
}

// TestOrderedMap_ReUnmarshalIntoPopulatedReplacesContent pins the
// spec-aligned overwrite-on-decode contract: each Unmarshal call replaces
// the receiver's contents with the decoded document. Re-unmarshalling
// into a populated OrderedMap produces a map containing only the keys
// from the new document — prior content is discarded — and overlapping
// keys do not trigger the duplicate-key check (which applies within a
// single source document, not across the receiver's lifetime).
//
// Covers both YAML and JSON Unmarshal paths.
func TestOrderedMap_ReUnmarshalIntoPopulatedReplacesContent(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		t.Parallel()

		var m OrderedMap[int]
		first := []byte("alpha: 1\nbravo: 2\ncharlie: 3\n")
		if err := yaml.Unmarshal(first, &m); err != nil {
			t.Fatalf("first Unmarshal: %v", err)
		}
		if got := m.Keys(); !slices.Equal(got, []string{"alpha", "bravo", "charlie"}) {
			t.Fatalf("after first Unmarshal Keys = %v, want [alpha bravo charlie]", got)
		}

		// Second document overlaps "alpha" with the first, adds "delta",
		// drops "bravo" and "charlie". Spec-aligned overwrite semantics
		// require this to succeed without a duplicate-key error and
		// produce a map containing only the second document's keys.
		second := []byte("alpha: 99\ndelta: 4\n")
		if err := yaml.Unmarshal(second, &m); err != nil {
			t.Fatalf("re-Unmarshal: %v", err)
		}
		if got := m.Keys(); !slices.Equal(got, []string{"alpha", "delta"}) {
			t.Errorf("after re-Unmarshal Keys = %v, want [alpha delta] (prior content should be discarded)", got)
		}
		if v, ok := m.Get("alpha"); !ok || v != 99 {
			t.Errorf("after re-Unmarshal Get(alpha) = %v ok=%v, want 99 ok=true (prior value should be replaced)", v, ok)
		}
		if v, ok := m.Get("delta"); !ok || v != 4 {
			t.Errorf("after re-Unmarshal Get(delta) = %v ok=%v, want 4 ok=true", v, ok)
		}
		if _, ok := m.Get("bravo"); ok {
			t.Error("after re-Unmarshal Get(bravo) reported ok=true, want absent (key from prior document)")
		}
		if _, ok := m.Get("charlie"); ok {
			t.Error("after re-Unmarshal Get(charlie) reported ok=true, want absent (key from prior document)")
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()

		var m OrderedMap[int]
		first := []byte(`{"alpha":1,"bravo":2,"charlie":3}`)
		if err := json.Unmarshal(first, &m); err != nil {
			t.Fatalf("first Unmarshal: %v", err)
		}
		if got := m.Keys(); !slices.Equal(got, []string{"alpha", "bravo", "charlie"}) {
			t.Fatalf("after first Unmarshal Keys = %v, want [alpha bravo charlie]", got)
		}

		second := []byte(`{"alpha":99,"delta":4}`)
		if err := json.Unmarshal(second, &m); err != nil {
			t.Fatalf("re-Unmarshal: %v", err)
		}
		if got := m.Keys(); !slices.Equal(got, []string{"alpha", "delta"}) {
			t.Errorf("after re-Unmarshal Keys = %v, want [alpha delta] (prior content should be discarded)", got)
		}
		if v, ok := m.Get("alpha"); !ok || v != 99 {
			t.Errorf("after re-Unmarshal Get(alpha) = %v ok=%v, want 99 ok=true (prior value should be replaced)", v, ok)
		}
		if v, ok := m.Get("delta"); !ok || v != 4 {
			t.Errorf("after re-Unmarshal Get(delta) = %v ok=%v, want 4 ok=true", v, ok)
		}
		if _, ok := m.Get("bravo"); ok {
			t.Error("after re-Unmarshal Get(bravo) reported ok=true, want absent (key from prior document)")
		}
		if _, ok := m.Get("charlie"); ok {
			t.Error("after re-Unmarshal Get(charlie) reported ok=true, want absent (key from prior document)")
		}
	})
}

// --- JSON-side gamma round-trip fidelity ---

func TestOrderedMapInterface_RoundTripFidelity_HighPrecisionInt(t *testing.T) {
	t.Parallel()
	const big int64 = 9007199254740993 // 2^53 + 1, smallest int losing precision via float64
	input := []byte(`{"big":9007199254740993}`)

	var m OrderedMap[interface{}]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("first Unmarshal: %v", err)
	}

	first, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("first Marshal: %v", err)
	}
	if !bytes.Equal(first, input) {
		t.Errorf("first round-trip diverged:\ninput:  %s\nfirst:  %s", input, first)
	}

	var m2 OrderedMap[interface{}]
	if err := json.Unmarshal(first, &m2); err != nil {
		t.Fatalf("second Unmarshal: %v", err)
	}
	second, err := json.Marshal(&m2)
	if err != nil {
		t.Fatalf("second Marshal: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("Marshal->Unmarshal->Marshal not idempotent:\nfirst:  %s\nsecond: %s", first, second)
	}

	raw, ok := m.Get("big")
	if !ok {
		t.Fatal("Get(big) returned ok=false")
	}
	num, ok := raw.(json.Number)
	if !ok {
		t.Fatalf("Get(big) Go-side type = %T, want json.Number", raw)
	}
	got, err := num.Int64()
	if err != nil {
		t.Fatalf("json.Number.Int64: %v", err)
	}
	if got != big {
		t.Errorf("Int64 = %d, want %d", got, big)
	}
}

func TestOrderedMapInterface_RoundTripFidelity_NestedObjectAuthorOrder(t *testing.T) {
	t.Parallel()
	input := []byte(`{"outer":{"zebra":1,"apple":2,"middle":3}}`)

	var m OrderedMap[interface{}]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	first, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(first, input) {
		t.Errorf("nested order not preserved:\ninput: %s\nfirst: %s", input, first)
	}

	raw, ok := m.Get("outer")
	if !ok {
		t.Fatal("Get(outer) ok=false")
	}
	nested, ok := raw.(*OrderedMap[interface{}])
	if !ok {
		t.Fatalf("Get(outer) Go-side type = %T, want *OrderedMap[interface{}]", raw)
	}
	want := []string{"zebra", "apple", "middle"}
	if got := nested.Keys(); !slices.Equal(got, want) {
		t.Errorf("nested Keys = %v, want %v", got, want)
	}
}

func TestOrderedMapInterface_RoundTripFidelity_DeeplyNestedAuthorOrder(t *testing.T) {
	t.Parallel()
	input := []byte(`{"L1":{"k1":1,"L2":{"k2":2,"L3":{"k3":3,"L4":{"k4":4}}}}}`)

	var m OrderedMap[interface{}]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Errorf("deeply nested round-trip diverged:\ninput:  %s\noutput: %s", input, out)
	}
}

func TestOrderedMapInterface_RoundTripFidelity_ArrayOfObjects(t *testing.T) {
	t.Parallel()
	input := []byte(`{"items":[{"z":1,"a":2},{"y":3,"b":4}]}`)

	var m OrderedMap[interface{}]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Errorf("array of objects round-trip diverged:\ninput:  %s\noutput: %s", input, out)
	}

	raw, _ := m.Get("items")
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("items: got %T, want []interface{}", raw)
	}
	if len(arr) != 2 {
		t.Fatalf("array length = %d, want 2", len(arr))
	}
	first, ok := arr[0].(*OrderedMap[interface{}])
	if !ok {
		t.Fatalf("arr[0]: got %T, want *OrderedMap[interface{}]", arr[0])
	}
	if got := first.Keys(); !slices.Equal(got, []string{"z", "a"}) {
		t.Errorf("arr[0] Keys = %v, want [z a]", got)
	}
}

func TestOrderedMapInterface_RoundTripFidelity_MixedTypes(t *testing.T) {
	t.Parallel()
	input := []byte(`{"i":1,"s":"text","f":1.5,"b":true,"n":null}`)

	var m OrderedMap[interface{}]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if v, _ := m.Get("i"); reflect.TypeOf(v) != reflect.TypeOf(json.Number("")) {
		t.Errorf("i: type = %T, want json.Number", v)
	}
	if v, _ := m.Get("s"); v != "text" {
		t.Errorf("s: got %v, want \"text\"", v)
	}
	if v, _ := m.Get("f"); reflect.TypeOf(v) != reflect.TypeOf(json.Number("")) {
		t.Errorf("f: type = %T, want json.Number", v)
	}
	if v, _ := m.Get("b"); v != true {
		t.Errorf("b: got %v, want true", v)
	}
	if v, ok := m.Get("n"); !ok || v != nil {
		t.Errorf("n: got %v ok=%v, want nil ok=true", v, ok)
	}
}

func TestOrderedMapInterface_RoundTripFidelity_NullValueDistinctFromAbsent(t *testing.T) {
	t.Parallel()
	input := []byte(`{"present":null,"absent":1}`)

	var m OrderedMap[interface{}]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v, ok := m.Get("present"); !ok || v != nil {
		t.Errorf("Get(present) = %v ok=%v, want nil ok=true", v, ok)
	}
	if _, ok := m.Get("missing"); ok {
		t.Error("Get(missing) ok=true, want false")
	}
}

func TestOrderedMapInterface_RoundTripFidelity_DepthBoundExhaustion(t *testing.T) {
	t.Parallel()
	// Generate a 200-level deeply-nested object: {"a":{"a":{...}}}
	const depth = 200
	var b bytes.Buffer
	for i := 0; i < depth; i++ {
		b.WriteString(`{"a":`)
	}
	b.WriteString(`null`)
	for i := 0; i < depth; i++ {
		b.WriteString(`}`)
	}

	var m OrderedMap[interface{}]
	err := json.Unmarshal(b.Bytes(), &m)
	if err == nil {
		t.Fatal("expected ErrOrderedMapDecodeDepthExceeded, got nil")
	}
	if !errors.Is(err, ErrOrderedMapDecodeDepthExceeded) {
		t.Errorf("error chain does not wrap ErrOrderedMapDecodeDepthExceeded: %v", err)
	}
}

// --- JSON V-method-set delegation ---

type customJSONV struct {
	Sentinel string
}

// UnmarshalJSON intentionally ignores input and writes a sentinel value;
// the test verifies gamma's recursive path defers to this method.
func (c *customJSONV) UnmarshalJSON(_ []byte) error {
	c.Sentinel = "from-custom-unmarshal"
	return nil
}

func TestOrderedMap_VImplementsUnmarshalJSON_DelegatesToVUnmarshaler(t *testing.T) {
	t.Parallel()
	input := []byte(`{"k":"would-be-ignored-by-custom-unmarshal"}`)

	var m OrderedMap[*customJSONV]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	v, ok := m.Get("k")
	if !ok {
		t.Fatal("Get(k) ok=false")
	}
	if v == nil || v.Sentinel != "from-custom-unmarshal" {
		t.Errorf("Sentinel = %q, want \"from-custom-unmarshal\"", func() string {
			if v == nil {
				return "<nil>"
			}
			return v.Sentinel
		}())
	}
}

type customJSONVValue struct {
	Sentinel string
}

// UnmarshalJSON on the value receiver. encoding/json finds it via &v.
func (c *customJSONVValue) UnmarshalJSON(_ []byte) error {
	c.Sentinel = "from-pointer-unmarshal"
	return nil
}

func TestOrderedMap_VPointerImplementsUnmarshalJSON_DelegatesToVUnmarshaler(t *testing.T) {
	t.Parallel()
	input := []byte(`{"k":"would-be-ignored"}`)

	var m OrderedMap[customJSONVValue]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	v, ok := m.Get("k")
	if !ok {
		t.Fatal("Get(k) ok=false")
	}
	if v.Sentinel != "from-pointer-unmarshal" {
		t.Errorf("Sentinel = %q, want \"from-pointer-unmarshal\"", v.Sentinel)
	}
}

func TestOrderedMap_VInterfaceWithoutUnmarshalJSON_UsesRecursivePath(t *testing.T) {
	t.Parallel()
	input := []byte(`{"k":{"nested":1}}`)

	var m OrderedMap[interface{}]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	raw, _ := m.Get("k")
	nested, ok := raw.(*OrderedMap[interface{}])
	if !ok {
		t.Fatalf("Get(k) Go-side type = %T, want *OrderedMap[interface{}]", raw)
	}
	if got := nested.Keys(); !slices.Equal(got, []string{"nested"}) {
		t.Errorf("nested Keys = %v, want [nested]", got)
	}
}

func TestOrderedMap_VConcreteWithoutUnmarshalJSON_UsesTypedDecodePath(t *testing.T) {
	t.Parallel()
	input := []byte(`{"k":"value"}`)

	var m OrderedMap[string]
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	v, ok := m.Get("k")
	if !ok {
		t.Fatal("Get(k) ok=false")
	}
	if v != "value" {
		t.Errorf("Get(k) = %q, want \"value\"", v)
	}
}

// --- YAML-side gamma round-trip fidelity ---

func TestOrderedMapInterface_YAMLRoundTripFidelity_HighPrecisionInt(t *testing.T) {
	t.Parallel()
	const big int64 = 9007199254740993 // 2^53 + 1
	input := []byte("big: 9007199254740993\n")

	var m OrderedMap[interface{}]
	if err := yaml.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	raw, ok := m.Get("big")
	if !ok {
		t.Fatal("Get(big) ok=false")
	}
	switch v := raw.(type) {
	case int:
		if int64(v) != big {
			t.Errorf("int value = %d, want %d", v, big)
		}
	case int64:
		if v != big {
			t.Errorf("int64 value = %d, want %d", v, big)
		}
	default:
		t.Fatalf("Get(big) Go-side type = %T, want int or int64", raw)
	}

	assertMarshalRoundTripIdempotent(t, &m)
}

func TestOrderedMapInterface_YAMLRoundTripFidelity_NestedObjectAuthorOrder(t *testing.T) {
	t.Parallel()
	input := []byte("outer:\n  zebra: 1\n  apple: 2\n  middle: 3\n")

	var m OrderedMap[interface{}]
	if err := yaml.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	raw, ok := m.Get("outer")
	if !ok {
		t.Fatal("Get(outer) ok=false")
	}
	nested, ok := raw.(*OrderedMap[interface{}])
	if !ok {
		t.Fatalf("Get(outer) Go-side type = %T, want *OrderedMap[interface{}]", raw)
	}
	want := []string{"zebra", "apple", "middle"}
	if got := nested.Keys(); !slices.Equal(got, want) {
		t.Errorf("nested Keys = %v, want %v", got, want)
	}

	assertMarshalRoundTripIdempotent(t, &m)
}

func TestOrderedMapInterface_YAMLRoundTripFidelity_DeeplyNestedAuthorOrder(t *testing.T) {
	t.Parallel()
	input := []byte("L1:\n  k1: 1\n  L2:\n    k2: 2\n    L3:\n      k3: 3\n      L4:\n        k4: 4\n")

	var m OrderedMap[interface{}]
	if err := yaml.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	assertMarshalRoundTripIdempotent(t, &m)

	// Walk to L4 and verify the leaf key order at the deepest level.
	cur := &m
	for _, key := range []string{"L1", "L2", "L3", "L4"} {
		raw, ok := cur.Get(key)
		if !ok {
			t.Fatalf("Get(%q) ok=false at level walk", key)
		}
		nested, ok := raw.(*OrderedMap[interface{}])
		if !ok {
			break // L4 is the leaf; the inner k4 entry is a scalar
		}
		cur = nested
	}
}

func TestOrderedMapInterface_YAMLRoundTripFidelity_ArrayOfMappings(t *testing.T) {
	t.Parallel()
	input := []byte("items:\n  - z: 1\n    a: 2\n  - y: 3\n    b: 4\n")

	var m OrderedMap[interface{}]
	if err := yaml.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	raw, _ := m.Get("items")
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("items: got %T, want []interface{}", raw)
	}
	if len(arr) != 2 {
		t.Fatalf("array length = %d, want 2", len(arr))
	}
	first, ok := arr[0].(*OrderedMap[interface{}])
	if !ok {
		t.Fatalf("arr[0]: got %T, want *OrderedMap[interface{}]", arr[0])
	}
	if got := first.Keys(); !slices.Equal(got, []string{"z", "a"}) {
		t.Errorf("arr[0] Keys = %v, want [z a]", got)
	}

	assertMarshalRoundTripIdempotent(t, &m)
}

func TestOrderedMapInterface_YAMLRoundTripFidelity_DepthBoundExhaustion(t *testing.T) {
	t.Parallel()
	const depth = 200
	var b bytes.Buffer
	for i := 0; i < depth; i++ {
		for j := 0; j < i; j++ {
			b.WriteString("  ")
		}
		b.WriteString("a:\n")
	}
	for j := 0; j < depth; j++ {
		b.WriteString("  ")
	}
	b.WriteString("null\n")

	var m OrderedMap[interface{}]
	err := yaml.Unmarshal(b.Bytes(), &m)
	if err == nil {
		t.Fatal("expected ErrOrderedMapDecodeDepthExceeded, got nil")
	}
	if !errors.Is(err, ErrOrderedMapDecodeDepthExceeded) {
		t.Errorf("error chain does not wrap ErrOrderedMapDecodeDepthExceeded: %v", err)
	}
}

// --- YAML V-method-set delegation ---

type customYAMLV struct {
	Sentinel string
}

// UnmarshalYAML intentionally ignores the input node and writes a sentinel;
// the test verifies gamma's recursive path defers to this method.
func (c *customYAMLV) UnmarshalYAML(_ *yaml.Node) error {
	c.Sentinel = "from-custom-yaml-unmarshal"
	return nil
}

func TestOrderedMap_VImplementsYAMLUnmarshaler_DelegatesToYAMLUnmarshaler(t *testing.T) {
	t.Parallel()
	input := []byte("k: would-be-ignored\n")

	var m OrderedMap[*customYAMLV]
	if err := yaml.Unmarshal(input, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	v, ok := m.Get("k")
	if !ok {
		t.Fatal("Get(k) ok=false")
	}
	if v == nil || v.Sentinel != "from-custom-yaml-unmarshal" {
		t.Errorf("Sentinel mismatch: %+v", v)
	}
}
