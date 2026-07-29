// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// parseSchemaBody hydrates a YAML source into the yaml.Node shape that
// ValidateSchemaPillars receives from the catalog loader — the inner
// content of a `spec.schema:` field, not a top-level DocumentNode. The
// helper unwraps the DocumentNode that yaml.Unmarshal produces so the
// test input mirrors what the unmarshaller stores on FeatureSpec.Schema /
// ComponentSpec.Schema in production.
func parseSchemaBody(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("test setup: yaml.Unmarshal: %v", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return &doc
}

// TestValidateSchemaPillars_NilNode covers the nil-pointer boundary —
// the function must not panic when called with a nil node.
func TestValidateSchemaPillars_NilNode(t *testing.T) {
	if err := ValidateSchemaPillars("Component", "core/x", "fixtures/x.yaml", "", nil, nil); err != nil {
		t.Fatalf("nil node should pass; got %v", err)
	}
}

// TestValidateSchemaPillars_SchemaLess covers the zero-value yaml.Node
// case — an entity that omits spec.schema entirely. Legitimate authoring;
// the validator must skip without error.
func TestValidateSchemaPillars_SchemaLess(t *testing.T) {
	var n yaml.Node // Kind == 0
	if err := ValidateSchemaPillars("Component", "core/x", "fixtures/x.yaml", "", &n, nil); err != nil {
		t.Fatalf("schema-less entity should pass; got %v", err)
	}
}

// TestValidateSchemaPillars_RejectsBlockScalarString rejects an entity whose
// spec.schema is authored as a YAML block-scalar string rather than a
// mapping of pillars. The error message must point at the file, the
// canonical pillar set, and the compact-form authoring doc so a fresh
// author has a direction to investigate.
func TestValidateSchemaPillars_RejectsBlockScalarString(t *testing.T) {
	scalar := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: "values: { port: int, default: 8080 }",
		Line:  10,
	}
	err := ValidateSchemaPillars("Component", "core/scalar-schema", "fixtures/scalar.yaml", "", scalar, nil)
	if err == nil {
		t.Fatal("expected rejection for scalar schema; got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"core/scalar-schema",
		"YAML map of pillars",
		"values / infra / provides",
		"compact-form-authoring.md",
		"fixtures/scalar.yaml",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q; got: %s", want, msg)
		}
	}
}

// TestValidateSchemaPillars_RejectsUnknownPillarKey rejects a top-level
// key that isn't one of {values, infra, provides}. The error must name
// the offending key and enumerate the allowed pillar set.
func TestValidateSchemaPillars_RejectsUnknownPillarKey(t *testing.T) {
	node := parseSchemaBody(t, `parameter:
  scrapePath: { type: string, default: "/metrics" }
`)
	err := ValidateSchemaPillars("Feature", "core/prom-exporter", "fixtures/prom.yaml", "", node, nil)
	if err == nil {
		t.Fatal("expected rejection for unknown pillar key 'parameter'; got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"core/prom-exporter",
		`"parameter"`,
		"allowed: values, infra, provides",
		"fixtures/prom.yaml",
		"data-pillars.md",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q; got: %s", want, msg)
		}
	}
}

// TestValidateSchemaPillars_RejectsPropertiesAtTopLevel catches the
// authoring mistake where someone writes JSON Schema YAML directly under
// spec.schema without wrapping it in a pillar. `properties:` at the top
// level is invalid — pillars are required.
func TestValidateSchemaPillars_RejectsPropertiesAtTopLevel(t *testing.T) {
	node := parseSchemaBody(t, `properties:
  port: { type: integer }
`)
	err := ValidateSchemaPillars("Component", "core/wrong", "fixtures/wrong.yaml", "", node, nil)
	if err == nil {
		t.Fatal("expected rejection for 'properties' at top level; got nil")
	}
	if !strings.Contains(err.Error(), `"properties"`) {
		t.Errorf("error should name the offending key; got: %v", err)
	}
}

