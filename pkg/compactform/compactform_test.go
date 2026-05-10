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
