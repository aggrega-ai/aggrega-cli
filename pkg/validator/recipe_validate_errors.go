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
// render time by the central input-kind gate (a synthesized Recipe bound to a
// provider with no workload-synthesis capability halts loudly there).
// The filename-collision rule is enforced once each emitting document's own
// emission is complete, which is the earliest point the names a block produced
// are known; its identity is declared here so every rule constraining a Recipe
// reports in one shape whatever surface raises it.
//
// Every rule here reports through NewShapeRuleError, so a Recipe failure is a
// *ValidationError whatever stage detected it.

// ErrRecipeValuesMappingRequiresSource: valuesMapping requires source.
func ErrRecipeValuesMappingRequiresSource() error { return errRecipeValuesMappingRequiresSource }

var errRecipeValuesMappingRequiresSource = cerrors.Register("validator/recipe-values-mapping-requires-source", cerrors.Meta{
	ShortCode: "E0209",
	Message:   "Recipe: valuesMapping requires source",
})

// ErrRecipeNoInputKind: at least one input kind must be present.
func ErrRecipeNoInputKind() error { return errRecipeNoInputKind }

var errRecipeNoInputKind = cerrors.Register("validator/recipe-no-input-kind", cerrors.Meta{
	ShortCode: "E0210",
	Message:   "Recipe: at least one input kind required",
})

// ErrRecipeFilenameCollisionAcrossInputKinds: two input-kind blocks of one
// document produced a file under a single output name — a source render, a
// workload synthesis, an artifacts entry and a pipeline step each choose their
// output names independently, and nothing declares those names distinct.
//
// It is not surfaced by ValidateRecipe at load time, and it cannot be: which
// names a block produces is known only once that block has rendered. The
// condition is raised where the emission is complete, per emitting document.
//
// One identity covers all three emitting documents — a component's own Recipe,
// and the FeatureRecipe an attachment binds on a component or on a boundary —
// because it is one authoring fault with one remedy: rename the output in one of
// the two blocks that met. Minting a second identity per document would present
// a single fault as unrelated conditions, and the document a finding is about is
// already carried by its own position and detail.
func ErrRecipeFilenameCollisionAcrossInputKinds() error {
	return errRecipeFilenameCollisionAcrossInputKinds
}

var errRecipeFilenameCollisionAcrossInputKinds = cerrors.Register("validator/recipe-filename-collision-across-input-kinds", cerrors.Meta{
	ShortCode: "E0211",
	Message:   "filename collision across input kinds: two input-kind blocks produced a file under one output name",
})

// ErrRecipeEvalProviderConstraint: the eval provider executes formatting
// in-memory and has no rendering surface; declaring source/workload/
// artifacts on an eval Recipe is rejected. The eval carve-out also
// exempts the Recipe from the input-kinds rule — eval Recipes legitimately
// declare only `provides:` and have zero input kinds.
func ErrRecipeEvalProviderConstraint() error { return errRecipeEvalProviderConstraint }

var errRecipeEvalProviderConstraint = cerrors.Register("validator/recipe-eval-provider-constraint", cerrors.Meta{
	ShortCode: "E0206",
	Message:   "recipe eval-provider constraint: input-kind fields are forbidden when provider is eval",
})

// ErrRecipeOutputLabelReservedNamespace: an outputLabels key uses a
// platform-reserved namespace (aggrega.ai/ or app.kubernetes.io/). Those
// prefixes are owned by the fixed provenance set; a recipe may not stamp
// them, which also keeps a validated output-label set disjoint from the
// provenance set. Engine-agnostic: enforced at load, before any provider
// consumes the labels.
func ErrRecipeOutputLabelReservedNamespace() error { return errRecipeOutputLabelReservedNamespace }

var errRecipeOutputLabelReservedNamespace = cerrors.Register("validator/recipe-output-label-reserved-namespace", cerrors.Meta{
	ShortCode: "E0208",
	Message:   "recipe output-labels constraint: key uses a reserved namespace",
})

// ErrRecipeOutputComponentLabelCollision: a key promoted through
// outputComponentLabels is also declared as a literal key in outputLabels.
// The two blocks stamp onto the same label surface, so a key named in both
// would carry a value from two sources — the component's own label and the
// literal written in place — with nothing to say which wins. Rejecting the
// overlap at load keeps every stamped key traceable to exactly one source.
func ErrRecipeOutputComponentLabelCollision() error { return errRecipeOutputComponentLabelCollision }

var errRecipeOutputComponentLabelCollision = cerrors.Register("validator/recipe-output-component-label-collision", cerrors.Meta{
	ShortCode: "E0207",
	Message:   "recipe output-component-labels constraint: key is also declared in outputLabels",
})
