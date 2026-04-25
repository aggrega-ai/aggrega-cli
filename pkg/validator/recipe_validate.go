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

// rule1Strict gates strict enforcement of rule 1 (valuesMapping requires
// source). It is `false` during the synthesis-foundations transition: the
// earlier sourceless-helm code path at renderer/helm/sourceless.go still
// ships, and shipping integration tests (TestExample22_HelmGenerative,
// TestExample22_HelmChartGenerative, TestDeterministicCompilation_*) exercise
// it via fixtures that declare valuesMapping without source. Flipping this
// flag to true breaks those tests; the  replacement is the synthesized
// input kind (workload:), which lands in WI05 (helm Workload synthesizer).
//
// TODO(synthesis-foundations WI05): activate strict rule 1 enforcement after
// sourceless-helm fixtures migrate to the workload: input kind. Removal
// contract: delete the rule1Strict declaration and the early-return guard
// in validateRecipeRule1 below; the strict body activates verbatim. WI05's
// acceptance carries this gate-removal as a tracked deliverable.
var rule1Strict = false

// validateRecipeRule1: valuesMapping requires source.
//
// Sourced-mode authoring presents a Source pointer alongside a values overlay.
// A Recipe with valuesMapping but no Source has no chart/module to overlay
// values onto.
func validateRecipeRule1(r *types.Recipe, sourcePath string) error {
	if !rule1Strict {
		// Transitional accept; see rule1Strict godoc.
		return nil
	}
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
//
// Rule 5 honours the rule1Strict gate: while rule 1 is non-strict, a Recipe
// with `valuesMapping` (or valuesMappingFile) alone — the earlier sourceless
// pattern — counts as a declared input kind. WI05's gate flip removes both
// this branch and rule 1's early-return; rule 5 then enforces the strict
// three-kinds enumeration verbatim.
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
	if !rule1Strict && (r.Spec.ValuesMapping != "" || r.Spec.ValuesMappingFile != "") {
		// Transitional: earlier sourceless-helm pattern (valuesMapping
		// alone) is accepted while rule 1 is gated. Removed in WI05.
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

// validateRecipeRule6: artifacts inline keys and artifactsFrom basenames must
// not collide.
//
// `artifacts:` keys are literal output filenames. `artifactsFrom:` entries
// are file paths whose basename becomes the output filename. A Recipe that
// declares both must not produce two emissions targeting the same output
// filename — that's a load-time error here, before Phase B.5 runs.
func validateRecipeRule6(r *types.Recipe, sourcePath string) error {
	if r.Spec.Artifacts.Len() == 0 || len(r.Spec.ArtifactsFrom) == 0 {
		return nil
	}

	// Inline filenames (already literals; map keys carry author author-order
	// from the OrderedMap, but for collision detection alphabetical is
	// sufficient and stable).
	inlineKeys := r.Spec.Artifacts.Keys()
	inlineSet := make(map[string]struct{}, len(inlineKeys))
	for _, k := range inlineKeys {
		inlineSet[k] = struct{}{}
	}

	// Collect collisions: artifactsFrom basenames matching inline keys.
	var collisions []string
	for _, p := range r.Spec.ArtifactsFrom {
		base := filepath.Base(p)
		if _, hit := inlineSet[base]; hit {
			collisions = append(collisions, base)
		}
	}
	if len(collisions) == 0 {
		return nil
	}

	sort.Strings(collisions)
	return &RecipeValidationError{
		Path:        sourcePath,
		Name:        r.Metadata.Name,
		Version:     r.Metadata.Version,
		Rule:        "6",
		Description: "artifacts inline keys collide with artifactsFrom basenames",
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
