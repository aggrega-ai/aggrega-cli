// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	stderrors "errors"
	"strings"
	"testing"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/validator"
)

// TestFormatCompilerError_RichShape verifies the formatter renders the
// documented multi-line shape for a CompilerError: category tag in
// brackets, source pointer, message, expression, active pillars list.
func TestFormatCompilerError_RichShape(t *testing.T) {
	t.Parallel()
	e := &cerrors.CompilerError{
		Category:   cerrors.CatalogError,
		Source:     cerrors.SourceLocation{File: "recipes/api.yaml", Line: 12, Column: 3},
		Message:    "FeatureRecipe template references per-component-only field",
		Expression: ".context.instance_id",
		Context: map[string]any{
			".values": "...",
			".infra":  "...",
		},
	}
	got := formatCompilerError(e)
	for _, want := range []string{
		"[CATALOG_ERROR]",
		"recipes/api.yaml:12:3",
		"FeatureRecipe template references per-component-only field",
		".context.instance_id",
		"active pillars",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output missing substring %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_NonCompilerError_Falls back to the err.Error()
// representation when the underlying error is not a CompilerError.
func TestFormatCompilerError_NonCompilerError(t *testing.T) {
	t.Parallel()
	plain := stderrors.New("plain error message")
	got := formatCompilerError(plain)
	if got != "plain error message" {
		t.Errorf("formatCompilerError fallback = %q; want %q", got, "plain error message")
	}
}

// TestFormatCompilerError_Nil returns empty string for nil input.
func TestFormatCompilerError_Nil(t *testing.T) {
	t.Parallel()
	if got := formatCompilerError(nil); got != "" {
		t.Errorf("formatCompilerError(nil) = %q; want empty", got)
	}
}

// TestFormatCompilerError_DeveloperCategory_RendersViaResolverError
// verifies the DeveloperError category renders correctly through the
// formatter when the input is a ResolverError with Aggregate-prefixed
// Entity. Exercises the structured-error detection branch + recursion
// path.
func TestFormatCompilerError_DeveloperCategory_RendersViaResolverError(t *testing.T) {
	t.Parallel()
	re := &resolver.ResolverError{
		Entity:  "Aggregate my-app",
		Field:   ".spec.components.bad-name",
		Message: "invalid component name",
		Err:     resolver.ErrInvalidInstanceName,
	}
	got := formatCompilerError(re)
	for _, want := range []string{
		"[DEVELOPER_ERROR]",
		".spec.components.bad-name",
		"Aggregate my-app",
		"invalid component name",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_OpsCategory_RendersViaResolverError verifies
// OpsError renders correctly for Release-prefixed ResolverError.
func TestFormatCompilerError_OpsCategory_RendersViaResolverError(t *testing.T) {
	t.Parallel()
	re := &resolver.ResolverError{
		Entity:  "Release prod",
		Field:   ".spec.boundaries.frontend.parameters",
		Message: "parameter type mismatch",
		Err:     resolver.ErrInvalidParameterRef,
	}
	got := formatCompilerError(re)
	if !strings.Contains(got, "[OPS_ERROR]") {
		t.Errorf("output missing [OPS_ERROR] tag\n--- got ---\n%s", got)
	}
}

// TestFormatCompilerError_CatalogCategory_RendersViaRecipeValidationError
// verifies CatalogError renders correctly for a RecipeValidationError.
// Exercises the second structured-error detection branch.
func TestFormatCompilerError_CatalogCategory_RendersViaRecipeValidationError(t *testing.T) {
	t.Parallel()
	rve := &validator.RecipeValidationError{
		Path:        "/path/to/recipe.yaml",
		Name:        "my-recipe",
		Version:     "1.0.0",
		Rule:        "1",
		Description: "valuesMapping requires source",
		Err:         validator.ErrRecipeRule1,
	}
	got := formatCompilerError(rve)
	for _, want := range []string{
		"[CATALOG_ERROR]",
		"/path/to/recipe.yaml",
		"valuesMapping requires source",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_TranslationCategory_RendersViaCompilerError
// verifies TranslationError renders correctly for a directly-constructed
// CompilerError (TranslationError sites are not currently fed by any
// structured-error type — they emit CompilerError directly via cerrors.Wrap
// at sites like the orchestrator's INSTANCE_ID_COLLISION path). This test
// completes the per-category coverage.
func TestFormatCompilerError_TranslationCategory_RendersViaCompilerError(t *testing.T) {
	t.Parallel()
	ce := cerrors.New(
		cerrors.TranslationError,
		cerrors.SourceLocation{File: "internal/compiler/renderer/orchestrator.go"},
		"chart name mismatch across boundaries",
		nil,
		nil,
	)
	got := formatCompilerError(ce)
	if !strings.Contains(got, "[TRANSLATION_ERROR]") {
		t.Errorf("output missing [TRANSLATION_ERROR] tag\n--- got ---\n%s", got)
	}
}
