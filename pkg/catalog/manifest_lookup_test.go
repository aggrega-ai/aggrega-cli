// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// fakeRecipe builds a Recipe with metadata.name URI and a spec.implements.
func fakeRecipe(uri, implements string) types.Recipe {
	return types.Recipe{
		Metadata: types.RecipeMetadata{Name: uri, Version: "1.0.0"},
		Spec:     types.RecipeSpec{Capability: "helm", Implements: implements},
	}
}

// fakeCatalog builds a Catalog populated with the given recipes, keyed by
// the provider-scoped identity the loader uses; RecipeByURI resolves
// qualified and bare URI forms against it.
func fakeCatalog(recipes ...types.Recipe) *Catalog {
	c := &Catalog{
		Components:     map[string]types.Component{},
		Features:       map[string]types.Feature{},
		Recipes:        map[string]types.Recipe{},
		FeatureRecipes: map[string]types.FeatureRecipe{},
		Aggregates:     map[string]types.Aggregate{},
	}
	for _, r := range recipes {
		c.Recipes[recipeIdentityKey(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version)] = r
	}
	return c
}

// TestRecipeByURI_DirectMatch pins the URI-keyed direct lookup.
func TestRecipeByURI_DirectMatch(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/datastore/postgres"),
	)
	r, ok := c.RecipeByURI("core/helm/postgres-bitnami")
	if !ok {
		t.Fatal("expected RecipeByURI hit")
	}
	if r.Spec.Implements != "core/datastore/postgres" {
		t.Errorf("wrong recipe resolved: %q", r.Spec.Implements)
	}
}

// TestRecipeLookupWithManifest_FlatShape pins layer-1 dispatch: a flat
// Manifest mapping resolves directly via RecipeByURI; the
// spec.implements walking is bypassed.
func TestRecipeLookupWithManifest_FlatShape(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/datastore/postgres"),
	)
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/datastore/postgres", "core/helm/postgres-bitnami")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flatComponents},
		},
	}
	lookup := c.RecipeLookupWithManifest(manifest)
	r, ok := lookup.Recipe("", "core/datastore/postgres")
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
	flat.Type.Set("core/datastore/postgres", "hub.aggrega.ai/recipe/helm/postgres:2.1.0")
	flat.Type.Set("core/cache/memcached", "memcached-recipe")
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
		{"full-path flat", "core/datastore/postgres", "hub.aggrega.ai/recipe/helm/postgres:2.1.0", true},
		// Unversioned mapping value returns verbatim — NOT normalized to a
		// versioned URI. This is the source-descent join's load-bearing guard: an
		// unversioned mapping value must stay unversioned so it joins the closure
		// entity recorded under the same verbatim value, never silently binding to
		// a versioned entity. A future version-normalization step would break this.
		{"unversioned flat", "core/cache/memcached", "memcached-recipe", true},
		{"legacy fallback", "core/cache/redis", "core/helm/redis-legacy:1.0.0", true},
		{"unmapped type", "core/queue/nats", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri, ok, err := ComponentRecipeURI(manifest, "", tc.compType)
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
	flat.Type.Set("core/observability/logs", "logs-fr")
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
		// Unversioned mapping value returns verbatim — the FeatureRecipe analogue
		// of the source-descent join guard: an unversioned value must not be
		// normalized to a versioned URI, or it would bind to a versioned closure
		// entity it never matched verbatim.
		{"unversioned flat", "core/observability/logs", "logs-fr", true},
		{"legacy fallback", "core/observability/otel", "core/feature-recipe/otel-legacy:1.0.0", true},
		{"unmapped type", "core/queue/nats", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri, ok, err := FeatureRecipeURI(manifest, "", tc.featType)
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
		fakeRecipe("core/helm/postgres-bitnami", "core/datastore/postgres"),
	)
	legacyHelm := types.ProviderMappings{Components: &types.MappingTier{}}
	legacyHelm.Components.Type.Set("core/datastore/postgres", "core/helm/postgres-bitnami")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{},
		},
	}
	manifest.Spec.Mappings.Legacy.Set("helm", legacyHelm)
	lookup := c.RecipeLookupWithManifest(manifest)
	r, ok := lookup.Recipe("", "core/datastore/postgres")
	if !ok {
		t.Fatal("expected legacy-shape dispatch to hit")
	}
	if r.Metadata.Name != "core/helm/postgres-bitnami" {
		t.Errorf("wrong recipe resolved: %q", r.Metadata.Name)
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
		fakeRecipe("core/helm/postgres-flat", "core/datastore/postgres-A"),
		fakeRecipe("core/helm/postgres-legacy", "core/datastore/postgres-B"),
	)
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/datastore/postgres-A", "core/helm/postgres-flat")
	legacyHelm := types.ProviderMappings{Components: &types.MappingTier{}}
	legacyHelm.Components.Type.Set("core/datastore/postgres-A", "core/helm/postgres-legacy")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flatComponents},
		},
	}
	manifest.Spec.Mappings.Legacy.Set("helm", legacyHelm)
	lookup := c.RecipeLookupWithManifest(manifest)
	r, _ := lookup.Recipe("", "core/datastore/postgres-A")
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
		fakeRecipe("core/helm/postgres-bitnami", "core/datastore/postgres"),
	)
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/datastore/postgres", "core/helm/postgres-bitnami")
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
		fakeRecipe("core/helm/wrong-recipe", "core/datastore/mysql"),
	)
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/datastore/postgres", "core/helm/wrong-recipe")
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

