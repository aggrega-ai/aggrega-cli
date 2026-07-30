// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for DeclaredSensitiveLeaves — the one reader of a schema's sensitivity
// declarations. Each case pairs a positive observation with the negative that
// makes it meaningful: a marker that appeared on every leaf, or a path spelling
// that did not match the one leaf paths are keyed by, would both look exactly
// like correct behaviour in a positive-only test.

package resolver

import (
	"strings"
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

// compileEntitySchemaWithLibrary compiles an entity schema that references a
// SchemaLibrary entry. It exists because a cross-document reference is the only
// authoring route by which a definitions block reaches a Component's compiled
// schema at all: an entity's own `spec.schema` accepts nothing but the three
// pillar keys, so a `$defs` alongside them is rejected before compilation, while a
// library entry carrying one is merged into the consumer with the block attached
// to the merged node rather than hoisted to the document root.
func compileEntitySchemaWithLibrary(t *testing.T, src, librarySrc string) compactform.Schema {
	t.Helper()
	var library types.SchemaLibrary
	if err := yaml.Unmarshal([]byte(librarySrc), &library); err != nil {
		t.Fatalf("library fixture parse: %v", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	compiled, err := compactform.Compile(&node, compactform.WithSchemaLibraries(
		map[string]types.SchemaLibrary{library.Metadata.Name: library},
	))
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

// TestDeclaredSensitiveLeaves_InsideRefTargetAndCompositionBranch covers the two
// shapes where the declaration sits INSIDE rather than beside, which is a
// different position from the beside cases above and reaches the datum by a
// different route. Each positive is paired with the non-declaring sibling that
// shares its enclosure: a walk that marked the whole referring property, or the
// whole composing one, would satisfy every positive here and still be wrong.
func TestDeclaredSensitiveLeaves_InsideRefTargetAndCompositionBranch(t *testing.T) {
	t.Parallel()
	declared := compileEntitySchemaWithLibrary(t, `
values:
  from_ref:
    $ref: "Vault#/schemas/Credential"
  from_branch:
    allOf:
      - properties:
          zz_branch_secret: { type: string, sensitive: true }
          alpha_branch_public: { type: string }
`, `
apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Vault
  version: 1.0.0
spec:
  schemas:
    Credential:
      $defs:
        Inner:
          zz_inner_secret: { type: string, sensitive: true }
          alpha_inner_public: { type: string }
      nested:
        $ref: "#/$defs/Inner"
      alpha_outer_public: { type: string }
`)
	values := DeclaredSensitiveLeaves(declared).Values

	// A declaration inside the target contributes at the REFERRING property's
	// path plus its own chain — the path the authored datum occupies. A path
	// spelled from inside the definitions block would be unfindable.
	if !values["from_ref.nested.zz_inner_secret"] {
		t.Errorf("declaration inside a reference target not collected at the referring path; got %v", values)
	}
	// A branch composes onto the property carrying it, so its declaration
	// contributes at that property's own path plus its chain — the branch's
	// position in the composition array is not part of the path.
	if !values["from_branch.zz_branch_secret"] {
		t.Errorf("declaration inside a composition branch not collected at the composing path; got %v", values)
	}
	for _, path := range []string{
		"from_ref",                           // the referring property itself declares nothing
		"from_ref.nested",                    // nor the intermediate object
		"from_ref.nested.alpha_inner_public", // a sibling inside the same target
		"from_ref.alpha_outer_public",        // a sibling beside the reference
		"from_branch",                        // the composing property itself
		"from_branch.alpha_branch_public",    // a sibling inside the same branch
	} {
		if values[path] {
			t.Errorf("%q declares nothing but was collected; got %v", path, values)
		}
	}
}

// TestDeclaredSensitiveLeaves_EveryBranchKeyword pins that each branch keyword the
// compiled vocabulary admits is descended. The reason to cover all of them rather
// than one representative is that they reach the walk through two different Go
// shapes — an array of schemas and a single schema — so covering one shape leaves
// the other untested.
func TestDeclaredSensitiveLeaves_EveryBranchKeyword(t *testing.T) {
	t.Parallel()
	values := DeclaredSensitiveLeaves(compileEntitySchema(t, `
values:
  in_allof:
    allOf: [ { properties: { zz_s: { type: string, sensitive: true } } } ]
  in_anyof:
    anyOf: [ { properties: { zz_s: { type: string, sensitive: true } } } ]
  in_oneof:
    oneOf: [ { properties: { zz_s: { type: string, sensitive: true } } } ]
  in_if:
    if: { properties: { zz_s: { type: string, sensitive: true } } }
  in_then:
    then: { properties: { zz_s: { type: string, sensitive: true } } }
  in_else:
    else: { properties: { zz_s: { type: string, sensitive: true } } }
  in_not:
    not: { properties: { zz_s: { type: string, sensitive: true } } }
  in_none:
    allOf: [ { properties: { alpha_s: { type: string } } } ]
`)).Values
	for _, carrier := range []string{"in_allof", "in_anyof", "in_oneof", "in_if", "in_then", "in_else", "in_not"} {
		if !values[carrier+".zz_s"] {
			t.Errorf("a declaration under %q was not collected; got %v", carrier, values)
		}
	}
	if values["in_none.alpha_s"] || values["in_none"] {
		t.Errorf("a branch declaring nothing contributed a path; got %v", values)
	}
}

// TestDeclaredSensitiveLeaves_UnresolvableRefCollectsTheReferringPath pins the
// conservative direction. What an unresolvable target declares is unknown, and the
// reading that cannot leak is to collect the referring property — a declaration on
// a pillar path withholds everything beneath it. The paired negative is the
// resolvable composition in the same schema, which must still contribute its
// precise inner path rather than being blanket-marked.
//
// Only the in-document shape is exercised, because it is the only unresolvable
// shape that can reach this reader: a cross-document reference is either merged
// into the consumer's own schema or halts compilation, so one cannot survive into a
// compiled schema unresolved.
func TestDeclaredSensitiveLeaves_UnresolvableRefCollectsTheReferringPath(t *testing.T) {
	t.Parallel()
	values := DeclaredSensitiveLeaves(compileEntitySchema(t, `
values:
  dangling: { $ref: "#/$defs/NotThere" }
  resolvable:
    allOf: [ { properties: { zz_s: { type: string, sensitive: true } } } ]
`)).Values
	if !values["dangling"] {
		t.Errorf("an unresolvable reference must collect the referring path; got %v", values)
	}
	if values["resolvable"] {
		t.Errorf("a resolvable composition must not be blanket-marked; got %v", values)
	}
	if !values["resolvable.zz_s"] {
		t.Errorf("the resolvable branch's own declaration went missing; got %v", values)
	}
}

// TestDeclaredSensitiveLeaves_SelfReferentialTargetTerminates covers the two
// recursive cases together, because they are the ones that must differ. A
// recursion carrying a declaration has a tail the walk cannot enumerate, so the
// tail is withheld; a recursion declaring nothing must stay readable, since a path
// is sensitive only when an author said so. A rule that withheld on every
// recursion would satisfy the first case and invent sensitivity in the second.
//
// Termination is the precondition for either assertion: an unguarded walk would
// not return at all, so reaching a comparison is itself part of what this covers.
func TestDeclaredSensitiveLeaves_SelfReferentialTargetTerminates(t *testing.T) {
	t.Parallel()
	values := DeclaredSensitiveLeaves(compileEntitySchemaWithLibrary(t, `
values:
  declaring: { $ref: "Vault#/schemas/Declaring" }
  silent: { $ref: "Vault#/schemas/Silent" }
`, `
apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Vault
  version: 1.0.0
spec:
  schemas:
    Declaring:
      $defs:
        Node:
          zz_secret: { type: string, sensitive: true }
          child: { $ref: "#/$defs/Node" }
      root: { $ref: "#/$defs/Node" }
    Silent:
      $defs:
        Node:
          alpha_public: { type: string }
          child: { $ref: "#/$defs/Node" }
      root: { $ref: "#/$defs/Node" }
`)).Values

	if !values["declaring.root.zz_secret"] {
		t.Errorf("the enumerable depth of a declaring recursion was not collected; got %v", values)
	}
	if !values["declaring.root.child"] {
		t.Errorf("the unenumerable tail of a declaring recursion must be withheld; got %v", values)
	}
	for path := range values {
		if strings.HasPrefix(path, "silent") {
			t.Errorf("a recursion declaring nothing must contribute no path; got %q in %v", path, values)
		}
	}
}
