// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCompileFixtures exercises the Compile entry point against the
// testdata fixture/golden pairs. Each fixture is a compact-form YAML
// input plus a golden JSON Schema 2020-12 output; Compile must produce
// output that round-trips equal to the golden.
func TestCompileFixtures(t *testing.T) {
	t.Parallel()

	fixtures := []string{
		"minimal-uuid-generator",
		"midsize-pod-resources",
		"production-realistic-http-server",
	}

	for _, name := range fixtures {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inputPath := filepath.Join("testdata", name, "schema.compact.yaml")
			goldenPath := filepath.Join("testdata", name, "schema.expected.json")

			inputBytes, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatalf("read input %s: %v", inputPath, err)
			}

			goldenBytes, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v", goldenPath, err)
			}

			var input yaml.Node
			if err := yaml.Unmarshal(inputBytes, &input); err != nil {
				t.Fatalf("unmarshal input %s: %v", inputPath, err)
			}

			got, err := Compile(&input)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}

			var golden any
			if err := json.Unmarshal(goldenBytes, &golden); err != nil {
				t.Fatalf("unmarshal golden %s: %v", goldenPath, err)
			}

			// Normalise both sides for structural comparison: marshal to
			// JSON, then unmarshal back to a canonical any. JSON
			// marshalling sorts map keys alphabetically, which gives a
			// stable comparison surface.
			gotNormalised, err := normaliseViaJSON(got)
			if err != nil {
				t.Fatalf("normalise got: %v", err)
			}
			goldenNormalised, err := normaliseViaJSON(golden)
			if err != nil {
				t.Fatalf("normalise golden: %v", err)
			}

			if !reflect.DeepEqual(gotNormalised, goldenNormalised) {
				gotJSON, _ := json.MarshalIndent(gotNormalised, "", "  ")
				goldenJSON, _ := json.MarshalIndent(goldenNormalised, "", "  ")
				t.Fatalf("%s: schema mismatch\n--- got ---\n%s\n--- golden ---\n%s",
					name, gotJSON, goldenJSON)
			}
		})
	}
}

// TestCompileDeterminism verifies the deterministic-output invariant:
// running Compile twice on the same input produces byte-identical JSON
// Schema output.
func TestCompileDeterminism(t *testing.T) {
	t.Parallel()

	inputBytes, err := os.ReadFile(filepath.Join("testdata", "midsize-pod-resources", "schema.compact.yaml"))
	if err != nil {
		t.Fatalf("read input: %v", err)
	}

	var input1, input2 yaml.Node
	if err := yaml.Unmarshal(inputBytes, &input1); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	if err := yaml.Unmarshal(inputBytes, &input2); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}

	first, err := Compile(&input1)
	if err != nil {
		t.Fatalf("Compile first: %v", err)
	}
	second, err := Compile(&input2)
	if err != nil {
		t.Fatalf("Compile second: %v", err)
	}

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}

	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("non-deterministic output:\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
}

// TestCompileNilInput verifies that a nil input surfaces as
// ErrEscapeContentInvalid wrapped in CompileError.
func TestCompileNilInput(t *testing.T) {
	t.Parallel()

	_, err := Compile(nil)
	if err == nil {
		t.Fatal("expected error on nil input; got nil")
	}
	if !errors.Is(err, ErrEscapeContentInvalid) {
		t.Fatalf("expected ErrEscapeContentInvalid; got %v", err)
	}
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CompileError; got %T", err)
	}
}

// TestCompileUnknownKeyword verifies that a malformed compact-form
// schema with an unknown keyword surfaces as ErrUnknownKeyword wrapped
// in CompileError carrying the offending keyword.
func TestCompileUnknownKeyword(t *testing.T) {
	t.Parallel()

	src := `port: { type: integer, mn: 1 }`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrUnknownKeyword; got nil")
	}
	if !errors.Is(err, ErrUnknownKeyword) {
		t.Fatalf("expected ErrUnknownKeyword; got %v", err)
	}
	if !strings.Contains(err.Error(), "mn") {
		t.Fatalf("error should name the offending keyword `mn`; got %q", err)
	}
}