// TestVerifyManifestImplementsConsistency_CaretAware pins that the consistency
// check honours caret ranges rather than comparing version-stripped bases: a
// recipe declaring a caret spec.implements validates against a mapping key
// whose concrete version falls in range, and fails against one that does not.
func TestVerifyManifestImplementsConsistency_CaretAware(t *testing.T) {
	t.Parallel()

	// In-range: recipe implements core/api:^1.0.0, mapping key core/api:1.2.0.
	inRange := fakeRecipe("recipes/api-impl", "core/api:^1.0.0")
	cPass := fakeCatalog(inRange)
	passComponents := &types.MappingTier{}
	passComponents.Type.Set("core/api:1.2.0", "recipes/api-impl")
	passManifest := &types.Manifest{
		Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: passComponents}},
	}
	if errs := cPass.VerifyManifestImplementsConsistency(passManifest, "manifest.yaml"); len(errs) != 0 {
		t.Errorf("caret in range should validate; got %v", errs)
	}

	// Out-of-range: recipe implements core/api:^2.0.0, mapping key core/api:1.0.0.
	outRange := fakeRecipe("recipes/api-impl", "core/api:^2.0.0")
	cFail := fakeCatalog(outRange)
	failComponents := &types.MappingTier{}
	failComponents.Type.Set("core/api:1.0.0", "recipes/api-impl")
	failManifest := &types.Manifest{
		Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: failComponents}},
	}
	errs := cFail.VerifyManifestImplementsConsistency(failManifest, "manifest.yaml")
	if len(errs) == 0 {
		t.Fatal("caret out of range should fail with ErrManifestImplementsMismatch")
	}
	if !errors.Is(errs[0], ErrManifestImplementsMismatch) {
		t.Errorf("expected ErrManifestImplementsMismatch in chain; got %v", errs[0])
	}
}

// TestManifestMappingConsistent pins the bidirectional version truth table the
// consistency check applies between a Manifest mapping key and the mapped
// recipe's spec.implements. The key may be bare or version-pinned; the
// implements may be bare, exact, or caret. A bare key imposes no version
// constraint (base match suffices, including against a version-pinned
// implements); a version-pinned key is judged by exact/caret/base
// satisfaction.
func TestManifestMappingConsistent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		impl, key string
		want      bool
	}{
		{"bare key, bare impl, same base", "core/datastore/postgres", "core/datastore/postgres", true},
		{"bare key, versioned impl, same base", "core/datastore/postgres:1.0.0", "core/datastore/postgres", true},
		{"bare key, caret impl, same base", "core/datastore/postgres:^1.0.0", "core/datastore/postgres", true},
		{"versioned key, bare impl, same base", "core/datastore/postgres", "core/datastore/postgres:1.0.0", true},
		{"versioned key, exact impl, same version", "core/datastore/postgres:1.0.0", "core/datastore/postgres:1.0.0", true},
		{"versioned key in caret range", "core/api:^1.0.0", "core/api:1.2.0", true},
		{"versioned key out of caret range", "core/api:^1.0.0", "core/api:2.0.0", false},
		{"versioned key, exact impl, version mismatch", "core/api:1.0.0", "core/api:2.0.0", false},
		{"bare key, differing base", "core/datastore/mysql", "core/datastore/postgres", false},
		{"versioned key, differing base", "core/datastore/mysql:1.0.0", "core/datastore/postgres:1.0.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := manifestMappingConsistent(tc.impl, tc.key); got != tc.want {
				t.Errorf("manifestMappingConsistent(impl=%q, key=%q) = %v; want %v", tc.impl, tc.key, got, tc.want)
			}
		})
	}
}

