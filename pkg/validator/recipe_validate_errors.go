// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Recipe-validation sentinels. The shape rules constrain how a Recipe composes
// its input kinds. Each rule is named by what it constrains, and that name is
// the identifier the diagnostic prints and the reference documentation heads
// its entry with.
//
// Shape rules surfaced at Recipe load time: the source entry, values-mapping and
// input-kinds. The workload-requires-provider-workloadSchema constraint is enforced at
// render time by the central input-kind gate (a synthesized Recipe bound to a
// provider with no workload-synthesis capability halts loudly there).
// The filename-collision rule is enforced once each emitting document's own
// emission is complete, which is the earliest point the names a block produced
// are known; its identity is declared here so every rule constraining a Recipe
// reports in one shape whatever surface raises it.
//
// Every rule here reports through NewShapeRuleError, so a Recipe failure is a
// *ValidationError whatever stage detected it.

// ErrEmptySource is the Loud Guardrail halt raised when a Recipe writes its
// source entry and leaves it an empty string. A Recipe's source selects between
// two authoring modes: sourceless, where the output is built entirely from the
// Recipe's other input kinds, and sourced, where the source names a chart, module,
// or file to render from. An absent source selects the first; a non-empty value
// selects the second; an empty string selects neither, so it is an authoring
// ambiguity rather than a value with a meaning.
//
// Admitting it as sourceless is the failure this halt exists to stop: the
// commonest way to write it is to delete a URI and leave the key behind, where
// the author meant sourced mode and the build would have shipped without the
// artifact, never saying which of the two readings it took. Silently resolving
// two authoring intents onto one behaviour is the silent reinterpretation the
// no-ghost-infrastructure invariant forbids, and its answer is a halt naming
// what to write instead.
//
// One condition, provider-agnostic: the ambiguity is a property of the authored
// Recipe and is identical whichever target renders it, so a reader comparing two
// providers meets one identity for one mistake rather than one per provider. It
// is raised by ValidateRecipe as the Recipe is loaded, so a pull, a build and a
// check of one Recipe or of a Release all name this condition, before anything
// tries to read the empty value as a path or infer a source kind from it. The
// diagnostic is positioned at the Recipe's source entry and names both
// valid authoring paths — omit the field, or give it a real path — so the
// correction is readable from the halt. Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrEmptySource()).
func ErrEmptySource() error { return errEmptySource }

var errEmptySource = cerrors.Register("recipe-source-is-empty", cerrors.Meta{
	ShortCode: "E0251",
	Message:   "recipe source is an empty string, which names neither sourceless mode nor sourced mode",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrRecipeValuesMappingRequiresSource: valuesMapping requires source.
func ErrRecipeValuesMappingRequiresSource() error { return errRecipeValuesMappingRequiresSource }

var errRecipeValuesMappingRequiresSource = cerrors.Register("recipe-values-mapping-requires-source", cerrors.Meta{
	ShortCode: "E0209",
	Message:   "Recipe: valuesMapping requires source",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrRecipeNoInputKind: at least one input kind must be present.
func ErrRecipeNoInputKind() error { return errRecipeNoInputKind }

var errRecipeNoInputKind = cerrors.Register("recipe-no-input-kind", cerrors.Meta{
	ShortCode: "E0210",
	Message:   "Recipe: at least one input kind required",
	RulePlace: cerrors.RuleOverAuthoredInput(),
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

var errRecipeFilenameCollisionAcrossInputKinds = cerrors.Register("recipe-filename-collision-across-input-kinds", cerrors.Meta{
	ShortCode: "E0211",
	Message:   "filename collision across input kinds: two input-kind blocks produced a file under one output name",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrRecipeOutputLabelReservedNamespace: an outputLabels key uses a
// platform-reserved namespace (aggrega.ai/ or app.kubernetes.io/). Those
// prefixes are owned by the fixed provenance set; a recipe may not stamp
// them, which also keeps a validated output-label set disjoint from the
// provenance set. Engine-agnostic: enforced at load, before any provider
// consumes the labels.
func ErrRecipeOutputLabelReservedNamespace() error { return errRecipeOutputLabelReservedNamespace }

var errRecipeOutputLabelReservedNamespace = cerrors.Register("recipe-output-label-reserved-namespace", cerrors.Meta{
	ShortCode: "E0208",
	Message:   "recipe output-labels constraint: key uses a reserved namespace",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrRecipeOutputComponentLabelCollision: a key promoted through
// outputComponentLabels is also declared as a literal key in outputLabels.
// The two blocks stamp onto the same label surface, so a key named in both
// would carry a value from two sources — the component's own label and the
// literal written in place — with nothing to say which wins. Rejecting the
// overlap at load keeps every stamped key traceable to exactly one source.
func ErrRecipeOutputComponentLabelCollision() error { return errRecipeOutputComponentLabelCollision }

var errRecipeOutputComponentLabelCollision = cerrors.Register("recipe-output-component-label-collision", cerrors.Meta{
	ShortCode: "E0207",
	Message:   "recipe output-component-labels constraint: key is also declared in outputLabels",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})
