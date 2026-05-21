// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestApplyParameterDefaults pins the discriminator matrix across:
//
//   - Flat-parameter defaulting for every supported type (integer,
//     string, boolean, object literal, array).
//   - Release-override-wins precedence at every depth.
//   - Required-marker interaction (with-default satisfies, without-default
//     stays an error surface).
//   - All parameter authoring uses the typed-contract form (the
//     bare-scalar shorthand was retired; `DecodeParameterContract`
//     rejects scalars at parse time).
//   - The three-case semantic for nested-object parameters: omitted
//     parent synthesizes children's defaults; explicit empty parent
//     preserves the empty and suppresses children's defaults;
//     partial parent merges children's defaults with Release-supplied
//     fields. Per-sibling independence at every depth.
//   - Type-mismatch in the Release surfaces at the contract validator
//     downstream — the default-fill does not silently correct it.
//
// The three-case semantic is documented in detail at the godoc of
// ApplyParameterDefaults and in the user-visible documentation at
// aggrega-docs/language-reference/templates/parameters-context.md.
func TestApplyParameterDefaults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contract types.OrderedMap[any]
		params   map[string]any
		want     map[string]any
	}{
		// ---- Flat-parameter cases ----
		{
			name:     "integer_fill_from_default",
			contract: contractFrom("pool_size", typedMap("type", "integer", "default", 10)),
			params:   map[string]any{},
			want:     map[string]any{"pool_size": 10},
		},
		{
			name:     "string_fill_from_default",
			contract: contractFrom("region", typedMap("type", "string", "default", "us-east-1")),
			params:   map[string]any{},
			want:     map[string]any{"region": "us-east-1"},
		},
		{
			name:     "boolean_false_default_fills_as_false",
			contract: contractFrom("debug", typedMap("type", "boolean", "default", false)),
			params:   map[string]any{},
			want:     map[string]any{"debug": false},
		},
		{
			name:     "object_literal_default_fills_as_map",
			contract: contractFrom("db", typedMap("type", "object", "default", map[string]any{"host": "localhost", "port": 5432})),
			params:   map[string]any{},
			want:     map[string]any{"db": map[string]any{"host": "localhost", "port": 5432}},
		},
		{
			name:     "array_default_fills_as_slice",
			contract: contractFrom("hosts", typedMap("type", "array", "default", []any{"a", "b"})),
			params:   map[string]any{},
			want:     map[string]any{"hosts": []any{"a", "b"}},
		},
		{
			name:     "release_value_overrides_default",
			contract: contractFrom("port", typedMap("type", "integer", "default", 8080)),
			params:   map[string]any{"port": 3000},
			want:     map[string]any{"port": 3000},
		},
		{
			name:     "no_default_no_value_stays_absent",
			contract: contractFrom("optional_x", typedMap("type", "string")),
			params:   map[string]any{},
			want:     map[string]any{},
		},
		{
			name: "multiple_slots_dispatch_independently",
			contract: contractFromPairs(
				"a", typedMap("type", "integer", "default", 1),
				"b", typedMap("type", "string", "default", "B"),
				"c", typedMap("type", "boolean"),
			),
			params: map[string]any{"b": "override"},
			want:   map[string]any{"a": 1, "b": "override"},
		},

		// ---- Nested-object three-case sub-cases ----
		{
			name: "case1_omitted_parent_with_children_defaults_synthesizes_and_fills",
			contract: contractFrom("database", typedMap("type", "object", "properties", map[string]any{
				"host": map[string]any{"type": "string", "default": "localhost"},
				"port": map[string]any{"type": "integer", "default": 5432},
			})),
			params: map[string]any{},
			want:   map[string]any{"database": map[string]any{"host": "localhost", "port": 5432}},
		},
		{
			name: "case1_omitted_parent_without_children_defaults_stays_absent",
			contract: contractFrom("database", typedMap("type", "object", "properties", map[string]any{
				"host": map[string]any{"type": "string"},
			})),
			params: map[string]any{},
			want:   map[string]any{},
		},
		{
			name: "case2_explicit_empty_parent_suppresses_children_defaults",
			contract: contractFrom("database", typedMap("type", "object", "properties", map[string]any{
				"host": map[string]any{"type": "string", "default": "localhost"},
				"port": map[string]any{"type": "integer", "default": 5432},
			})),
			params: map[string]any{"database": map[string]any{}},
			want:   map[string]any{"database": map[string]any{}},
		},
		{
			name: "case2_explicit_empty_grandparent_suppresses_grandchildren_defaults",
			contract: contractFrom("database", typedMap("type", "object", "properties", map[string]any{
				"connection": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"timeout": map[string]any{"type": "integer", "default": 30},
					},
				},
			})),
			params: map[string]any{"database": map[string]any{"connection": map[string]any{}}},
			want:   map[string]any{"database": map[string]any{"connection": map[string]any{}}},
		},
		{
			name: "case3_mixed_deep_authoring_fills_per_sibling",
			contract: contractFrom("database", typedMap("type", "object", "properties", map[string]any{
				"host": map[string]any{"type": "string", "default": "localhost"},
				"connection": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"timeout": map[string]any{"type": "integer", "default": 30},
						"retries": map[string]any{"type": "integer", "default": 3},
					},
				},
			})),
			params: map[string]any{"database": map[string]any{"connection": map[string]any{"timeout": 60}}},
			want: map[string]any{"database": map[string]any{
				"host": "localhost",
				"connection": map[string]any{
					"timeout": 60,
					"retries": 3,
				},
			}},
		},
		{
			name: "siblings_dispatch_independently_per_case",
			contract: contractFrom("database", typedMap("type", "object", "properties", map[string]any{
				"host": map[string]any{"type": "string", "default": "localhost"},
				"connection": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"timeout": map[string]any{"type": "integer", "default": 30},
					},
				},
			})),
			params: map[string]any{"database": map[string]any{"connection": map[string]any{}}},
			want: map[string]any{"database": map[string]any{
				"host":       "localhost",      // sibling fills (Case 3 at depth 0)
				"connection": map[string]any{}, // grandchild stays empty (Case 2 at depth 1)
			}},
		},
		{
			name: "type_mismatch_in_release_is_not_default_fill_concern",
			contract: contractFrom("database", typedMap("type", "object", "properties", map[string]any{
				"host": map[string]any{"type": "string", "default": "localhost"},
			})),
			params: map[string]any{"database": "some-string"},
			want:   map[string]any{"database": "some-string"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyParameterDefaults(tc.contract, tc.params)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ApplyParameterDefaults(...) = %#v;\n  want %#v", got, tc.want)
			}
		})
	}
}

