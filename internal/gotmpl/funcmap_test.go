// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"strings"
	"testing"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
)

// TestCapitalizedWordsIsReachableFromARenderedTemplate asserts the helper is
// invoked by name from template text and that its result reaches the rendered
// output — not merely that the name is registered. A helper bound but
// unreachable renders as a parse error at the first authored use, which is a
// failure an author meets rather than one this package's tests do.
//
// It is asserted through both bindings, because they are different surfaces: a
// value template renders through [FuncMap], while a surface evaluating an
// authored coordinate takes [Helpers] alone. A helper registered in one and not
// the other means the same authored expression answers on one surface and fails
// to parse on the other.
func TestCapitalizedWordsIsReachableFromARenderedTemplate(t *testing.T) {
	bindings := map[string]template.FuncMap{
		"FuncMap (Sprig plus Aggrega's helpers)": FuncMap(),
		"Helpers (Aggrega's helpers alone)":      Helpers(),
	}

	for name, funcs := range bindings {
		t.Run(name, func(t *testing.T) {
			tpl, err := template.New("t").Funcs(funcs).Parse(`{{ capitalizedWords .instance_id }}`)
			if err != nil {
				t.Fatalf("the helper is not reachable by name from template text: %v", err)
			}
			var b strings.Builder
			if err := tpl.Execute(&b, map[string]any{"instance_id": "payments-db.primary"}); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got, want := b.String(), "PaymentsDbPrimary"; got != want {
				t.Fatalf("rendered %q, want %q — the helper's result did not reach the output", got, want)
			}
		})
	}
}

// TestHelperSetIsTheSameThroughBothBindings asserts every helper [Helpers]
// declares is also present in [FuncMap], so a helper cannot be added to one
// binding and silently missed by the other.
//
// The direction is deliberate: FuncMap is Sprig's set plus Aggrega's, so it is
// legitimately larger, while a name in Helpers that FuncMap lacks would mean the
// layering dropped it.
func TestHelperSetIsTheSameThroughBothBindings(t *testing.T) {
	full := FuncMap()
	helpers := Helpers()
	if len(helpers) == 0 {
		t.Fatal("Helpers() is empty, so this assertion would hold vacuously")
	}
	for name := range helpers {
		if _, ok := full[name]; !ok {
			t.Errorf("FuncMap() does not carry the helper %q that Helpers() declares", name)
		}
	}
}

// TestAggregaHelpersDoNotShadowSprig asserts no Aggrega helper name collides
// with a Sprig function name.
//
// 🔴 This is not a style check. [FuncMap] layers Aggrega's helpers OVER Sprig's,
// so a colliding name silently replaces a Sprig function on every template
// surface in the product — every catalog entity using that Sprig function would
// change behavior with nothing failing and nothing said. The collision is
// invisible from the helper's own definition, which is why it is asserted here.
func TestAggregaHelpersDoNotShadowSprig(t *testing.T) {
	full := FuncMap()
	helpers := Helpers()

	// Sprig's set is FuncMap minus Aggrega's helpers, recovered by counting: a
	// name in both maps that is shadowed cannot be seen by comparing values, so
	// the assertion is made against a fresh Sprig set instead.
	sprigOnly := sprigFuncNames()
	if len(sprigOnly) == 0 {
		t.Fatal("recovered no Sprig name, so this assertion would hold vacuously")
	}
	if len(full) != len(sprigOnly)+len(helpers) {
		t.Errorf("FuncMap() carries %d names but Sprig has %d and Aggrega declares %d; "+
			"the shortfall is a helper name shadowing a Sprig function",
			len(full), len(sprigOnly), len(helpers))
	}
	for name := range helpers {
		if _, clash := sprigOnly[name]; clash {
			t.Errorf("the Aggrega helper %q shadows the Sprig function of the same name on every template surface", name)
		}
	}
}

// TestOptional_PresenceAwareFallback walks the absent-vs-authored-zero
// discriminator that is the function's reason to exist. Each case pins
// one branch of optional's behavior; the suite as a whole covers the
// presence/absence x value-type matrix sufficiently to flip the
// implementation under common operator mutations (`ok` → `!ok`, equality
// flips, missing-vs-present branch removal).
func TestOptional_PresenceAwareFallback(t *testing.T) {
	cases := []struct {
		name     string
		m        map[string]any
		key      string
		fallback any
		want     any
	}{
		{
			name:     "present non-zero integer returns authored value",
			m:        map[string]any{"pool_size": 10},
			key:      "pool_size",
			fallback: 99,
			want:     10,
		},
		{
			name:     "present authored zero returns zero (discriminator vs default)",
			m:        map[string]any{"replicas": 0},
			key:      "replicas",
			fallback: 1,
			want:     0,
		},
		{
			name:     "present authored empty string returns empty string",
			m:        map[string]any{"prefix": ""},
			key:      "prefix",
			fallback: "default-prefix",
			want:     "",
		},
		{
			name:     "present authored false returns false",
			m:        map[string]any{"enabled": false},
			key:      "enabled",
			fallback: true,
			want:     false,
		},
		{
			name:     "present nil value returns nil (a key with nil value is still present)",
			m:        map[string]any{"x": nil},
			key:      "x",
			fallback: "fallback",
			want:     nil,
		},
		{
			name:     "absent key returns fallback",
			m:        map[string]any{"other": 5},
			key:      "missing",
			fallback: "fallback",
			want:     "fallback",
		},
		{
			name:     "nil map returns fallback without panic",
			m:        nil,
			key:      "anything",
			fallback: "fallback",
			want:     "fallback",
		},
		{
			name:     "empty map returns fallback",
			m:        map[string]any{},
			key:      "anything",
			fallback: "fallback",
			want:     "fallback",
		},
		{
			name:     "absent key with structured fallback returns fallback as-is",
			m:        map[string]any{},
			key:      "missing",
			fallback: map[string]any{"a": 1},
			want:     map[string]any{"a": 1},
		},
		{
			name:     "present structured value returns structured value",
			m:        map[string]any{"cfg": map[string]any{"a": 1}},
			key:      "cfg",
			fallback: map[string]any{"b": 2},
			want:     map[string]any{"a": 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := optional(tc.m, tc.key, tc.fallback)
			// Use reflect.DeepEqual via a comparable shim so structured-value
			// cases compare properly. For scalar cases, plain != is enough,
			// but the structured cases need DeepEqual semantics.
			if !deepEq(got, tc.want) {
				t.Errorf("optional(%v, %q, %v) = %v; want %v", tc.m, tc.key, tc.fallback, got, tc.want)
			}
		})
	}
}

// sprigFuncNames returns the Sprig function names, taken from Sprig itself
// rather than from a list written here. A hand-written list would be a second
// copy of the library's surface and would go stale on the next dependency bump,
// which is precisely when a newly-introduced collision would need catching.
func sprigFuncNames() map[string]bool {
	names := map[string]bool{}
	for name := range sprig.FuncMap() {
		names[name] = true
	}
	return names
}

// deepEq compares two values for structural equality in a minimal way
// sufficient for this test suite's case set: scalars compare by ==;
// map[string]any compares key-by-key with recursive deepEq on values.
// Anything else falls back to ==.
func deepEq(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		if len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			bv, ok := bm[k]
			if !ok {
				return false
			}
			if !deepEq(av, bv) {
				return false
			}
		}
		return true
	}
	return a == b
}