// TestVerifyManifestImplementsConsistency_BareKeyVersionedImpl pins the
// regressed case end-to-end through the public verifier: a bare mapping key
// against a recipe whose spec.implements carries a version is consistent (the
// bare key does not constrain the version).
func TestVerifyManifestImplementsConsistency_BareKeyVersionedImpl(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/datastore/postgres:1.0.0"),
	)
	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/datastore/postgres", "core/helm/postgres-bitnami")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flatComponents},
		},
	}
	if errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml"); len(errs) != 0 {
		t.Errorf("bare key against versioned implements should be consistent; got %v", errs)
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
	flat.Type.Set("core/datastore/mariadb:1.0.0", "mariadb-bitnami:1.0.0") // versioned key
	flat.Type.Set("core/cache/redis", "redis-helm:1.0.0")                  // unversioned key
	flat.Type.Set("core/datastore/postgres:2.0.0", "postgres-helm:2.0.0")  // versioned key
	flat.Type.Set("core/queue/nats", "nats-helm:1.0.0")                    // unversioned key
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
		{"unversioned type, versioned key (the unblock)", "core/datastore/mariadb", "mariadb-bitnami:1.0.0", true},
		{"versioned type, unversioned key (reverse)", "core/cache/redis:3.1.0", "redis-helm:1.0.0", true},
		{"versioned type, versioned key (exact, unchanged)", "core/datastore/postgres:2.0.0", "postgres-helm:2.0.0", true},
		{"unversioned type, unversioned key (exact, unchanged)", "core/queue/nats", "nats-helm:1.0.0", true},
		{"version mismatch, base matches", "core/datastore/postgres:9.9.9", "postgres-helm:2.0.0", true},
		{"legacy version-tolerant", "core/legacy/thing", "thing-legacy:1.0.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri, ok, err := ComponentRecipeURI(manifest, "", tc.compType)
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
			uri, ok, err := FeatureRecipeURI(manifest, "", tc.featType)
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
	flat.Type.Set("core/datastore/mariadb:1.0.0", "mariadb-v1:1.0.0")
	flat.Type.Set("core/datastore/mariadb:2.0.0", "mariadb-v2:2.0.0")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flat},
		},
	}

	if _, ok, err := ComponentRecipeURI(manifest, "", "core/datastore/mariadb"); err == nil {
		t.Fatalf("expected ErrAmbiguousTypeMapping; got ok=%v, nil error", ok)
	} else if !errors.Is(err, ErrAmbiguousTypeMapping) {
		t.Errorf("expected ErrAmbiguousTypeMapping in chain; got %v", err)
	}

	uri, ok, err := ComponentRecipeURI(manifest, "", "core/datastore/mariadb:2.0.0")
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

	if _, ok, err := FeatureRecipeURI(manifest, "", "core/security/tls"); err == nil {
		t.Fatalf("expected ErrAmbiguousTypeMapping; got ok=%v, nil error", ok)
	} else if !errors.Is(err, ErrAmbiguousTypeMapping) {
		t.Errorf("expected ErrAmbiguousTypeMapping in chain; got %v", err)
	}
}

