// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonpatch

import (
	"encoding/json"
	"math/big"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// asOrderedMap returns the document-model object behind v, accepting
// both the pointer form (*types.OrderedMap[any], produced by JSON
// decoding) and the value form (types.OrderedMap[any], which YAML
// decoding can produce). The value form returns a pointer to a copy;
// callers that mutate must use the pointer form or upgrade explicitly
// (navigation does), so the copy is safe only for read-only use
// (equality, deep copy).
func asOrderedMap(v any) (*types.OrderedMap[any], bool) {
	switch m := v.(type) {
	case *types.OrderedMap[any]:
		return m, true
	case types.OrderedMap[any]:
		return &m, true
	}
	return nil, false
}

// toRat converts a JSON numeric value to an exact rational, accepting
// json.Number (the document-model form) and the float/int shapes a
// caller's externally-decoded patch value may carry. A json.Number
// compares by its exact decimal value, so "1", "1.0", and "1e0" are
// equal numbers. Returns false for any non-numeric value (notably bool
// and nil, which are never numbers).
func toRat(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case json.Number:
		r := new(big.Rat)
		if _, ok := r.SetString(n.String()); ok {
			return r, true
		}
		return nil, false
	case float64:
		if r := new(big.Rat).SetFloat64(n); r != nil {
			return r, true
		}
		return nil, false
	case float32:
		if r := new(big.Rat).SetFloat64(float64(n)); r != nil {
			return r, true
		}
		return nil, false
	case int:
		return new(big.Rat).SetInt64(int64(n)), true
	case int64:
		return new(big.Rat).SetInt64(n), true
	}
	return nil, false
}

// deepEqual reports JSON value equality between two document-model
// values. Numbers compare by exact value; objects compare as unordered
// key sets (JSON object equality is order-independent); arrays compare
// element-wise in order; scalars compare by Go equality. This is the
// equality the RFC 6902 test operation evaluates.
func deepEqual(a, b any) bool {
	if ra, ok := toRat(a); ok {
		rb, ok := toRat(b)
		return ok && ra.Cmp(rb) == 0
	}
	if _, ok := toRat(b); ok {
		return false
	}

	if ma, ok := asOrderedMap(a); ok {
		mb, ok := asOrderedMap(b)
		if !ok || ma.Len() != mb.Len() {
			return false
		}
		for _, k := range ma.Keys() {
			va, _ := ma.Get(k)
			vb, present := mb.Get(k)
			if !present || !deepEqual(va, vb) {
				return false
			}
		}
		return true
	}
	if _, ok := asOrderedMap(b); ok {
		return false
	}

	if aa, ok := a.([]any); ok {
		ab, ok := b.([]any)
		if !ok || len(aa) != len(ab) {
			return false
		}
		for i := range aa {
			if !deepEqual(aa[i], ab[i]) {
				return false
			}
		}
		return true
	}
	if _, ok := b.([]any); ok {
		return false
	}

	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	}
	return false
}

// deepCopy returns a structural copy of a document-model value so that
// mutating the copy cannot alias the source. Objects and arrays are
// rebuilt (object key order preserved); scalars are immutable and
// returned as-is. Used by copy (and move) so a copied subtree is
// independent of its origin.
func deepCopy(v any) any {
	if m, ok := asOrderedMap(v); ok {
		cp := types.NewOrderedMap[any]()
		for _, k := range m.Keys() {
			val, _ := m.Get(k)
			cp.Set(k, deepCopy(val))
		}
		return cp
	}
	if arr, ok := v.([]any); ok {
		cp := make([]any, len(arr))
		for i := range arr {
			cp[i] = deepCopy(arr[i])
		}
		return cp
	}
	return v
}
