// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

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
// Rule 7 is enforced at emission time (Phase B.5 filename collision detection)
// and is reserved here so the load-time validator and the emission-time
// detector share an error shape.

// ErrRecipeRule1: valuesMapping requires source.
const ErrRecipeRule1 cerrors.Code = "validator/recipe-rule-1"

var _ = cerrors.Register(ErrRecipeRule1, cerrors.Meta{
	Message: "recipe rule 1: valuesMapping requires source",
})

// ErrRecipeRule5: at least one input kind must be present.
const ErrRecipeRule5 cerrors.Code = "validator/recipe-rule-5"

var _ = cerrors.Register(ErrRecipeRule5, cerrors.Meta{
	Message: "recipe rule 5: at least one input kind required",
})

// ErrRecipeRule7: emission-time filename collision across input-kind
// origins (source output, workload synthesis, artifacts). RESERVED
// for Phase B.5 emission-time use; not surfaced by ValidateRecipe at
// load time. The sentinel is declared here so the load-time validator
// and the emission-time detector share a single RecipeValidationError
// shape and a single CATALOG_ERROR message format.
const ErrRecipeRule7 cerrors.Code = "validator/recipe-rule-7"

var _ = cerrors.Register(ErrRecipeRule7, cerrors.Meta{
	Message: "recipe rule 7: filename collision across input-kind origins at emission",
})

// ErrRecipeEvalProviderConstraint: the eval provider executes formatting
// in-memory and has no rendering surface; declaring source/workload/
// artifacts on an eval Recipe is rejected. The eval carve-out also
// exempts the Recipe from rule 5 — eval Recipes legitimately declare
// only `provides:` and have zero input kinds.
const ErrRecipeEvalProviderConstraint cerrors.Code = "validator/recipe-eval-provider-constraint"

var _ = cerrors.Register(ErrRecipeEvalProviderConstraint, cerrors.Meta{
	Message: "recipe eval-provider constraint: input-kind fields are forbidden when provider is eval",
})

// ErrRecipeOutputLabelReservedNamespace: an outputLabels key uses a
// platform-reserved namespace (aggrega.ai/ or app.kubernetes.io/). Those
// prefixes are owned by the fixed provenance set; a recipe may not stamp
// them, which also keeps a validated output-label set disjoint from the
// provenance set. Engine-agnostic: enforced at load, before any provider
// consumes the labels.
const ErrRecipeOutputLabelReservedNamespace cerrors.Code = "validator/recipe-output-label-reserved-namespace"

var _ = cerrors.Register(ErrRecipeOutputLabelReservedNamespace, cerrors.Meta{
	Message: "recipe output-labels constraint: key uses a reserved namespace",
})

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
	// Detail is rule-specific context (e.g., for rule 7: the colliding
	// basenames).
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

// ToCompilerError converts the structured RecipeValidationError into a
// *cerrors.CompilerError carrying CategoryCatalogError. Recipe rule
// violations are always catalog-domain — the Recipe is catalog content
// authored by Ops/Catalog Architects, never by the developer (Aggregate)
// or operator (Release).
//
// The Source location maps Path (the Recipe file path) into
// SourceLocation.File. The Message combines Description and Detail so the
// CompilerError-rendered output retains the structured detail the
// CATALOG_ERROR-prefixed Error() shape carries. Inner error chain
// preserved via cerrors.Wrap so errors.Is against the recipe-rule sentinels
// continues to match through the converted CompilerError.
func (e *RecipeValidationError) ToCompilerError() *cerrors.CompilerError {
	msg := e.Description
	if e.Detail != "" {
		msg = msg + " — " + e.Detail
	}
	return cerrors.Wrap(e, cerrors.CatalogError, cerrors.SourceLocation{File: e.Path}, msg)
}