// TestApplyParameterDefaults_NilParams pins the nil-map handling.
func TestApplyParameterDefaults_NilParams(t *testing.T) {
	t.Parallel()
	contract := contractFrom("region", typedMap("type", "string", "default", "us-east-1"))
	got := ApplyParameterDefaults(contract, nil)
	if got == nil {
		t.Fatal("ApplyParameterDefaults(nil) returned nil; want allocated map")
	}
	if v, ok := got["region"]; !ok || v != "us-east-1" {
		t.Errorf("ApplyParameterDefaults(nil) missing default fill; got %#v", got)
	}
}

// TestApplyParameterDefaults_EmptyContract pins the no-parameters-block
// case — a contract with no entries leaves params untouched.
func TestApplyParameterDefaults_EmptyContract(t *testing.T) {
	t.Parallel()
	params := map[string]any{"x": 1}
	got := ApplyParameterDefaults(types.OrderedMap[any]{}, params)
	if !reflect.DeepEqual(got, map[string]any{"x": 1}) {
		t.Errorf("empty contract should leave params unchanged; got %#v", got)
	}
}

// TestApplyParameterDefaults_PreservesRequiredMarker confirms that the
// fill-then-validate ordering preserves the required-marker discipline:
//   - required+default → fill happens → validator sees the filled
//     value → no ErrParameterMissing.
//   - required+no-default → fill is a no-op for this slot → validator
//     fires ErrParameterMissing.
//
// The test exercises the ApplyParameterDefaults + ValidateParametersAgainstContract
// pair at the unit level (not just the fill in isolation).
func TestApplyParameterDefaults_PreservesRequiredMarker(t *testing.T) {
	t.Parallel()

	t.Run("required_with_default_satisfies_after_fill", func(t *testing.T) {
		contract := contractFrom("region", typedMap("type", "string", "default", "us-east-1", "required", true))
		params := ApplyParameterDefaults(contract, map[string]any{})
		errs := ValidateParametersAgainstContract(contract, params, "test-aggregate.yaml")
		if len(errs) != 0 {
			t.Errorf("required+default should satisfy required after fill; got errors: %v", errs)
		}
	})

	t.Run("required_without_default_still_errors", func(t *testing.T) {
		contract := contractFrom("secret_token", typedMap("type", "string", "required", true))
		params := ApplyParameterDefaults(contract, map[string]any{})
		errs := ValidateParametersAgainstContract(contract, params, "test-aggregate.yaml")
		if len(errs) == 0 {
			t.Errorf("required without default and without Release value should error; got no errors")
		}
	})
}

// ---- Test helpers ----

// contractFrom builds a single-entry OrderedMap[any] for fixture
// construction. The Aggregate's `spec.parameters` is an OrderedMap[any]
// where each value is a typed-form map authoring the compact-form vocabulary.
func contractFrom(key string, value any) types.OrderedMap[any] {
	c := types.OrderedMap[any]{}
	c.Set(key, value)
	return c
}

// contractFromPairs builds a multi-entry OrderedMap[any] from key/value
// pairs in declaration order.
func contractFromPairs(pairs ...any) types.OrderedMap[any] {
	c := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		key, _ := pairs[i].(string)
		c.Set(key, pairs[i+1])
	}
	return c
}

// typedMap builds a map[string]any from key/value pairs, matching the
// shape a typed-form parameter contract entry takes when decoded from
// YAML into the OrderedMap[any] tree.
func typedMap(pairs ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		key, _ := pairs[i].(string)
		m[key] = pairs[i+1]
	}
	return m
}
