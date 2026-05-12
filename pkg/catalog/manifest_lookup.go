// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"fmt"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ErrManifestImplementsMismatch fires when a Manifest mapping entry's
// resolved Recipe (or FeatureRecipe) carries a spec.implements that
// doesn't match the Manifest's component-type / feature-type key for
// that entry. The mismatch is a structural consistency invariant per
// the schema-language migration's mapping authoring surface: the
// Manifest entry IS the Recipe URI; the Recipe's spec.implements is
// a consistency anchor, not a dispatch mechanism. Mismatches halt at
// workspace load.
var ErrManifestImplementsMismatch = errors.New("manifest mapping entry's recipe spec.implements does not match the mapped component-type / feature-type key")

// RecipeLookupWithManifest returns a renderer.RecipeLookup whose Recipe
// and FeatureRecipe closures dispatch through a three-layer cascade
// when a Manifest is supplied:
//
//  1. Flat-shape lookup (manifest.Spec.Mappings.Components.Type and
//     .Features.Type) — when populated, resolve the mapping entry's
//     Recipe URI directly against the catalog by Recipe URI (not by
//     spec.implements).
//  2. Legacy v2 per-provider lookup (manifest.Spec.Mappings.Legacy) —
//     unchanged from pre-migration behavior; included as a transitional
//     fallback while fixtures still author v2 shape.
//  3. matchImplements walking spec.implements — final fallback for
//     fixtures that don't author Manifest mappings at all.
//
// Each layer is active for the fixtures that opt in. When manifest is
// nil, the cascade collapses to layer 3 only — semantically identical
// to RecipeLookup().
func (c *Catalog) RecipeLookupWithManifest(manifest *types.Manifest) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe:        c.componentTypeToRecipeWithManifest(manifest),
		FeatureRecipe: c.featureTypeToFeatureRecipeWithManifest(manifest),
	}
}

func (c *Catalog) componentTypeToRecipeWithManifest(manifest *types.Manifest) func(string) (*types.Recipe, bool) {
	return func(componentType string) (*types.Recipe, bool) {
		// Layer 1: flat-shape mapping.
		if uri, ok := flatComponentURI(manifest, componentType); ok {
			if r, found := c.RecipeByURI(uri); found {
				return &r, true
			}
			// Manifest mapped this type to a URI that doesn't resolve;
			// fall through to subsequent layers so legacy fixtures don't
			// break, but in production this would surface as a CATALOG_ERROR
			// at the consistency-check step.
		}
		// Layer 2: legacy v2 per-provider mapping.
		if uri, ok := legacyComponentURI(manifest, componentType); ok {
			if r, found := c.RecipeByURI(uri); found {
				return &r, true
			}
		}
		// Layer 3: matchImplements walking spec.implements.
		r, ok := matchImplements(c.Recipes, componentType)
		if !ok {
			return nil, false
		}
		return &r, true
	}
}

func (c *Catalog) featureTypeToFeatureRecipeWithManifest(manifest *types.Manifest) func(string) (*types.FeatureRecipe, bool) {
	return func(featureType string) (*types.FeatureRecipe, bool) {
		if uri, ok := flatFeatureURI(manifest, featureType); ok {
			if fr, found := c.FeatureRecipeByURI(uri); found {
				return &fr, true
			}
		}
		if uri, ok := legacyFeatureURI(manifest, featureType); ok {
			if fr, found := c.FeatureRecipeByURI(uri); found {
				return &fr, true
			}
		}
		fr, ok := matchImplements(c.FeatureRecipes, featureType)
		if !ok {
			return nil, false
		}
		return &fr, true
	}
}

// RecipeByURI resolves a Recipe URI directly against the catalog. The
// URI carries the canonical namespace/provider/name shape with an
// optional version segment (exact, caret-range, or unversioned per
// the three-form matching rules). The function walks the catalog's
// Recipes map applying matchImplements semantics against the Recipe
// URIs (not the spec.implements strings).
func (c *Catalog) RecipeByURI(uri string) (types.Recipe, bool) {
	// The catalog's Recipes map is keyed by spec.implements today, not
	// by Recipe URI. Build a transient URI-keyed view (metadata.name)
	// once per lookup. The catalog is small enough today (<100 entries)
	// that the per-call allocation is negligible; future work
	// promotes this to a pre-built index when scale demands it.
	uriIndex := make(map[string]types.Recipe, len(c.Recipes))
	for _, r := range c.Recipes {
		uriIndex[r.Metadata.Name] = r
	}
	return matchImplements(uriIndex, uri)
}

// FeatureRecipeByURI is the FeatureRecipe analogue of RecipeByURI.
func (c *Catalog) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	uriIndex := make(map[string]types.FeatureRecipe, len(c.FeatureRecipes))
	for _, fr := range c.FeatureRecipes {
		uriIndex[fr.Metadata.Name] = fr
	}
	return matchImplements(uriIndex, uri)
}

