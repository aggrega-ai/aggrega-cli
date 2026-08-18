// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
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
		Err:     resolver.ErrInvalidInstanceName(),
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
		Err:     resolver.ErrInvalidParameterRef(),
	}
	got := formatCompilerError(re)
	if !strings.Contains(got, "[OPS_ERROR]") {
		t.Errorf("output missing [OPS_ERROR] tag\n--- got ---\n%s", got)
	}
}

// TestFormatCompilerError_CatalogCategory_RendersRecipeShapeRule verifies a
// Recipe shape-rule failure renders under the catalog category, naming the
// file, the rule and what the rule requires.
//
// It also pins that the diagnostic is said ONCE. A failure whose own text is
// not the text the envelope's message was built from renders a second time as
// a "caused by:" continuation, because the chain walk cannot tell a restatement
// from a new frame. Reporting through one shape is what keeps the two strings
// equal, so the count is the thing worth asserting rather than the absence of
// any particular marker.
func TestFormatCompilerError_CatalogCategory_RendersRecipeShapeRule(t *testing.T) {
	t.Parallel()
	rve := validator.NewShapeRuleError(validator.ShapeRuleViolation{
		Kind:       envelope.KindRecipe,
		Name:       "my-recipe",
		Version:    "1.0.0",
		Path:       "/path/to/recipe.yaml",
		Rule:       "1",
		Constraint: "valuesMapping requires source",
		Err:        validator.ErrRecipeRule1(),
	})
	got := formatCompilerError(rve)
	for _, want := range []string{
		"[CATALOG_ERROR]",
		"/path/to/recipe.yaml",
		"rule 1: valuesMapping requires source",
		"Recipe my-recipe:1.0.0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if n := strings.Count(got, "valuesMapping requires source"); n != 1 {
		t.Errorf("diagnostic rendered %d times, want exactly 1\n--- got ---\n%s", n, got)
	}
}

// TestFormatCompilerError_CatalogCategory_RendersFeatureRecipeShapeRule covers
// the FeatureRecipe half of the same path. Both kinds report through one shape,
// so both reach the category tag; neither falls through to a bare string.
func TestFormatCompilerError_CatalogCategory_RendersFeatureRecipeShapeRule(t *testing.T) {
	t.Parallel()
	fre := validator.NewShapeRuleError(validator.ShapeRuleViolation{
		Kind:       envelope.KindFeatureRecipe,
		Name:       "my-featurerecipe",
		Version:    "1.0.0",
		Path:       "/path/to/featurerecipe.yaml",
		Rule:       "1",
		Constraint: "valuesMapping requires source",
		Err:        validator.ErrFeatureRecipeRule1(),
	})
	got := formatCompilerError(fre)
	for _, want := range []string{
		"[CATALOG_ERROR]",
		"/path/to/featurerecipe.yaml",
		"FeatureRecipe my-featurerecipe:1.0.0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if n := strings.Count(got, "valuesMapping requires source"); n != 1 {
		t.Errorf("diagnostic rendered %d times, want exactly 1\n--- got ---\n%s", n, got)
	}
}

