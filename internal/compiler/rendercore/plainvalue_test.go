// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPlainValueJSONNumberInteger proves an integral json.Number renormalizes to
// int64 (the Go numeric type a YAML scalar parse yields), so a value that crossed
// the wire serializes unquoted rather than as the string-kind json.Number.
func TestPlainValueJSONNumberInteger(t *testing.T) {
	got := PlainValue(json.Number("8080"))
	want := int64(8080)
	if got != want {
		t.Fatalf("PlainValue(json.Number(\"8080\")) = %#v (%T); want %#v (int64)", got, got, want)
	}
}

// TestPlainValueJSONNumberFloat proves a fractional json.Number renormalizes to
// float64.
func TestPlainValueJSONNumberFloat(t *testing.T) {
	got := PlainValue(json.Number("1.5"))
	want := float64(1.5)
	if got != want {
		t.Fatalf("PlainValue(json.Number(\"1.5\")) = %#v (%T); want %#v (float64)", got, got, want)
	}
}

// TestPlainValueJSONNumberUnparseable proves a json.Number that parses as neither
// an integer nor a float is left untouched — the fallback never fabricates a
// numeric type from a value it cannot parse.
func TestPlainValueJSONNumberUnparseable(t *testing.T) {
	in := json.Number("1.2.3")
	got := PlainValue(in)
	n, ok := got.(json.Number)
	if !ok {
		t.Fatalf("PlainValue(json.Number(\"1.2.3\")) = %#v (%T); want the json.Number left untouched", got, got)
	}
	if n != in {
		t.Fatalf("PlainValue mutated the untouched json.Number: got %q want %q", n, in)
	}
}

// TestPlainValueScalarPassThrough proves non-numeric scalars pass through with
// their Go type and value intact.
func TestPlainValueScalarPassThrough(t *testing.T) {
	cases := []any{"hello", true, int64(3), 4.2}
	for _, c := range cases {
		if got := PlainValue(c); got != c {
			t.Errorf("PlainValue(%#v) = %#v; want the value unchanged", c, got)
		}
	}
}

// TestPlainValueNestedOrderedMapPointer proves a *OrderedMap[any] value flattens
// to map[string]any recursively, renormalizing a nested json.Number.
func TestPlainValueNestedOrderedMapPointer(t *testing.T) {
	nested := types.NewOrderedMap[any]()
	nested.Set("port", json.Number("5432"))

	got := PlainValue(nested)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("PlainValue(*OrderedMap) = %#v (%T); want map[string]any", got, got)
	}
	if v, want := m["port"], int64(5432); v != want {
		t.Fatalf("nested port = %#v (%T); want %#v (int64)", v, v, want)
	}
}

// TestPlainValueOrderedMapValueForm proves the non-pointer OrderedMap[any] value
// form also flattens to map[string]any.
func TestPlainValueOrderedMapValueForm(t *testing.T) {
	nested := types.NewOrderedMap[any]()
	nested.Set("replicas", json.Number("3"))

	got := PlainValue(*nested)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("PlainValue(OrderedMap value) = %#v (%T); want map[string]any", got, got)
	}
	if v, want := m["replicas"], int64(3); v != want {
		t.Fatalf("replicas = %#v (%T); want %#v (int64)", v, v, want)
	}
}

// TestPlainValueSliceRecursion proves a []any is walked element-wise, so a
// json.Number inside a slice is renormalized in place.
func TestPlainValueSliceRecursion(t *testing.T) {
	got := PlainValue([]any{json.Number("1"), "two", json.Number("3.5")})
	s, ok := got.([]any)
	if !ok {
		t.Fatalf("PlainValue([]any) = %#v (%T); want []any", got, got)
	}
	if len(s) != 3 {
		t.Fatalf("slice length = %d; want 3", len(s))
	}
	if v, want := s[0], int64(1); v != want {
		t.Errorf("s[0] = %#v (%T); want %#v (int64)", v, v, want)
	}
	if v, want := s[1], "two"; v != want {
		t.Errorf("s[1] = %#v; want %q", v, want)
	}
	if v, want := s[2], float64(3.5); v != want {
		t.Errorf("s[2] = %#v (%T); want %#v (float64)", v, v, want)
	}
}

// TestOrderedMapToPlainFlattensAndRenormalizes proves the ordered map flattens to
// a plain map[string]any, recursing into a nested ordered map and a slice, and
// renormalizing every json.Number to the Go numeric type a YAML scalar parse
// would yield.
func TestOrderedMapToPlainFlattensAndRenormalizes(t *testing.T) {
	inner := types.NewOrderedMap[any]()
	inner.Set("published", json.Number("8080"))

	m := types.NewOrderedMap[any]()
	m.Set("replicas", json.Number("3"))
	m.Set("weight", json.Number("1.5"))
	m.Set("name", "api")
	m.Set("ports", inner)
	m.Set("tags", []any{json.Number("7"), "edge"})

	got := OrderedMapToPlain(m)

	want := map[string]any{
		"replicas": int64(3),
		"weight":   float64(1.5),
		"name":     "api",
		"ports":    map[string]any{"published": int64(8080)},
		"tags":     []any{int64(7), "edge"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderedMapToPlain mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