// writeCatalogEntity writes one entity YAML under dir so tests load through the
// REAL catalog loader rather than a hand-built map.
func writeCatalogEntity(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// recipeYAML builds a minimal loadable Recipe. target: eval bans the
// three-input-kinds, so the recipe needs no source/workload/artifacts to pass
// load validation — enough for a resolution-only test.
func recipeYAML(name, implements string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  capability: eval\n  implements: " + implements + "\n"
}

func featureRecipeYAML(name, implements string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: FeatureRecipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  capability: eval\n  implements: " + implements + "\n"
}

// TestNameTierOverride_FiresAtBothJoins is the per-instance recipe-override
// guard, proven through the REAL catalog loader: two recipes implementing ONE
// component type (and two
// feature recipes implementing one feature type) both survive load, and a
// per-instance `name:` override resolves to its override while siblings resolve
// to the type-tier recipe — at BOTH the pull join (ComponentRecipeURI /
// FeatureRecipeURI) and the build join (RecipeLookupWithManifest closures).
func TestNameTierOverride_FiresAtBothJoins(t *testing.T) {
	t.Parallel()
	const compType = "app/db"
	const featType = "sec/db-cred"
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "recipe/db-default.yaml", recipeYAML("db-default", compType))
	writeCatalogEntity(t, dir, "recipe/db-override.yaml", recipeYAML("db-override", compType))
	writeCatalogEntity(t, dir, "feature-recipe/cred-default.yaml", featureRecipeYAML("cred-default", featType))
	writeCatalogEntity(t, dir, "feature-recipe/cred-override.yaml", featureRecipeYAML("cred-override", featType))

	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	// Fiction-killer: BOTH same-type recipes (and feature recipes) survive the
	// real loader — the catalog state the override depends on is reachable.
	if ids := recipesImplementing(c.Recipes, compType); len(ids) != 2 {
		t.Fatalf("expected 2 recipes implementing %q to coexist after load; got %v", compType, ids)
	}
	if ids := featureRecipesImplementing(c.FeatureRecipes, featType); len(ids) != 2 {
		t.Fatalf("expected 2 feature recipes implementing %q to coexist after load; got %v", featType, ids)
	}

	comp := &types.MappingTier{}
	comp.Type.Set(compType, "db-default:1.0.0")      // bulk type mapping
	comp.Name.Set("db-special", "db-override:1.0.0") // per-instance override
	feat := &types.MappingTier{}
	feat.Type.Set(featType, "cred-default:1.0.0")
	feat.Name.Set("cred-special", "cred-override:1.0.0")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp, Features: feat}}}

	// Pull join: ComponentRecipeURI / FeatureRecipeURI return the override URI
	// for the named instance, the type URI for siblings, and (component) the
	// type URI for an empty instance name.
	assertURI := func(label, gotURI string, ok bool, gotErr error, want string) {
		t.Helper()
		if gotErr != nil || !ok || gotURI != want {
			t.Errorf("%s = %q (ok=%v err=%v); want %q", label, gotURI, ok, gotErr, want)
		}
	}
	u, ok, e := ComponentRecipeURI(manifest, "db-special", compType)
	assertURI("pull override db-special", u, ok, e, "db-override:1.0.0")
	u, ok, e = ComponentRecipeURI(manifest, "db-other", compType)
	assertURI("pull sibling db-other", u, ok, e, "db-default:1.0.0")
	u, ok, e = ComponentRecipeURI(manifest, "", compType)
	assertURI("pull empty name", u, ok, e, "db-default:1.0.0")
	u, ok, e = FeatureRecipeURI(manifest, "cred-special", featType)
	assertURI("pull override cred-special", u, ok, e, "cred-override:1.0.0")
	u, ok, e = FeatureRecipeURI(manifest, "cred-other", featType)
	assertURI("pull sibling cred-other", u, ok, e, "cred-default:1.0.0")

	// Build join: the render closures resolve the override instance to the
	// override recipe and siblings to the type-tier recipe.
	lookup := c.RecipeLookupWithManifest(manifest)
	assertRecipe := func(label, name, want string) {
		t.Helper()
		r, ok := lookup.Recipe(name, compType)
		if !ok {
			t.Errorf("%s: not resolved", label)
			return
		}
		if r.Metadata.Name != want {
			t.Errorf("%s: got %q; want %q", label, r.Metadata.Name, want)
		}
	}
	assertRecipe("build override db-special", "db-special", "db-override")
	assertRecipe("build sibling db-other", "db-other", "db-default")
	assertFR := func(label, name, want string) {
		t.Helper()
		fr, ok := lookup.FeatureRecipe(name, featType)
		if !ok {
			t.Errorf("%s: not resolved", label)
			return
		}
		if fr.Metadata.Name != want {
			t.Errorf("%s: got %q; want %q", label, fr.Metadata.Name, want)
		}
	}
	assertFR("build override cred-special", "cred-special", "cred-override")
	assertFR("build sibling cred-other", "cred-other", "cred-default")
}

