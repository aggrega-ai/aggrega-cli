// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Recipe-validation sentinels. The numbered rules are general Recipe shape
// rules from chapter 16; the eval-provider constraint is provider-specific and
// covers the eval mode's mathematical-evaluation-only contract.
//
// Rules surfaced at Recipe load time: 1, 5.
// The workload-requires-provider-workloadSchema constraint is enforced at
// render time by the central input-kind gate (a synthesised Recipe bound to a
// provider with no workload-synthesis capability halts loudly there).
// Rule 7 is enforced at emission time (filename collision detection) and is
// reserved here so the load-time validator and the emission-time detector
// share an error shape.
//
// Every rule here reports through NewShapeRuleError, so a Recipe failure is a
// *ValidationError whatever stage detected it.

// ErrRecipeRule1: valuesMapping requires source.
func ErrRecipeRule1() error { return errRecipeRule1 }

var errRecipeRule1 = cerrors.Register("validator/recipe-rule-1", cerrors.Meta{
	Message: "recipe rule 1: valuesMapping requires source",
})

// ErrRecipeRule5: at least one input kind must be present.
func ErrRecipeRule5() error { return errRecipeRule5 }

var errRecipeRule5 = cerrors.Register("validator/recipe-rule-5", cerrors.Meta{
	Message: "recipe rule 5: at least one input kind required",
})

// ErrRecipeRule7: emission-time filename collision across input-kind
// origins (source output, workload synthesis, artifacts). RESERVED for
// emission-time use; not surfaced by ValidateRecipe at load time. The
// sentinel is declared here so the load-time validator and the
// emission-time detector report the same failure shape.
func ErrRecipeRule7() error { return errRecipeRule7 }

var errRecipeRule7 = cerrors.Register("validator/recipe-rule-7", cerrors.Meta{
	Message: "recipe rule 7: filename collision across input-kind origins at emission",
})

// ErrRecipeEvalProviderConstraint: the eval provider executes formatting
// in-memory and has no rendering surface; declaring source/workload/
// artifacts on an eval Recipe is rejected. The eval carve-out also
// exempts the Recipe from rule 5 — eval Recipes legitimately declare
// only `provides:` and have zero input kinds.
func ErrRecipeEvalProviderConstraint() error { return errRecipeEvalProviderConstraint }

var errRecipeEvalProviderConstraint = cerrors.Register("validator/recipe-eval-provider-constraint", cerrors.Meta{
	Message: "recipe eval-provider constraint: input-kind fields are forbidden when provider is eval",
})

// ErrRecipeOutputLabelReservedNamespace: an outputLabels key uses a
// platform-reserved namespace (aggrega.ai/ or app.kubernetes.io/). Those
// prefixes are owned by the fixed provenance set; a recipe may not stamp
// them, which also keeps a validated output-label set disjoint from the
// provenance set. Engine-agnostic: enforced at load, before any provider
// consumes the labels.
func ErrRecipeOutputLabelReservedNamespace() error { return errRecipeOutputLabelReservedNamespace }

var errRecipeOutputLabelReservedNamespace = cerrors.Register("validator/recipe-output-label-reserved-namespace", cerrors.Meta{
	Message: "recipe output-labels constraint: key uses a reserved namespace",
})
