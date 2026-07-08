// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestAssertionsField_ComponentRoundTrip pins author-order preservation of
// the Assertions OrderedMap[string] field across Component decode + encode.
// Three named rules are decoded; key iteration order matches author order;
// round-trip preserves all three with their template bodies intact.
func TestAssertionsField_ComponentRoundTrip(t *testing.T) {
	t.Parallel()
	src := `extends: ""
assertions:
  rule-A: 'template-body-A'
  rule-B: 'template-body-B'
  rule-C: 'template-body-C'
`
	var spec ComponentSpec
	if err := yaml.Unmarshal([]byte(src), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.Assertions.Len() != 3 {
		t.Fatalf("expected 3 assertions; got %d", spec.Assertions.Len())
	}
	keys := spec.Assertions.Keys()
	want := []string{"rule-A", "rule-B", "rule-C"}
	for i, k := range want {
		if keys[i] != k {
			t.Errorf("key[%d] = %q; want %q", i, keys[i], k)
		}
	}
	bodyA, _ := spec.Assertions.Get("rule-A")
	if bodyA != "template-body-A" {
		t.Errorf("rule-A body = %q", bodyA)
	}

	out, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	outStr := string(out)
	for _, want := range []string{"rule-A", "rule-B", "rule-C", "template-body-A", "template-body-B", "template-body-C"} {
		if !strings.Contains(outStr, want) {
			t.Errorf("round-trip lost %q; got:\n%s", want, outStr)
		}
	}
}

// TestAssertionsField_FeatureRoundTrip pins the field on FeatureSpec.
func TestAssertionsField_FeatureRoundTrip(t *testing.T) {
	t.Parallel()
	src := `assertions:
  rule-1: 'body-1'
`
	var spec FeatureSpec
	if err := yaml.Unmarshal([]byte(src), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.Assertions.Len() != 1 {
		t.Errorf("expected 1 assertion; got %d", spec.Assertions.Len())
	}
}

// TestAssertionsField_RecipeRoundTrip pins the field on RecipeSpec.
func TestAssertionsField_RecipeRoundTrip(t *testing.T) {
	t.Parallel()
	src := `target: helm
implements: core/db:^1.0.0
assertions:
  rule-1: 'body-1'
  rule-2: 'body-2'
`
	var spec RecipeSpec
	if err := yaml.Unmarshal([]byte(src), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.Assertions.Len() != 2 {
		t.Errorf("expected 2 assertions; got %d", spec.Assertions.Len())
	}
}

// TestAssertionsField_FeatureRecipeRoundTrip pins the field on
// FeatureRecipeSpec.
func TestAssertionsField_FeatureRecipeRoundTrip(t *testing.T) {
	t.Parallel()
	src := `target: helm
implements: ops/audit:^1.0.0
assertions:
  rule-1: 'body-1'
`
	var spec FeatureRecipeSpec
	if err := yaml.Unmarshal([]byte(src), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.Assertions.Len() != 1 {
		t.Errorf("expected 1 assertion; got %d", spec.Assertions.Len())
	}
}

// TestAssertionsField_OmitemptyElidesEmpty pins that an empty Assertions
// field elides from marshaled output.
func TestAssertionsField_OmitemptyElidesEmpty(t *testing.T) {
	t.Parallel()
	spec := ComponentSpec{}
	out, err := yaml.Marshal(&spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "assertions") {
		t.Errorf("expected assertions elided; got:\n%s", out)
	}
}