// TestCompileConstraintTypeMismatch verifies that a `min:` constraint
// applied to a `type: object` field surfaces as ErrConstraintTypeMismatch.
func TestCompileConstraintTypeMismatch(t *testing.T) {
	t.Parallel()

	src := `bad: { type: object, min: 5 }`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	_, err := Compile(&input)
	if err == nil {
		t.Fatal("expected ErrConstraintTypeMismatch; got nil")
	}
	if !errors.Is(err, ErrConstraintTypeMismatch) {
		t.Fatalf("expected ErrConstraintTypeMismatch; got %v", err)
	}
}

// TestCompileNodeUnsupportedKind verifies compileNode's defensive
// branch for YAML node kinds outside the documented set. Reaches the
// default arm of the dispatch switch by passing a synthetic node.
func TestCompileNodeUnsupportedKind(t *testing.T) {
	t.Parallel()

	bogus := &yaml.Node{Kind: yaml.Kind(99), Line: 7}
	_, err := compileNode(bogus, "synthetic", nil)
	if err == nil {
		t.Fatal("expected error for unsupported node kind; got nil")
	}
	if !errors.Is(err, ErrEscapeContentInvalid) {
		t.Errorf("expected ErrEscapeContentInvalid; got %v", err)
	}
}

// TestCompileNodeNilSilent verifies compileNode tolerates a nil node
// (returns nil, nil) rather than panicking. Defensive contract for
// helpers that may pass nil through.
func TestCompileNodeNilSilent(t *testing.T) {
	t.Parallel()

	v, err := compileNode(nil, "", nil)
	if err != nil {
		t.Errorf("nil node should not error; got %v", err)
	}
	if v != nil {
		t.Errorf("nil node should return nil; got %v", v)
	}
}

// TestCompileAliasNilTarget verifies an alias node with a nil target
// returns (nil, nil) safely — the defensive branch in compileNode's
// AliasNode case.
func TestCompileAliasNilTarget(t *testing.T) {
	t.Parallel()

	alias := &yaml.Node{Kind: yaml.AliasNode, Alias: nil}
	v, err := compileNode(alias, "", nil)
	if err != nil {
		t.Errorf("alias-with-nil-target should not error; got %v", err)
	}
	if v != nil {
		t.Errorf("alias-with-nil-target should return nil; got %v", v)
	}
}

// TestEmitSchemaMapNonMapping verifies emitSchemaMap's defensive
// branch for nil and non-mapping inputs returns an empty schema map
// without error.
func TestEmitSchemaMapNonMapping(t *testing.T) {
	t.Parallel()

	out, err := emitSchemaMap(nil, "", nil)
	if err != nil {
		t.Errorf("nil node should not error; got %v", err)
	}
	if len(out) != 0 {
		t.Errorf("nil node should produce empty map; got %v", out)
	}
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Value: "x"}
	out, err = emitSchemaMap(scalar, "", nil)
	if err != nil {
		t.Errorf("non-mapping should not error; got %v", err)
	}
	if len(out) != 0 {
		t.Errorf("non-mapping should produce empty map; got %v", out)
	}
}

