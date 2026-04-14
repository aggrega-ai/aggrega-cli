// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

func TestTranslateCUEErrors_TypeMismatch(t *testing.T) {
	ctx := cuecontext.New()
	schema := ctx.CompileString(`{ port: int }`)
	data := ctx.CompileString(`{ port: "not-int" }`)
	unified := schema.Unify(data)

	errs := translateCUEErrors(unified.Validate(), "Component test:1.0.0", ErrSchemaValidation)
	if len(errs) == 0 {
		t.Fatal("expected validation errors")
	}

	for _, e := range errs {
		if !errors.Is(e, ErrSchemaValidation) {
			t.Errorf("expected ErrSchemaValidation, got %v", e.Err)
		}
		if e.Entity != "Component test:1.0.0" {
			t.Errorf("expected entity name, got %s", e.Entity)
		}
		// Must not contain raw CUE internals
		msg := e.Error()
		if strings.Contains(msg, "cuelang.org") {
			t.Errorf("error contains raw CUE internals: %s", msg)
		}
	}
}

func TestTranslateCUEErrors_NilError(t *testing.T) {
	errs := translateCUEErrors(nil, "Component test:1.0.0", ErrSchemaValidation)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for nil input, got %d", len(errs))
	}
}

func TestTranslateCUEErrors_StructuredFields(t *testing.T) {
	ctx := cuecontext.New()
	schema := ctx.CompileString(`{ port: int & >=1024 }`)
	data := ctx.CompileString(`{ port: 80 }`)
	unified := schema.Unify(data)

	errs := translateCUEErrors(unified.Validate(), "Component api:2.0.0", ErrSchemaValidation)
	if len(errs) == 0 {
		t.Fatal("expected errors for constraint violation")
	}

	e := errs[0]
	if e.Entity != "Component api:2.0.0" {
		t.Errorf("entity: got %q", e.Entity)
	}
	if e.Constraint == "" {
		t.Error("constraint should not be empty")
	}
	if e.Err == nil {
		t.Error("sentinel error should not be nil")
	}
}

func TestValidationError_ErrorFormat(t *testing.T) {
	e := &ValidationError{
		Entity:     "Component test:1.0.0",
		FieldPath:  ".values.port",
		Constraint: "must be >= 1024",
		Actual:     "80",
		Suggestion: "Use a port number >= 1024",
		Err:        ErrSchemaValidation,
	}

	msg := e.Error()
	if !strings.Contains(msg, "Component test:1.0.0") {
		t.Errorf("expected entity in message, got: %s", msg)
	}
	if !strings.Contains(msg, ".values.port") {
		t.Errorf("expected field path in message, got: %s", msg)
	}
	if !strings.Contains(msg, "must be >= 1024") {
		t.Errorf("expected constraint in message, got: %s", msg)
	}
	if !strings.Contains(msg, "80") {
		t.Errorf("expected actual value in message, got: %s", msg)
	}

	if !errors.Is(e, ErrSchemaValidation) {
		t.Error("expected errors.Is to match ErrSchemaValidation")
	}
}
