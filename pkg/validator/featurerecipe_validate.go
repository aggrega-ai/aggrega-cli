// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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

// ErrFeatureRecipeEvalProviderConstraint: the eval provider executes
// formatting in-memory and has no rendering surface; declaring
// source/valuesMapping/workload/artifacts on an eval FeatureRecipe is
// rejected. injectMapping remains allowed (it is an inject-mapping delivery
// transform, not a rendering surface).
func ErrFeatureRecipeEvalProviderConstraint() error { return errFeatureRecipeEvalProviderConstraint }

var errFeatureRecipeEvalProviderConstraint = cerrors.Register("feature-recipe-eval-provider-constraint", cerrors.Meta{
	ShortCode: "E0203",
	Message:   "featurerecipe eval-provider constraint: render input-kind fields are forbidden when provider is eval",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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
//
// The eval rule is invoked first and the first failure is returned, and that
// ordering is the rule's precedence rather than an arrangement. An eval
// FeatureRecipe declaring a values overlay and no source satisfies both rules'
// conditions, and the eval one is the finding an author needs: the overlay is
// forbidden on that capability whatever else the document says, so telling them
// to add a source would send them further from a document that loads. The
// overlay is one of the fields the eval rule refuses, so it fires on every such
// document and the values-mapping rule is reached only where no overlay is
// declared at all.
func ValidateFeatureRecipe(fr *types.FeatureRecipe, sourcePath string) error {
	if fr == nil {
		return nil
	}
	if err := validateFeatureRecipeEvalProvider(fr, sourcePath); err != nil {
		return err
	}
	if err := validateFeatureRecipeValuesMapping(fr, sourcePath); err != nil {
		return err
	}
	return nil
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
	var presented constraint.DocumentView = newFeatureRecipeDocumentView(sourcePath, fr)
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

// validateFeatureRecipeEvalProvider enforces the eval-provider constraint: a
// FeatureRecipe binding the eval capability declares no rendering surface.
//
// The rule itself is declared rather than written out here: see
// featureRecipeEvalDeclaresNoRenderingSurface, which names the facts it reads —
// the declared capability among them — and is invoked once per presented
// document. What this function does is present the document the load is holding
// and render whatever came back into the failure shape this package's callers
// read.
//
// It is invoked for every FeatureRecipe rather than only for an eval one, because
// the capability gate belongs to the rule. There is deliberately no
// at-least-one-input-kind rule on this kind, so nothing else here depends on
// knowing which capability the document binds.
//
// The rule raises at most one objection, for the reason
// validateFeatureRecipeValuesMapping states.
func validateFeatureRecipeEvalProvider(fr *types.FeatureRecipe, sourcePath string) error {
	var presented constraint.DocumentView = newFeatureRecipeDocumentView(sourcePath, fr)
	failures := shapeRuleErrorsFrom(
		constraint.RunOnDocument(
			sourcePath,
			presented,
			featureRecipeEvalDeclaresNoRenderingSurface,
		),
		ruleSubject{
			Kind:    envelope.KindFeatureRecipe,
			Name:    fr.Metadata.Name,
			Version: fr.Metadata.Version,
			Rule:    "eval",
		},
	)
	if len(failures) == 0 {
		return nil
	}
	return failures[0]
}