// TestHelperNilNodeGuards exercises the defensive nil/non-mapping
// guards in mappingValue, mappingKeyOrder, nodeLine, decodeScalar,
// and isRequiredLeaf. Internal-API tests; document the contract that
// these helpers tolerate nil rather than panicking.
func TestHelperNilNodeGuards(t *testing.T) {
	t.Parallel()

	if v := mappingValue(nil, "any"); v != nil {
		t.Errorf("mappingValue(nil, ...) should return nil; got %v", v)
	}
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Value: "x"}
	if v := mappingValue(scalar, "any"); v != nil {
		t.Errorf("mappingValue(non-mapping, ...) should return nil; got %v", v)
	}
	if keys := mappingKeyOrder(nil); keys != nil {
		t.Errorf("mappingKeyOrder(nil) should return nil; got %v", keys)
	}
	if line := nodeLine(nil); line != 0 {
		t.Errorf("nodeLine(nil) should return 0; got %d", line)
	}
	if v := decodeScalar(nil); v != nil {
		t.Errorf("decodeScalar(nil) should return nil; got %v", v)
	}
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	if v := decodeScalar(mapping); v != nil {
		t.Errorf("decodeScalar(non-scalar) should return nil; got %v", v)
	}
	if isRequiredLeaf(nil) {
		t.Errorf("isRequiredLeaf(nil) should return false")
	}
	if isRequiredLeaf(scalar) {
		t.Errorf("isRequiredLeaf(non-mapping) should return false")
	}
}

// TestCompileLargeSequence exercises emitSequence's indexSegment path
// for indices >= 2 (intToString is exercised for multi-digit values).
func TestCompileLargeSequence(t *testing.T) {
	t.Parallel()

	src := `
listed:
  type: array
  items: { type: integer }
  enum: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	listed := out["properties"].(Schema)["listed"].(Schema)
	enum, ok := listed["enum"].([]any)
	if !ok {
		t.Fatalf("enum should be a slice; got %T", listed["enum"])
	}
	if len(enum) != 11 {
		t.Errorf("expected 11 enum entries; got %d", len(enum))
	}
}

// TestCompileYAMLAlias exercises compileNode's alias-node path. YAML
// anchors + aliases produce alias nodes that dereference to their
// target during compile.
func TestCompileYAMLAlias(t *testing.T) {
	t.Parallel()

	src := `
$defs:
  Common: &common
    type: string
    pattern: "^[a-z]+$"
field1: *common
field2: *common
`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	props := out["properties"].(Schema)
	f1, ok := props["field1"].(Schema)
	if !ok {
		t.Fatalf("field1 should be Schema after alias deref; got %T", props["field1"])
	}
	if f1["type"] != "string" {
		t.Errorf("aliased field1.type=string expected; got %v", f1["type"])
	}
	f2, ok := props["field2"].(Schema)
	if !ok {
		t.Fatalf("field2 should be Schema after alias deref; got %T", props["field2"])
	}
	if f2["pattern"] != "^[a-z]+$" {
		t.Errorf("aliased field2.pattern expected; got %v", f2["pattern"])
	}
}

// TestCompileEmptyMapping exercises emitImplicitObject on an empty
// mapping (no properties; required[] empty; rootMeta empty).
func TestCompileEmptyMapping(t *testing.T) {
	t.Parallel()

	src := `{}`
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out["type"] != "object" {
		t.Errorf("empty mapping should compile to type=object; got %v", out)
	}
	props, ok := out["properties"].(Schema)
	if !ok || len(props) != 0 {
		t.Errorf("empty mapping should have empty properties; got %v", out["properties"])
	}
}

// TestCompileRequiredFalseNotAccumulated verifies that `required: false`
// (or any non-true value) on a leaf does NOT accumulate to the parent's
// required array. Exercises isRequiredLeaf's non-"true" branch.
func TestCompileRequiredFalseNotAccumulated(t *testing.T) {
	t.Parallel()

	src := `
field1: { type: string, required: true }
field2: { type: string, required: false }
field3: { type: string }
`
	out := mustCompile(t, src)
	required, ok := out["required"].([]string)
	if !ok {
		t.Fatalf("required should be []string; got %T", out["required"])
	}
	if len(required) != 1 || required[0] != "field1" {
		t.Errorf("only field1 (required: true) should accumulate; got %v", required)
	}
}

// normaliseViaJSON round-trips a value through JSON encode/decode to
// produce a canonical structural representation suitable for
// reflect.DeepEqual comparison. JSON's map iteration order is
// alphabetical-by-key; the round-trip elides Go's map iteration
// nondeterminism for comparison purposes only.
func normaliseViaJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
