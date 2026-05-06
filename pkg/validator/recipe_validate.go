// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
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
//  1. Reject non-empty source / workload / workloadFrom / artifacts /
//     artifactsFrom (eval has no rendering surface).
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
	if err := validateRecipeRule3(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeRule5(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeRule6(r, sourcePath); err != nil {
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
	if r.Spec.Workload != "" {
		declared = append(declared, "workload")
	}
	if r.Spec.WorkloadFrom != "" {
		declared = append(declared, "workloadFrom")
	}
	if r.Spec.Artifacts.Len() > 0 {
		declared = append(declared, "artifacts")
	}
	if len(r.Spec.ArtifactsFrom) > 0 {
		declared = append(declared, "artifactsFrom")
	}
	if r.Spec.ValuesMapping != "" {
		declared = append(declared, "valuesMapping")
	}
	if r.Spec.ValuesMappingFile != "" {
		declared = append(declared, "valuesMappingFile")
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
	hasValuesMapping := r.Spec.ValuesMapping != "" || r.Spec.ValuesMappingFile != ""
	if !hasValuesMapping {
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
	hasValuesMapping := r.Spec.ValuesMapping != "" || r.Spec.ValuesMappingFile != ""
	if hasValuesMapping {
		return nil
	}
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "2",
		Description: "source requires valuesMapping",
		Detail:      "declare a valuesMapping (or valuesMappingFile) alongside source, or use a different input kind (workload, workloadFrom, artifacts, artifactsFrom)",
		Err:         ErrRecipeRule2,
	}
}

// validateRecipeRule3: workload and workloadFrom are mutually exclusive.
func validateRecipeRule3(r *types.Recipe, sourcePath string) error {
	if r.Spec.Workload == "" || r.Spec.WorkloadFrom == "" {
		return nil
	}
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "3",
		Description: "workload and workloadFrom are mutually exclusive",
		Detail:      "declare workload (inline template) or workloadFrom (file reference), not both",
		Err:         ErrRecipeRule3,
	}
}

// validateRecipeRule5: at least one input kind must be present.
//
// The three input kinds (any one suffices):
//
//  1. Sourced — source + valuesMapping (and ValuesMapping has its own paired rules above).
//  2. Synthesized — workload OR workloadFrom.
//  3. Artifacts — artifacts OR artifactsFrom.
//
// A Recipe with none of these declared has no rendering surface.
func validateRecipeRule5(r *types.Recipe, sourcePath string) error {
	if !sourceIsEmpty(r.Spec.Source) {
		return nil
	}
	if r.Spec.Workload != "" || r.Spec.WorkloadFrom != "" {
		return nil
	}
	if r.Spec.Artifacts.Len() > 0 || len(r.Spec.ArtifactsFrom) > 0 {
		return nil
	}
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "5",
		Description: "at least one input kind must be present",
		Detail:      "declare one of: source (with valuesMapping), workload, workloadFrom, artifacts, artifactsFrom",
		Err:         ErrRecipeRule5,
	}
}

// validateRecipeRule6: artifacts output filenames must not collide. Two
// sub-classes are caught at load time:
//
//  1. Inline `artifacts:` keys clashing with `artifactsFrom:` basenames.
//  2. Two or more `artifactsFrom:` entries whose basenames collide
//     (e.g., `./a/foo.yaml` and `./b/foo.yaml`).
//
// Both classes produce the same sentinel (ErrRecipeRule6); the Detail line
// names every colliding output filename.
func validateRecipeRule6(r *types.Recipe, sourcePath string) error {
	hasInline := r.Spec.Artifacts.Len() > 0
	hasFromFile := len(r.Spec.ArtifactsFrom) > 0
	if !hasInline && !hasFromFile {
		return nil
	}

	collisionSet := map[string]struct{}{}

	if hasInline && hasFromFile {
		inlineKeys := r.Spec.Artifacts.Keys()
		inlineSet := make(map[string]struct{}, len(inlineKeys))
		for _, k := range inlineKeys {
			inlineSet[k] = struct{}{}
		}
		for _, p := range r.Spec.ArtifactsFrom {
			base := filepath.Base(p)
			if _, hit := inlineSet[base]; hit {
				collisionSet[base] = struct{}{}
			}
		}
	}

	if hasFromFile {
		baseCounts := map[string]int{}
		for _, p := range r.Spec.ArtifactsFrom {
			baseCounts[filepath.Base(p)]++
		}
		for base, n := range baseCounts {
			if n > 1 {
				collisionSet[base] = struct{}{}
			}
		}
	}

	if len(collisionSet) == 0 {
		return nil
	}

	collisions := make([]string, 0, len(collisionSet))
	for c := range collisionSet {
		collisions = append(collisions, c)
	}
	sort.Strings(collisions)

	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "6",
		Description: "artifacts output filename collision: inline keys vs artifactsFrom basenames, or within artifactsFrom",
		Detail:      fmt.Sprintf("colliding output filenames: %s", strings.Join(collisions, ", ")),
		Err:         ErrRecipeRule6,
	}
}

// sourceIsEmpty reports whether the Recipe's Spec.Source field carries no
// value. Source is interface{} and can be a string URI, a structured object,
// or nil; any non-nil non-empty value counts as a declared source.
func sourceIsEmpty(src interface{}) bool {
	if src == nil {
		return true
	}
	if s, ok := src.(string); ok && s == "" {
		return true
	}
	return false
}
