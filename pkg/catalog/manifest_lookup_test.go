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

// TestComponentRecipeURI_VerbatimMappingValue pins the source-descent join
// key: ComponentRecipeURI returns the Manifest mapping value verbatim — short
// form or full-path form — so the value matches the entity URI the dependency
// closure records for that Recipe. Per-component infra keyed by this value
// therefore joins against the closure regardless of mapping authoring style.
// A type with no mapping returns false (it carries no source-descent entity).
func TestComponentRecipeURI_VerbatimMappingValue(t *testing.T) {
	t.Parallel()
	flat := &types.MappingTier{}
	flat.Type.Set("mariadb-bitnami", "mariadb-bitnami:1.0.0")
	flat.Type.Set("core/database/postgres", "hub.aggrega.ai/recipe/helm/postgres:2.1.0")
	legacy := types.ProviderMappings{Components: &types.MappingTier{}}
	legacy.Components.Type.Set("core/cache/redis", "core/helm/redis-legacy:1.0.0")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flat},
		},
	}
	manifest.Spec.Mappings.Legacy.Set("helm", legacy)

	cases := []struct {
		name, compType, wantURI string
		wantOK                  bool
	}{
		{"short-form flat", "mariadb-bitnami", "mariadb-bitnami:1.0.0", true},
		{"full-path flat", "core/database/postgres", "hub.aggrega.ai/recipe/helm/postgres:2.1.0", true},
		{"legacy fallback", "core/cache/redis", "core/helm/redis-legacy:1.0.0", true},
		{"unmapped type", "core/queue/nats", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri, ok, err := ComponentRecipeURI(manifest, tc.compType)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v; want %v", ok, tc.wantOK)
			}
			if uri != tc.wantURI {
				t.Errorf("uri = %q; want %q", uri, tc.wantURI)
			}
		})
	}
}

// TestFeatureRecipeURI_VerbatimMappingValue is the FeatureRecipe analogue of
// TestComponentRecipeURI_VerbatimMappingValue: FeatureRecipeURI returns the
// Manifest feature-mapping value verbatim (short form or full path), matching the
// feature_recipe entity URI the dependency closure records, so per-feature-instance
// infra keyed by this value joins against the closure.
func TestFeatureRecipeURI_VerbatimMappingValue(t *testing.T) {
	t.Parallel()
	flat := &types.MappingTier{}
	flat.Type.Set("tls-cert", "tls-cert:1.0.0")
	flat.Type.Set("core/security/tls", "hub.aggrega.ai/feature-recipe/helm/tls:2.1.0")
	legacy := types.ProviderMappings{Features: &types.MappingTier{}}
	legacy.Features.Type.Set("core/observability/otel", "core/feature-recipe/otel-legacy:1.0.0")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Features: flat},
		},
	}
	manifest.Spec.Mappings.Legacy.Set("helm", legacy)

	cases := []struct {
		name, featType, wantURI string
		wantOK                  bool
	}{
		{"short-form flat", "tls-cert", "tls-cert:1.0.0", true},
		{"full-path flat", "core/security/tls", "hub.aggrega.ai/feature-recipe/helm/tls:2.1.0", true},
		{"legacy fallback", "core/observability/otel", "core/feature-recipe/otel-legacy:1.0.0", true},
		{"unmapped type", "core/queue/nats", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri, ok, err := FeatureRecipeURI(manifest, tc.featType)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v; want %v", ok, tc.wantOK)
			}
			if uri != tc.wantURI {
				t.Errorf("uri = %q; want %q", uri, tc.wantURI)
			}
		})
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
// precedence: when both flat and legacy mappings carry the same component
// type, the flat-shape mapping resolves. Uses distinct spec.implements
// values so both Recipes survive the catalog's spec.implements-keyed Recipes
// map; keying that map so multiple Recipes can share one spec.implements is
// future work, but the precedence behavior is demonstrable without it.
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