// TestNameTier_DuplicateIdentityDetected pins the load-time guard: two recipes
// sharing one identity (metadata.name + version) halt loudly rather than one
// silently shadowing the other.
func TestNameTier_DuplicateIdentityDetected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "a.yaml", recipeYAML("dup", "app/db"))
	writeCatalogEntity(t, dir, "b.yaml", recipeYAML("dup", "app/other"))
	if _, err := LoadAll("", dir); err == nil || !errors.Is(err, ErrDuplicateRecipeIdentity) {
		t.Fatalf("expected ErrDuplicateRecipeIdentity for identity dup:1.0.0; got %v", err)
	}
}

// TestVerifyRecipeResolution_HaltsLoudly pins the pre-render resolution-
// completeness pass: an unmapped type implemented by >1 recipe halts naming the
// colliding recipes; an unimplemented type halts as no-recipe; a mapped type
// resolves cleanly.
func TestVerifyRecipeResolution_HaltsLoudly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "r1.yaml", recipeYAML("db-a", "app/db"))
	writeCatalogEntity(t, dir, "r2.yaml", recipeYAML("db-b", "app/db"))
	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	empty := &types.Manifest{}

	gAmbig := &types.IRGraph{}
	gAmbig.Spec.Nodes.Set("db-x", types.IRNode{Component: "app/db"})
	err = c.VerifyRecipeResolution(empty, gAmbig)
	if err == nil || !errors.Is(err, ErrNoRecipeResolved) {
		t.Errorf("expected ErrNoRecipeResolved for unmapped 2-impl type; got %v", err)
	} else if !strings.Contains(err.Error(), "db-a") || !strings.Contains(err.Error(), "db-b") {
		t.Errorf("candidate-list error must name both implementing recipes; got %v", err)
	}

	gNone := &types.IRGraph{}
	gNone.Spec.Nodes.Set("x", types.IRNode{Component: "app/none"})
	if err := c.VerifyRecipeResolution(empty, gNone); err == nil || !errors.Is(err, ErrNoRecipeResolved) {
		t.Errorf("expected ErrNoRecipeResolved for unimplemented type; got %v", err)
	}

	comp := &types.MappingTier{}
	comp.Type.Set("app/db", "db-a:1.0.0")
	mapped := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp}}}
	if err := c.VerifyRecipeResolution(mapped, gAmbig); err != nil {
		t.Errorf("a mapped type should resolve cleanly; got %v", err)
	}
}

