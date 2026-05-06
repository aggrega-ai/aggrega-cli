// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"fmt"
)

// Recipe-validation sentinels. The seven rules are general Recipe shape rules
// from chapter 16; the eval-provider constraint is provider-specific and
// covers the eval mode's mathematical-evaluation-only contract.
//
// Rules surfaced at Recipe load time: 1, 2, 3, 5, 6.
// Rule 4 is enforced after Manifest resolution binds Recipe to Provider.
// Rule 7 is enforced at emission time (Phase B.5 filename collision detection)
// and is reserved here so the load-time validator and the emission-time
// detector share an error shape.
var (
	// ErrRecipeRule1: valuesMapping requires source.
	ErrRecipeRule1 = errors.New("recipe rule 1: valuesMapping requires source")

	// ErrRecipeRule2: source requires valuesMapping.
	ErrRecipeRule2 = errors.New("recipe rule 2: source requires valuesMapping")

	// ErrRecipeRule3: workload and workloadFrom are mutually exclusive.
	ErrRecipeRule3 = errors.New("recipe rule 3: workload and workloadFrom are mutually exclusive")

	// ErrRecipeRule4: workload/workloadFrom requires the resolved Provider to
	// declare workloadSchema. Surfaced post-Manifest-resolution by
	// ValidateRecipeProviderBinding, not at Recipe load time.
	ErrRecipeRule4 = errors.New("recipe rule 4: workload/workloadFrom requires provider workloadSchema")

	// ErrRecipeRule5: at least one input kind must be present.
	ErrRecipeRule5 = errors.New("recipe rule 5: at least one input kind required")

	// ErrRecipeRule6: filenames emitted by the artifacts input kind must
	// not collide. Two sub-classes: inline `artifacts:` keys clashing with
	// `artifactsFrom:` basenames; and two or more `artifactsFrom:` entries
	// whose basenames collide. Both classes share this sentinel.
	ErrRecipeRule6 = errors.New("recipe rule 6: artifact-emitted output filename collision")

	// ErrRecipeHelmChartInlineArtifacts: provider helm-chart with inline
	// artifacts: declared. Helm-chart emits an umbrella chart structure;
	// the artifacts input kind contributes via physical files (declared
	// under artifactsFrom:) that the engine injects into the chart's
	// templates/ directory pre-render. Authors declare artifactsFrom:
	// instead of inline artifacts:. The descriptive sentinel name (rather
	// than continuing the ErrRecipeRule<N> sequence used for the
	// cross-provider shape rules 1-7) reflects that this is a
	// provider-specific constraint, not a member of chapter 16's numbered
	// rule list.
	ErrRecipeHelmChartInlineArtifacts = errors.New("recipe helm-chart provider does not support inline artifacts:")

	// ErrRecipeRule7: emission-time filename collision across input-kind
	// origins (source output, workload synthesis, artifacts). RESERVED
	// for Phase B.5 emission-time use; not surfaced by ValidateRecipe at
	// load time. The sentinel is declared here so the load-time validator
	// and the emission-time detector share a single RecipeValidationError
	// shape and a single CATALOG_ERROR message format.
	ErrRecipeRule7 = errors.New("recipe rule 7: filename collision across input-kind origins at emission")

	// ErrRecipeEvalProviderConstraint: the eval provider executes formatting
	// in-memory and has no rendering surface; declaring source/workload/
	// workloadFrom/artifacts/artifactsFrom on an eval Recipe is rejected.
	// The eval carve-out also exempts the Recipe from rule 5 — eval Recipes
	// legitimately declare only `provides:` and have zero input kinds.
	ErrRecipeEvalProviderConstraint = errors.New("recipe eval-provider constraint: input-kind fields are forbidden when provider is eval")
)

// RecipeValidationError carries the structured detail for a load-time or
// post-Manifest-resolution Recipe validation failure. The Error() output is
// the CATALOG_ERROR-prefixed message format documented in the user-facing
// fatal-halts reference.
type RecipeValidationError struct {
	// Path is the file path the Recipe was loaded from (e.g.,
	// "aggrega-catalog/recipe/helm/mariadb-bitnami/recipe.yaml").
	Path string
	// Name is the Recipe's metadata.name.
	Name string
	// Version is the Recipe's metadata.version.
	Version string
	// Rule is the violated rule identifier — typically a number 1..7. For
	// the eval-provider constraint it is the string "eval".
	Rule string
	// Description is the human-readable rule description (e.g., "valuesMapping
	// requires source"). Authored by the validator at the call site.
	Description string
	// Detail is rule-specific context (for rule 6: the colliding basenames;
	// for rule 4: the Provider name and missing capability).
	Detail string
	// Err is the sentinel for errors.Is matching.
	Err error
}

// Error formats the validation failure as a CATALOG_ERROR-prefixed multi-line
// message. The shape is shared with the emission-time rule 7 detector so
// load-time and render-time Recipe errors look identical to the operator.
func (e *RecipeValidationError) Error() string {
	header := fmt.Sprintf("CATALOG_ERROR: Recipe %s (%s:%s) violates rule %s:",
		e.Path, e.Name, e.Version, e.Rule)
	out := header + "\n  " + e.Description
	if e.Detail != "" {
		out += "\n  " + e.Detail
	}
	return out
}

// Unwrap returns the sentinel for errors.Is matching.
func (e *RecipeValidationError) Unwrap() error {
	return e.Err
}
