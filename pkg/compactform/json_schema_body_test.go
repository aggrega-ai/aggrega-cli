// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// TestEscape_BodyIsReadAsJSONSchema asserts the body of a `$jsonschema:` escape
// compiles as the JSON Schema it is: no mapping in it is read as an inferred
// object, a keyword taking data carries that data as written, and a compact-form
// keyword written there keeps its compact-form meaning.
func TestEscape_BodyIsReadAsJSONSchema(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name  string
		src   string
		check func(t *testing.T, field Schema)
	}{
		{
			name: "allOf beside patternProperties",
			src: `field:
  $jsonschema: |
    type: object
    unevaluatedProperties: false
    allOf:
      - $ref: "#/$defs/Foo"
      - not:
          required: [conflicting_field]
    patternProperties:
      "^x-": { type: string }
`,
			check: func(t *testing.T, field Schema) {
				pattern, ok := field["patternProperties"].(Schema)["^x-"].(Schema)
				if !ok || pattern["type"] != "string" {
					t.Errorf("patternProperties is not a map of schemas: %v", field["patternProperties"])
				}
				if branches, ok := field["allOf"].([]any); !ok || len(branches) != 2 {
					t.Errorf("allOf did not carry its two branches: %v", field["allOf"])
				}
				if _, inferred := field["properties"]; inferred {
					t.Errorf("the escape body was read as an inferred object: %v", field)
				}
			},
		},
		{
			name: "a node holding only $dynamicRef",
			src: `field:
  $jsonschema: |
    $dynamicAnchor: node
    type: object
    properties:
      children:
        type: array
        items: { $dynamicRef: "#node" }
`,
			check: func(t *testing.T, field Schema) {
				items := field["properties"].(Schema)["children"].(Schema)["items"].(Schema)
				want := Schema{"$dynamicRef": "#node"}
				if !reflect.DeepEqual(items, want) {
					t.Errorf("items compiled to %v, want %v", items, want)
				}
				if field["$dynamicAnchor"] != "node" {
					t.Errorf("$dynamicAnchor not carried: %v", field["$dynamicAnchor"])
				}
			},
		},
		{
			name: "const, enum and default carry data",
			src: `field:
  $jsonschema: |
    type: object
    properties:
      mode: { const: { kind: fast, level: 2 } }
      tier: { enum: [ { name: gold } ] }
    default: { mode: { kind: fast, level: 2 } }
`,
			check: func(t *testing.T, field Schema) {
				mode := field["properties"].(Schema)["mode"].(Schema)
				want := map[string]any{"kind": "fast", "level": 2}
				if !reflect.DeepEqual(mode["const"], want) {
					t.Errorf("const compiled to %v, want the value %v", mode["const"], want)
				}
				tier := field["properties"].(Schema)["tier"].(Schema)
				if !reflect.DeepEqual(tier["enum"], []any{map[string]any{"name": "gold"}}) {
					t.Errorf("enum compiled to %v", tier["enum"])
				}
				if !reflect.DeepEqual(field["default"], map[string]any{"mode": want}) {
					t.Errorf("default compiled to %v", field["default"])
				}
			},
		},
		{
			name: "the property-count bounds",
			src: `field:
  $jsonschema: |
    type: object
    minProperties: 1
    maxProperties: 3
`,
			check: func(t *testing.T, field Schema) {
				if field["minProperties"] != 1 || field["maxProperties"] != 3 {
					t.Errorf("property-count bounds not carried: %v", field)
				}
			},
		},
		{
			name: "a compact-form keyword keeps its meaning",
			src: `field:
  $jsonschema:
    type: string
    min: 2
`,
			check: func(t *testing.T, field Schema) {
				if field["minLength"] != 2 {
					t.Errorf("min on a string did not compile to minLength: %v", field)
				}
			},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := mustCompile(t, row.src)
			field, ok := out["properties"].(Schema)["field"].(Schema)
			if !ok {
				t.Fatalf("field did not compile to a schema: %v", out)
			}
			row.check(t, field)
		})
	}
}

