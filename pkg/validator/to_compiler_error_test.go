// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// TestShapeRuleError_RoutesToCatalog verifies that a shape-rule failure on
// either catalog kind that carries them converts to a CompilerError with the
// CatalogError category, and that the rule identifier survives the fold into
// the constraint text — it is the only thing telling an author WHICH rule
// failed.
func TestShapeRuleError_RoutesToCatalog(t *testing.T) {
	for _, tc := range []struct {
		kind          envelope.Kind
		wantReference string
		sentinel      error
	}{
		{envelope.KindRecipe, "Recipe my-recipe:1.0.0", validator.ErrRecipeRule1()},
		{envelope.KindFeatureRecipe, "FeatureRecipe my-recipe:1.0.0", validator.ErrFeatureRecipeRule1()},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			err := validator.NewShapeRuleError(validator.ShapeRuleViolation{
				Kind:       tc.kind,
				Name:       "my-recipe",
				Version:    "1.0.0",
				Path:       "/path/to/recipe.yaml",
				Rule:       "1",
				Constraint: "valuesMapping requires source",
				Suggestion: "declare a source: pointer alongside valuesMapping",
				Err:        tc.sentinel,
			})
			ce := err.ToCompilerError()
			if ce == nil {
				t.Fatal("ToCompilerError returned nil")
			}
			if ce.Category != cerrors.CatalogError {
				t.Errorf("Category = %v, want %v", ce.Category, cerrors.CatalogError)
			}
			if ce.Source.File != "/path/to/recipe.yaml" {
				t.Errorf("Source.File = %q, want %q", ce.Source.File, "/path/to/recipe.yaml")
			}
			for _, want := range []string{
				tc.wantReference,
				"rule 1: valuesMapping requires source",
				"declare a source: pointer alongside valuesMapping",
			} {
				if !strings.Contains(ce.Message, want) {
					t.Errorf("Message missing %q: %q", want, ce.Message)
				}
			}
			// Inner chain preserved.
			if !stderrors.Is(ce, tc.sentinel) {
				t.Errorf("errors.Is(ce, %v) = false, want true", tc.sentinel)
			}
			// The envelope's message and the failure's own text are one string,
			// which is what keeps a rendered diagnostic from being said twice.
			if ce.Message != err.Error() {
				t.Errorf("envelope message %q differs from Error() %q", ce.Message, err.Error())
			}
		})
	}
}

// TestShapeRuleError_OmitsAnAbsentRuleAndVersion verifies the two optional
// parts render away cleanly rather than leaving a dangling separator.
func TestShapeRuleError_OmitsAnAbsentRuleAndVersion(t *testing.T) {
	err := validator.NewShapeRuleError(validator.ShapeRuleViolation{
		Kind:       envelope.KindRecipe,
		Name:       "my-recipe",
		Path:       "/path/to/recipe.yaml",
		Constraint: "valuesMapping requires source",
	})
	msg := err.Error()
	if !strings.HasPrefix(msg, "Recipe my-recipe:") {
		t.Errorf("an absent version should leave no trailing separator: %q", msg)
	}
	if strings.Contains(msg, "rule :") {
		t.Errorf("an absent rule identifier should leave no empty rule token: %q", msg)
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
		Err: validator.ErrWorkloadValidation(),
	}
	ce := wve.ToCompilerError()
	if ce.Category != cerrors.CatalogError {
		t.Errorf("Category = %v, want %v", ce.Category, cerrors.CatalogError)
	}
	if ce.Source.File != "/path/to/recipe.yaml" {
		t.Errorf("Source.File = %q, want %q", ce.Source.File, "/path/to/recipe.yaml")
	}
	if !stderrors.Is(ce, validator.ErrWorkloadValidation()) {
		t.Errorf("errors.Is(ce, ErrWorkloadValidation) = false, want true")
	}
}

// TestValidationError_ToCompilerError_ComponentRoutesToCatalog verifies the
// kind-keyed classification for ValidationError: a Component failure is
// catalog-domain, and the rendered reference is built from that same kind
// rather than the kind being read back out of the reference.
func TestValidationError_ToCompilerError_ComponentRoutesToCatalog(t *testing.T) {
	ve := validator.NewValidationError(envelope.KindComponent, "core/api-server:1.0.0", validator.ValidationDetail{
		FieldPath:  ".values.port",
		Constraint: "must be >= 1024",
		Actual:     "80",
		Suggestion: "Use a port number >= 1024",
		Err:        validator.ErrInheritanceMerge(),
	})
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
	if !stderrors.Is(ce, validator.ErrInheritanceMerge()) {
		t.Errorf("errors.Is(ce, ErrInheritanceMerge) = false, want true")
	}
}

