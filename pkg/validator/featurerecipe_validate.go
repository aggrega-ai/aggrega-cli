// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// FeatureRecipe-validation sentinels. FeatureRecipes carry the same three
// input kinds as Recipes (sourced, synthesised, artifacts) plus the
// FeatureRecipe-only injectMapping, so the shape rules mirror the Recipe
// rules — with one divergence: a FeatureRecipe is NOT required to declare a
// render input kind. A FeatureRecipe declaring none (only provider +
// implements) is a legitimate pure-wiring binding — it renders nothing; its
// effect is the uses-edge the Feature it implements carries. An inject-only
// FeatureRecipe is likewise valid. So there is no at-least-one-input-kind rule
// here, unlike Recipe rule 5.
//
// The workload-requires-provider-workloadSchema constraint is enforced at
// render time by the central input-kind gate (a synthesised FeatureRecipe
// bound to a provider with no workload-synthesis capability halts loudly
// there), not at load. Cross-input-kind filename collision is enforced at
// emission time by Phase B.5.

// ErrFeatureRecipeRule1: valuesMapping requires source.
func ErrFeatureRecipeRule1() error { return errFeatureRecipeRule1 }

var errFeatureRecipeRule1 = cerrors.Register("validator/feature-recipe-rule-1", cerrors.Meta{
	Message: "featurerecipe rule 1: valuesMapping requires source",
})

// ErrFeatureRecipeEvalProviderConstraint: the eval provider executes
// formatting in-memory and has no rendering surface; declaring
// source/valuesMapping/workload/artifacts on an eval FeatureRecipe is
// rejected. injectMapping remains allowed (it is a Phase-A delivery
// transform, not a rendering surface).
func ErrFeatureRecipeEvalProviderConstraint() error { return errFeatureRecipeEvalProviderConstraint }

var errFeatureRecipeEvalProviderConstraint = cerrors.Register("validator/feature-recipe-eval-provider-constraint", cerrors.Meta{
	Message: "featurerecipe eval-provider constraint: render input-kind fields are forbidden when provider is eval",
})

// FeatureRecipeValidationError carries the structured detail for a load-time
// FeatureRecipe validation failure. The Error() output is the
// CATALOG_ERROR-prefixed message format shared with the Recipe validator so
// FeatureRecipe and Recipe load errors look identical to the operator.
type FeatureRecipeValidationError struct {
	Path        string
	Name        string
	Version     string
	Rule        string
	Description string
	Detail      string
	Err         error
}

// Error formats the validation failure as a CATALOG_ERROR-prefixed message.
func (e *FeatureRecipeValidationError) Error() string {
	header := fmt.Sprintf("CATALOG_ERROR: FeatureRecipe %s (%s:%s) violates rule %s:",
		e.Path, e.Name, e.Version, e.Rule)
	out := header + "\n  " + e.Description
	if e.Detail != "" {
		out += "\n  " + e.Detail
	}
	return out
}

// Unwrap returns the sentinel for errors.Is matching.
func (e *FeatureRecipeValidationError) Unwrap() error { return e.Err }

// ToCompilerError converts the structured error into a *cerrors.CompilerError
// carrying CategoryCatalogError — FeatureRecipe content is catalog content.
func (e *FeatureRecipeValidationError) ToCompilerError() *cerrors.CompilerError {
	msg := e.Description
	if e.Detail != "" {
		msg = msg + " — " + e.Detail
	}
	return cerrors.Wrap(e, cerrors.CatalogError, cerrors.SourceLocation{File: e.Path}, msg)
}

// ValidateFeatureRecipe enforces the FeatureRecipe shape rules at load time.
// Called from catalog loading immediately after yaml.Unmarshal succeeds.
// Returns a *FeatureRecipeValidationError (or nil on pass).
//
// Two rules: the eval-provider carve-out (reject render input-kind fields —
// eval has no rendering surface) and rule 1 (valuesMapping requires source).
// There is deliberately no at-least-one-input-kind rule: a FeatureRecipe may
// be pure-wiring (provider + implements only) or inject-only.
func ValidateFeatureRecipe(fr *types.FeatureRecipe, sourcePath string) error {
	if fr == nil {
		return nil
	}
	if fr.Spec.Capability == "eval" {
		return validateFeatureRecipeEvalProvider(fr, sourcePath)
	}
	if err := validateFeatureRecipeRule1(fr, sourcePath); err != nil {
		return err
	}
	return nil
}

// validateFeatureRecipeRule1: valuesMapping requires source. A FeatureRecipe
// with valuesMapping but no source has no chart/module to overlay values onto.
func validateFeatureRecipeRule1(fr *types.FeatureRecipe, sourcePath string) error {
	if fr.Spec.ValuesMapping.IsEmpty() {
		return nil
	}
	if !sourceIsEmpty(fr.Spec.Source) {
		return nil
	}
	return &FeatureRecipeValidationError{
		Path:        sourcePath,
		Name:        fr.Metadata.Name,
		Version:     fr.Metadata.Version,
		Rule:        "1",
		Description: "valuesMapping requires source",
		Detail:      "declare a source: pointer alongside valuesMapping (or remove the valuesMapping if no overlay is intended)",
		Err:         ErrFeatureRecipeRule1(),
	}
}

// validateFeatureRecipeEvalProvider enforces the eval-provider constraint:
// render input-kind fields must be empty. The at-least-one-input-kind check is
// intentionally skipped — an eval FeatureRecipe declaring zero render kinds is
// legitimate (it provides values / inject mutation without rendering).
func validateFeatureRecipeEvalProvider(fr *types.FeatureRecipe, sourcePath string) error {
	var declared []string
	if !sourceIsEmpty(fr.Spec.Source) {
		declared = append(declared, "source")
	}
	if !fr.Spec.ValuesMapping.IsEmpty() {
		declared = append(declared, "valuesMapping")
	}
	if !fr.Spec.Workload.IsEmpty() {
		declared = append(declared, "workload")
	}
	if fr.Spec.Artifacts.Len() > 0 {
		declared = append(declared, "artifacts")
	}
	if len(declared) == 0 {
		return nil
	}
	return &FeatureRecipeValidationError{
		Path:        sourcePath,
		Name:        fr.Metadata.Name,
		Version:     fr.Metadata.Version,
		Rule:        "eval",
		Description: "the eval provider executes formatting in-memory and forbids render input-kind fields",
		Detail:      fmt.Sprintf("forbidden fields declared: %s", strings.Join(declared, ", ")),
		Err:         ErrFeatureRecipeEvalProviderConstraint(),
	}
}
