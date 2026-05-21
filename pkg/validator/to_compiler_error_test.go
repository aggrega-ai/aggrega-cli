// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	stderrors "errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// TestRecipeValidationError_ToCompilerError_RoutesToCatalog verifies that
// every RecipeValidationError converts to a CompilerError with
// CatalogError category. Recipe rule violations are always catalog-domain.
func TestRecipeValidationError_ToCompilerError_RoutesToCatalog(t *testing.T) {
	rve := &validator.RecipeValidationError{
		Path:        "/path/to/recipe.yaml",
		Name:        "my-recipe",
		Version:     "1.0.0",
		Rule:        "1",
		Description: "valuesMapping requires source",
		Detail:      "Recipe declares valuesMapping but no source field",
		Err:         validator.ErrRecipeRule1,
	}
	ce := rve.ToCompilerError()
	if ce == nil {
		t.Fatal("ToCompilerError returned nil")
	}
	if ce.Category != cerrors.CatalogError {
		t.Errorf("Category = %v, want %v", ce.Category, cerrors.CatalogError)
	}
	if ce.Source.File != "/path/to/recipe.yaml" {
		t.Errorf("Source.File = %q, want %q", ce.Source.File, "/path/to/recipe.yaml")
	}
	// Description + Detail composed into Message.
	if !strings.Contains(ce.Message, "valuesMapping requires source") {
		t.Errorf("Message missing Description: %q", ce.Message)
	}
	if !strings.Contains(ce.Message, "Recipe declares valuesMapping") {
		t.Errorf("Message missing Detail: %q", ce.Message)
	}
	// Inner chain preserved.
	if !stderrors.Is(ce, validator.ErrRecipeRule1) {
		t.Errorf("errors.Is(ce, ErrRecipeRule1) = false, want true")
	}
}

// TestWorkloadValidationError_ToCompilerError_RoutesToCatalog verifies
// WorkloadValidationError → CatalogError. Workload spec is part of the
// Recipe authoring surface, so violations are catalog-domain.
func TestWorkloadValidationError_ToCompilerError_RoutesToCatalog(t *testing.T) {
	wve := &validator.WorkloadValidationError{
		Path:          "/path/to/recipe.yaml",
		RecipeName:    "my-recipe",
		RecipeVersion: "1.0.0",
		Stage:         "schema",
		Findings: []validator.WorkloadValidationFinding{
			{FieldPath: ".containers.main.image", Constraint: "required field missing"},
		},
		Err: validator.ErrWorkloadValidation,
	}
	ce := wve.ToCompilerError()
	if ce.Category != cerrors.CatalogError {
		t.Errorf("Category = %v, want %v", ce.Category, cerrors.CatalogError)
	}
	if ce.Source.File != "/path/to/recipe.yaml" {
		t.Errorf("Source.File = %q, want %q", ce.Source.File, "/path/to/recipe.yaml")
	}
	if !stderrors.Is(ce, validator.ErrWorkloadValidation) {
		t.Errorf("errors.Is(ce, ErrWorkloadValidation) = false, want true")
	}
}

// TestValidationError_ToCompilerError_ComponentRoutesToCatalog verifies
// the prefix-based classification for ValidationError: Component →
// CatalogError (Component schema violations are catalog-domain).
func TestValidationError_ToCompilerError_ComponentRoutesToCatalog(t *testing.T) {
	ve := &validator.ValidationError{
		Entity:     "Component core/api-server:1.0.0",
		FieldPath:  ".values.port",
		Constraint: "must be >= 1024",
		Actual:     "80",
		Suggestion: "Use a port number >= 1024",
		Err:        validator.ErrSchemaValidation,
	}
	ce := ve.ToCompilerError()
	if ce.Category != cerrors.CatalogError {
		t.Errorf("Category = %v, want %v (Component prefix routes to CatalogError)", ce.Category, cerrors.CatalogError)
	}
	if ce.Source.File != ".values.port" {
		t.Errorf("Source.File = %q, want %q", ce.Source.File, ".values.port")
	}
	if !strings.Contains(ce.Message, "must be >= 1024") {
		t.Errorf("Message missing Constraint: %q", ce.Message)
	}
	if !strings.Contains(ce.Message, "(got 80)") {
		t.Errorf("Message missing Actual: %q", ce.Message)
	}
	if !strings.Contains(ce.Message, "Use a port number >= 1024") {
		t.Errorf("Message missing Suggestion: %q", ce.Message)
	}
	if !stderrors.Is(ce, validator.ErrSchemaValidation) {
		t.Errorf("errors.Is(ce, ErrSchemaValidation) = false, want true")
	}
}

// TestValidationError_ToCompilerError_AggregateRoutesToDeveloper verifies
// that an Aggregate-prefixed ValidationError converts to DeveloperError
// (Aggregate values violations are developer-domain).
func TestValidationError_ToCompilerError_AggregateRoutesToDeveloper(t *testing.T) {
	ve := &validator.ValidationError{
		Entity:    "Aggregate my-app",
		FieldPath: ".values.replicas",
		Err:       validator.ErrSchemaValidation,
	}
	ce := ve.ToCompilerError()
	if ce.Category != cerrors.DeveloperError {
		t.Errorf("Category = %v, want %v (Aggregate prefix routes to DeveloperError)", ce.Category, cerrors.DeveloperError)
	}
}
