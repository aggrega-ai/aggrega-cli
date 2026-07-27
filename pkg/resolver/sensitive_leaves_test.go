// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for DeclaredSensitiveLeaves — the one reader of a schema's sensitivity
// declarations. Each case pairs a positive observation with the negative that
// makes it meaningful: a marker that appeared on every leaf, or a path spelling
// that did not match the one leaf paths are keyed by, would both look exactly
// like correct behaviour in a positive-only test.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

func compileEntitySchema(t *testing.T, src string) compactform.Schema {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	compiled, err := compactform.Compile(&node)
	if err != nil {
		t.Fatalf("schema fixture compile: %v", err)
	}
	return compiled
}

func TestDeclaredSensitiveLeaves_PerPillar(t *testing.T) {
	t.Parallel()
	declared := DeclaredSensitiveLeaves(compileEntitySchema(t, `
values:
  password: { type: string, sensitive: true }
  host: { type: string }
infra:
  root_token: { type: string, sensitive: true }
  replicas: { type: integer }
provides:
  dsn: { type: string, sensitive: true }
  name: { type: string }
  key: { type: string }
`))

	for _, tc := range []struct {
		pillar string
		got    map[string]bool
		want   string
		absent string
	}{
		{"values", declared.Values, "password", "host"},
		{"infra", declared.Infra, "root_token", "replicas"},
		{"provides", declared.Provides, "dsn", "name"},
	} {
		if !tc.got[tc.want] {
			t.Errorf("%s: %q declares sensitive but was not collected; got %v", tc.pillar, tc.want, tc.got)
		}
		if tc.got[tc.absent] {
			t.Errorf("%s: %q declares nothing but was collected; got %v", tc.pillar, tc.absent, tc.got)
		}
		if len(tc.got) != 1 {
			t.Errorf("%s: expected exactly one collected path, got %v", tc.pillar, tc.got)
		}
	}

	// A path is unique only within its pillar, so a name declared sensitive in
	// one pillar must not appear in another's set.
	if declared.Values["dsn"] || declared.Provides["password"] {
		t.Errorf("a pillar's declaration leaked into another pillar: %+v", declared)
	}
	if !declared.Any() {
		t.Errorf("Any() must be true when a pillar declares a sensitive leaf")
	}

	// The provides half is what the per-delivery-key marking reads, so the
	// non-sensitive provides keys of a referenced-secret shape must stay out.
	if declared.Provides["key"] {
		t.Errorf("an undeclared provides key was collected: %v", declared.Provides)
	}
}

// TestDeclaredSensitiveLeaves_NestedPathMatchesFlattenLeaves is the pinning test
// for the path spelling. A nested declaration is only findable if the collected
// path is spelled the way a leaf path is spelled; a silent divergence there makes
// every nested declaration invisible to a consumer holding a leaf path.
func TestDeclaredSensitiveLeaves_NestedPathMatchesFlattenLeaves(t *testing.T) {
	t.Parallel()
	declared := DeclaredSensitiveLeaves(compileEntitySchema(t, `
values:
  auth:
    basic:
      password: { type: string, sensitive: true }
      username: { type: string }
`))

	// The equivalent authored value tree, flattened by the one leaf-path walker
	// every per-leaf consumer uses.
	basic := types.OrderedMap[any]{}
	basic.Set("password", "s3cret")
	basic.Set("username", "admin")
	auth := types.OrderedMap[any]{}
	auth.Set("basic", basic)
	tree := types.OrderedMap[any]{}
	tree.Set("auth", auth)

	var passwordLeaf, usernameLeaf string
	for _, leaf := range types.FlattenLeaves(tree) {
		switch leaf.Path {
		case "auth.basic.password":
			passwordLeaf = leaf.Path
		case "auth.basic.username":
			usernameLeaf = leaf.Path
		}
	}
	if passwordLeaf == "" || usernameLeaf == "" {
		t.Fatalf("leaf-path fixture drifted: %+v", types.FlattenLeaves(tree))
	}

	if !declared.Values[passwordLeaf] {
		t.Errorf("nested declaration not collected under the leaf-path spelling %q; got %v", passwordLeaf, declared.Values)
	}
	if declared.Values[usernameLeaf] {
		t.Errorf("a sibling declaring nothing was collected at %q; got %v", usernameLeaf, declared.Values)
	}
	// The intermediate object carries no declaration of its own.
	if declared.Values["auth"] || declared.Values["auth.basic"] {
		t.Errorf("an intermediate object was collected: %v", declared.Values)
	}
}

