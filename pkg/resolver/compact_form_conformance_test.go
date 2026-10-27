// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// conformancePrefix is where a finding on the values of the probe component is
// reported, ahead of the path inside the pillar.
const conformancePrefix = `.spec.components["app"].values`

// object builds an authored value from alternating keys and values, keeping
// the order they are written in.
func object(kv ...any) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for i := 0; i+1 < len(kv); i += 2 {
		out.Set(kv[i].(string), kv[i+1])
	}
	return out
}

// conformanceCase is one authored values pillar and the field its single
// finding is expected at, relative to the pillar; "" means it is accepted.
type conformanceCase struct {
	name      string
	values    types.OrderedMap[any]
	wantField string
	wantText  string
}

// checkConformance measures each case against the compiled schema the way a
// build measures a component's values.
func checkConformance(t *testing.T, schema string, cases []conformanceCase) {
	t.Helper()
	compiled := compileEntitySchema(t, schema)
	for _, c := range cases {
		errs := ValidatePillarConformance(compiled, "values", c.values, nil,
			"Aggregate probe", `.spec.components["app"]`, "the Component schema")
		if c.wantField == "" {
			if len(errs) != 0 {
				t.Errorf("%s: refused: %v", c.name, errs)
			}
			continue
		}
		if len(errs) != 1 {
			t.Errorf("%s: got %d findings, want 1: %v", c.name, len(errs), errs)
			continue
		}
		if want := conformancePrefix + c.wantField; errs[0].Field != want {
			t.Errorf("%s: finding at %q, want %q", c.name, errs[0].Field, want)
		}
		if !strings.Contains(errs[0].Message, c.wantText) {
			t.Errorf("%s: finding %q does not say %q", c.name, errs[0].Message, c.wantText)
		}
	}
}

// TestPillarConformance_EscapeHatchTakesJSONSchema measures values against a
// `$jsonschema:` escape written in ordinary JSON Schema 2020-12 — a reference
// beside patternProperties under allOf, and a recursive shape through
// $dynamicRef — accepting what conforms and refusing what does not.
func TestPillarConformance_EscapeHatchTakesJSONSchema(t *testing.T) {
	t.Parallel()

	checkConformance(t, `values:
  $defs:
    Foo:
      type: object
      properties:
        name: { type: string }
  some_complex_field:
    $jsonschema: |
      type: object
      unevaluatedProperties: false
      allOf:
        - $ref: "#/$defs/Foo"
        - not:
            required: [conflicting_field]
      patternProperties:
        "^x-": { type: string }
  tree:
    $jsonschema: |
      $dynamicAnchor: node
      type: object
      properties:
        name: { type: string }
        children:
          type: array
          items: { $dynamicRef: "#node" }
`, []conformanceCase{
		{"conforming", object(
			"some_complex_field", object("name", "n", "x-a", "s"),
			"tree", object("name", "a", "children", []any{object("name", "b", "children", []any{object("name", "c")})}),
		), "", ""},
		{"a pattern property of the wrong type", object(
			"some_complex_field", object("name", "n", "x-a", 1),
		), ".some_complex_field.x-a", `want "string"`},
		{"a shape the not branch excludes", object(
			"some_complex_field", object("name", "n", "conflicting_field", "c"),
		), ".some_complex_field", "not allowed"},
		{"a recursive element of the wrong type", object(
			"tree", object("name", "a", "children", []any{object("name", "b", "children", []any{object("name", 5)})}),
		), ".tree.children", `want "string"`},
	})
}

// TestPillarConformance_RequiredInsidePropertiesIsEnforced measures values
// against a property marked `required: true` inside an explicit `properties:`
// block, in both explicit forms: a value without it is refused naming it, a
// value with it is accepted.
func TestPillarConformance_RequiredInsidePropertiesIsEnforced(t *testing.T) {
	t.Parallel()

	for _, form := range []struct{ name, schema string }{
		{"type: object with properties", `values:
  auth:
    type: object
    properties:
      username: { type: string }
      password: { type: string, required: true }
`},
		{"properties alone", `values:
  auth:
    properties:
      username: { type: string }
      password: { type: string, required: true }
`},
	} {
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			checkConformance(t, form.schema, []conformanceCase{
				{"the property present", object("auth", object("username", "u", "password", "p")), "", ""},
				{"the property absent", object("auth", object("username", "u")),
					".auth.password", "missing required property: password"},
			})
		})
	}
}

// TestPillarConformance_PointerIntoAPropertyReadsFromItsPillar measures values
// against a pointer into a declared property, spelled from the root of the
// pillar it is written in: it resolves, and a value failing through it is
// refused at the field carrying the pointer rather than at the field it names.
func TestPillarConformance_PointerIntoAPropertyReadsFromItsPillar(t *testing.T) {
	t.Parallel()

	requests := func(cpu string) types.OrderedMap[any] {
		return object("resources", object("requests", object("cpu", cpu)))
	}
	checkConformance(t, `values:
  primary:
    resources:
      requests:
        cpu: { type: string, pattern: "^[0-9]+(m|\\.[0-9]+)?$" }
  secondary:
    resources: { $ref: "#/properties/primary/properties/resources" }
`, []conformanceCase{
		{"conforming", object("primary", requests("250m"), "secondary", requests("500m")), "", ""},
		{"an invalid value through the pointer", object("primary", requests("250m"), "secondary", requests("lots")),
			".secondary.resources", "does not match"},
	})
}

// TestPillarConformance_WhenGatesAReferenceAndItsRequirement measures values
// against the conditional shape the compact form teaches: a property carrying
// a reference, a `when:` predicate, `required: true` and `properties:`. Its
// presence and its constraints apply only while the predicate holds.
func TestPillarConformance_WhenGatesAReferenceAndItsRequirement(t *testing.T) {
	t.Parallel()

	checkConformance(t, `values:
  $defs:
    NodePool:
      replicaCount: { type: integer, min: 1, max: 9, default: 1 }
  architecture: { type: string, enum: [standalone, replication], default: standalone }
  secondary:
    $ref: "#/$defs/NodePool"
    when: '{{ eq $.architecture "replication" }}'
    required: true
    properties:
      replicaCount: { type: integer, min: 2 }
`, []conformanceCase{
		{"predicate false, property absent", object("architecture", "standalone"), "", ""},
		{"predicate false, constraint broken", object("architecture", "standalone",
			"secondary", object("replicaCount", 1)), "", ""},
		{"predicate true, property absent", object("architecture", "replication"),
			".secondary", "missing required property: secondary"},
		{"predicate true, constraint broken", object("architecture", "replication",
			"secondary", object("replicaCount", 1)), ".secondary.replicaCount", "less than 2"},
		{"predicate true, conforming", object("architecture", "replication",
			"secondary", object("replicaCount", 3)), "", ""},
	})
}
