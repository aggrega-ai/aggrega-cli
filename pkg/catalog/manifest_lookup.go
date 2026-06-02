// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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

// ErrAmbiguousTypeMapping fires when a component/feature type resolves to more
// than one version-distinct Manifest mapping key under version-tolerant
// matching — the lookup cannot pick one without guessing, so it halts loudly
// rather than silently resolving the first.
var ErrAmbiguousTypeMapping = errors.New("manifest maps multiple version-distinct keys to the same base type; the type-to-recipe lookup is ambiguous")

// versionTolerantTypeGet resolves a component / feature TYPE to its mapped recipe
// URI in typeMap, tolerant of a version mismatch between the authored type and
// the mapping key. It tries an exact key match first (so an exact-version key
// wins), then falls back to a BASE match: the authored type and a mapping key
// resolve together when their stripVersion bases are equal.
//
// The tolerance is base-equality, NOT semver-range satisfaction — the
// type-to-recipe mapping only needs to find the recipe; the concrete version is
// pinned downstream by the closure's minimal-version selection. A caret key
// (`core/x:^1.0.0`) base-matches like any other; a single base match resolves; a
// base shared by multiple version-distinct keys is ambiguous and returns
// ErrAmbiguousTypeMapping (a Loud Guardrail), never a silent first-wins.
func versionTolerantTypeGet(typeMap types.OrderedMap[string], lookupType string) (string, bool, error) {
	if uri, ok := typeMap.Get(lookupType); ok {
		return uri, true, nil
	}
	base := stripVersion(lookupType)
	var matchURI string
	var matchedKeys []string
	for key, uri := range typeMap.All() {
		if stripVersion(key) == base {
			matchURI = uri
			matchedKeys = append(matchedKeys, key)
		}
	}
	switch len(matchedKeys) {
	case 0:
		return "", false, nil
	case 1:
		return matchURI, true, nil
	default:
		return "", false, fmt.Errorf("%w: type %q matches version-distinct keys %v — pin the authored type's version or remove the duplicate mapping keys", ErrAmbiguousTypeMapping, lookupType, matchedKeys)
	}
}

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
		// Layer 1: flat-shape mapping. A version-ambiguous mapping error is
		// swallowed to a miss here — render is resilient via the Layer-3
		// implements fallback below; the loud ErrAmbiguousTypeMapping is the
		// pull/source-descent contract (ComponentRecipeURI), where a wrong pick
		// mis-pins a source.
		if uri, ok, err := flatComponentURI(manifest, componentType); err == nil && ok {
			if r, found := c.RecipeByURI(uri); found {
				return &r, true
			}
			// Manifest mapped this type to a URI that doesn't resolve;
			// fall through to subsequent layers so legacy fixtures don't
			// break, but in production this would surface as a CATALOG_ERROR
			// at the consistency-check step.
		}
		// Layer 2: legacy v2 per-provider mapping.
		if uri, ok, err := legacyComponentURI(manifest, componentType); err == nil && ok {
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
		if uri, ok, err := flatFeatureURI(manifest, featureType); err == nil && ok {
			if fr, found := c.FeatureRecipeByURI(uri); found {
				return &fr, true
			}
		}
		if uri, ok, err := legacyFeatureURI(manifest, featureType); err == nil && ok {
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
func flatComponentURI(manifest *types.Manifest, componentType string) (string, bool, error) {
	if manifest == nil || manifest.Spec.Mappings.Components == nil {
		return "", false, nil
	}
	return versionTolerantTypeGet(manifest.Spec.Mappings.Components.Type, componentType)
}

// legacyComponentURI walks the legacy v2 per-provider mappings looking
// for componentType. Returns the first match across providers in
// author order. Pre-migration semantics: the renderer dispatched per
// (path, provider) and the right provider's mapping won; for the
// transitional overlay, we accept the first match as a sufficient
// fallback (the legacy fixtures with populated mappings all carry
// the single-provider shape).
func legacyComponentURI(manifest *types.Manifest, componentType string) (string, bool, error) {
	if manifest == nil {
		return "", false, nil
	}
	for _, pm := range manifest.Spec.Mappings.Legacy.All() {
		if pm.Components == nil {
			continue
		}
		uri, ok, err := versionTolerantTypeGet(pm.Components.Type, componentType)
		if err != nil {
			return "", false, err
		}
		if ok {
			return uri, true, nil
		}
	}
	return "", false, nil
}

// ComponentRecipeURI returns the Recipe URI the Manifest maps componentType
// to — the flat-shape mapping first, then the legacy per-provider shape. The
// returned URI is the same verbatim string the dependency-closure walker
// records as a source-descent entity, so callers keying per-component data by
// this value join cleanly against the closure's entities (regardless of
// whether the author wrote the mapping value in short form or full-path form).
//
// Returns ("", false) when no Manifest mapping names componentType. Such a
// component carries no source-descent entity — it would be selected for render
// by spec.implements, which is a render-selection path, not a
// source-acquisition path — so it correctly contributes no source artifact.
func ComponentRecipeURI(manifest *types.Manifest, componentType string) (string, bool, error) {
	if uri, ok, err := flatComponentURI(manifest, componentType); err != nil {
		return "", false, err
	} else if ok {
		return uri, true, nil
	}
	return legacyComponentURI(manifest, componentType)
}

// FeatureRecipeURI is the FeatureRecipe analogue of ComponentRecipeURI: it
// returns the FeatureRecipe URI the Manifest maps featureType to — the
// flat-shape mapping first, then the legacy per-provider shape. The returned URI
// is the same verbatim string the dependency-closure walker records as a
// feature_recipe source-descent entity, so callers keying per-feature-instance
// data by this value join cleanly against the closure's entities.
//
// Returns ("", false) when no Manifest mapping names featureType.
func FeatureRecipeURI(manifest *types.Manifest, featureType string) (string, bool, error) {
	if uri, ok, err := flatFeatureURI(manifest, featureType); err != nil {
		return "", false, err
	} else if ok {
		return uri, true, nil
	}
	return legacyFeatureURI(manifest, featureType)
}

// flatFeatureURI is the Feature analogue of flatComponentURI.
func flatFeatureURI(manifest *types.Manifest, featureType string) (string, bool, error) {
	if manifest == nil || manifest.Spec.Mappings.Features == nil {
		return "", false, nil
	}
	return versionTolerantTypeGet(manifest.Spec.Mappings.Features.Type, featureType)
}

// legacyFeatureURI is the Feature analogue of legacyComponentURI.
func legacyFeatureURI(manifest *types.Manifest, featureType string) (string, bool, error) {
	if manifest == nil {
		return "", false, nil
	}
	for _, pm := range manifest.Spec.Mappings.Legacy.All() {
		if pm.Features == nil {
			continue
		}
		uri, ok, err := versionTolerantTypeGet(pm.Features.Type, featureType)
		if err != nil {
			return "", false, err
		}
		if ok {
			return uri, true, nil
		}
	}
	return "", false, nil
}

// stripVersion removes the :version segment from a URI, returning the
// base. Used by the consistency check to compare base-URI portions
// independent of version form.
func stripVersion(uri string) string {
	base, _, _ := splitVersionedURI(uri)
	return base
}
