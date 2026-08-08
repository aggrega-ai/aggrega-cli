// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestEmitCompactConstraint_TypeArray_LeafPosition verifies the
// compactform `type:` handler accepts JSON Schema 2020-12 array-of-types
// at a leaf-position node. The pattern "a map of arbitrary keys whose
// values are string-or-int-or-bool" translates to
// `additionalProperties: { type: [string, integer, boolean] }`;
// the inner `additionalProperties` value is a leaf-position node whose
// `type:` value is a SequenceNode. Prior to the fix the SequenceNode value
// was silently dropped because the `type:` handler accepted ScalarNode
// only; the compiled output had no `type:` key at all.
func TestEmitCompactConstraint_TypeArray_LeafPosition(t *testing.T) {
	t.Parallel()

	src := `type: [string, integer, boolean]`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got, ok := out["type"]
	if !ok {
		t.Fatalf("expected `type:` key in compiled schema; got %v", out)
	}
	want := []any{"string", "integer", "boolean"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("type = %v (%T); want %v ([]any)", got, got, want)
	}
}

// TestEmitCompactConstraint_TypeArray_UnionWithMinHalts verifies the
// loud-halt error path documented at minMaxKeyForType: a node with
// `type: [string, integer, ...]` (union) plus `min:` cannot resolve
// the polymorphic dispatch (minimum vs minLength vs minItems) and
// raises ErrConstraintTypeMismatch with a message that names the
// unresolved-type origin.
func TestEmitCompactConstraint_TypeArray_UnionWithMinHalts(t *testing.T) {
	t.Parallel()

	src := `type: [string, integer]
min: 5`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrConstraintTypeMismatch for union-with-min; got nil")
	}
	if !errors.Is(err, ErrConstraintTypeMismatch()) {
		t.Errorf("expected ErrConstraintTypeMismatch; got %v", err)
	}
	if !strings.Contains(err.Error(), "unresolved") {
		t.Errorf("error message should name the unresolved-type origin; got %q", err.Error())
	}
}

// TestEmitCompactConstraint_TypeUnsupportedNodeKind_Halts verifies the
// explicit error path for `type:` values that are neither scalar nor
// sequence (e.g., a YAML mapping authored at the `type:` slot). Prior
// to the cleanup, MappingNode and AliasNode values at `type:` silently
// dropped — bug surface was smaller than pre-P1 but not closed.
func TestEmitCompactConstraint_TypeUnsupportedNodeKind_Halts(t *testing.T) {
	t.Parallel()

	src := `type:
  foo: bar
min: 1`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrConstraintTypeMismatch for unsupported type-value node kind; got nil")
	}
	if !errors.Is(err, ErrConstraintTypeMismatch()) {
		t.Errorf("expected ErrConstraintTypeMismatch; got %v", err)
	}
}

