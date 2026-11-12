// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package featurerecipe_pipeline_test

import (
	"encoding/json"
	"reflect"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// abiAllowedKinds is the set of reflect.Kinds a Wasm-ABI-shaped payload field
// may have: scalars, strings, []byte (Slice of Uint8), and nested structs /
// slices / maps of these. A rich in-process type (chan, func, interface to a
// non-serializable, unsafe pointer) must never appear on a payload that
// crosses the host/guest boundary.
func assertSerializableField(t *testing.T, path string, ty reflect.Type) {
	t.Helper()
	switch ty.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return
	case reflect.Slice, reflect.Array:
		assertSerializableField(t, path+"[]", ty.Elem())
	case reflect.Map:
		assertSerializableField(t, path+".key", ty.Key())
		assertSerializableField(t, path+".val", ty.Elem())
	case reflect.Ptr:
		assertSerializableField(t, path+"*", ty.Elem())
	case reflect.Struct:
		for i := 0; i < ty.NumField(); i++ {
			f := ty.Field(i)
			assertSerializableField(t, path+"."+f.Name, f.Type)
		}
	case reflect.Interface:
		// `any` is permitted for a guest-authored JSON value fragment (e.g.
		// JSONPatchOp.Value) — the host pipes it through opaquely as JSON. No
		// other interface is acceptable; `any` is the empty interface.
		if ty.NumMethod() != 0 {
			t.Errorf("%s: non-empty interface %s crosses the ABI boundary (rich type)", path, ty)
		}
	default:
		t.Errorf("%s: field kind %s is not a serializable ABI payload (rich type %s)", path, ty.Kind(), ty)
	}
}

// TestCodecPayloads_NoRichTypes is the guard: every struct that crosses the
// codec/applier ABI boundary carries only serializable payload fields — no
// *yaml.Node, no func seam, no io.Reader, no channel. A future field of a rich
// type fails this reflection walk.
func TestCodecPayloads_NoRichTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ty   reflect.Type
	}{
		{"Unit", reflect.TypeOf(frp.Unit{})},
		{"PatchedUnit", reflect.TypeOf(frp.PatchedUnit{})},
		{"DocumentSet", reflect.TypeOf(frp.DocumentSet{})},
		{"JSONPatchOp", reflect.TypeOf(frp.JSONPatchOp{})},
		{"FRPStepOutput", reflect.TypeOf(frp.FRPStepOutput{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertSerializableField(t, tc.name, tc.ty)
		})
	}
}

// TestUnit_MarshalRoundTrip proves a structured unit and an opaque unit each
// survive a JSON marshal round-trip byte-identically — the ABI shape is
// stable and the opaque/structured distinction (present vs absent tree) rides
// the wire.
func TestUnit_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	structured := frp.Unit{Token: "a.yaml", MediaType: "application/yaml", Tree: json.RawMessage(`{"kind":"Deployment"}`)}
	opaque := frp.Unit{Token: "blob.bin", MediaType: "application/octet-stream", Bytes: []byte("\x00\x01raw")}

	for _, u := range []frp.Unit{structured, opaque} {
		first, err := json.Marshal(u)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var round frp.Unit
		if err := json.Unmarshal(first, &round); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		second, err := json.Marshal(round)
		if err != nil {
			t.Fatalf("re-marshal: %v", err)
		}
		if string(first) != string(second) {
			t.Errorf("unit round-trip not byte-identical:\n first: %s\n second: %s", first, second)
		}
		if round.Structured() != u.Structured() {
			t.Errorf("structured flag drifted across the wire for %q", u.Token)
		}
	}

	// A structured unit omits bytes; an opaque unit omits tree.
	sb, _ := json.Marshal(structured)
	if got, want := string(sb), `{"token":"a.yaml","media_type":"application/yaml","tree":{"kind":"Deployment"}}`; got != want {
		t.Errorf("structured unit wire:\n want: %s\n  got: %s", want, got)
	}
}

// TestPatchedUnit_MarshalRoundTrip proves the PatchedUnit ABI payload (token,
// tree, ops) round-trips byte-identically.
func TestPatchedUnit_MarshalRoundTrip(t *testing.T) {
	t.Parallel()
	pu := frp.PatchedUnit{
		Token: "a.yaml",
		Tree:  json.RawMessage(`{"x":1}`),
		Ops:   []frp.JSONPatchOp{{Scope: frp.DocumentScopeOwn, Key: "a.yaml", Op: "replace", Path: "/x", Value: float64(1)}},
	}
	first, err := json.Marshal(pu)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round frp.PatchedUnit
	if err := json.Unmarshal(first, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	second, err := json.Marshal(round)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("PatchedUnit round-trip not byte-identical:\n first: %s\n second: %s", first, second)
	}
}

// TestStockApplier_PreservesKeyOrder proves the stock applier applies an
// RFC-6902 op over an order-preserving model: a value replaced deep in an
// author-ordered object leaves every key in author order on the way out.
func TestStockApplier_PreservesKeyOrder(t *testing.T) {
	t.Parallel()
	tree := json.RawMessage(`{"replicas":1,"image":"x","port":8080}`)
	out, err := frp.StockApplier{}.Apply(tree, []frp.JSONPatchOp{
		{Op: "replace", Path: "/image", Value: "y"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	const want = `{"replicas":1,"image":"y","port":8080}`
	if string(out) != want {
		t.Errorf("stock applier did not preserve key order:\n want: %s\n  got: %s", want, out)
	}
}
