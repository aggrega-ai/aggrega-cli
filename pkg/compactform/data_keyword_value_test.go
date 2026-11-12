// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compactform

import (
	"reflect"
	"testing"
)

// TestDataKeyword_ValueIsReadAsWritten asserts a keyword whose value is data —
// default, const, enum, examples — carries a mapping or a list as written in
// compact form, outside any `$jsonschema:` escape, through every emitter that
// carries a keyword: a constraint leaf, an explicit `properties:` node, and a
// sibling of a composition keyword. Read as a schema, `default: {host: db}`
// would compile into an object schema declaring a property named host.
func TestDataKeyword_ValueIsReadAsWritten(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name    string
		src     string
		field   string
		keyword string
		want    any
	}{
		{"a mapping default on a leaf", `endpoint: { type: object, default: { host: db, port: 5432 } }`,
			"endpoint", "default", map[string]any{"host": "db", "port": 5432}},
		{"a list default holding mappings", `tiers: { type: array, default: [ { name: gold } ] }`,
			"tiers", "default", []any{map[string]any{"name": "gold"}}},
		{"a mapping const", `mode: { type: object, const: { kind: fast } }`,
			"mode", "const", map[string]any{"kind": "fast"}},
		{"an enum of mappings", `tier: { type: object, enum: [ { name: gold }, { name: silver } ] }`,
			"tier", "enum", []any{map[string]any{"name": "gold"}, map[string]any{"name": "silver"}}},
		{"examples holding a mapping", `cfg: { type: object, examples: [ { a: x } ] }`,
			"cfg", "examples", []any{map[string]any{"a": "x"}}},
		{"a const on an explicit properties node", `cfg:
  properties:
    a: { type: string }
  const: { a: x }
`, "cfg", "const", map[string]any{"a": "x"}},
		{"a default beside a composition keyword", `either:
  anyOf:
    - { type: string }
    - { type: object }
  default: { a: 1 }
`, "either", "default", map[string]any{"a": 1}},
		{"a mapping whose keys are numbers keeps them as written", `ports: { type: object, default: { 80: http } }`,
			"ports", "default", map[string]any{"80": "http"}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := mustCompile(t, row.src)
			field, ok := out["properties"].(Schema)[row.field].(Schema)
			if !ok {
				t.Fatalf("%s did not compile to a schema: %v", row.field, out)
			}
			if got := field[row.keyword]; !reflect.DeepEqual(got, row.want) {
				t.Errorf("%s compiled to %#v, want the value %#v", row.keyword, got, row.want)
			}
		})
	}
}
