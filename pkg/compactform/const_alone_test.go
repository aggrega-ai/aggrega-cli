// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"reflect"
	"testing"
)

// TestConstWrittenAlone_IsAConstraintLeaf compiles a property whose node carries
// JSON Schema's `const` and nothing else, and asserts the node compiles to that
// constraint holding its value as written — never to an inferred object declaring
// a property named `const`. The rows beside it pin the two readings the change
// leaves as they were: `const` beside `type:`, and a property named `const` under
// an explicit `properties:` block.
func TestConstWrittenAlone_IsAConstraintLeaf(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name string
		src  string
		want Schema
	}{
		{"a string", `mode: { const: fast }`, Schema{"const": "fast"}},
		{"a number", `replicas: { const: 3 }`, Schema{"const": 3}},
		{"a boolean", `enabled: { const: true }`, Schema{"const": true}},
		{"a mapping", `mode: { const: { kind: fast, level: 2 } }`,
			Schema{"const": map[string]any{"kind": "fast", "level": 2}}},
		{"a list", `pair: { const: [a, b] }`, Schema{"const": []any{"a", "b"}}},
		{"beside type", `mode: { type: string, const: fast }`, Schema{"type": "string", "const": "fast"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := mustCompile(t, row.src)
			props, ok := out["properties"].(Schema)
			if !ok || len(props) != 1 {
				t.Fatalf("expected one compiled property, got %#v", out)
			}
			for name, got := range props {
				if !reflect.DeepEqual(got, row.want) {
					t.Errorf("%s compiled to %#v, want %#v", name, got, row.want)
				}
			}
		})
	}

	t.Run("a property named const under properties stays a property", func(t *testing.T) {
		t.Parallel()
		out := mustCompile(t, `settings:
  properties:
    const: { type: string }
`)
		settings := out["properties"].(Schema)["settings"].(Schema)
		props, ok := settings["properties"].(Schema)
		if !ok {
			t.Fatalf("settings did not compile to an object with properties: %#v", settings)
		}
		if got, want := props["const"], (Schema{"type": "string"}); !reflect.DeepEqual(got, want) {
			t.Errorf("the property named const compiled to %#v, want %#v", got, want)
		}
		if _, isKeyword := settings["const"]; isKeyword {
			t.Errorf("the property named const was read as the keyword: %#v", settings)
		}
	})
}
