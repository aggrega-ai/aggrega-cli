// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nodeRecipeResolution joins the Manifest's mapping table and this catalog's
// recipe index into the single answer about one component instance: which
// reference the mapping names for it, whether the catalog holds a Recipe there,
// which keys a version-tolerant match could not choose between, and which
// Recipes implement the instance's type.
//
// It is the derivation the compiler already performs, projected rather than
// repeated: the same mapping lookup and the same recipe lookup decide which
// Recipe a node renders through, and this reads their outcome instead of
// deciding anything of its own. That is what makes the answer available to a
// rule asked where the node set is complete without the rule reaching for the
// Manifest, which is a document a compilation-stage view does not carry.
//
// The pin candidates are resolved only for an instance no mapping reached. An
// instance the Manifest maps needs no candidate offered — the author has already
// chosen — and scanning the recipe index for one would pay for a set nothing
// reads. An instance whose lookup refused is offered none either: the fault is
// in the mapping table's keys rather than in a missing entry, so candidates
// would suggest the wrong edit.
//
// The candidates carry the scope they were searched in. A shell over a view that
// reads no catalog directory holds only the Recipes the lockfile pins, so its
// candidates are marked as enumerated from the pins alone, and a reader claiming
// that no Recipe implements the type can make that claim of the pins rather
// than of the catalog.
//
// A refused lookup yields no reference and no candidates, and the keys it could
// not choose between travel instead. That ordering is the whole reason the keys
// are a field: a reader seeing an unmapped instance and a reader seeing a
// refused lookup have to act differently, and an answer carrying only "not
// mapped" cannot tell them which they have.
//
// The candidates of an instance whose type names no version are the Recipes
// implementing the version the build resolves that type to, resolved through
// component — the view's own Component resolution, which a recipe shell over
// another backing does not hold and is therefore handed. It is the enumeration
// the import's draft Manifest reads, resolved the same way, so a drafted
// Manifest and this halt name the same Recipes.
//
// The instance's type at the version the build compiles it as is resolved
// through that same resolution, whatever the outcome, and travels with the
// answer: it is what the mapped Recipe's implements is judged against when the
// key that reached the instance names no version, and asking the one resolution
// keeps that judgement and the pin candidates agreeing about which version a
// bare type stands for. The type-axis key the lookup settled on travels too,
// because it is the lookup alone that knows it.
func (c *Catalog) nodeRecipeResolution(
	manifest *types.Manifest,
	instanceName string,
	componentType string,
	component componentResolver,
) resolver.NodeRecipeResolution {
	resolved := implementsRequest(componentType, component)
	match, mapped, err := componentMappingEntry(manifest, instanceName, componentType)
	if keys, refused := ambiguousMappingKeys(err); refused {
		return resolver.NodeRecipeResolution{AmbiguousKeys: keys, ResolvedType: resolved}
	}
	if err != nil {
		// No other refusal shape exists today, and one added later is reported as
		// an instance nothing mapped rather than silently resolved: this is a
		// projection of a lookup it does not own, so inventing an outcome for a
		// failure it cannot classify would state something about the mapping table
		// that the lookup never said.
		return resolver.NodeRecipeResolution{ResolvedType: resolved}
	}
	if mapped {
		_, present := c.RecipeByURI(match.reference)
		return resolver.NodeRecipeResolution{
			Reference:    match.reference,
			Mapped:       true,
			Present:      present,
			TypeKey:      match.typeKey,
			MappedByType: match.byType,
			ResolvedType: resolved,
		}
	}
	return resolver.NodeRecipeResolution{
		PinCandidates:              recipesImplementing(c.Recipes, componentType, component),
		PinCandidatesFromPinsAlone: c.recipeSetsFromPinsAlone,
		ResolvedType:               resolved,
	}
}
