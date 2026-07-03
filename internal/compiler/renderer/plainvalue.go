// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// OrderedMapToPlain flattens an ordered value map into the plain map[string]any
// a render or assemble core consumes. Nested ordered maps decode into
// *OrderedMap[any]; they are flattened recursively so the core sees plain Go
// maps rather than the wire container. The values feed the core by key, so the
// author order the wire payload preserves is not load-bearing here.
func OrderedMapToPlain(m *types.OrderedMap[any]) map[string]any {
	out := make(map[string]any, m.Len())
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = PlainValue(v)
	}
	return out
}

// PlainValue recursively converts a decoded wire value into plain Go containers:
// nested ordered maps become map[string]any and slices are walked element-wise.
// A json.Number is renormalized to the Go numeric type a YAML scalar parse would
// yield; other scalars pass through unchanged.
func PlainValue(v any) any {
	switch t := v.(type) {
	case *types.OrderedMap[any]:
		return OrderedMapToPlain(t)
	case types.OrderedMap[any]:
		return OrderedMapToPlain(&t)
	case []any:
		for i := range t {
			t[i] = PlainValue(t[i])
		}
		return t
	case json.Number:
		// The wire codec decodes JSON numerics to json.Number (high-precision
		// integer preservation via UseNumber). go-yaml serializes a json.Number —
		// a string-kind type — as a quoted scalar, which would diverge from the
		// in-tree engine, whose values carry the Go numeric types a YAML scalar
		// parse yields. Renormalize to those same types so a value that crosses
		// the wire serializes byte-identically to one that did not: an integral
		// value to int64, a fractional value to float64, and anything neither
		// leaves the number untouched.
		if i, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(string(t), 64); err == nil {
			return f
		}
		return t
	default:
		return v
	}
}
