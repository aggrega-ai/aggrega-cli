// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver_test

import (
	stderrors "errors"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestResolverError_ToCompilerError_AggregateRoutesToDeveloper verifies
// that a ResolverError with Entity prefix "Aggregate" converts to a
// CompilerError with DeveloperError category. Covers the developer-side
// of the structured-error coverage.
func TestResolverError_ToCompilerError_AggregateRoutesToDeveloper(t *testing.T) {
	re := &resolver.ResolverError{
		Entity:  "Aggregate my-app",
		Field:   ".spec.components.bad-name",
		Message: "invalid component name",
		Err:     resolver.ErrInvalidInstanceName(),
	}
	ce := re.ToCompilerError()
	if ce == nil {
		t.Fatal("ToCompilerError returned nil")
	}
	if ce.Category != cerrors.DeveloperError {
		t.Errorf("Category = %v, want %v", ce.Category, cerrors.DeveloperError)
	}
	if ce.Source.File != ".spec.components.bad-name" {
		t.Errorf("Source.File = %q, want %q", ce.Source.File, ".spec.components.bad-name")
	}
	// Inner error chain preserved: errors.Is against the resolver sentinel
	// matches through the converted CompilerError.
	if !stderrors.Is(ce, resolver.ErrInvalidInstanceName()) {
		t.Errorf("errors.Is(ce, ErrInvalidInstanceName) = false, want true (chain preservation)")
	}
}

// TestResolverError_ToCompilerError_ReleaseRoutesToOps verifies the
// Release entity prefix routes to OpsError category.
func TestResolverError_ToCompilerError_ReleaseRoutesToOps(t *testing.T) {
	re := &resolver.ResolverError{
		Entity:  "Release prod",
		Field:   ".spec.boundaries.frontend.parameters.bad-key",
		Message: "parameter type mismatch",
		Err:     resolver.ErrInvalidParameterRef(),
	}
	ce := re.ToCompilerError()
	if ce.Category != cerrors.OpsError {
		t.Errorf("Category = %v, want %v", ce.Category, cerrors.OpsError)
	}
}

// TestResolverError_ToCompilerError_RecipeRoutesToCatalog verifies the
// Recipe entity prefix routes to CatalogError category.
func TestResolverError_ToCompilerError_RecipeRoutesToCatalog(t *testing.T) {
	re := &resolver.ResolverError{
		Entity:  "Recipe my-recipe",
		Field:   ".spec.implements",
		Message: "unknown component type",
		Err:     resolver.ErrYAMLLoad(),
	}
	ce := re.ToCompilerError()
	if ce.Category != cerrors.CatalogError {
		t.Errorf("Category = %v, want %v", ce.Category, cerrors.CatalogError)
	}
}