// TestComponentRecipeURI_VersionTolerant pins the source-descent join's version
// tolerance: an authored component type resolves to its Manifest mapping recipe
// even when the authored type's version differs from (or omits) the mapping
// key's version — the idiomatic shape where the Aggregate authors a bare type
// and the Manifest pins a version. Both mismatch directions resolve, and the
// exact-version / unversioned-key fast paths are unchanged. The legacy
// per-provider shape is tolerant on the same terms.
func TestComponentRecipeURI_VersionTolerant(t *testing.T) {
	t.Parallel()
	flat := &types.MappingTier{}
	flat.Type.Set("core/database/mariadb:1.0.0", "mariadb-bitnami:1.0.0") // versioned key
	flat.Type.Set("core/cache/redis", "redis-helm:1.0.0")                 // unversioned key
	flat.Type.Set("core/database/postgres:2.0.0", "postgres-helm:2.0.0")  // versioned key
	flat.Type.Set("core/queue/nats", "nats-helm:1.0.0")                   // unversioned key
	legacy := types.ProviderMappings{Components: &types.MappingTier{}}
	legacy.Components.Type.Set("core/legacy/thing:1.0.0", "thing-legacy:1.0.0")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flat},
		},
	}
	manifest.Spec.Mappings.Legacy.Set("helm", legacy)

	cases := []struct {
		name, compType, wantURI string
		wantOK                  bool
	}{
		{"unversioned type, versioned key (the unblock)", "core/database/mariadb", "mariadb-bitnami:1.0.0", true},
		{"versioned type, unversioned key (reverse)", "core/cache/redis:3.1.0", "redis-helm:1.0.0", true},
		{"versioned type, versioned key (exact, unchanged)", "core/database/postgres:2.0.0", "postgres-helm:2.0.0", true},
		{"unversioned type, unversioned key (exact, unchanged)", "core/queue/nats", "nats-helm:1.0.0", true},
		{"version mismatch, base matches", "core/database/postgres:9.9.9", "postgres-helm:2.0.0", true},
		{"legacy version-tolerant", "core/legacy/thing", "thing-legacy:1.0.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri, ok, err := ComponentRecipeURI(manifest, tc.compType)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v; want %v", ok, tc.wantOK)
			}
			if uri != tc.wantURI {
				t.Errorf("uri = %q; want %q", uri, tc.wantURI)
			}
		})
	}
}

// TestFeatureRecipeURI_VersionTolerant is the FeatureRecipe analogue of
// TestComponentRecipeURI_VersionTolerant — the feature source-descent join
// matches version-tolerantly on both mismatch directions.
func TestFeatureRecipeURI_VersionTolerant(t *testing.T) {
	t.Parallel()
	flat := &types.MappingTier{}
	flat.Type.Set("core/security/tls:1.0.0", "tls-cert:1.0.0")  // versioned key
	flat.Type.Set("core/observability/otel", "otel-helm:1.0.0") // unversioned key
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Features: flat},
		},
	}

	cases := []struct {
		name, featType, wantURI string
		wantOK                  bool
	}{
		{"unversioned type, versioned key", "core/security/tls", "tls-cert:1.0.0", true},
		{"versioned type, unversioned key", "core/observability/otel:4.0.0", "otel-helm:1.0.0", true},
		{"versioned type, versioned key (exact)", "core/security/tls:1.0.0", "tls-cert:1.0.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri, ok, err := FeatureRecipeURI(manifest, tc.featType)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v; want %v", ok, tc.wantOK)
			}
			if uri != tc.wantURI {
				t.Errorf("uri = %q; want %q", uri, tc.wantURI)
			}
		})
	}
}

// TestComponentRecipeURI_AmbiguousVersionKeys pins the Loud Guardrail: two
// version-distinct Manifest keys sharing one base make an UNVERSIONED authored
// type ambiguous, so the lookup halts with ErrAmbiguousTypeMapping rather than
// silently resolving the first. An exact-version authored type still resolves —
// the exact key match wins before the base-match ambiguity check.
func TestComponentRecipeURI_AmbiguousVersionKeys(t *testing.T) {
	t.Parallel()
	flat := &types.MappingTier{}
	flat.Type.Set("core/database/mariadb:1.0.0", "mariadb-v1:1.0.0")
	flat.Type.Set("core/database/mariadb:2.0.0", "mariadb-v2:2.0.0")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flat},
		},
	}

	if _, ok, err := ComponentRecipeURI(manifest, "core/database/mariadb"); err == nil {
		t.Fatalf("expected ErrAmbiguousTypeMapping; got ok=%v, nil error", ok)
	} else if !errors.Is(err, ErrAmbiguousTypeMapping) {
		t.Errorf("expected ErrAmbiguousTypeMapping in chain; got %v", err)
	}

	uri, ok, err := ComponentRecipeURI(manifest, "core/database/mariadb:2.0.0")
	if err != nil {
		t.Fatalf("unexpected error on exact-version match: %v", err)
	}
	if !ok || uri != "mariadb-v2:2.0.0" {
		t.Errorf("exact-version match: uri=%q ok=%v; want %q true", uri, ok, "mariadb-v2:2.0.0")
	}
}

// TestFeatureRecipeURI_AmbiguousVersionKeys is the FeatureRecipe analogue:
// version-distinct feature-mapping keys sharing a base halt loudly.
func TestFeatureRecipeURI_AmbiguousVersionKeys(t *testing.T) {
	t.Parallel()
	flat := &types.MappingTier{}
	flat.Type.Set("core/security/tls:1.0.0", "tls-v1:1.0.0")
	flat.Type.Set("core/security/tls:2.0.0", "tls-v2:2.0.0")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Features: flat},
		},
	}

	if _, ok, err := FeatureRecipeURI(manifest, "core/security/tls"); err == nil {
		t.Fatalf("expected ErrAmbiguousTypeMapping; got ok=%v, nil error", ok)
	} else if !errors.Is(err, ErrAmbiguousTypeMapping) {
		t.Errorf("expected ErrAmbiguousTypeMapping in chain; got %v", err)
	}
}