// VerifyManifestImplementsConsistency walks every populated mapping
// entry in the Manifest and verifies the resolved Recipe / FeatureRecipe's
// spec.implements matches the Manifest's component-type / feature-type
// key for that entry. Mismatches produce ErrManifestImplementsMismatch
// wrapped with file:line provenance on both sides; the function
// collects all violations rather than short-circuiting on the first.
//
// Returns nil when manifest is nil, when no flat-shape mappings are
// populated, or when every populated mapping resolves consistently.
// Legacy v2 mappings are not consistency-checked at this surface —
// they keep pre-migration semantics (Recipe lookup via spec.implements
// walking) where the consistency is implicit in the matching rule.
func (c *Catalog) VerifyManifestImplementsConsistency(manifest *types.Manifest, manifestPath string) []error {
	if manifest == nil {
		return nil
	}
	var errs []error
	// Component mappings.
	if manifest.Spec.Mappings.Components != nil {
		for compType, recipeURI := range manifest.Spec.Mappings.Components.Type.All() {
			r, ok := c.RecipeByURI(recipeURI)
			if !ok {
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch, cerrors.CatalogError, cerrors.SourceLocation{File: manifestPath}, fmt.Sprintf("manifest mapping %q -> %q: recipe not found in catalog", compType, recipeURI)))
				continue
			}
			recipeImplements := stripVersion(r.Spec.Implements)
			expected := stripVersion(compType)
			if recipeImplements != expected {
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch, cerrors.CatalogError, cerrors.SourceLocation{File: manifestPath}, fmt.Sprintf("manifest mapping %q -> %q: recipe spec.implements is %q (expected %q)", compType, recipeURI, r.Spec.Implements, compType)))
			}
		}
	}
	// Feature mappings.
	if manifest.Spec.Mappings.Features != nil {
		for featType, frURI := range manifest.Spec.Mappings.Features.Type.All() {
			fr, ok := c.FeatureRecipeByURI(frURI)
			if !ok {
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch, cerrors.CatalogError, cerrors.SourceLocation{File: manifestPath}, fmt.Sprintf("manifest feature mapping %q -> %q: featurerecipe not found in catalog", featType, frURI)))
				continue
			}
			frImplements := stripVersion(fr.Spec.Implements)
			expected := stripVersion(featType)
			if frImplements != expected {
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch, cerrors.CatalogError, cerrors.SourceLocation{File: manifestPath}, fmt.Sprintf("manifest feature mapping %q -> %q: featurerecipe spec.implements is %q (expected %q)", featType, frURI, fr.Spec.Implements, featType)))
			}
		}
	}
	return errs
}

// flatComponentURI returns the Recipe URI mapped to the given
// componentType in the Manifest's flat-shape mapping, when populated.
func flatComponentURI(manifest *types.Manifest, componentType string) (string, bool) {
	if manifest == nil || manifest.Spec.Mappings.Components == nil {
		return "", false
	}
	return manifest.Spec.Mappings.Components.Type.Get(componentType)
}

// legacyComponentURI walks the legacy v2 per-provider mappings looking
// for componentType. Returns the first match across providers in
// author order. Pre-migration semantics: the renderer dispatched per
// (path, provider) and the right provider's mapping won; for the
// transitional overlay, we accept the first match as a sufficient
// fallback (the legacy fixtures with populated mappings all carry
// the single-provider shape).
func legacyComponentURI(manifest *types.Manifest, componentType string) (string, bool) {
	if manifest == nil {
		return "", false
	}
	for _, pm := range manifest.Spec.Mappings.Legacy.All() {
		if pm.Components == nil {
			continue
		}
		if uri, ok := pm.Components.Type.Get(componentType); ok {
			return uri, true
		}
	}
	return "", false
}

// flatFeatureURI is the Feature analogue of flatComponentURI.
func flatFeatureURI(manifest *types.Manifest, featureType string) (string, bool) {
	if manifest == nil || manifest.Spec.Mappings.Features == nil {
		return "", false
	}
	return manifest.Spec.Mappings.Features.Type.Get(featureType)
}

// legacyFeatureURI is the Feature analogue of legacyComponentURI.
func legacyFeatureURI(manifest *types.Manifest, featureType string) (string, bool) {
	if manifest == nil {
		return "", false
	}
	for _, pm := range manifest.Spec.Mappings.Legacy.All() {
		if pm.Features == nil {
			continue
		}
		if uri, ok := pm.Features.Type.Get(featureType); ok {
			return uri, true
		}
	}
	return "", false
}

// stripVersion removes the :version segment from a URI, returning the
// base. Used by the consistency check to compare base-URI portions
// independent of version form.
func stripVersion(uri string) string {
	base, _, _ := splitVersionedURI(uri)
	return base
}
