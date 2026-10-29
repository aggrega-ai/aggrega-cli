// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nestedValues is a value set whose nested objects hold their keys in an order
// that is not sorted and whose numbers sit at every depth, the shape on which a
// decode that produced plain maps would lose both the nested order and the
// numbers' normalization.
func nestedValues() types.OrderedMap[any] {
	deeper := types.NewOrderedMap[any]()
	deeper.Set("y", 2)
	deeper.Set("b", 3)
	inner := types.NewOrderedMap[any]()
	inner.Set("zeta", 1)
	inner.Set("alpha", deeper)
	var values types.OrderedMap[any]
	values.Set("resources", inner)
	values.Set("replicas", 3)
	values.Set("list", []any{deeper})
	return values
}

// TestOrderedValuesDecodeIdenticallyOnBothLegs pins that a value an
// order-preserving container holds decodes to the same Go value from the binary
// encoding as from the textual one, for every such container the provider render
// family carries: a nested object becomes an ordered map in the order its bytes
// carry, at every depth, and a number keeps its exact text. A provider guest
// reads these values, so a decode that differed by encoding would hand it a
// different value depending on the wire, and what it renders from them with it.
func TestOrderedValuesDecodeIdenticallyOnBothLegs(t *testing.T) {
	t.Parallel()

	values := nestedValues()
	cases := []struct {
		name string
		// roundTrip encodes a payload carrying values on both legs and returns the
		// container each decode rebuilt.
		roundTrip func(t *testing.T) (fromText, fromBinary types.OrderedMap[any])
	}{
		{"a node's values", func(t *testing.T) (types.OrderedMap[any], types.OrderedMap[any]) {
			in := IRNodeProjection{ID: "n", Type: "t", ResolvedRecipe: "r", Values: values}
			var a, b IRNodeProjection
			bothLegs(t, in, &a, &b)
			return a.Values, b.Values
		}},
		{"a contributor's values", func(t *testing.T) (types.OrderedMap[any], types.OrderedMap[any]) {
			in := RenderedNode{ID: "n", Type: "t", Values: values}
			var a, b RenderedNode
			bothLegs(t, in, &a, &b)
			return a.Values, b.Values
		}},
		{"a sub-chart's values", func(t *testing.T) (types.OrderedMap[any], types.OrderedMap[any]) {
			in := SubChart{InstanceID: "n", Values: values}
			var a, b SubChart
			bothLegs(t, in, &a, &b)
			return a.Values, b.Values
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fromText, fromBinary := tc.roundTrip(t)
			if !reflect.DeepEqual(fromText, fromBinary) {
				t.Fatalf("the binary decode differs from the text decode:\n text:   %#v\n binary: %#v", fromText, fromBinary)
			}
			resources, _ := fromBinary.Get("resources")
			inner, ok := resources.(*types.OrderedMap[any])
			if !ok {
				t.Fatalf("a nested object decoded as %T, not as an ordered map", resources)
			}
			if got := inner.Keys(); !reflect.DeepEqual(got, []string{"zeta", "alpha"}) {
				t.Errorf("a nested object's keys decoded in the order %v, not the order its bytes carry", got)
			}
			deeper, _ := inner.Get("alpha")
			if got := deeper.(*types.OrderedMap[any]).Keys(); !reflect.DeepEqual(got, []string{"y", "b"}) {
				t.Errorf("an object two levels down decoded its keys in the order %v", got)
			}
			if got := PlainValue(resources); !reflect.DeepEqual(got, map[string]any{
				"zeta": int64(1), "alpha": map[string]any{"y": int64(2), "b": int64(3)},
			}) {
				t.Errorf("the flattened nested value is %#v; its numbers did not normalize", got)
			}
		})
	}
}

// legs is a payload type carrying both of its generated codecs.
type legs interface {
	MarshalJSON() ([]byte, error)
	EncodeCBOR() ([]byte, error)
}

// bothLegs encodes in on each leg and decodes each encoding into its own
// destination: text into fromText, binary into fromBinary.
func bothLegs[P interface {
	UnmarshalJSON([]byte) error
	DecodeCBOR([]byte) error
}](t *testing.T, in legs, fromText, fromBinary P) {
	t.Helper()
	text, err := in.MarshalJSON()
	if err != nil {
		t.Fatalf("encode the text leg: %v", err)
	}
	if err := fromText.UnmarshalJSON(text); err != nil {
		t.Fatalf("decode the text leg: %v", err)
	}
	binary, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the binary leg: %v", err)
	}
	if err := fromBinary.DecodeCBOR(binary); err != nil {
		t.Fatalf("decode the binary leg: %v", err)
	}
}
