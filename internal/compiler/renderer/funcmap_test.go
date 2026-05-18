// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"
)

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

// TestOptional_InTemplateContext verifies that optional is registered in
// aggregaFuncMap and that EvalTemplate (the dominant value-template
// surface) renders correctly when authoring templates use optional.
func TestOptional_InTemplateContext(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		data any
		want string
	}{
		{
			name: "absent infra.replicas falls back to 1",
			tmpl: `{{ optional .infra "replicas" 1 }}`,
			data: map[string]any{"infra": map[string]any{}},
			want: "1",
		},
		{
			name: "authored infra.replicas:0 returns 0 (scale-to-zero)",
			tmpl: `{{ optional .infra "replicas" 1 }}`,
			data: map[string]any{"infra": map[string]any{"replicas": 0}},
			want: "0",
		},
		{
			name: "authored infra.replicas:3 returns 3",
			tmpl: `{{ optional .infra "replicas" 1 }}`,
			data: map[string]any{"infra": map[string]any{"replicas": 3}},
			want: "3",
		},
		{
			name: "boolean fallback path on absent feature flag",
			tmpl: `{{ optional .values "enabled" true }}`,
			data: map[string]any{"values": map[string]any{}},
			want: "true",
		},
		{
			name: "authored false on feature flag returns false (not fallback)",
			tmpl: `{{ optional .values "enabled" true }}`,
			data: map[string]any{"values": map[string]any{"enabled": false}},
			want: "false",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalTemplate("optional-test", tc.tmpl, tc.data)
			if err != nil {
				t.Fatalf("EvalTemplate returned error: %v", err)
			}
			if got != tc.want {
				t.Errorf("EvalTemplate(%q) = %q; want %q", tc.tmpl, got, tc.want)
			}
		})
	}
}

// TestOptional_PathTemplatesRejectAtParse confirms that EvalPathTemplate
// — which does not use aggregaFuncMap and therefore does not register
// optional — fails at parse time when a path template references
// optional. The parse-time rejection is the load-bearing enforcement
// that keeps optional out of path-template surfaces.
func TestOptional_PathTemplatesRejectAtParse(t *testing.T) {
	_, err := EvalPathTemplate(
		"path-test",
		"directory",
		"some-boundary",
		`{{ optional .parameters "x" "y" }}`,
		map[string]any{"parameters": map[string]any{}},
	)
	if err == nil {
		t.Fatalf("expected parse-time error for optional in path template; got nil")
	}
	// The wrap renders a path-template parse-failure message; the
	// underlying text/template error is what flagged the unknown function.
	// Confirm the wrapped surface signals a parse failure.
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected error message to indicate parse failure; got: %v", err)
	}
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