// TestVerifyRecipeResolution_ShapeFaultVersusAbsentEntity separates the two
// mapping failures the pre-render resolution-completeness pass can report. A
// mapping value carrying more segments than a recipe reference accepts is a
// SHAPE fault: the recipe it names is in the catalog, so the diagnostic must
// name the accepted and the typed shape and must NOT claim the entity is
// missing. A mapping value at an accepted shape that names nothing in the
// catalog stays the missing-entity halt. Both halves are asserted together
// because either alone would pass while the discrimination was broken — a shape
// diagnostic that also fired for a genuinely absent entity would be a
// regression. Component and feature paths both, since each has its own branch.
func TestVerifyRecipeResolution_ShapeFaultVersusAbsentEntity(t *testing.T) {
	t.Parallel()
	const compType = "app/db"
	const featType = "sec/db-cred"
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "recipe/present.yaml", recipeYAML("present", compType))
	writeCatalogEntity(t, dir, "feature-recipe/cred.yaml", featureRecipeYAML("cred", featType))
	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("db", types.IRNode{Component: compType})
	feats := types.OrderedMap[types.IRFeatureEntry]{}
	feats.Set("db-cred", types.IRFeatureEntry{Type: featType})
	graph.Spec.FeatureValuesMapping.Set("db", feats)

	manifestFor := func(compURI, featURI string) *types.Manifest {
		comp := &types.MappingTier{}
		comp.Type.Set(compType, compURI)
		feat := &types.MappingTier{}
		feat.Type.Set(featType, featURI)
		return &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{
			Components: comp,
			Features:   feat,
		}}}
	}

	// Fiction-killer: at an accepted shape both references resolve, so the two
	// failures below are caused by the mapping value and nothing else.
	if err := c.VerifyRecipeResolution(manifestFor("eval/present:1.0.0", "eval/cred:1.0.0"), graph); err != nil {
		t.Fatalf("capability-qualified references should resolve cleanly; got %v", err)
	}

	// Shape fault: one segment past the ceiling, naming a recipe that IS present.
	shapeErr := c.VerifyRecipeResolution(manifestFor("core/eval/present:1.0.0", "core/eval/cred:1.0.0"), graph)
	if shapeErr == nil {
		t.Fatalf("a reference past the segment ceiling must halt")
	}
	if !errors.Is(shapeErr, ErrMalformedRecipeReference) {
		t.Errorf("expected ErrMalformedRecipeReference in chain; got %v", shapeErr)
	}
	if errors.Is(shapeErr, ErrNoRecipeResolved) {
		t.Errorf("a shape fault must not report as no-recipe-resolved; got %v", shapeErr)
	}
	if strings.Contains(shapeErr.Error(), "not in the catalog") {
		t.Errorf("a shape fault must not claim the entity is absent; got %v", shapeErr)
	}
	for _, want := range []string{
		`"core/eval/present:1.0.0"`,   // the shape the author typed
		"3 slash-separated segments",  // and its segment count
		acceptedRecipeReferenceShapes, // the shapes a reference accepts
		"correct the mapping value in the Manifest",
		`"core/eval/cred:1.0.0"`, // the feature twin fires too
	} {
		if !strings.Contains(shapeErr.Error(), want) {
			t.Errorf("shape diagnostic missing %q; got %v", want, shapeErr)
		}
	}

	// Genuinely absent: accepted shape, nothing in the catalog by that name.
	absentErr := c.VerifyRecipeResolution(manifestFor("no-such-recipe:1.0.0", "no-such-feature-recipe:1.0.0"), graph)
	if absentErr == nil {
		t.Fatalf("a mapping to an absent recipe must halt")
	}
	if !errors.Is(absentErr, ErrNoRecipeResolved) {
		t.Errorf("expected ErrNoRecipeResolved in chain; got %v", absentErr)
	}
	if errors.Is(absentErr, ErrMalformedRecipeReference) {
		t.Errorf("an absent entity must not report as a shape fault; got %v", absentErr)
	}
	for _, want := range []string{
		`recipe "no-such-recipe:1.0.0", which is not in the catalog`,
		`feature recipe "no-such-feature-recipe:1.0.0", which is not in the catalog`,
	} {
		if !strings.Contains(absentErr.Error(), want) {
			t.Errorf("missing-entity diagnostic changed; expected %q in %v", want, absentErr)
		}
	}
}

// TestCatalogImmunity_UnmappedSingleImplementerHalts pins the de-fallback
// permanently: an instance with NO Manifest mapping halts even when EXACTLY ONE
// recipe implements its type. Under the removed implements-search fallback the
// lone implementer resolved silently; resolution is now mapping-only, so the
// build halts with the implementing recipe named as a pin candidate. This is
// red against the pre-removal code (which returned nil for a single implementer)
// and green after — the fallback is provably gone.
func TestCatalogImmunity_UnmappedSingleImplementerHalts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "r.yaml", recipeYAML("only-db", "app/db"))
	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	g := &types.IRGraph{}
	g.Spec.Nodes.Set("db", types.IRNode{Component: "app/db"})

	err = c.VerifyRecipeResolution(&types.Manifest{}, g)
	if err == nil || !errors.Is(err, ErrNoRecipeResolved) {
		t.Fatalf("an unmapped instance with exactly ONE implementer must halt with ErrNoRecipeResolved; got %v", err)
	}
	if !strings.Contains(err.Error(), "only-db") {
		t.Errorf("the halt must name the lone implementer as a pin candidate; got %v", err)
	}
}