func TestDeclaredSensitiveLeaves_NothingDeclared(t *testing.T) {
	t.Parallel()
	declared := DeclaredSensitiveLeaves(compileEntitySchema(t, `
values:
  host: { type: string }
provides:
  name: { type: string }
`))
	if declared.Any() {
		t.Errorf("Any() must be false when no pillar declares a sensitive leaf: %+v", declared)
	}
	if len(declared.Values)+len(declared.Infra)+len(declared.Provides) != 0 {
		t.Errorf("expected no collected paths, got %+v", declared)
	}
}

func TestDeclaredSensitiveLeaves_NoSchemaAndNoPillars(t *testing.T) {
	t.Parallel()
	if DeclaredSensitiveLeaves(nil).Any() {
		t.Errorf("a nil schema must declare nothing")
	}
	if DeclaredSensitiveLeaves(compactform.Schema{}).Any() {
		t.Errorf("an empty schema must declare nothing")
	}
	// A schema declaring only pillars this axis does not cover.
	if DeclaredSensitiveLeaves(compileEntitySchema(t, "locals:\n  x: { type: string, sensitive: true }\n")).Any() {
		t.Errorf("a pillar outside values/infra/provides must contribute nothing")
	}
}

// TestDeclaredSensitiveLeaves_OnlyBooleanTrueDeclares pins the strictness the
// reader documents. A composed schema carries a sibling map recording which
// entity in an extends chain declared each keyword, and its entries are entity
// names; a presence-only test would read one of those as a declaration.
func TestDeclaredSensitiveLeaves_OnlyBooleanTrueDeclares(t *testing.T) {
	t.Parallel()
	schema := compactform.Schema{
		"properties": map[string]any{
			"values": map[string]any{
				"properties": map[string]any{
					"truthy":  map[string]any{"x-aggrega-sensitive": true},
					"falsy":   map[string]any{"x-aggrega-sensitive": false},
					"stringy": map[string]any{"x-aggrega-sensitive": "parent"},
					"nully":   map[string]any{"x-aggrega-sensitive": nil},
				},
			},
		},
	}
	declared := DeclaredSensitiveLeaves(schema)
	if !declared.Values["truthy"] {
		t.Errorf("boolean true must declare; got %v", declared.Values)
	}
	for _, name := range []string{"falsy", "stringy", "nully"} {
		if declared.Values[name] {
			t.Errorf("%q must not declare; got %v", name, declared.Values)
		}
	}
}

// TestDeclaredSensitiveLeaves_MarkerBesideRefAndComposition covers the two node
// shapes that carry the marker without routing through the leaf emitter. Both
// are ordinary properties from the reader's side, so both must be collected.
func TestDeclaredSensitiveLeaves_MarkerBesideRefAndComposition(t *testing.T) {
	t.Parallel()
	declared := DeclaredSensitiveLeaves(compileEntitySchema(t, `
values:
  by_ref:
    $ref: "#/$defs/Credential"
    sensitive: true
  by_composition:
    anyOf:
      - type: string
      - type: integer
    sensitive: true
  plain:
    anyOf:
      - type: string
`))
	for _, name := range []string{"by_ref", "by_composition"} {
		if !declared.Values[name] {
			t.Errorf("%q declares sensitive beside its keyword but was not collected; got %v", name, declared.Values)
		}
	}
	if declared.Values["plain"] {
		t.Errorf("a composition declaring nothing was collected; got %v", declared.Values)
	}
}
