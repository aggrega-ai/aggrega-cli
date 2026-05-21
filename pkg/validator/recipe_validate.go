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
// Rules enforced here: 1, 2, 3, 5, 6 (chapter 16).
// Rule 4 is enforced separately by ValidateRecipeProviderBinding after
// Manifest resolution binds Recipe → Provider.
// Rule 7 is enforced at emission time by Phase B.5.
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

	if r.Spec.Provider == "eval" {
		return validateRecipeEvalProvider(r, sourcePath)
	}

	if err := validateRecipeRule1(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeRule2(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeRule5(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeHelmChartProvider(r, sourcePath); err != nil {
		return err
	}
	return nil
}

// validateRecipeHelmChartProvider enforces the helm-chart-specific load-time
// constraint: inline-mode artifact entries are forbidden. Helm-chart emits
// an umbrella chart structure rather than flat manifests; artifact bodies
// must be physical files that the engine can inject into the chart's
// templates/ directory before Helm rendering, so inline (mode 1) entries
// have no place in this authoring path. Import-mode entries (mode 2,
// {$import: <local-path>}) are accepted. The fix the message recommends
// is to migrate the inline content to a file and reference it via
// {$import:}.
func validateRecipeHelmChartProvider(r *types.Recipe, sourcePath string) error {
	if r.Spec.Provider != "helm-chart" || r.Spec.Artifacts.Len() == 0 {
		return nil
	}
	var inlineKeys []string
	for _, name := range r.Spec.Artifacts.Keys() {
		entry, _ := r.Spec.Artifacts.Get(name)
		if entry.Inline != "" {
			inlineKeys = append(inlineKeys, name)
		}
	}
	if len(inlineKeys) == 0 {
		return nil
	}
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "helm-chart-inline-artifacts",
		Description: "provider helm-chart does not support inline artifact entries",
		Detail:      fmt.Sprintf("inline artifacts: %s; migrate the content to a file and reference it via {$import: <path>} so the file can be injected into the chart's templates/ directory", strings.Join(inlineKeys, ", ")),
		Err:         ErrRecipeHelmChartInlineArtifacts,
	}
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

// validateRecipeRule2: source requires valuesMapping.
//
// A Recipe pointing at a Source with no valuesMapping has no overlay to
// apply; the Recipe authors no Aggrega-side contribution beyond the raw
// chart/module content.
func validateRecipeRule2(r *types.Recipe, sourcePath string) error {
	if sourceIsEmpty(r.Spec.Source) {
		return nil
	}
	if !r.Spec.ValuesMapping.IsEmpty() {
		return nil
	}
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "2",
		Description: "source requires valuesMapping",
		Detail:      "declare a valuesMapping alongside source, or use a different input kind (workload, artifacts)",
		Err:         ErrRecipeRule2,
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
