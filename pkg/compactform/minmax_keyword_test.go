// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import "testing"

// TestMinMaxKeyword pins the one statement of what a compact-form `min` and
// `max` mean per declared type, which both the schema compiler and a reader
// applying the pair outside a compiled schema ask. A type the pair does not apply
// to — and an unresolved one — reports false, so a reader never applies a bound
// the compiler would have refused.
func TestMinMaxKeyword(t *testing.T) {
	t.Parallel()
	for _, r := range []struct {
		typ          string
		min, max     string
		appliesToTyp bool
	}{
		{typ: "integer", min: "minimum", max: "maximum", appliesToTyp: true},
		{typ: "number", min: "minimum", max: "maximum", appliesToTyp: true},
		{typ: "string", min: "minLength", max: "maxLength", appliesToTyp: true},
		{typ: "array", min: "minItems", max: "maxItems", appliesToTyp: true},
		{typ: "boolean"},
		{typ: "object"},
		{typ: ""},
	} {
		gotMin, okMin := MinMaxKeyword(r.typ, true)
		gotMax, okMax := MinMaxKeyword(r.typ, false)
		if okMin != r.appliesToTyp || okMax != r.appliesToTyp {
			t.Errorf("type %q: applies = (%v, %v); want %v", r.typ, okMin, okMax, r.appliesToTyp)
		}
		if gotMin != r.min || gotMax != r.max {
			t.Errorf("type %q: min/max = %q/%q; want %q/%q", r.typ, gotMin, gotMax, r.min, r.max)
		}
	}
}
