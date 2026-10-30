// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for a pillar whose own node is an in-document reference. Two readers ask
// what such a pillar declares — the sensitivity reader over a whole-entity
// compile, and the reader of the key names a pillar publishes over a compile of
// the pillar alone — and both read the answer from pillarPropertyMaps. Each table
// carries an inline control row, whose answer must not move, beside the reference
// rows, whose answer is the target's declarations composed with any property map
// written beside the reference.

package resolver

import (
	"slices"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// pillarReferenceSchemaNode parses an authored spec.schema into the node an
// entity carries, unwrapping the document node exactly as a loaded entity's
// schema is held.
func pillarReferenceSchemaNode(t *testing.T, src string) yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		return *doc.Content[0]
	}
	return doc
}

func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPillarKeys_ReferenceOnThePillarNode pins which names a pillar written as a
// reference publishes, through each exported entry point an inject guard and a
// provides-contract check read. The sibling-map rows are the ones a consumer's
// `from:` reference is refused on when the target's names are missing: a key the
// referenced definition declares is a declared key of the pillar.
func TestPillarKeys_ReferenceOnThePillarNode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string // the pillar node, indented under its pillar key
		want []string
	}{
		{"inline control", `
    host: { type: string }
    port: { type: string }
`, []string{"host", "port"}},
		{"bare reference exposes the target's names", `
    $ref: "#/$defs/Contract"
    $defs:
      Contract:
        host: { type: string }
        port: { type: string }
`, []string{"host", "port"}},
		{"reference beside a property map exposes both", `
    $ref: "#/$defs/Base"
    $defs:
      Base:
        display_name: { type: string }
    properties:
      host: { type: string }
      port: { type: string }
`, []string{"display_name", "host", "port"}},
		{"a name declared on both sides is published once", `
    $ref: "#/$defs/Base"
    $defs:
      Base:
        host: { type: string }
    properties:
      host: { type: string, pattern: "^[a-z]+$" }
`, []string{"host"}},
		{"a target that is itself a reference is followed", `
    $ref: "#/$defs/Outer"
    $defs:
      Outer:
        $ref: "#/$defs/Inner"
        properties:
          outer_key: { type: string }
      Inner:
        inner_key: { type: string }
`, []string{"inner_key", "outer_key"}},
		{"a reference to the pillar itself terminates", `
    $ref: "#"
    properties:
      host: { type: string }
`, []string{"host"}},
		{"an unresolvable pointer adds nothing to the property map beside it", `
    $ref: "#/$defs/Missing"
    properties:
      host: { type: string }
`, []string{"host"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, pillar := range []string{"provides", "values"} {
				schema := pillarReferenceSchemaNode(t, pillar+":"+tc.body)
				var got map[string]bool
				switch pillar {
				case "provides":
					got = ComponentProvidesKeys(types.Component{Spec: types.ComponentSpec{Schema: schema}}, nil)
					feature := FeatureProvidesKeys(types.Feature{Spec: types.FeatureSpec{Schema: schema}}, nil)
					if !slices.Equal(sortedNames(feature), sortedNames(got)) {
						t.Errorf("a Feature's provides pillar read %v where a Component's identical pillar read %v",
							sortedNames(feature), sortedNames(got))
					}
				case "values":
					got = ComponentValuesKeys(types.Component{Spec: types.ComponentSpec{Schema: schema}}, nil)
				}
				if names := sortedNames(got); !slices.Equal(names, tc.want) {
					t.Errorf("%s pillar exposed %v, want %v", pillar, names, tc.want)
				}
			}
		})
	}
}

// TestDeclaredSensitiveLeaves_ReferenceOnThePillarNode pins the sensitivity half
// of the same reading: a declaration inside the pillar's reference target is
// collected at its own path below the pillar, and one in a property map beside
// the reference is collected too. Every row carries an undeclared name that must
// stay out of the set, so a reader marking everything below the pillar fails here.
func TestDeclaredSensitiveLeaves_ReferenceOnThePillarNode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		schema string
		want   []string
	}{
		{"inline control", `
values:
  token: { type: string, sensitive: true }
  host:  { type: string }
`, []string{"token"}},
		{"definitions on the pillar node", `
values:
  $ref: "#/$defs/Creds"
  $defs:
    Creds:
      token: { type: string, sensitive: true }
      host:  { type: string }
`, []string{"token"}},
		{"definitions at the document root", `
$defs:
  Creds:
    token: { type: string, sensitive: true }
    host:  { type: string }
values:
  $ref: "#/$defs/Creds"
`, []string{"token"}},
		{"reference beside a property map declares from both", `
values:
  $ref: "#/$defs/Creds"
  $defs:
    Creds:
      properties:
        token: { type: string, sensitive: true }
  properties:
    password: { type: string, sensitive: true }
    host:     { type: string }
`, []string{"password", "token"}},
		{"one name declared sensitive on one side only", `
values:
  $ref: "#/$defs/Creds"
  $defs:
    Creds:
      properties:
        token: { type: string, sensitive: true }
  properties:
    token: { type: string }
    host:  { type: string }
`, []string{"token"}},
		{"a nested declaration in the target keeps its dotted path", `
values:
  $ref: "#/$defs/Creds"
  $defs:
    Creds:
      auth:
        password: { type: string, sensitive: true }
        user:     { type: string }
`, []string{"auth.password"}},
		{"a marker on the pillar node itself declares nothing", `
values:
  $ref: "#/$defs/Creds"
  sensitive: true
  $defs:
    Creds:
      host: { type: string }
`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sortedNames(DeclaredSensitiveLeaves(compileEntitySchema(t, tc.schema)).Values)
			if !slices.Equal(got, tc.want) {
				t.Errorf("collected %v, want %v", got, tc.want)
			}
		})
	}
}
