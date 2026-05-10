// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePillar_ValidValues(t *testing.T) {
	t.Parallel()
	schema := `values: { port?: int & >=1024 }
infra: { replicas?: int & >0 }`
	errs := ValidatePillar(schema, "values", map[string]any{"port": 8080}, "Component api:1.0.0")
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d: %v", len(errs), errs[0].Error())
	}
}

func TestValidatePillar_InvalidValues(t *testing.T) {
	t.Parallel()
	schema := `values: { port: int & >=1024 }`
	errs := ValidatePillar(schema, "values", map[string]any{"port": 80}, "Component api:1.0.0")
	if len(errs) == 0 {
		t.Fatal("expected constraint violation error")
	}
	if !errors.Is(errs[0], ErrSchemaValidation) {
		t.Errorf("expected ErrSchemaValidation sentinel, got %v", errs[0].Err)
	}
	if !strings.HasPrefix(errs[0].FieldPath, ".values") {
		t.Errorf("expected .values prefix, got %q", errs[0].FieldPath)
	}
}

func TestValidatePillar_InfraPillarIsolated(t *testing.T) {
	t.Parallel()
	// Infra pillar should be validated against infra data, NOT values data.
	schema := `values: { port: int }
infra: { replicas: int & >0 }`
	errs := ValidatePillar(schema, "infra", map[string]any{"replicas": 3}, "Component api:1.0.0")
	if len(errs) != 0 {
		t.Fatalf("expected infra validation to pass, got %d errors", len(errs))
	}

	// Invalid infra
	errs = ValidatePillar(schema, "infra", map[string]any{"replicas": 0}, "Component api:1.0.0")
	if len(errs) == 0 {
		t.Fatal("expected infra constraint violation")
	}
	if !strings.HasPrefix(errs[0].FieldPath, ".infra") {
		t.Errorf("expected .infra prefix, got %q", errs[0].FieldPath)
	}
}

func TestValidatePillar_MissingPillarIsNoOp(t *testing.T) {
	t.Parallel()
	// Schema has values but not infra — asking for the infra pillar returns nil.
	schema := `values: { port: int }`
	errs := ValidatePillar(schema, "infra", map[string]any{"replicas": 3}, "Component api:1.0.0")
	if len(errs) != 0 {
		t.Errorf("expected no-op for missing pillar, got %d errors", len(errs))
	}
}

func TestValidatePillar_EmptySchema(t *testing.T) {
	t.Parallel()
	errs := ValidatePillar("", "values", map[string]any{"x": 1}, "Component api:1.0.0")
	if len(errs) != 0 {
		t.Errorf("expected no-op for empty schema, got %d", len(errs))
	}
}

func TestValidatePillar_InvalidCUECompilation(t *testing.T) {
	t.Parallel()
	errs := ValidatePillar("{ not valid CUE !!!", "values", map[string]any{}, "Component api:1.0.0")
	if len(errs) == 0 {
		t.Fatal("expected compile error")
	}
	if errs[0].Constraint == "" {
		t.Error("expected constraint message")
	}
}

func TestValidatePillar_NoRawCUEInOutput(t *testing.T) {
	t.Parallel()
	schema := `values: { port: int }`
	errs := ValidatePillar(schema, "values", map[string]any{"port": "bad"}, "Component api:1.0.0")
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, ".cue:") {
			t.Errorf("error message leaks raw CUE source references: %s", msg)
		}
	}
}
