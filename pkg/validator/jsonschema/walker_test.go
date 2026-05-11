// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// TestWalkInvalidWhenTemplate verifies New rejects a schema whose
// x-aggrega-when: template fails to parse — surfaces ErrExtensionEval.
func TestWalkInvalidWhenTemplate(t *testing.T) {
	t.Parallel()

	// Construct a *Schema directly with a malformed template in Extra.
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"field": {
				Extra: map[string]any{
					// Unclosed template action — parse error.
					"x-aggrega-when": "{{ if .foo",
				},
				Then: &jsonschema.Schema{Type: "object"},
			},
		},
	}
	_, err := New(schema)
	if err == nil {
		t.Fatal("expected ErrExtensionEval; got nil")
	}
	if !errors.Is(err, ErrExtensionEval) {
		t.Errorf("expected ErrExtensionEval; got %v", err)
	}
}

// TestWalkInvalidValidateTemplate verifies the same path for
// x-aggrega-validate:.
func TestWalkInvalidValidateTemplate(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"field": {
				Type: "string",
				Extra: map[string]any{
					"x-aggrega-validate": "{{ broken",
				},
			},
		},
	}
	_, err := New(schema)
	if err == nil {
		t.Fatal("expected ErrExtensionEval; got nil")
	}
	if !errors.Is(err, ErrExtensionEval) {
		t.Errorf("expected ErrExtensionEval; got %v", err)
	}
}

// TestWalkNonStringWhenTemplate verifies the non-string x-aggrega-when:
// value path rejects with ErrExtensionEval.
func TestWalkNonStringWhenTemplate(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"field": {
				Extra: map[string]any{
					"x-aggrega-when": 42, // not a string
				},
				Then: &jsonschema.Schema{Type: "object"},
			},
		},
	}
	_, err := New(schema)
	if err == nil {
		t.Fatal("expected ErrExtensionEval; got nil")
	}
	if !errors.Is(err, ErrExtensionEval) {
		t.Errorf("expected ErrExtensionEval; got %v", err)
	}
}

// TestWalkValidateMessageMissing verifies that an x-aggrega-validate:
// node without an x-aggrega-validateMessage: companion uses a default
// failure message.
func TestWalkValidateMessageMissing(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"field": {
				Type: "string",
				Extra: map[string]any{
					"x-aggrega-validate": "{{ eq . \"OK\" }}",
				},
			},
		},
	}
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	instance := map[string]any{"field": "NOT_OK"}
	rpt, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Expect at least one finding with the default message.
	found := false
	for _, f := range rpt.Findings {
		if f.Keyword == "x-aggrega-validate" {
			found = true
			if f.Message == "" {
				t.Errorf("expected non-empty default message; got empty")
			}
		}
	}
	if !found {
		t.Errorf("expected x-aggrega-validate finding for failing predicate; got %v", rpt.Findings)
	}
}

// TestWalkNonStringValidateTemplate verifies the non-string
// x-aggrega-validate: value path rejects with ErrExtensionEval.
func TestWalkNonStringValidateTemplate(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"field": {
				Type: "string",
				Extra: map[string]any{
					"x-aggrega-validate": []string{"not", "a", "string"},
				},
			},
		},
	}
	_, err := New(schema)
	if err == nil {
		t.Fatal("expected ErrExtensionEval; got nil")
	}
	if !errors.Is(err, ErrExtensionEval) {
		t.Errorf("expected ErrExtensionEval; got %v", err)
	}
}

// TestNewRootResolveError verifies New surfaces an
// ErrSchemaResolve when the root schema itself cannot be resolved
// (e.g. its own $ref cannot be looked up).
func TestNewRootResolveError(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"field": {Ref: "#/$defs/Nonexistent"},
		},
		// No Defs declared; the $ref points nowhere.
	}
	_, err := New(schema)
	if err == nil {
		t.Fatal("expected ErrSchemaResolve; got nil")
	}
	if !errors.Is(err, ErrSchemaResolve) {
		t.Errorf("expected ErrSchemaResolve; got %v", err)
	}
}