// TestCatalogImmunity_CatalogGrowthNonBreakingForMappedType pins that adding a
// second recipe implementing an already-MAPPED type does not change which recipe
// the mapped build resolves: the Manifest mapping pins the choice and the new
// implementer is ignored. Identical resolution is the proximate proof that
// rendered output stays byte-identical across catalog growth — catalog growth is
// non-breaking for mapped manifests (the property the fallback violated, where
// one implementer becoming two turned a clean build into an ambiguity halt).
func TestCatalogImmunity_CatalogGrowthNonBreakingForMappedType(t *testing.T) {
	t.Parallel()
	comp := &types.MappingTier{}
	comp.Type.Set("app/db", "db-a")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp}}}

	c1 := fakeCatalog(fakeRecipe("db-a", "app/db"))
	r1, ok1 := c1.RecipeLookupWithManifest(manifest).Recipe("db", "app/db")
	if !ok1 {
		t.Fatal("baseline catalog must resolve the mapped type")
	}

	// Catalog grows: a second recipe also implements app/db.
	c2 := fakeCatalog(fakeRecipe("db-a", "app/db"), fakeRecipe("db-b", "app/db"))
	r2, ok2 := c2.RecipeLookupWithManifest(manifest).Recipe("db", "app/db")
	if !ok2 {
		t.Fatal("grown catalog must still resolve the mapped type")
	}

	if r1.Metadata.Name != r2.Metadata.Name || r1.Metadata.Version != r2.Metadata.Version {
		t.Errorf("catalog growth changed the mapped resolution: %s:%s -> %s:%s (must be identical)",
			r1.Metadata.Name, r1.Metadata.Version, r2.Metadata.Name, r2.Metadata.Version)
	}
}

// TestNameTier_PrecedenceWithVersionTolerantType pins the cascade order: a name
// override short-circuits before the (version-tolerant) type tier, and a lookup
// with no name override still resolves through the version-tolerant type match.
func TestNameTier_PrecedenceWithVersionTolerantType(t *testing.T) {
	t.Parallel()
	comp := &types.MappingTier{}
	comp.Type.Set("core/datastore/mariadb:1.0.0", "recipe-typed") // versioned type key
	comp.Name.Set("db-primary", "recipe-named")                   // per-instance override
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp}},
	}
	// Name wins even though the type tier would version-tolerantly match.
	if uri, ok, _ := ComponentRecipeURI(manifest, "db-primary", "core/datastore/mariadb"); !ok || uri != "recipe-named" {
		t.Errorf("name must win over version-tolerant type: got %q ok=%v; want recipe-named", uri, ok)
	}
	// No name override -> version-tolerant type match still resolves.
	if uri, ok, _ := ComponentRecipeURI(manifest, "db-replica", "core/datastore/mariadb"); !ok || uri != "recipe-typed" {
		t.Errorf("no-name lookup must keep version-tolerant type: got %q ok=%v; want recipe-typed", uri, ok)
	}
}

// TestNameTier_LegacyShape pins the name tier in the legacy per-provider mapping
// shape (so the override is not silently shape-dependent).
func TestNameTier_LegacyShape(t *testing.T) {
	t.Parallel()
	legacy := types.ProviderMappings{Components: &types.MappingTier{}}
	legacy.Components.Type.Set("core/datastore/mariadb", "recipe-typed-legacy")
	legacy.Components.Name.Set("db-primary", "recipe-named-legacy")
	manifest := &types.Manifest{}
	manifest.Spec.Mappings.Legacy.Set("helm", legacy)

	if uri, ok, _ := ComponentRecipeURI(manifest, "db-primary", "core/datastore/mariadb"); !ok || uri != "recipe-named-legacy" {
		t.Errorf("legacy-shape name override must resolve: got %q ok=%v; want recipe-named-legacy", uri, ok)
	}
	if uri, ok, _ := ComponentRecipeURI(manifest, "db-replica", "core/datastore/mariadb"); !ok || uri != "recipe-typed-legacy" {
		t.Errorf("legacy-shape sibling -> type: got %q ok=%v; want recipe-typed-legacy", uri, ok)
	}
}
