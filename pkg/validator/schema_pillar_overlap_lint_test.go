// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Schema-pillar-overlap lint tests. Each test supplies an entity
// schema (a yaml.Node parsed from inline YAML in compact form or JSON
// Schema YAML) and asserts the lint emits the expected number of
// warnings with the expected paths.

func parseSchema(t *testing.T, src string) yaml.Node {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("schema unmarshal: %v", err)
	}
	// Unwrap the document node so the lint receives the mapping
	// node directly (matches the shape entity loaders place on
	// Component/Feature.Spec.Schema).
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return *node.Content[0]
	}
	return node
}

func TestSchemaPillarOverlap_NoOverlap_NoWarning(t *testing.T) {
	t.Parallel()
	// Disjoint property sets across the two pillars; the lint stays
	// silent.
	schema := parseSchema(t, `
values:
  pool:
    type: string
infra:
  upstream:
    type: string
`)
	warnings := ValidateSchemaPillarOverlap("Feature", "core/test", "test.yaml", schema)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings; got %d: %v", len(warnings), warnings)
	}
}

func TestSchemaPillarOverlap_TopLevelOverlap_OneWarning(t *testing.T) {
	t.Parallel()
	// Both pillars declare a property named "port" at the root.
	schema := parseSchema(t, `
values:
  port:
    type: integer
infra:
  port:
    type: integer
`)
	warnings := ValidateSchemaPillarOverlap("Feature", "core/test", "test.yaml", schema)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning; got %d: %v", len(warnings), warnings)
	}
	for _, want := range []string{"Feature", "core/test", "test.yaml", "port", "path: port"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning missing %q: %s", want, warnings[0])
		}
	}
}

func TestSchemaPillarOverlap_NestedOverlap_OneWarning(t *testing.T) {
	t.Parallel()
	// Overlap surfaces only at depth 2 — both pillars declare
	// ingress.port but no top-level overlap.
	schema := parseSchema(t, `
values:
  ingress:
    host:
      type: string
    port:
      type: integer
infra:
  ingress:
    port:
      type: integer
`)
	warnings := ValidateSchemaPillarOverlap("Component", "core/api-server", "api-server.yaml", schema)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 nested warning; got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "path: ingress.port") {
		t.Errorf("warning should name the nested path 'ingress.port': %s", warnings[0])
	}
}

func TestSchemaPillarOverlap_MultipleOverlapsAtSameDepth_PerOverlap(t *testing.T) {
	t.Parallel()
	// Three same-name overlaps at the root; the lint emits three
	// warnings sorted by property name for deterministic output.
	schema := parseSchema(t, `
values:
  charlie:
    type: string
  alpha:
    type: string
  bravo:
    type: string
infra:
  alpha:
    type: string
  bravo:
    type: string
  charlie:
    type: string
`)
	warnings := ValidateSchemaPillarOverlap("Feature", "core/test", "test.yaml", schema)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings (one per overlap); got %d: %v", len(warnings), warnings)
	}
	// Sorted order: alpha, bravo, charlie.
	for i, want := range []string{"alpha", "bravo", "charlie"} {
		wantSubstring := "path: " + want
		if !strings.Contains(warnings[i], wantSubstring) {
			t.Errorf("warnings[%d] = %q; want it to contain %q", i, warnings[i], wantSubstring)
		}
	}
}

func TestSchemaPillarOverlap_SchemalessEntity_NoCrash(t *testing.T) {
	t.Parallel()
	// Schema with only one pillar declared — no overlap possible;
	// the lint returns nil silently.
	schema := parseSchema(t, `
values:
  pool:
    type: string
`)
	warnings := ValidateSchemaPillarOverlap("Feature", "core/test", "test.yaml", schema)
	if len(warnings) != 0 {
		t.Errorf("entity with only one pillar should produce no warnings; got: %v", warnings)
	}

	// Schema with no pillars at all — also no overlap; no crash.
	emptySchema := parseSchema(t, `description: schema with no pillars`)
	warnings = ValidateSchemaPillarOverlap("Feature", "core/empty", "empty.yaml", emptySchema)
	if len(warnings) != 0 {
		t.Errorf("entity with no pillars should produce no warnings; got: %v", warnings)
	}
}

func TestSchemaPillarOverlap_EscapeHatch_TerminatesBranch(t *testing.T) {
	t.Parallel()
	// A patternProperties node at the values root escapes the
	// recursion — the walker cannot statically prove what names the
	// pattern matches, so the lint stays silent for that branch
	// rather than risking a false-positive halt.
	schema := parseSchema(t, `
$jsonschema:
  type: object
  properties:
    values:
      type: object
      patternProperties:
        "^.*$":
          type: string
    infra:
      type: object
      properties:
        upstream:
          type: string
`)
	warnings := ValidateSchemaPillarOverlap("Feature", "core/dynamic", "dynamic.yaml", schema)
	if len(warnings) != 0 {
		t.Errorf("escape-hatch branch should produce no warnings; got: %v", warnings)
	}
}