// TestValidationError_ToCompilerError_AggregateRoutesToDeveloper verifies
// that a ValidationError built for an Aggregate converts to DeveloperError
// (Aggregate values violations are developer-domain).
func TestValidationError_ToCompilerError_AggregateRoutesToDeveloper(t *testing.T) {
	ve := validator.NewValidationError(envelope.KindAggregate, "my-app", validator.ValidationDetail{
		FieldPath: ".values.replicas",
		Err:       validator.ErrInheritanceMerge(),
	})
	ce := ve.ToCompilerError()
	if ce.Category != cerrors.DeveloperError {
		t.Errorf("Category = %v, want %v (an Aggregate failure is developer-domain)", ce.Category, cerrors.DeveloperError)
	}
	if !strings.Contains(ce.Message, "Aggregate my-app") {
		t.Errorf("Message should carry the reference derived from the kind; got %q", ce.Message)
	}
}

// TestValidationError_ReferenceIsDerivedFromTheKind pins the direction of the
// dependency the failure carries: the rendered reference is built from the
// kind, so the same name reported for two kinds reads as two different
// entities and classifies into two different domains. Nothing reads a kind
// back out of the rendered text.
func TestValidationError_ReferenceIsDerivedFromTheKind(t *testing.T) {
	const name = "core/shared:1.0.0"
	cases := []struct {
		kind         envelope.Kind
		wantCategory cerrors.Category
		wantPrefix   string
	}{
		{envelope.KindComponent, cerrors.CatalogError, "Component core/shared:1.0.0"},
		{envelope.KindFeature, cerrors.CatalogError, "Feature core/shared:1.0.0"},
		{envelope.KindRelease, cerrors.OpsError, "Release core/shared:1.0.0"},
	}
	for _, tc := range cases {
		ve := validator.NewValidationError(tc.kind, name, validator.ValidationDetail{FieldPath: ".spec.extends"})
		if !strings.HasPrefix(ve.Error(), tc.wantPrefix) {
			t.Errorf("kind %q: Error() = %q, want it to open with %q", tc.kind, ve.Error(), tc.wantPrefix)
		}
		if got := ve.ToCompilerError().Category; got != tc.wantCategory {
			t.Errorf("kind %q: Category = %v, want %v", tc.kind, got, tc.wantCategory)
		}
	}
}

// TestNewValidationError_RefusesAKindWithNoDomain verifies the loud outcome
// for a kind no authoring domain covers, the zero kind included. Such a kind
// can only come from a call site that failed to supply one, so it is refused
// where it is built rather than being quietly reported under whichever domain
// a default would have chosen.
func TestNewValidationError_RefusesAKindWithNoDomain(t *testing.T) {
	cases := map[string]envelope.Kind{
		"the zero kind":          envelope.Kind(""),
		"a kind off the mapping": envelope.KindPlugin,
	}
	for label, kind := range cases {
		t.Run(label, func(t *testing.T) {
			// The kind is matched in its quoted form, so the empty kind is
			// checked as `""` rather than as a substring every message
			// trivially contains.
			quoted := fmt.Sprintf("%q", kind)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("NewValidationError accepted %q; want a refusal", kind)
				}
				msg, ok := r.(string)
				if !ok || !strings.Contains(msg, quoted) {
					t.Errorf("the refusal should name the offending kind as %s; got %v", quoted, r)
				}
			}()
			validator.NewValidationError(kind, "core/api:1.0.0", validator.ValidationDetail{FieldPath: ".spec.extends"})
		})
	}
}

// TestValidationError_ZeroValueIsRefusedAtConversion covers the one way a
// failure can exist without having gone through the constructor: an empty
// composite literal, which is legal from any package because it sets no
// unexported field. Such a value carries no kind, so it belongs to no
// authoring domain, and converting it is refused rather than yielding a
// failure with an empty category tag.
func TestValidationError_ZeroValueIsRefusedAtConversion(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("converting a kindless failure produced a category; want a refusal")
		}
	}()
	ve := &validator.ValidationError{}
	_ = ve.ToCompilerError()
}