// TestValidateSchemaPillars_RejectsCompactformError surfaces a pillar
// body that compactform.Compile rejects. We use `min:` on `type: object`
// to drive the ErrConstraintTypeMismatch path. Bodies whose syntax does
// not surface as a compactform error at this layer are caught downstream
// by JSON Schema 2020-12 metaschema validation when the catalog consumer
// eventually parses them; load-time coverage here pins the wiring
// between the validator and compactform so the layer stays connected.
func TestValidateSchemaPillars_RejectsCompactformError(t *testing.T) {
	node := parseSchemaBody(t, `values:
  port: { type: object, min: 5 }
`)
	err := ValidateSchemaPillars("Component", "core/bad-body", "fixtures/bad-body.yaml", "", node, nil)
	if err == nil {
		t.Fatal("expected rejection for 'min' on type: object; got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"core/bad-body",
		"spec.schema.values",
		"fixtures/bad-body.yaml",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q; got: %s", want, msg)
		}
	}
}

// TestValidateSchemaPillars_AcceptsInferredForm covers the canonical
// authoring pattern: pillar keys, inferred-form property declarations,
// no `properties:` wrapper. This is what migrated fixtures use.
func TestValidateSchemaPillars_AcceptsInferredForm(t *testing.T) {
	node := parseSchemaBody(t, `values:
  port: { type: integer, default: 8080 }
provides:
  host: { type: string, required: true }
  service_port: { type: string, required: true }
`)
	if err := ValidateSchemaPillars("Component", "core/api", "fixtures/api.yaml", "", node, nil); err != nil {
		t.Errorf("valid inferred-form schema should pass; got: %v", err)
	}
}

// TestValidateSchemaPillars_AcceptsExplicitForm covers the alternative
// authoring path — explicit `type: object` + `properties:` enumeration.
// Valid per vocabulary/compact-form-authoring.md; the validator accepts
// both forms.
func TestValidateSchemaPillars_AcceptsExplicitForm(t *testing.T) {
	node := parseSchemaBody(t, `values:
  type: object
  properties:
    port: { type: integer, default: 8080 }
  additionalProperties: false
`)
	if err := ValidateSchemaPillars("Feature", "core/x", "fixtures/x.yaml", "", node, nil); err != nil {
		t.Errorf("valid explicit-form schema should pass; got: %v", err)
	}
}

// TestValidateSchemaPillars_AcceptsAllThreePillars confirms that the
// canonical pillar keys (values, infra, provides) are accepted
// simultaneously.
func TestValidateSchemaPillars_AcceptsAllThreePillars(t *testing.T) {
	node := parseSchemaBody(t, `values:
  database: { type: string, default: "app" }
infra:
  replicas: { type: integer, default: 1 }
provides:
  host: { type: string, required: true }
`)
	if err := ValidateSchemaPillars("Component", "core/db", "fixtures/db.yaml", "", node, nil); err != nil {
		t.Errorf("three-pillar schema should pass; got: %v", err)
	}
}

// TestValidateSchemaPillars_AcceptsSensitiveLeafInEveryPillar pins that a
// leaf declared `sensitive: true` clears the catalog-load gate on all three
// free-form pillars. Without this the keyword would compile in isolation and
// still be unauthorable, because this gate compiles each pillar body and a
// key it does not recognise halts the load.
func TestValidateSchemaPillars_AcceptsSensitiveLeafInEveryPillar(t *testing.T) {
	node := parseSchemaBody(t, `values:
  admin_password: { type: string, sensitive: true }
infra:
  backup_token: { type: string, sensitive: true }
provides:
  dsn: { type: string, sensitive: true }
  host: { type: string }
`)
	if err := ValidateSchemaPillars("Component", "core/db", "fixtures/db.yaml", "", node, nil); err != nil {
		t.Errorf("a leaf declared sensitive should pass the pillar gate; got: %v", err)
	}
}

// TestValidateSchemaPillars_RejectsSequenceAtRoot rejects a YAML sequence
// at spec.schema. Mappings are required; sequences (and any other non-
// mapping node kind) must surface a shape-check error naming the actual
// node kind so the author knows what to change.
func TestValidateSchemaPillars_RejectsSequenceAtRoot(t *testing.T) {
	node := parseSchemaBody(t, `- values
- provides
`)
	err := ValidateSchemaPillars("Component", "core/x", "fixtures/x.yaml", "", node, nil)
	if err == nil {
		t.Fatal("expected rejection for sequence at root; got nil")
	}
	if !strings.Contains(err.Error(), "YAML map of pillars") {
		t.Errorf("error should call out the map requirement; got: %v", err)
	}
	if !strings.Contains(err.Error(), "sequence") {
		t.Errorf("error should name the actual node kind 'sequence'; got: %v", err)
	}
}
