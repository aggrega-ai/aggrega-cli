// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestParametersBlock_RoundTripInline pins mode 1: an inline parameter
// object decodes into Inline and re-emits as a YAML map.
func TestParametersBlock_RoundTripInline(t *testing.T) {
	t.Parallel()
	in := `parameters:
  api_port: "8080"
  db:
    host: release-db.internal
`
	type wrap struct {
		Parameters ParametersBlock `yaml:"parameters"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if w.Parameters.Inline.Len() == 0 {
		t.Fatal("expected Inline populated")
	}
	if w.Parameters.Import != "" {
		t.Errorf("expected Import empty; got %q", w.Parameters.Import)
	}
	port, ok := w.Parameters.Inline.Get("api_port")
	if !ok || port != "8080" {
		t.Errorf("expected api_port=8080; got %v", port)
	}
}

// TestParametersBlock_RoundTripImport pins mode 2.
func TestParametersBlock_RoundTripImport(t *testing.T) {
	t.Parallel()
	in := `parameters:
  $import: ./prod-values.yaml
`
	type wrap struct {
		Parameters ParametersBlock `yaml:"parameters"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if w.Parameters.Import != "./prod-values.yaml" {
		t.Errorf("expected Import = './prod-values.yaml'; got %q", w.Parameters.Import)
	}
	if w.Parameters.Inline.Len() != 0 {
		t.Errorf("expected Inline empty in import mode; got %d entries", w.Parameters.Inline.Len())
	}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "$import: ./prod-values.yaml") {
		t.Errorf("round-trip lost $import; got:\n%s", out)
	}
}

// TestParametersBlock_RoundTripNestedStructures pins inline-mode
// fidelity for nested maps and lists (per the operator's plan-gate
// refinement: ParametersBlock's Inline carries OrderedMap[any], so
// any-typed nested structures need explicit coverage).
func TestParametersBlock_RoundTripNestedStructures(t *testing.T) {
	t.Parallel()
	in := `parameters:
  primitives:
    str: "hello"
    int_val: 42
    bool_val: true
  list:
    - first
    - second
    - third
  nested:
    inner:
      key: value
`
	type wrap struct {
		Parameters ParametersBlock `yaml:"parameters"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(in), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(&w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	outStr := string(out)
	for _, frag := range []string{"primitives", "int_val", "list", "first", "third", "nested", "inner"} {
		if !strings.Contains(outStr, frag) {
			t.Errorf("round-trip lost fragment %q; output:\n%s", frag, outStr)
		}
	}
}

// TestParametersBlock_RejectsImportWithExtraKeys pins the
// single-source-only error path.
func TestParametersBlock_RejectsImportWithExtraKeys(t *testing.T) {
	t.Parallel()
	in := `parameters:
  $import: ./prod-values.yaml
  inline_key: value
`
	type wrap struct {
		Parameters ParametersBlock `yaml:"parameters"`
	}
	var w wrap
	err := yaml.Unmarshal([]byte(in), &w)
	if err == nil {
		t.Fatal("expected error: $import: must be the only key")
	}
	if !errors.Is(err, ErrParametersShape()) {
		t.Errorf("expected ErrParametersShape; got %v", err)
	}
}

// TestParametersBlock_IsEmpty_DistinguishesZeroFromExplicitEmptyMap
// pins the IsEmpty helper boundary: a zero-value ParametersBlock and
// a ParametersBlock with an explicitly-empty Inline OrderedMap both
// report IsEmpty() == true. The semantic is "no parameters declared OR
// declared as empty"; consumers don't need to discriminate.
func TestParametersBlock_IsEmpty_DistinguishesZeroFromExplicitEmptyMap(t *testing.T) {
	t.Parallel()
	var zero ParametersBlock
	if !zero.IsEmpty() {
		t.Error("zero-value ParametersBlock should report IsEmpty() == true")
	}
	explicitEmpty := ParametersBlock{Inline: OrderedMap[any]{}}
	if !explicitEmpty.IsEmpty() {
		t.Error("explicitly-empty Inline OrderedMap should report IsEmpty() == true")
	}
	withInline := ParametersBlock{}
	withInline.Inline.Set("key", "value")
	if withInline.IsEmpty() {
		t.Error("populated Inline should report IsEmpty() == false")
	}
	withImport := ParametersBlock{Import: "./file.yaml"}
	if withImport.IsEmpty() {
		t.Error("populated Import should report IsEmpty() == false")
	}
}
