// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ManifestRecipeReferenceFaults reports every mapping value in a composed
// Manifest whose shape a Recipe or FeatureRecipe reference cannot express, one
// fault per value, in the order the Manifest declares them.
//
// The verdict is purely syntactic: it counts the separators in a string and
// needs no catalog at all. That is what lets it run before anything resolves —
// and running it there is the point. A mapping value carrying more segments than
// a reference accepts can never match a catalog entity, so whatever gate looks
// the value up first will report it as an entity the catalog does not hold, which
// sends the author looking for a missing recipe that is in fact present and
// correctly named. Judging the shape first means the shape fault is reported as
// a shape fault, and every absent-entity report that follows is true.
//
// It is scoped to Recipe and FeatureRecipe references — the Manifest's mapping
// values — and nothing else. A Component, Feature or Aggregate reference is the
// entity's name verbatim and carries no segment ceiling to violate, so the same
// count applied there would reject valid authoring.
//
// Every fault wraps [ErrMalformedRecipeReference] and quotes the accepted shapes
// from their single home, so a later grammar change reaches this reading without
// an edit here. Returns nil when every mapping value is well-shaped, and for a
// nil Manifest.
func ManifestRecipeReferenceFaults(manifest *types.Manifest) []error {
	if manifest == nil {
		return nil
	}
	var faults []error
	add := func(coordinate, key, uri, subject string) {
		n, tooMany := recipeReferenceSegments(uri)
		if !tooMany {
			return
		}
		faults = append(faults, fmt.Errorf(
			"%w: %s.%s maps to %q, which carries %d slash-separated segments; a %s accepts %s; correct the mapping value in the Manifest",
			ErrMalformedRecipeReference(), coordinate, key, uri, n, subject, acceptedRecipeReferenceShapes))
	}
	walkTier := func(coordinate string, tier *types.MappingTier, subject string) {
		if tier == nil {
			return
		}
		for _, k := range tier.Type.Keys() {
			if uri, ok := tier.Type.Get(k); ok {
				add(coordinate+".type", k, uri, subject)
			}
		}
		for _, k := range tier.Name.Keys() {
			if uri, ok := tier.Name.Get(k); ok {
				add(coordinate+".name", k, uri, subject)
			}
		}
	}

	m := &manifest.Spec.Mappings
	walkTier("spec.mappings.components", m.Components, "recipe reference")
	walkTier("spec.mappings.features", m.Features, "feature recipe reference")
	return faults
}
