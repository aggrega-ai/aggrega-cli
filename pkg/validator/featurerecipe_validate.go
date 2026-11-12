// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/documentview"
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

var errFeatureRecipeValuesMappingRequiresSource = cerrors.Register("feature-recipe-values-mapping-requires-source", cerrors.Meta{
	ShortCode: "E0204",
	Message:   "FeatureRecipe: valuesMapping requires source",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// Every rule here reports through NewShapeRuleError, so a FeatureRecipe failure
// is a *ValidationError carrying the FeatureRecipe kind — the same shape a
// Recipe failure takes, so the two read identically to the operator.

// ValidateFeatureRecipe enforces the FeatureRecipe shape rules at load time.
// Called from catalog loading immediately after yaml.Unmarshal succeeds.
// Returns a *ValidationError against the FeatureRecipe kind (or nil on pass).
//
// Three rules: values-mapping (valuesMapping requires source), source-kind (a
// structured source names a kind an adapter serves) and source-url (the reference
// a source names carries no credential of its own). There is deliberately no
// at-least-one-input-kind rule: a FeatureRecipe may be pure-wiring (provider +
// implements only) or inject-only.
func ValidateFeatureRecipe(fr *types.FeatureRecipe, sourcePath string) error {
	if fr == nil {
		return nil
	}
	if err := validateFeatureRecipeValuesMapping(fr, sourcePath); err != nil {
		return err
	}
	if err := validateFeatureRecipeSourceKind(fr, sourcePath); err != nil {
		return err
	}
	if err := validateFeatureRecipeSourceURL(fr, sourcePath); err != nil {
		return err
	}
	return nil
}

// validateFeatureRecipeSourceURL enforces the source-url rule: the reference a
// source names carries no credential of its own.
//
// The rule itself is declared rather than written out here: see
// featureRecipeSourceURLCarriesNoCredential, which names the facts it reads and is
// invoked once per presented document. What this function does is present the
// document the load is holding and render whatever came back into the failure
// shape this package's callers read.
func validateFeatureRecipeSourceURL(fr *types.FeatureRecipe, sourcePath string) error {
	var presented constraint.DocumentView = documentview.NewFeatureRecipe(sourcePath, fr)
	failures := shapeRuleErrorsFrom(
		constraint.RunOnDocument(
			sourcePath,
			presented,
			featureRecipeSourceURLCarriesNoCredential,
		),
		ruleSubject{
			Kind:    envelope.KindFeatureRecipe,
			Name:    fr.Metadata.Name,
			Version: fr.Metadata.Version,
			Rule:    "source-url",
		},
	)
	if len(failures) == 0 {
		return nil
	}
	return failures[0]
}

// validateFeatureRecipeSourceKind enforces the source-kind rule: a structured
// source names a kind a source adapter serves.
//
// The rule itself is declared rather than written out here: see
// featureRecipeSourceKindHasAnAdapter, which names the facts it reads and is
// invoked once per presented document. What this function does is present the
// document the load is holding and render whatever came back into the failure
// shape this package's callers read.
func validateFeatureRecipeSourceKind(fr *types.FeatureRecipe, sourcePath string) error {
	var presented constraint.DocumentView = documentview.NewFeatureRecipe(sourcePath, fr)
	failures := shapeRuleErrorsFrom(
		constraint.RunOnDocument(
			sourcePath,
			presented,
			featureRecipeSourceKindHasAnAdapter,
		),
		ruleSubject{
			Kind:    envelope.KindFeatureRecipe,
			Name:    fr.Metadata.Name,
			Version: fr.Metadata.Version,
			Rule:    "source-kind",
		},
	)
	if len(failures) == 0 {
		return nil
	}
	return failures[0]
}

// validateFeatureRecipeValuesMapping enforces the values-mapping rule:
// valuesMapping requires source. A FeatureRecipe with valuesMapping but no
// source has no chart/module to overlay values onto.
//
// The rule itself is declared rather than written out here: see
// featureRecipeValuesMappingHasASource, which names the facts it reads and is
// invoked once per presented document. What this function does is present the
// document the load is holding and render whatever came back into the failure
// shape this package's callers read.
//
// The rule raises at most one objection, because it is invoked once per document
// and objects about the document as a whole, so the first failure is the only one
// there can be. Taking the first rather than returning the set is what keeps this
// function's result the single failure the sibling rule here returns.
func validateFeatureRecipeValuesMapping(fr *types.FeatureRecipe, sourcePath string) error {
	// The presentation is held as the view the rule declared rather than as its
	// own concrete type, because the entry takes the check and the view together
	// and infers one type from both. Handing over the concrete type would infer
	// that type and then refuse the rule, which declares the view.
	var presented constraint.DocumentView = documentview.NewFeatureRecipe(sourcePath, fr)
	failures := shapeRuleErrorsFrom(
		constraint.RunOnDocument(
			sourcePath,
			presented,
			featureRecipeValuesMappingHasASource,
		),
		ruleSubject{
			Kind:    envelope.KindFeatureRecipe,
			Name:    fr.Metadata.Name,
			Version: fr.Metadata.Version,
			Rule:    "values-mapping",
		},
	)
	if len(failures) == 0 {
		return nil
	}
	return failures[0]
}
