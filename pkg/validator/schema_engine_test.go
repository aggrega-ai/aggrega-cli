// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"testing"
)

func TestEvaluateSchema_ValidValues(t *testing.T) {
	schema := `{ port: int & >=1024, name: string }`
	values := map[string]any{
		"port": 8080,
		"name": "api",
	}
	errs := evaluateSchema(schema, values, "Component test:1.0.0")
	if len(errs) > 0 {
		t.Fatalf("expected no errors, got %d: %v", len(errs), errs[0].Error())
	}
}

func TestEvaluateSchema_InvalidType(t *testing.T) {
	schema := `{ port: int }`
	values := map[string]any{
		"port": "not-a-number",
	}
	errs := evaluateSchema(schema, values, "Component test:1.0.0")
	if len(errs) == 0 {
		t.Fatal("expected validation error for type mismatch")
	}
	for _, e := range errs {
		if !errors.Is(e, ErrSchemaValidation) {
			t.Errorf("expected ErrSchemaValidation sentinel, got %v", e.Err)
		}
	}
}

func TestEvaluateSchema_ConstraintViolation(t *testing.T) {
	schema := `{ port: int & >=1024 }`
	values := map[string]any{
		"port": 80,
	}
	errs := evaluateSchema(schema, values, "Component test:1.0.0")
	if len(errs) == 0 {
		t.Fatal("expected validation error for constraint violation")
	}
}

func TestEvaluateSchema_EmptySchema(t *testing.T) {
	errs := evaluateSchema("", map[string]any{"a": 1}, "Component test:1.0.0")
	if len(errs) != 0 {
		t.Fatalf("expected no errors for empty schema, got %d", len(errs))
	}
}

func TestEvaluateSchema_InvalidCUE(t *testing.T) {
	errs := evaluateSchema("{ this is not valid CUE !!!", map[string]any{}, "Component test:1.0.0")
	if len(errs) == 0 {
		t.Fatal("expected error for invalid CUE schema")
	}
}

func TestEvaluateSchema_NoRawCUE(t *testing.T) {
	schema := `{ port: int }`
	values := map[string]any{
		"port": "bad",
	}
	errs := evaluateSchema(schema, values, "Component test:1.0.0")
	for _, e := range errs {
		msg := e.Error()
		// Raw CUE errors typically contain ".cue:" file references
		if containsCUEInternals(msg) {
			t.Errorf("error message contains raw CUE internals: %s", msg)
		}
	}
}

func containsCUEInternals(msg string) bool {
	// Check for CUE source file references that should not be exposed
	for _, pattern := range []string{".cue:", "// ", "cuelang.org"} {
		if len(msg) > 0 {
			for i := 0; i <= len(msg)-len(pattern); i++ {
				if msg[i:i+len(pattern)] == pattern {
					return true
				}
			}
		}
	}
	return false
}
