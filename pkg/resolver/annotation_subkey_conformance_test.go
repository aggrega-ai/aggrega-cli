// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// subkeyFeature builds a catalog Feature whose spec.schema is the supplied
// compact-form YAML, for the annotation-binding subkey conformance tests.
func subkeyFeature(t *testing.T, schemaYAML string) *types.Feature {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(schemaYAML), &node); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	return &types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/monitoring", Version: "1.0.0"},
		Spec:     types.FeatureSpec{Schema: node},
	}
}

// TestSubkeyConformance_FullValidationNotTypeOnly pins Gap-3: an annotation
// binding subkey value is validated against the FULL JSON Schema at its path
// (enum, pattern, numeric range, nested shape) — not the prior type-only check.
// Each violating value here passes a type-only check (right Go type) but fails
// the declared constraint, so the new gate halts where the old one passed.
func TestSubkeyConformance_FullValidationNotTypeOnly(t *testing.T) {
	t.Parallel()
	const schema = `values:
  level: { type: string, enum: [info, warn, error] }
  port: { type: integer, minimum: 1, maximum: 65535 }
  name: { type: string, pattern: "^[a-z]+$" }
`
	feature := subkeyFeature(t, schema)
	cache := newFeatureSchemaCache(nil)

	cases := []struct {
		subkey  string
		value   any
		wantErr bool
	}{
		{"level", "info", false}, // conforms
		{"level", "bogus", true}, // enum violation (type-only would pass)
		{"port", 8080, false},    // conforms
		{"port", 0, true},        // below minimum (type-only would pass)
		{"port", 70000, true},    // above maximum
		{"name", "api", false},   // conforms
		{"name", "API", true},    // pattern violation (type-only would pass)
		{"missing", "x", true},   // undeclared subkey
	}
	for _, c := range cases {
		err := validateSubkeyValueAgainstFeatureSchema(cache, feature, "values", c.subkey, c.value)
		if c.wantErr && err == nil {
			t.Errorf("subkey %q=%v: expected a conformance halt; got nil", c.subkey, c.value)
		}
		if !c.wantErr && err != nil {
			t.Errorf("subkey %q=%v: expected pass; got %v", c.subkey, c.value, err)
		}
		if c.wantErr && err != nil && !strings.Contains(err.Error(), "OPS_ERROR") {
			t.Errorf("subkey %q=%v: halt should keep OPS_ERROR framing; got %v", c.subkey, c.value, err)
		}
	}
}

// TestSubkeyConformance_TerminalRefResolved pins that a terminal sub-schema
// expressed as a single-document $ref (previously soft-skipped by the type-only
// check, which read no `type`) is now resolved against the schema's $defs and
// validated in full.
func TestSubkeyConformance_TerminalRefResolved(t *testing.T) {
	t.Parallel()
	const schema = `$defs:
  Tier:
    type: string
    enum: [gold, silver]
values:
  tier: { $ref: "#/$defs/Tier" }
`
	feature := subkeyFeature(t, schema)
	cache := newFeatureSchemaCache(nil)

	if err := validateSubkeyValueAgainstFeatureSchema(cache, feature, "values", "tier", "gold"); err != nil {
		t.Errorf("conforming $ref value should pass; got %v", err)
	}
	if err := validateSubkeyValueAgainstFeatureSchema(cache, feature, "values", "tier", "bronze"); err == nil {
		t.Error("a terminal $ref enum violation must now halt (was soft-skipped under type-only)")
	}
}

// TestSubkeyConformance_NilFeatureSkips pins the defensive nil-feature guard.
func TestSubkeyConformance_NilFeatureSkips(t *testing.T) {
	t.Parallel()
	if err := validateSubkeyValueAgainstFeatureSchema(newFeatureSchemaCache(nil), nil, "values", "x", 1); err != nil {
		t.Errorf("nil feature must skip; got %v", err)
	}
}
