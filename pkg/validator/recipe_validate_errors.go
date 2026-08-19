// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Recipe-validation sentinels. The general shape rules constrain how a Recipe
// composes its input kinds; the eval-provider constraint is provider-specific
// and covers the eval mode's mathematical-evaluation-only contract. Each rule
// is named by what it constrains, and that name is the identifier the
// diagnostic prints and the reference documentation heads its entry with.
//
// Shape rules surfaced at Recipe load time: values-mapping and input-kinds.
// The workload-requires-provider-workloadSchema constraint is enforced at
// render time by the central input-kind gate (a synthesised Recipe bound to a
// provider with no workload-synthesis capability halts loudly there).
// The filename-collision rule is enforced at emission time (collision across
// input-kind origins) and is reserved here so the load-time validator and the
// emission-time detector share an error shape.
//
// Every rule here reports through NewShapeRuleError, so a Recipe failure is a
// *ValidationError whatever stage detected it.

// ErrRecipeRule1: valuesMapping requires source.
func ErrRecipeRule1() error { return errRecipeRule1 }

var errRecipeRule1 = cerrors.Register("validator/recipe-values-mapping-requires-source", cerrors.Meta{
	Message: "Recipe: valuesMapping requires source",
})

// ErrRecipeRule5: at least one input kind must be present.
func ErrRecipeRule5() error { return errRecipeRule5 }

var errRecipeRule5 = cerrors.Register("validator/recipe-no-input-kind", cerrors.Meta{
	Message: "Recipe: at least one input kind required",
})

// ErrRecipeRule7: emission-time filename collision across input-kind
// origins (source output, workload synthesis, artifacts). RESERVED for
// emission-time use; not surfaced by ValidateRecipe at load time. The
// sentinel is declared here so the load-time validator and the
// emission-time detector report the same failure shape.
func ErrRecipeRule7() error { return errRecipeRule7 }

var errRecipeRule7 = cerrors.Register("validator/recipe-filename-collision-across-input-kinds", cerrors.Meta{
	Message: "Recipe: filename collision across input-kind origins at emission",
})

// ErrRecipeEvalProviderConstraint: the eval provider executes formatting
// in-memory and has no rendering surface; declaring source/workload/
// artifacts on an eval Recipe is rejected. The eval carve-out also
// exempts the Recipe from the input-kinds rule — eval Recipes legitimately
// declare only `provides:` and have zero input kinds.
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

// ErrRecipeOutputComponentLabelCollision: a key promoted through
// outputComponentLabels is also declared as a literal key in outputLabels.
// The two blocks stamp onto the same label surface, so a key named in both
// would carry a value from two sources — the component's own label and the
// literal written in place — with nothing to say which wins. Rejecting the
// overlap at load keeps every stamped key traceable to exactly one source.
func ErrRecipeOutputComponentLabelCollision() error { return errRecipeOutputComponentLabelCollision }

var errRecipeOutputComponentLabelCollision = cerrors.Register("validator/recipe-output-component-label-collision", cerrors.Meta{
	Message: "recipe output-component-labels constraint: key is also declared in outputLabels",
})