// TestFormatCompilerError_WorkloadValidation_CategorySaidOnce pins that the
// grouped Workload report names its category exactly once. The envelope carries
// the category as its own bracketed tag, so a message that also opened with the
// category word printed it twice in one diagnostic.
func TestFormatCompilerError_WorkloadValidation_CategorySaidOnce(t *testing.T) {
	t.Parallel()
	wve := &validator.WorkloadValidationError{
		Path:          "/path/to/recipe.yaml",
		RecipeName:    "my-recipe",
		RecipeVersion: "1.0.0",
		Stage:         "schema",
		Findings: []validator.WorkloadValidationFinding{
			{FieldPath: ".containers.main.image", Constraint: "required field missing"},
		},
		Err: validator.ErrWorkloadValidation(),
	}
	got := formatCompilerError(wve)
	if n := strings.Count(got, "CATALOG_ERROR"); n != 1 {
		t.Errorf("category named %d times, want exactly 1\n--- got ---\n%s", n, got)
	}
	for _, want := range []string{"[CATALOG_ERROR]", ".containers.main.image", "required field missing"} {
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

// TestFormatCompilerError_AnalysisError_RendersAsDeveloper verifies that
// an AnalysisError bubbled through the error chain renders correctly via
// the new detection branch (the analyzer-violation refactor's CLI render
// path).
func TestFormatCompilerError_AnalysisError_RendersAsDeveloper(t *testing.T) {
	t.Parallel()
	ae := &analyzer.AnalysisError{
		Kind:       analyzer.AnalysisErrorLocated,
		Message:    "no matching component on socket \"apis\"",
		OriginFile: "aggregate.yaml",
		Field:      ".spec.components.frontend.uses.apis",
	}
	got := formatCompilerError(ae)
	for _, want := range []string{"[DEVELOPER_ERROR]", "aggregate.yaml", "no matching component"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_ErrorsJoin_RendersAll verifies that an
// errors.Join'd chain of three CompilerErrors renders all three with the
// `\n---\n` separator. Without the multi-error branch, errors.As walks
// the chain and matches only the first; the others would silently drop.
// The build.go Step 4 refactor's "bubble each AnalysisError as a
// CompilerError via errors.Join" depends on this rendering correctness.
func TestFormatCompilerError_ErrorsJoin_RendersAll(t *testing.T) {
	t.Parallel()
	ce1 := cerrors.New(cerrors.DeveloperError, cerrors.SourceLocation{File: "node-a"}, "violation a", nil, nil)
	ce2 := cerrors.New(cerrors.DeveloperError, cerrors.SourceLocation{File: "node-b"}, "violation b", nil, nil)
	ce3 := cerrors.New(cerrors.DeveloperError, cerrors.SourceLocation{File: "node-c"}, "violation c", nil, nil)
	joined := stderrors.Join(ce1, ce2, ce3)

	got := formatCompilerError(joined)
	for _, want := range []string{"violation a", "violation b", "violation c", "\n---\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("Join'd render missing %q\n--- got ---\n%s", want, got)
		}
	}
	// Each violation rendered exactly once.
	for _, msg := range []string{"violation a", "violation b", "violation c"} {
		if strings.Count(got, msg) != 1 {
			t.Errorf("expected exactly 1 occurrence of %q, got %d\n--- got ---\n%s",
				msg, strings.Count(got, msg), got)
		}
	}
}

// TestFormatCompilerError_ErrorsJoin_MixedTypes verifies the multi-error
// branch handles a Join'd chain of mixed structured types (CompilerError +
// ResolverError + a Recipe shape-rule failure). Each sub-error converts via
// its own ToCompilerError path; all three render.
func TestFormatCompilerError_ErrorsJoin_MixedTypes(t *testing.T) {
	t.Parallel()
	ce := cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{}, "translation msg", nil, nil)
	re := &resolver.ResolverError{Entity: "Aggregate my-app", Field: ".f", Message: "resolver msg", Err: resolver.ErrInvalidInstanceName()}
	rve := validator.NewShapeRuleError(validator.ShapeRuleViolation{
		Kind:       envelope.KindRecipe,
		Name:       "r",
		Path:       "/r.yaml",
		Constraint: "recipe rule msg",
		Err:        validator.ErrRecipeRule1(),
	})
	joined := stderrors.Join(ce, re, rve)

	got := formatCompilerError(joined)
	for _, want := range []string{"[TRANSLATION_ERROR]", "[DEVELOPER_ERROR]", "[CATALOG_ERROR]", "translation msg", "resolver msg", "recipe rule msg"} {
		if !strings.Contains(got, want) {
			t.Errorf("Mixed-type Join missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_ErrorsJoin_RecursionSafe verifies nested
// errors.Join chains render fully without stack overflow. The recursive
// call flow: outer Join → multi-error branch → render each → inner
// Join hits multi-error branch → render each leaf. Termination is
// guaranteed because each leaf is non-multi-error and reaches a
// terminal rendering branch.
func TestFormatCompilerError_ErrorsJoin_RecursionSafe(t *testing.T) {
	t.Parallel()
	ce1 := cerrors.New(cerrors.DeveloperError, cerrors.SourceLocation{}, "leaf 1", nil, nil)
	ce2 := cerrors.New(cerrors.DeveloperError, cerrors.SourceLocation{}, "leaf 2", nil, nil)
	ce3 := cerrors.New(cerrors.DeveloperError, cerrors.SourceLocation{}, "leaf 3", nil, nil)
	inner := stderrors.Join(ce1, ce2)
	outer := stderrors.Join(inner, ce3)

	got := formatCompilerError(outer)
	for _, want := range []string{"leaf 1", "leaf 2", "leaf 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("nested Join missing %q\n--- got ---\n%s", want, got)
		}
	}
}
