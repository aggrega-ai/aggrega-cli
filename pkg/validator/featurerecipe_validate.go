// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// FeatureRecipe-validation sentinels. FeatureRecipes carry the same three
// input kinds as Recipes (sourced, synthesized, artifacts) plus the
// FeatureRecipe-only injectMapping, so the shape rules mirror the Recipe
// rules — with one divergence: a FeatureRecipe is NOT required to declare a
// render input kind. A FeatureRecipe declaring none (only provider +
// implements) is a legitimate pure-wiring binding — it renders nothing; its
// effect is the uses-edge the Feature it implements carries. An inject-only
// FeatureRecipe is likewise valid. So there is no at-least-one-input-kind rule
// here, unlike a Recipe's input-kinds rule.
//
// The workload-requires-provider-workloadSchema constraint is enforced at
// render time by the central input-kind gate (a synthesized FeatureRecipe
// bound to a provider with no workload-synthesis capability halts loudly
// there), not at load. Cross-input-kind filename collision is enforced at
// emission time by feature rendering.

// ErrFeatureRecipeValuesMappingRequiresSource: valuesMapping requires source.
func ErrFeatureRecipeValuesMappingRequiresSource() error {
	return errFeatureRecipeValuesMappingRequiresSource
}

var errFeatureRecipeValuesMappingRequiresSource = cerrors.Register("validator/feature-recipe-values-mapping-requires-source", cerrors.Meta{
	ShortCode: "E0204",
	Message:   "FeatureRecipe: valuesMapping requires source",
})

// ErrFeatureRecipeEvalProviderConstraint: the eval provider executes
// formatting in-memory and has no rendering surface; declaring
// source/valuesMapping/workload/artifacts on an eval FeatureRecipe is
// rejected. injectMapping remains allowed (it is an inject-mapping delivery
// transform, not a rendering surface).
func ErrFeatureRecipeEvalProviderConstraint() error { return errFeatureRecipeEvalProviderConstraint }

var errFeatureRecipeEvalProviderConstraint = cerrors.Register("validator/feature-recipe-eval-provider-constraint", cerrors.Meta{
	ShortCode: "E0203",
	Message:   "featurerecipe eval-provider constraint: render input-kind fields are forbidden when provider is eval",
})

// Both rules here report through NewShapeRuleError, so a FeatureRecipe failure
// is a *ValidationError carrying the FeatureRecipe kind — the same shape a
// Recipe failure takes, so the two read identically to the operator.

// ValidateFeatureRecipe enforces the FeatureRecipe shape rules at load time.
// Called from catalog loading immediately after yaml.Unmarshal succeeds.
// Returns a *ValidationError against the FeatureRecipe kind (or nil on pass).
//
// Two rules: the eval-provider carve-out (reject render input-kind fields —
// eval has no rendering surface) and values-mapping (valuesMapping requires
// source).
// There is deliberately no at-least-one-input-kind rule: a FeatureRecipe may
// be pure-wiring (provider + implements only) or inject-only.
func ValidateFeatureRecipe(fr *types.FeatureRecipe, sourcePath string) error {
	if fr == nil {
		return nil
	}
	if fr.Spec.Capability == "eval" {
		return validateFeatureRecipeEvalProvider(fr, sourcePath)
	}
	if err := validateFeatureRecipeValuesMapping(fr, sourcePath); err != nil {
		return err
	}
	return nil
}

// validateFeatureRecipeValuesMapping enforces the values-mapping rule:
// valuesMapping requires source. A FeatureRecipe with valuesMapping but no
// source has no chart/module to overlay values onto.
func validateFeatureRecipeValuesMapping(fr *types.FeatureRecipe, sourcePath string) error {
	if fr.Spec.ValuesMapping.IsEmpty() {
		return nil
	}
	if !sourceIsEmpty(fr.Spec.Source) {
		return nil
	}
	return NewShapeRuleError(ShapeRuleViolation{
		Kind:       envelope.KindFeatureRecipe,
		Name:       fr.Metadata.Name,
		Version:    fr.Metadata.Version,
		Path:       sourcePath,
		Rule:       "values-mapping",
		Suggestion: "declare a source: pointer alongside valuesMapping (or remove the valuesMapping if no overlay is intended)",
		Err:        ErrFeatureRecipeValuesMappingRequiresSource(),
	})
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
	return NewShapeRuleError(ShapeRuleViolation{
		Kind:       envelope.KindFeatureRecipe,
		Name:       fr.Metadata.Name,
		Version:    fr.Metadata.Version,
		Path:       sourcePath,
		Rule:       "eval",
		Suggestion: fmt.Sprintf("forbidden fields declared: %s", strings.Join(declared, ", ")),
		Err:        ErrFeatureRecipeEvalProviderConstraint(),
	})
}