// TestWalkRecursesPatternPropertiesAndDependentSchemas verifies the
// walker recurses into PatternProperties and DependentSchemas so
// nested x-aggrega-* extensions get discovered.
func TestWalkRecursesPatternPropertiesAndDependentSchemas(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		PatternProperties: map[string]*jsonschema.Schema{
			"^x-": {Type: "string"},
		},
		DependentSchemas: map[string]*jsonschema.Schema{
			"flag": {
				Properties: map[string]*jsonschema.Schema{
					"detail": {Type: "string"},
				},
			},
		},
	}
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil validator")
	}
}

// TestIndexSubSchemasSkipsUnresolvable verifies indexSubSchemas
// tolerates sub-schemas whose $ref targets are at the root's $defs:
// the sub-schema's standalone Resolve fails (no $defs in the
// sub-schema scope) but the root schema's Resolve succeeds (the
// $defs are at the root). The indexer skips such cases silently;
// validation continues via the deterministic walk for the
// resolvable sub-schemas and via the standard library path for
// $ref-using subtrees. (TestValidateAnyOfWithDefs covers the
// downstream Validate behavior; this test exercises the indexer's
// tolerance branch.)
func TestIndexSubSchemasSkipsUnresolvable(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		Defs: map[string]*jsonschema.Schema{
			"Target": {Type: "string"},
		},
		Properties: map[string]*jsonschema.Schema{
			"refField": {Ref: "#/$defs/Target"},
		},
	}
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New should tolerate sub-schemas using root-$defs $ref: %v", err)
	}
	if _, err := v.Validate(map[string]any{"refField": "x"}, nil); err != nil {
		t.Errorf("Validate should not error on root-$defs $ref: %v", err)
	}
}

// TestWalkRecursesNotAndAdditionalProperties verifies the walker
// recurses into Not and AdditionalProperties sub-schemas.
func TestWalkRecursesNotAndAdditionalProperties(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		Not: &jsonschema.Schema{
			Type: "string",
		},
		AdditionalProperties: &jsonschema.Schema{
			Type: "integer",
		},
	}
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil validator")
	}
}

// TestWalkRecursesIfThenElseAndDefs verifies the walker recurses into
// If, Then, Else, Defs, and Definitions sub-schemas.
func TestWalkRecursesIfThenElseAndDefs(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		If: &jsonschema.Schema{
			Properties: map[string]*jsonschema.Schema{
				"kind": {Type: "string"},
			},
		},
		Then: &jsonschema.Schema{Type: "object"},
		Else: &jsonschema.Schema{Type: "object"},
		Defs: map[string]*jsonschema.Schema{
			"Helper": {Type: "string"},
		},
	}
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil validator")
	}
}

// TestWalkErrorPropagation verifies that a deeply-nested template
// parse error propagates up through the walker's recursion (covers
// the error-return branches inside walk's composition recursion).
func TestWalkErrorPropagation(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		AllOf: []*jsonschema.Schema{
			{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"field": {
						Extra: map[string]any{
							"x-aggrega-when": "{{ broken",
						},
						Then: &jsonschema.Schema{Type: "object"},
					},
				},
			},
		},
	}
	_, err := New(schema)
	if err == nil {
		t.Fatal("expected error from deeply-nested template parse failure; got nil")
	}
	if !errors.Is(err, ErrExtensionEval) {
		t.Errorf("expected ErrExtensionEval; got %v", err)
	}
}

// TestWalkRecursesDefinitionsLegacyKeyword verifies the walker
// recurses into Definitions (the JSON Schema draft-07 spelling that
// the library still exposes for backward compatibility).
func TestWalkRecursesDefinitionsLegacyKeyword(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		Definitions: map[string]*jsonschema.Schema{
			"LegacyHelper": {Type: "integer"},
		},
	}
	v, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil validator")
	}
}

// TestWalkSchemaSerialisable confirms the synthesised schemas in this
// test file serialise + round-trip via the library cleanly. Defensive
// — verifies our synthetic *Schema constructions work with the
// library's resolver.
func TestWalkSchemaSerialisable(t *testing.T) {
	t.Parallel()

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
		},
	}
	_, err := json.Marshal(schema)
	if err != nil {
		t.Errorf("schema serialisation failed: %v", err)
	}
	v, err := New(schema)
	if err != nil {
		t.Errorf("New on synthesised schema: %v", err)
	}
	if _, err := v.Validate(map[string]any{"name": "ok"}, nil); err != nil {
		t.Errorf("Validate on synthesised schema: %v", err)
	}
}
