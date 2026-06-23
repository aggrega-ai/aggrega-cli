// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonpatch

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func parseDoc(t *testing.T, js string) *types.OrderedMap[any] {
	t.Helper()
	m := types.NewOrderedMap[any]()
	if err := json.Unmarshal([]byte(js), m); err != nil {
		t.Fatalf("parseDoc(%s): %v", js, err)
	}
	return m
}

// parseVal decodes a JSON value fragment into the canonical document
// model (json.Number for numbers, *OrderedMap[any] for objects, []any
// for arrays) by wrapping it in a one-key object and reading the value
// back. Patch values must be in this form so equality and emitted order
// match the document side.
func parseVal(t *testing.T, js string) any {
	t.Helper()
	wrap := types.NewOrderedMap[any]()
	if err := json.Unmarshal([]byte(`{"v":`+js+`}`), wrap); err != nil {
		t.Fatalf("parseVal(%s): %v", js, err)
	}
	v, _ := wrap.Get("v")
	return v
}

func mustMarshal(t *testing.T, m *types.OrderedMap[any]) string {
	t.Helper()
	b, err := m.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestRFC6902AppendixA exercises the worked examples from RFC 6902
// Appendix A. Result comparison is order-independent (deepEqual), so it
// asserts semantic correctness; byte-level key-order is asserted
// separately in TestKeyOrderPreserved.
func TestRFC6902AppendixA(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		ops  []Op
		want string // expected document; "" means expect an error
	}{
		{
			name: "A.1 add object member",
			doc:  `{"foo":"bar"}`,
			ops:  []Op{{Op: "add", Path: "/baz", Value: parseVal(t, `"qux"`)}},
			want: `{"foo":"bar","baz":"qux"}`,
		},
		{
			name: "A.2 add array element",
			doc:  `{"foo":["bar","baz"]}`,
			ops:  []Op{{Op: "add", Path: "/foo/1", Value: parseVal(t, `"qux"`)}},
			want: `{"foo":["bar","qux","baz"]}`,
		},
		{
			name: "A.3 remove object member",
			doc:  `{"baz":"qux","foo":"bar"}`,
			ops:  []Op{{Op: "remove", Path: "/baz"}},
			want: `{"foo":"bar"}`,
		},
		{
			name: "A.4 remove array element",
			doc:  `{"foo":["bar","qux","baz"]}`,
			ops:  []Op{{Op: "remove", Path: "/foo/1"}},
			want: `{"foo":["bar","baz"]}`,
		},
		{
			name: "A.5 replace value",
			doc:  `{"baz":"qux","foo":"bar"}`,
			ops:  []Op{{Op: "replace", Path: "/baz", Value: parseVal(t, `"boo"`)}},
			want: `{"baz":"boo","foo":"bar"}`,
		},
		{
			name: "A.6 move value",
			doc:  `{"foo":{"bar":"baz","waldo":"fred"},"qux":{"corge":"grault"}}`,
			ops:  []Op{{Op: "move", From: "/foo/waldo", Path: "/qux/thud"}},
			want: `{"foo":{"bar":"baz"},"qux":{"corge":"grault","thud":"fred"}}`,
		},
		{
			name: "A.7 move array element",
			doc:  `{"foo":["all","grass","cows","eat"]}`,
			ops:  []Op{{Op: "move", From: "/foo/1", Path: "/foo/3"}},
			want: `{"foo":["all","cows","eat","grass"]}`,
		},
		{
			name: "A.8 test success",
			doc:  `{"baz":"qux","foo":["a",2,"c"]}`,
			ops: []Op{
				{Op: "test", Path: "/baz", Value: parseVal(t, `"qux"`)},
				{Op: "test", Path: "/foo/1", Value: parseVal(t, `2`)},
			},
			want: `{"baz":"qux","foo":["a",2,"c"]}`,
		},
		{
			name: "A.9 test error",
			doc:  `{"baz":"qux"}`,
			ops:  []Op{{Op: "test", Path: "/baz", Value: parseVal(t, `"bar"`)}},
			want: "",
		},
		{
			name: "A.10 add nested member object",
			doc:  `{"foo":"bar"}`,
			ops:  []Op{{Op: "add", Path: "/child", Value: parseVal(t, `{"grandchild":{}}`)}},
			want: `{"foo":"bar","child":{"grandchild":{}}}`,
		},
		{
			name: "A.12 add to nonexistent target",
			doc:  `{"foo":"bar"}`,
			ops:  []Op{{Op: "add", Path: "/baz/bat", Value: parseVal(t, `"qux"`)}},
			want: "",
		},
		{
			name: "A.14 escape ordering test",
			doc:  `{"/":9,"~1":10}`,
			ops:  []Op{{Op: "test", Path: "/~01", Value: parseVal(t, `10`)}},
			want: `{"/":9,"~1":10}`,
		},
		{
			name: "A.15 compare string and number",
			doc:  `{"/":9,"~1":10}`,
			ops:  []Op{{Op: "test", Path: "/~01", Value: parseVal(t, `"10"`)}},
			want: "",
		},
		{
			name: "A.16 add array value",
			doc:  `{"foo":["bar"]}`,
			ops:  []Op{{Op: "add", Path: "/foo/-", Value: parseVal(t, `["abc","def"]`)}},
			want: `{"foo":["bar",["abc","def"]]}`,
		},
		{
			name: "copy object member",
			doc:  `{"foo":{"a":1},"bar":{}}`,
			ops:  []Op{{Op: "copy", From: "/foo", Path: "/bar/clone"}},
			want: `{"foo":{"a":1},"bar":{"clone":{"a":1}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseDoc(t, tc.doc)
			err := Apply(doc, tc.ops)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("expected error, got nil; doc=%s", mustMarshal(t, doc))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !deepEqual(doc, parseVal(t, tc.want)) {
				t.Fatalf("result mismatch\n got: %s\nwant: %s", mustMarshal(t, doc), tc.want)
			}
		})
	}
}

// TestKeyOrderPreserved asserts byte-exact output: a mutation must not
// reorder sibling keys, and an added key appends. This is the property
// the public conformance suite does not cover and that Invariant 8
// depends on.
func TestKeyOrderPreserved(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		ops  []Op
		want string
	}{
		{
			name: "replace leaves sibling order intact",
			doc:  `{"z":1,"a":2,"m":3}`,
			ops:  []Op{{Op: "replace", Path: "/a", Value: parseVal(t, `99`)}},
			want: `{"z":1,"a":99,"m":3}`,
		},
		{
			name: "add appends at end",
			doc:  `{"z":1,"a":2}`,
			ops:  []Op{{Op: "add", Path: "/m", Value: parseVal(t, `3`)}},
			want: `{"z":1,"a":2,"m":3}`,
		},
		{
			name: "remove keeps remaining order",
			doc:  `{"z":1,"a":2,"m":3}`,
			ops:  []Op{{Op: "remove", Path: "/a"}},
			want: `{"z":1,"m":3}`,
		},
		{
			name: "nested replace preserves both levels",
			doc:  `{"outer":{"y":1,"b":2},"tail":0}`,
			ops:  []Op{{Op: "replace", Path: "/outer/b", Value: parseVal(t, `5`)}},
			want: `{"outer":{"y":1,"b":5},"tail":0}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseDoc(t, tc.doc)
			if err := Apply(doc, tc.ops); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if got := mustMarshal(t, doc); got != tc.want {
				t.Fatalf("byte mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestDeterminism applies the same patch set to two fresh copies of a
// document and asserts byte-identical output.
func TestDeterminism(t *testing.T) {
	docJSON := `{"spec":{"template":{"spec":{"containers":[{"name":"app","env":[{"name":"A","value":"1"}]}]}}},"meta":{"z":1,"a":2}}`
	ops := []Op{
		{Op: "add", Path: "/spec/template/spec/containers/0/env/-", Value: parseVal(t, `{"name":"B","value":"2"}`)},
		{Op: "replace", Path: "/meta/a", Value: parseVal(t, `42`)},
		{Op: "add", Path: "/spec/replicas", Value: parseVal(t, `3`)},
	}
	first := parseDoc(t, docJSON)
	if err := Apply(first, ops); err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	second := parseDoc(t, docJSON)
	if err := Apply(second, ops); err != nil {
		t.Fatalf("apply 2: %v", err)
	}
	if mustMarshal(t, first) != mustMarshal(t, second) {
		t.Fatalf("non-deterministic output\n1: %s\n2: %s", mustMarshal(t, first), mustMarshal(t, second))
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		ops  []Op
	}{
		{"remove missing member", `{"a":1}`, []Op{{Op: "remove", Path: "/b"}}},
		{"replace missing member", `{"a":1}`, []Op{{Op: "replace", Path: "/b", Value: parseVal(t, `2`)}}},
		{"array index out of range", `{"a":[1,2]}`, []Op{{Op: "replace", Path: "/a/5", Value: parseVal(t, `9`)}}},
		{"array leading zero", `{"a":[1,2]}`, []Op{{Op: "remove", Path: "/a/01"}}},
		{"dash index for remove", `{"a":[1,2]}`, []Op{{Op: "remove", Path: "/a/-"}}},
		{"descend through scalar", `{"a":1}`, []Op{{Op: "add", Path: "/a/b", Value: parseVal(t, `2`)}}},
		{"move into own child", `{"a":{"b":1}}`, []Op{{Op: "move", From: "/a", Path: "/a/c"}}},
		{"unknown op", `{"a":1}`, []Op{{Op: "frobnicate", Path: "/a"}}},
		{"bad pointer", `{"a":1}`, []Op{{Op: "add", Path: "a", Value: parseVal(t, `2`)}}},
		// move/copy with an empty "from" must halt, not be read as a
		// root-pointer source that silently clears the document.
		{"move empty from", `{"a":1}`, []Op{{Op: "move", Path: "/x"}}},
		{"copy empty from", `{"a":1}`, []Op{{Op: "copy", Path: "/x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseDoc(t, tc.doc)
			if err := Apply(doc, tc.ops); err == nil {
				t.Fatalf("expected error, got nil; doc=%s", mustMarshal(t, doc))
			}
		})
	}
}

// TestTestFailureSentinel confirms a failed test op is distinguishable
// from a malformed patch via errors.Is, so a caller can treat it as a
// controlled outcome.
func TestTestFailureSentinel(t *testing.T) {
	doc := parseDoc(t, `{"a":1}`)
	err := Apply(doc, []Op{{Op: "test", Path: "/a", Value: parseVal(t, `2`)}})
	if !errors.Is(err, ErrTestFailed) {
		t.Fatalf("expected ErrTestFailed, got %v", err)
	}
}

// TestArrayWriteBack verifies that a length-changing mutation deep
// inside nested arrays propagates to the emitted document — the
// settable-parent-handle path.
func TestArrayWriteBack(t *testing.T) {
	doc := parseDoc(t, `{"matrix":[[1,2],[3,4]]}`)
	if err := Apply(doc, []Op{
		{Op: "add", Path: "/matrix/1/-", Value: parseVal(t, `5`)},
		{Op: "remove", Path: "/matrix/0/0"},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := `{"matrix":[[2],[3,4,5]]}`
	if got := mustMarshal(t, doc); got != want {
		t.Fatalf("write-back mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestRootReplace verifies whole-document replacement via the "" path.
func TestRootReplace(t *testing.T) {
	doc := parseDoc(t, `{"old":1}`)
	if err := Apply(doc, []Op{{Op: "replace", Path: "", Value: parseVal(t, `{"new":2,"more":3}`)}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := `{"new":2,"more":3}`
	if got := mustMarshal(t, doc); got != want {
		t.Fatalf("root replace mismatch\n got: %s\nwant: %s", got, want)
	}
	// Root replacement to a non-object is rejected. The document root is
	// always a JSON object (a rendered manifest), so this engine is
	// deliberately narrower than general RFC 6902, which permits an
	// array- or scalar-rooted document.
	if err := Apply(doc, []Op{{Op: "replace", Path: "", Value: parseVal(t, `42`)}}); err == nil {
		t.Fatal("expected error replacing root with non-object")
	}
	if err := Apply(doc, []Op{{Op: "add", Path: "", Value: parseVal(t, `[]`)}}); err == nil {
		t.Fatal("expected error replacing root with array")
	}
}
