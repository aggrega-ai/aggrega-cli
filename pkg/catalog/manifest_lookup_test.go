// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// fakeRecipe builds a Recipe with metadata.name URI and a spec.implements.
func fakeRecipe(uri, implements string) types.Recipe {
	return types.Recipe{
		Metadata: types.RecipeMetadata{Name: uri, Version: "1.0.0"},
		Spec:     types.RecipeSpec{Provider: "helm", Implements: implements},
	}
}

// fakeCatalog builds a Catalog populated with the given recipes. The
// Recipes map is keyed by spec.implements per the catalog's existing
// convention; RecipeByURI walks the map by metadata.name URI.
func fakeCatalog(recipes ...types.Recipe) *Catalog {
	c := &Catalog{
		Components:     map[string]types.Component{},
		Features:       map[string]types.Feature{},
		Recipes:        map[string]types.Recipe{},
		FeatureRecipes: map[string]types.FeatureRecipe{},
		Aggregates:     map[string]types.Aggregate{},
		Providers:      map[string]types.Provider{},
	}
	for _, r := range recipes {
		c.Recipes[r.Spec.Implements] = r
	}
	return c
}

// TestRecipeByURI_DirectMatch pins the URI-keyed direct lookup.
func TestRecipeByURI_DirectMatch(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/database/postgres"),
	)
	r, ok := c.RecipeByURI("core/helm/postgres-bitnami")
	if !ok {
		t.Fatal("expected RecipeByURI hit")
	}
	if r.Spec.Implements != "core/database/postgres" {
		t.Errorf("wrong recipe resolved: %q", r.Spec.Implements)
	}
}

// TestRecipeLookupWithManifest_FlatShape pins layer-1 dispatch: a flat
// Manifest mapping resolves directly via RecipeByURI; the
// spec.implements walking is bypassed.
func TestRecipeLookupWithManifest_FlatShape(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/database/postgres"),
	)
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/database/postgres", "core/helm/postgres-bitnami")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flatComponents},
		},
	}
	lookup := c.RecipeLookupWithManifest(manifest)
	r, ok := lookup.Recipe("core/database/postgres")
	if !ok {
		t.Fatal("expected flat-shape dispatch to hit")
	}
	if r.Metadata.Name != "core/helm/postgres-bitnami" {
		t.Errorf("wrong recipe resolved: %q", r.Metadata.Name)
	}
}

// TestRecipeLookupWithManifest_LegacyFallback pins layer-2: when the
// flat shape is absent, the legacy v2 per-provider shape resolves.
func TestRecipeLookupWithManifest_LegacyFallback(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/database/postgres"),
	)
	legacyHelm := types.ProviderMappings{Components: &types.MappingTier{}}
	legacyHelm.Components.Type.Set("core/database/postgres", "core/helm/postgres-bitnami")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{},
		},
	}
	manifest.Spec.Mappings.Legacy.Set("helm", legacyHelm)
	lookup := c.RecipeLookupWithManifest(manifest)
	r, ok := lookup.Recipe("core/database/postgres")
	if !ok {
		t.Fatal("expected legacy-shape dispatch to hit")
	}
	if r.Metadata.Name != "core/helm/postgres-bitnami" {
		t.Errorf("wrong recipe resolved: %q", r.Metadata.Name)
	}
}

// TestRecipeLookupWithManifest_MatchImplementsFinalFallback pins
// layer-3: when neither flat nor legacy mappings are present, the
// dispatch falls through to matchImplements walking spec.implements
// (pre-migration behavior preserved).
func TestRecipeLookupWithManifest_MatchImplementsFinalFallback(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/database/postgres"),
	)
	// Empty Manifest mappings — neither flat nor legacy populated.
	manifest := &types.Manifest{Spec: types.ManifestSpec{}}
	lookup := c.RecipeLookupWithManifest(manifest)
	r, ok := lookup.Recipe("core/database/postgres")
	if !ok {
		t.Fatal("expected matchImplements fallback to hit")
	}
	if r.Spec.Implements != "core/database/postgres" {
		t.Errorf("wrong recipe resolved: %q", r.Spec.Implements)
	}
}

// TestRecipeLookupWithManifest_FlatWinsOverLegacy pins the dispatch
// precedence pre-decided at the plan gate: when both flat and legacy
// mappings carry the same component type, the flat-shape mapping
// resolves. Uses distinct spec.implements values so both Recipes
// survive the catalog's spec.implements-keyed Recipes map (today's
// catalog map keying — wave 6's catalog refactor handles multi-Recipe-
// per-implements via the RecipeKey shape; for this WI the precedence
// behavior is demonstrable without that refactor).
func TestRecipeLookupWithManifest_FlatWinsOverLegacy(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-flat", "core/database/postgres-A"),
		fakeRecipe("core/helm/postgres-legacy", "core/database/postgres-B"),
	)
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/database/postgres-A", "core/helm/postgres-flat")
	legacyHelm := types.ProviderMappings{Components: &types.MappingTier{}}
	legacyHelm.Components.Type.Set("core/database/postgres-A", "core/helm/postgres-legacy")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flatComponents},
		},
	}
	manifest.Spec.Mappings.Legacy.Set("helm", legacyHelm)
	lookup := c.RecipeLookupWithManifest(manifest)
	r, _ := lookup.Recipe("core/database/postgres-A")
	if r.Metadata.Name != "core/helm/postgres-flat" {
		t.Errorf("expected flat-shape to win; got %q", r.Metadata.Name)
	}
}

// TestVerifyManifestImplementsConsistency_PassOnMatch pins the
// consistency-check pass path: flat-shape mappings whose resolved
// recipes carry matching spec.implements produce no errors.
func TestVerifyManifestImplementsConsistency_PassOnMatch(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/database/postgres"),
	)
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/database/postgres", "core/helm/postgres-bitnami")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flatComponents},
		},
	}
	errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml")
	if len(errs) != 0 {
		t.Errorf("expected no consistency errors; got %v", errs)
	}
}

// TestVerifyManifestImplementsConsistency_FailOnMismatch pins the
// fail path: a mapping pointing at a Recipe whose spec.implements
// differs from the mapping key produces ErrManifestImplementsMismatch.
func TestVerifyManifestImplementsConsistency_FailOnMismatch(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/wrong-recipe", "core/database/mysql"),
	)
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/database/postgres", "core/helm/wrong-recipe")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flatComponents},
		},
	}
	errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml")
	if len(errs) == 0 {
		t.Fatal("expected ErrManifestImplementsMismatch")
	}
	if !errors.Is(errs[0], ErrManifestImplementsMismatch) {
		t.Errorf("expected ErrManifestImplementsMismatch in chain; got %v", errs[0])
	}
}

// TestVerifyManifestImplementsConsistency_NilManifest pins boundary:
// nil manifest returns nil (no consistency errors to surface).
func TestVerifyManifestImplementsConsistency_NilManifest(t *testing.T) {
	t.Parallel()
	c := fakeCatalog()
	if errs := c.VerifyManifestImplementsConsistency(nil, ""); errs != nil {
		t.Errorf("expected nil on nil manifest; got %v", errs)
	}
}