// TestEmitCompactConstraint_TypeArray_CrossCutThroughJSONSchemaEscape
// verifies the compactform `type:` array-of-types support through the
// `$jsonschema:` escape's directly-nested mapping form — the path that
// flowise's `override_config?: { [string]: string | int | bool }`
// authoring exercises in the catalog. The compile must produce the
// full nested shape with `additionalProperties: { type: [...] }`
// preserved verbatim.
func TestEmitCompactConstraint_TypeArray_CrossCutThroughJSONSchemaEscape(t *testing.T) {
	t.Parallel()

	src := `
override_config:
  $jsonschema:
    type: object
    additionalProperties:
      type: [string, integer, boolean]
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	props, ok := out["properties"].(Schema)
	if !ok {
		t.Fatalf("expected properties map; got %v", out["properties"])
	}
	override, ok := props["override_config"].(Schema)
	if !ok {
		t.Fatalf("expected override_config schema; got %v", props["override_config"])
	}
	if got := override["type"]; got != "object" {
		t.Errorf("override_config.type = %v; want \"object\"", got)
	}
	addProps, ok := override["additionalProperties"].(Schema)
	if !ok {
		t.Fatalf("expected additionalProperties schema; got %v", override["additionalProperties"])
	}
	typeField, hasType := addProps["type"]
	if !hasType {
		t.Fatalf("expected additionalProperties.type key; got %v", addProps)
	}
	want := []any{"string", "integer", "boolean"}
	if !reflect.DeepEqual(typeField, want) {
		t.Errorf("additionalProperties.type = %v (%T); want %v ([]any)", typeField, typeField, want)
	}
}

// TestEmitMinMaxDispatchInteger verifies min/max on a `type: integer`
// field emit as `minimum` and `maximum`.
func TestEmitMinMaxDispatchInteger(t *testing.T) {
	t.Parallel()

	src := `port: { type: integer, min: 1, max: 65535 }`
	out := mustCompile(t, src)
	port := out["properties"].(Schema)["port"].(Schema)
	if port["minimum"] != 1 {
		t.Errorf("expected minimum=1; got %v (%T)", port["minimum"], port["minimum"])
	}
	if port["maximum"] != 65535 {
		t.Errorf("expected maximum=65535; got %v", port["maximum"])
	}
}

// TestEmitMinMaxDispatchString verifies min/max on a `type: string`
// field emit as `minLength` and `maxLength`.
func TestEmitMinMaxDispatchString(t *testing.T) {
	t.Parallel()

	src := `name: { type: string, min: 1, max: 64 }`
	out := mustCompile(t, src)
	name := out["properties"].(Schema)["name"].(Schema)
	if name["minLength"] != 1 {
		t.Errorf("expected minLength=1; got %v", name["minLength"])
	}
	if name["maxLength"] != 64 {
		t.Errorf("expected maxLength=64; got %v", name["maxLength"])
	}
}

// TestEmitMinMaxDispatchArray verifies min/max on a `type: array`
// field emit as `minItems` and `maxItems`.
func TestEmitMinMaxDispatchArray(t *testing.T) {
	t.Parallel()

	src := `
tags:
  type: array
  min: 1
  max: 10
  items: { type: string }
`
	out := mustCompile(t, src)
	tags := out["properties"].(Schema)["tags"].(Schema)
	if tags["minItems"] != 1 {
		t.Errorf("expected minItems=1; got %v", tags["minItems"])
	}
	if tags["maxItems"] != 10 {
		t.Errorf("expected maxItems=10; got %v", tags["maxItems"])
	}
}

// TestEmitValuesType verifies the `valuesType:` shorthand expands to
// `additionalProperties: { type: <X> }`.
func TestEmitValuesType(t *testing.T) {
	t.Parallel()

	src := `labels: { type: object, valuesType: string }`
	out := mustCompile(t, src)
	labels := out["properties"].(Schema)["labels"].(Schema)
	ap, ok := labels["additionalProperties"].(Schema)
	if !ok {
		t.Fatalf("additionalProperties is not a Schema; got %T", labels["additionalProperties"])
	}
	if ap["type"] != "string" {
		t.Errorf("expected additionalProperties.type=string; got %v", ap["type"])
	}
}

// TestEmitRequiredAccumulation verifies per-leaf `required: true`
// markers accumulate into the parent object's `required:` array.
func TestEmitRequiredAccumulation(t *testing.T) {
	t.Parallel()

	src := `
auth:
  username: { type: string, default: app_user }
  password: { type: string, required: true }
  database: { type: string, required: true }
`
	out := mustCompile(t, src)
	auth := out["properties"].(Schema)["auth"].(Schema)
	required, ok := auth["required"].([]string)
	if !ok {
		t.Fatalf("required is not []string; got %T", auth["required"])
	}
	if len(required) != 2 {
		t.Fatalf("expected 2 required fields; got %d", len(required))
	}
	hasPassword, hasDatabase := false, false
	for _, r := range required {
		if r == "password" {
			hasPassword = true
		}
		if r == "database" {
			hasDatabase = true
		}
	}
	if !hasPassword || !hasDatabase {
		t.Errorf("expected required=[password,database]; got %v", required)
	}
}

// TestEmitImplicitObjectClosure verifies compact-form objects are
// closed by default (`additionalProperties: false`).
func TestEmitImplicitObjectClosure(t *testing.T) {
	t.Parallel()

	src := `
spec:
  name: { type: string }
`
	out := mustCompile(t, src)
	spec := out["properties"].(Schema)["spec"].(Schema)
	if spec["additionalProperties"] != false {
		t.Errorf("expected additionalProperties=false (closure); got %v", spec["additionalProperties"])
	}
}

// TestEmitValidateExtensionKeyword verifies `validate:` + `validateMessage:`
// emit as `x-aggrega-validate:` and `x-aggrega-validateMessage:`
// extension keywords for the validator adapter to consume.
func TestEmitValidateExtensionKeyword(t *testing.T) {
	t.Parallel()

	src := `
db_size:
  type: string
  validate: '{{ regexFind "^[0-9]+" . | atoi | gt 100 }}'
  validateMessage: "db_size must be at least 100"
`
	out := mustCompile(t, src)
	dbSize := out["properties"].(Schema)["db_size"].(Schema)
	if _, ok := dbSize["x-aggrega-validate"]; !ok {
		t.Errorf("expected x-aggrega-validate extension keyword; got %v", dbSize)
	}
	if msg := dbSize["x-aggrega-validateMessage"]; msg != "db_size must be at least 100" {
		t.Errorf("expected x-aggrega-validateMessage to round-trip; got %v", msg)
	}
}

// TestEmitWhenExtensionKeyword verifies `when:` emits as
// `x-aggrega-when:` extension keyword. Trivial form: no `then:` /
// `else:` siblings, so no JSON Schema if/then/else wrapper is injected
// — the `when:` continues to gate the surrounding node's constraints
// per the original compact-form semantic.
func TestEmitWhenExtensionKeyword(t *testing.T) {
	t.Parallel()

	src := `
secondary:
  $ref: "#/$defs/NodePool"
  when: '{{ eq $.architecture "replication" }}'
`
	out := mustCompile(t, src)
	secondary := out["properties"].(Schema)["secondary"].(Schema)
	if _, ok := secondary["x-aggrega-when"]; !ok {
		t.Errorf("expected x-aggrega-when extension keyword; got %v", secondary)
	}
	if secondary["$ref"] != "#/$defs/NodePool" {
		t.Errorf("expected $ref to round-trip alongside when; got %v", secondary["$ref"])
	}
	// Trivial `when:` form (no then/else): no if/then/else wrapper.
	if _, hasIf := secondary["if"]; hasIf {
		t.Errorf("trivial when: form should not inject if/then/else wrapper; got if=%v", secondary["if"])
	}
}

// TestEmitWhenWrapperThenOnly verifies the if/then/else wrapper is
// emitted when `when:` pairs with a `then:` clause and no `else:`.
// Per ratified decision D-001b-2: omit `else:` entirely when not
// authored.
func TestEmitWhenWrapperThenOnly(t *testing.T) {
	t.Parallel()

	src := `
secondary:
  when: '{{ eq $.architecture "replication" }}'
  then:
    replicaCount: { type: integer, min: 2 }
`
	out := mustCompile(t, src)
	secondary := out["properties"].(Schema)["secondary"].(Schema)

	if v, ok := secondary["if"]; !ok || v != true {
		t.Errorf("expected if=true wrapper (D-001b-1); got if=%v ok=%v", v, ok)
	}
	if _, ok := secondary["then"]; !ok {
		t.Errorf("expected then: in wrapper; got %v", secondary)
	}
	if _, hasElse := secondary["else"]; hasElse {
		t.Errorf("D-001b-2: else: should be omitted when not authored; got else=%v", secondary["else"])
	}
	if _, ok := secondary["x-aggrega-when"]; !ok {
		t.Errorf("x-aggrega-when extension keyword must accompany the wrapper")
	}
}

// TestEmitWhenWrapperThenAndElse verifies both then: and else: emit
// when authored, with the if: true wrapper alongside x-aggrega-when:.
func TestEmitWhenWrapperThenAndElse(t *testing.T) {
	t.Parallel()

	src := `
tlsConfig:
  when: '{{ eq $.tlsEnabled true }}'
  then:
    certPath: { type: string, required: true }
    keyPath: { type: string, required: true }
  else:
    plainPort: { type: integer, min: 1024, max: 65535 }
`
	out := mustCompile(t, src)
	tlsConfig := out["properties"].(Schema)["tlsConfig"].(Schema)

	if v, ok := tlsConfig["if"]; !ok || v != true {
		t.Errorf("expected if=true wrapper; got if=%v ok=%v", v, ok)
	}
	if _, ok := tlsConfig["then"]; !ok {
		t.Errorf("expected then: in wrapper")
	}
	if _, ok := tlsConfig["else"]; !ok {
		t.Errorf("expected else: in wrapper when authored")
	}
	if _, ok := tlsConfig["x-aggrega-when"]; !ok {
		t.Errorf("x-aggrega-when extension keyword must accompany the wrapper")
	}
}

// TestEmitWhenWrapperWithRef verifies the wrapper composes correctly
// when `when:` pairs with `$ref:` plus a `then:` clause (the
// emitReference path).
func TestEmitWhenWrapperWithRef(t *testing.T) {
	t.Parallel()

	src := `
secondary:
  $ref: "#/$defs/NodePool"
  when: '{{ eq $.architecture "replication" }}'
  then:
    replicaCount: { type: integer, min: 2 }
`
	out := mustCompile(t, src)
	secondary := out["properties"].(Schema)["secondary"].(Schema)

	if secondary["$ref"] != "#/$defs/NodePool" {
		t.Errorf("$ref must round-trip alongside the wrapper; got %v", secondary["$ref"])
	}
	if v, ok := secondary["if"]; !ok || v != true {
		t.Errorf("expected if=true wrapper at $ref node; got if=%v ok=%v", v, ok)
	}
	if _, ok := secondary["then"]; !ok {
		t.Errorf("expected then: in wrapper at $ref node")
	}
}

// TestEmitWhenWrapperNested verifies a nested `when:` block inside a
// `then:` body composes correctly — the outer wrapper carries its own
// if/then/else; the inner then-body's nested when produces its own
// wrapper.
func TestEmitWhenWrapperNested(t *testing.T) {
	t.Parallel()

	src := `
outerToggle:
  when: '{{ .featureEnabled }}'
  then:
    inner:
      when: '{{ .innerToggle }}'
      then:
        deepField: { type: string, required: true }
`
	out := mustCompile(t, src)
	outer := out["properties"].(Schema)["outerToggle"].(Schema)

	if outer["if"] != true {
		t.Errorf("outer wrapper missing if=true; got %v", outer["if"])
	}
	thenBody, ok := outer["then"].(Schema)
	if !ok {
		t.Fatalf("outer.then should be a Schema; got %T", outer["then"])
	}
	// thenBody is an implicit object with property `inner`.
	inner, ok := thenBody["properties"].(Schema)["inner"].(Schema)
	if !ok {
		t.Fatalf("then-body.inner not a Schema; got %T", thenBody["properties"].(Schema)["inner"])
	}
	if inner["if"] != true {
		t.Errorf("inner nested wrapper missing if=true; got %v", inner["if"])
	}
	if _, ok := inner["x-aggrega-when"]; !ok {
		t.Errorf("inner wrapper missing x-aggrega-when")
	}
}

// TestEmitExplicitIfNotOverwritten verifies that when the author writes
// an explicit `if:` alongside `when:` + `then:`, the explicit `if:`
// schema is preserved — the wrapper synthesis sees an existing `if:`
// and skips injection. The defensive guard in injectWhenWrapper exists
// for this case.
func TestEmitExplicitIfNotOverwritten(t *testing.T) {
	t.Parallel()

	src := `
field:
  if:
    properties:
      kind: { const: special }
  when: '{{ eq .kind "special" }}'
  then:
    extra: { type: string }
`
	out := mustCompile(t, src)
	field := out["properties"].(Schema)["field"].(Schema)
	ifValue, ok := field["if"]
	if !ok {
		t.Fatalf("authored if: should round-trip; got %v", field)
	}
	if asBool, isBool := ifValue.(bool); isBool {
		t.Errorf("authored if: should not be coerced to bool=true by the wrapper; got bool=%v", asBool)
	}
	// The wrapper helper saw an existing if: in `out` and skipped
	// injection; the authored if: stands. then: and x-aggrega-when:
	// remain alongside.
	if _, ok := field["then"]; !ok {
		t.Errorf("then: should round-trip alongside the authored if:")
	}
	if _, ok := field["x-aggrega-when"]; !ok {
		t.Errorf("x-aggrega-when: should round-trip alongside the authored if:")
	}
}

// TestEmitValuesTypeNonScalarRejected verifies valuesType: with a
// non-scalar value (e.g., a nested object) rejects with
// ErrConstraintTypeMismatch — the shorthand requires a scalar type name.
func TestEmitValuesTypeNonScalarRejected(t *testing.T) {
	t.Parallel()

	src := `bad: { type: object, valuesType: { complex: shape } }`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrConstraintTypeMismatch; got nil")
	}
	if !errors.Is(err, ErrConstraintTypeMismatch()) {
		t.Errorf("expected ErrConstraintTypeMismatch; got %v", err)
	}
}

// TestEmitAdditionalPropertiesExplicit verifies an explicit
// `additionalProperties: true` on a compact-form constraint leaf
// emits unchanged (overrides closure-by-default).
func TestEmitAdditionalPropertiesExplicit(t *testing.T) {
	t.Parallel()

	src := `labels: { type: object, additionalProperties: true }`
	out := mustCompile(t, src)
	labels := out["properties"].(Schema)["labels"].(Schema)
	if labels["additionalProperties"] != true {
		t.Errorf("expected explicit additionalProperties=true; got %v", labels["additionalProperties"])
	}
}

// TestEmitReferenceEmptyRefSibling verifies emitReference's
// non-$ref sibling key handling (the default branch).
func TestEmitReferenceWithDescription(t *testing.T) {
	t.Parallel()

	src := `
shared:
  $ref: "#/$defs/Thing"
  description: "shared reference with adjacent description"
`
	out := mustCompile(t, src)
	shared := out["properties"].(Schema)["shared"].(Schema)
	if shared["$ref"] != "#/$defs/Thing" {
		t.Errorf("$ref should round-trip; got %v", shared["$ref"])
	}
	if shared["description"] != "shared reference with adjacent description" {
		t.Errorf("description sibling should round-trip; got %v", shared["description"])
	}
}

// TestEmitEscapeNestedMappingForm verifies the $jsonschema: escape
// with a directly-nested mapping (not a block-scalar string).
func TestEmitEscapeNestedMappingForm(t *testing.T) {
	t.Parallel()

	src := `
field:
  $jsonschema:
    type: object
    properties:
      direct: { type: string }
    additionalProperties: false
`
	out := mustCompile(t, src)
	field := out["properties"].(Schema)["field"].(Schema)
	if field["type"] != "object" {
		t.Errorf("expected type=object via nested-mapping escape; got %v", field["type"])
	}
	props := field["properties"].(Schema)
	direct, ok := props["direct"].(Schema)
	if !ok {
		t.Fatalf("direct entry should be Schema; got %T", props["direct"])
	}
	if direct["type"] != "string" {
		t.Errorf("nested escape property type=string expected; got %v", direct["type"])
	}
}

// TestEmitMinMaxOnObjectRejected verifies a `min:` constraint applied
// to a `type: object` field surfaces as ErrConstraintTypeMismatch.
// Targets minMaxKeyForType's default branch.
func TestEmitMinMaxOnBooleanRejected(t *testing.T) {
	t.Parallel()

	src := `flag: { type: boolean, max: 5 }`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrConstraintTypeMismatch; got nil")
	}
	if !errors.Is(err, ErrConstraintTypeMismatch()) {
		t.Errorf("expected ErrConstraintTypeMismatch; got %v", err)
	}
}

// TestEmitJSONSchemaPassthroughWithDefs verifies emitJSONSchemaPassthrough's
// schema-map handling for `$defs:` (not just `properties:`).
func TestEmitJSONSchemaPassthroughWithDefs(t *testing.T) {
	t.Parallel()

	src := `
root:
  $jsonschema: |
    type: object
    properties:
      thing: { $ref: "#/$defs/Thing" }
    $defs:
      Thing:
        type: integer
        minimum: 0
`
	out := mustCompile(t, src)
	root := out["properties"].(Schema)["root"].(Schema)
	defs, ok := root["$defs"].(Schema)
	if !ok {
		t.Fatalf("escape with $defs should emit $defs at root; got %T", root["$defs"])
	}
	thing, ok := defs["Thing"].(Schema)
	if !ok {
		t.Fatalf("$defs/Thing should be Schema; got %T", defs["Thing"])
	}
	if thing["type"] != "integer" {
		t.Errorf("$defs/Thing.type=integer expected; got %v", thing["type"])
	}
}

// TestEmitJSONSchemaPassthroughDependentSchemas verifies the
// dependentSchemas: keyword routes through emitSchemaMap (the keyword
// holds a map-of-schemas like properties/patternProperties/$defs).
func TestEmitJSONSchemaPassthroughDependentSchemas(t *testing.T) {
	t.Parallel()

	src := `
root:
  $jsonschema: |
    type: object
    dependentSchemas:
      tlsEnabled:
        properties:
          certPath: { type: string }
        required: [certPath]
`
	out := mustCompile(t, src)
	root := out["properties"].(Schema)["root"].(Schema)
	deps, ok := root["dependentSchemas"].(Schema)
	if !ok {
		t.Fatalf("dependentSchemas should be a map-of-schemas; got %T", root["dependentSchemas"])
	}
	tls, ok := deps["tlsEnabled"].(Schema)
	if !ok {
		t.Fatalf("dependentSchemas/tlsEnabled should be Schema; got %T", deps["tlsEnabled"])
	}
	// The dependent schema is a partial JSON Schema (properties +
	// required) without an explicit type. Verify properties round-trip
	// rather than asserting a synthesised type.
	props, ok := tls["properties"].(Schema)
	if !ok {
		t.Fatalf("dependent schema should carry properties; got %v", tls)
	}
	cert, ok := props["certPath"].(Schema)
	if !ok {
		t.Fatalf("dependentSchemas/tlsEnabled/properties/certPath should be Schema; got %T", props["certPath"])
	}
	if cert["type"] != "string" {
		t.Errorf("certPath.type=string expected; got %v", cert["type"])
	}
}

// TestCompileScalarRootWrapped verifies that a compact-form root that
// resolves to a scalar (rather than a Schema) is wrapped in a minimal
// schema. Edge case for the Compile entry point's wrap-fallback.
func TestCompileScalarRootWrapped(t *testing.T) {
	t.Parallel()

	src := `"just a string"`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, ok := out["value"]; !ok {
		t.Errorf("scalar root should wrap as {value: ...}; got %v", out)
	}
}

// TestEmitSensitiveExtensionKeyword verifies a leaf declared
// `sensitive: true` emits the `x-aggrega-sensitive:` extension keyword,
// and that a leaf that declares nothing gets no marker. The negative half
// is the load-bearing one: a marker that appeared unconditionally would
// make every leaf in every catalog read as secret material.
func TestEmitSensitiveExtensionKeyword(t *testing.T) {
	t.Parallel()

	src := `
db_password:
  type: string
  sensitive: true
db_host:
  type: string
`
	out := mustCompile(t, src)
	props := out["properties"].(Schema)

	pw := props["db_password"].(Schema)
	if got := pw["x-aggrega-sensitive"]; got != true {
		t.Errorf("expected x-aggrega-sensitive=true on the declared leaf; got %v (%T) in %v", got, got, pw)
	}
	if _, leaked := pw["sensitive"]; leaked {
		t.Errorf("the compact-form key must not survive alongside its extension form; got %v", pw)
	}

	host := props["db_host"].(Schema)
	if _, ok := host["x-aggrega-sensitive"]; ok {
		t.Errorf("an undeclared leaf must carry no marker; got %v", host)
	}

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	t.Logf("compiled schema: %s", b)
}

// TestEmitSensitiveExtensionKeyword_RefAndCompositionSiblings verifies the
// marker survives at the two node shapes that do not route through the
// constraint-leaf emitter — a sibling of an in-document `$ref:` and a
// sibling of a composition keyword. Both have their own key-to-extension
// mapping, so a leaf-only implementation would silently emit a raw
// `sensitive:` key that is neither JSON Schema nor an Aggrega extension.
func TestEmitSensitiveExtensionKeyword_RefAndCompositionSiblings(t *testing.T) {
	t.Parallel()

	src := `
by_ref:
  $ref: "#/$defs/Credential"
  sensitive: true
by_composition:
  anyOf:
    - type: string
    - type: integer
  sensitive: true
`
	out := mustCompile(t, src)
	props := out["properties"].(Schema)

	for _, name := range []string{"by_ref", "by_composition"} {
		node := props[name].(Schema)
		if got := node["x-aggrega-sensitive"]; got != true {
			t.Errorf("%s: expected x-aggrega-sensitive=true; got %v in %v", name, got, node)
		}
		if _, leaked := node["sensitive"]; leaked {
			t.Errorf("%s: raw `sensitive` key leaked into the emitted schema: %v", name, node)
		}
	}
}

// TestEmitSensitiveExtensionKeyword_BareLeafNoType covers the shape where
// `sensitive:` is the node's only key — a leaf whose type is left
// unconstrained. Membership in the compact-form keyword set is what steers
// this node to the leaf emitter; a node of unrecognised keys is read as an
// object scope instead, which would turn the marker into a property literally
// named `sensitive` and emit no marker at all.
func TestEmitSensitiveExtensionKeyword_BareLeafNoType(t *testing.T) {
	t.Parallel()

	out := mustCompile(t, "admin_password:\n  sensitive: true\n")
	node := out["properties"].(Schema)["admin_password"].(Schema)
	if got := node["x-aggrega-sensitive"]; got != true {
		t.Errorf("expected x-aggrega-sensitive=true on a type-less leaf; got %v in %v", got, node)
	}
	if _, misread := node["properties"]; misread {
		t.Errorf("the node was read as an object scope rather than a leaf: %v", node)
	}
}

// mustCompile compiles src or fails the test. Helper for test brevity.
func mustCompile(t *testing.T, src string) Schema {
	t.Helper()
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return out
}