// TestRequiredMarker_InsideExplicitProperties asserts a `required: true` on an
// entry of an explicit `properties:` block adds the entry's name to the
// enclosing object's required list, as it does on a child of an inferred object,
// in every place such a block is written.
func TestRequiredMarker_InsideExplicitProperties(t *testing.T) {
	t.Parallel()

	library := loadLibrary(t, `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Lib
  version: 1.0.0
spec:
  schemas:
    Entry:
      type: object
      properties:
        id: { type: string }
`)

	rows := []struct {
		name string
		src  string
		libs bool
		want []string
	}{
		{"type: object with properties", `auth:
  type: object
  properties:
    username: { type: string }
    password: { type: string, required: true }
`, false, []string{"password"}},
		{"properties alone", `auth:
  properties:
    username: { type: string }
    password: { type: string, required: true }
`, false, []string{"password"}},
		{"after an authored list, which keeps its order", `auth:
  type: object
  properties:
    password: { type: string, required: true }
    username: { type: string }
  required: [username]
`, false, []string{"username", "password"}},
		{"a name the authored list already holds, once", `auth:
  type: object
  required: [password]
  properties:
    password: { type: string, required: true }
`, false, []string{"password"}},
		{"required: false marks nothing", `auth:
  type: object
  properties:
    password: { type: string, required: false }
`, false, nil},
		{"beside a cross-document reference", `auth:
  $ref: "Lib#/schemas/Entry"
  properties:
    extra: { type: string, required: true }
`, true, []string{"extra"}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var opts []CompileOption
			if row.libs {
				opts = append(opts, WithSchemaLibraries(map[string]types.SchemaLibrary{"Lib": library}))
			}
			out, err := Compile(parseConsumer(t, row.src), opts...)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			auth := out["properties"].(Schema)["auth"].(Schema)
			got := requiredNames(auth["required"])
			if !reflect.DeepEqual(got, row.want) {
				t.Errorf("required = %v, want %v", got, row.want)
			}
		})
	}
}

// requiredNames reads a compiled required list in either of the two shapes the
// compiler writes it in.
func requiredNames(raw any) []string {
	var out []string
	switch list := raw.(type) {
	case []string:
		out = append(out, list...)
	case []any:
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// TestInDocumentReference_SiblingsAreCompactForm asserts the keys beside an
// in-document `$ref:` are read as compact form: `required: true` marks the node
// required in its parent, `properties:` declares properties, `when:` gates the
// node, and a key that is not a keyword, or a `min` with no type, is refused.
func TestInDocumentReference_SiblingsAreCompactForm(t *testing.T) {
	t.Parallel()

	out := mustCompile(t, `architecture: { type: string, enum: [standalone, replication] }
secondary:
  $ref: "#/$defs/NodePool"
  when: '{{ eq $.architecture "replication" }}'
  required: true
  properties:
    replicaCount: { type: integer, min: 2 }
`)
	secondary := out["properties"].(Schema)["secondary"].(Schema)
	if secondary["$ref"] != "#/$defs/NodePool" {
		t.Errorf("$ref not carried: %v", secondary["$ref"])
	}
	if _, ok := secondary["x-aggrega-when"]; !ok {
		t.Errorf("when: did not compile to its extension keyword: %v", secondary)
	}
	replicaCount, ok := secondary["properties"].(Schema)["replicaCount"].(Schema)
	if !ok || replicaCount["minimum"] != 2 || replicaCount["type"] != "integer" {
		t.Errorf("properties: beside the reference is not a map of schemas: %v", secondary["properties"])
	}
	if _, ok := secondary["required"]; ok {
		t.Errorf("the per-leaf marker was left on the node: %v", secondary["required"])
	}
	if got := requiredNames(out["required"]); !reflect.DeepEqual(got, []string{"secondary"}) {
		t.Errorf("parent required = %v, want [secondary]", got)
	}

	refusals := []struct {
		name string
		src  string
		want error
	}{
		{"a key that is not a keyword", `node:
  $ref: "#/$defs/A"
  host: { type: string }
`, ErrUnknownKeyword()},
		{"min with no type", `node:
  $ref: "#/$defs/A"
  min: 2
`, ErrConstraintTypeMismatch()},
	}
	for _, refusal := range refusals {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()
			var input yaml.Node
			if err := yaml.Unmarshal([]byte(refusal.src), &input); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			_, err := Compile(&input)
			if !errors.Is(err, refusal.want) {
				t.Errorf("Compile error = %v, want %v", err, refusal.want)
			}
		})
	}
}
