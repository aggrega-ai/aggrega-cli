// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ValidateRecipe enforces the Recipe shape rules at load time. Called from
// catalog loading immediately after yaml.Unmarshal succeeds, before the
// Recipe is admitted to the catalog map. Returns a *RecipeValidationError
// (or nil on pass).
//
// Rules enforced here: 1, 5 (chapter 16), plus the eval-provider constraint.
// The workload-requires-provider-workloadSchema constraint is enforced at
// render time by the central input-kind gate (a synthesised Recipe bound to a
// provider that does not implement workload synthesis halts loudly there).
// Rule 7 (cross-input-kind filename collision) is enforced at emission time by
// Phase B.5.
//
// The eval-provider carve-out is two-sided:
//
//  1. Reject non-empty source / workload / artifacts (eval has no rendering
//     surface).
//  2. Exempt eval Recipes from rule 5 (a Recipe declaring only `provides:`
//     is legitimate eval-mode authoring).
func ValidateRecipe(r *types.Recipe, sourcePath string) error {
	if r == nil {
		return nil
	}

	if r.Spec.Target == "eval" {
		return validateRecipeEvalProvider(r, sourcePath)
	}

	if err := validateRecipeRule1(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeRule5(r, sourcePath); err != nil {
		return err
	}
	return nil
}

// validateRecipeEvalProvider enforces the eval-provider constraint: input-kind
// fields must be empty. Rule 5 is intentionally skipped here — an eval Recipe
// declaring zero input kinds is legitimate (it provides values without
// rendering anything).
func validateRecipeEvalProvider(r *types.Recipe, sourcePath string) error {
	var declared []string
	if !sourceIsEmpty(r.Spec.Source) {
		declared = append(declared, "source")
	}
	if !r.Spec.Workload.IsEmpty() {
		declared = append(declared, "workload")
	}
	if r.Spec.Artifacts.Len() > 0 {
		declared = append(declared, "artifacts")
	}
	if !r.Spec.ValuesMapping.IsEmpty() {
		declared = append(declared, "valuesMapping")
	}

	if len(declared) == 0 {
		return nil
	}
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "eval",
		Description: "the eval provider executes formatting in-memory and forbids input-kind fields",
		Detail:      fmt.Sprintf("forbidden fields declared: %s", strings.Join(declared, ", ")),
		Err:         ErrRecipeEvalProviderConstraint,
	}
}

// validateRecipeRule1: valuesMapping requires source.
//
// Sourced-mode authoring presents a Source pointer alongside a values overlay.
// A Recipe with valuesMapping but no Source has no chart/module to overlay
// values onto.
func validateRecipeRule1(r *types.Recipe, sourcePath string) error {
	if r.Spec.ValuesMapping.IsEmpty() {
		return nil
	}
	if !sourceIsEmpty(r.Spec.Source) {
		return nil
	}
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "1",
		Description: "valuesMapping requires source",
		Detail:      "declare a source: pointer alongside valuesMapping (or remove the valuesMapping if no overlay is intended)",
		Err:         ErrRecipeRule1,
	}
}

// validateRecipeRule5: at least one input kind must be present.
//
// The three input kinds (any one suffices):
//
//  1. Sourced — source + valuesMapping (and ValuesMapping has its own paired rules above).
//  2. Synthesized — workload (inline OR $import: file).
//  3. Artifacts — at least one entry under artifacts.
//
// A Recipe with none of these declared has no rendering surface.
func validateRecipeRule5(r *types.Recipe, sourcePath string) error {
	if !sourceIsEmpty(r.Spec.Source) {
		return nil
	}
	if !r.Spec.Workload.IsEmpty() {
		return nil
	}
	if r.Spec.Artifacts.Len() > 0 {
		return nil
	}
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "5",
		Description: "at least one input kind must be present",
		Detail:      "declare one of: source (with valuesMapping), workload, artifacts",
		Err:         ErrRecipeRule5,
	}
}

// sourceIsEmpty reports whether the Recipe's Spec.Source field carries no
// value. Source is any and can be a string URI, a structured object,
// or nil; any non-nil non-empty value counts as a declared source.
func sourceIsEmpty(src any) bool {
	if src == nil {
		return true
	}
	if s, ok := src.(string); ok && s == "" {
		return true
	}
	return false
}
