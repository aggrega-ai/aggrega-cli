// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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
		t.Fatal("expected the mapping to resolve")
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
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flat},
		},
	}

	cases := []struct {
		name, compType, wantURI string
		wantOK                  bool
	}{
		{"short-form", "mariadb-bitnami", "mariadb-bitnami:1.0.0", true},
		{"full-path", "core/datastore/postgres", "hub.aggrega.ai/recipe/helm/postgres:2.1.0", true},
		// Unversioned mapping value returns verbatim — NOT normalized to a
		// versioned URI. This is the source-descent join's load-bearing guard: an
		// unversioned mapping value must stay unversioned so it joins the closure
		// entity recorded under the same verbatim value, never silently binding to
		// a versioned entity. A future version-normalization step would break this.
		{"unversioned", "core/cache/memcached", "memcached-recipe", true},
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
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Features: flat},
		},
	}

	cases := []struct {
		name, featType, wantURI string
		wantOK                  bool
	}{
		{"short-form", "tls-cert", "tls-cert:1.0.0", true},
		{"full-path", "core/security/tls", "hub.aggrega.ai/feature-recipe/helm/tls:2.1.0", true},
		// Unversioned mapping value returns verbatim — the FeatureRecipe analogue
		// of the source-descent join guard: an unversioned value must not be
		// normalized to a versioned URI, or it would bind to a versioned closure
		// entity it never matched verbatim.
		{"unversioned", "core/observability/logs", "logs-fr", true},
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

// TestRecipeLookupWithManifest_MappingResolves pins the render lookup's one
// resolution route: a component type the Manifest maps resolves to the mapped
// Recipe, by Recipe URI rather than by spec.implements.
func TestRecipeLookupWithManifest_MappingResolves(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/datastore/postgres"),
	)
	components := &types.MappingTier{}
	components.Type.Set("core/datastore/postgres", "core/helm/postgres-bitnami")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: components},
		},
	}
	lookup := c.RecipeLookupWithManifest(manifest)
	r, ok := lookup.Recipe("", "core/datastore/postgres")
	if !ok {
		t.Fatal("expected the mapping to resolve")
	}
	if r.Metadata.Name != "core/helm/postgres-bitnami" {
		t.Errorf("wrong recipe resolved: %q", r.Metadata.Name)
	}
}

// TestRecipeLookupWithManifest_UnresolvableMappingIsAMiss pins the precedence
// rule stated on RecipeLookupWithManifest: a mapping entry is the author's
// answer for that type, so a value naming a recipe the catalog does not hold
// is a MISS here rather than an invitation to resolve some other way — even
// though the catalog does hold a recipe whose spec.implements matches the type.
//
// The fault itself is reported by the pre-render resolution-completeness pass,
// which reads the same mapping value, on a surface that loads the catalog
// eagerly as this test does; a surface that resolves the Release's dependency
// closure first stops in that resolution before the pass runs. This test pins
// only that the render lookup refuses to answer.
func TestRecipeLookupWithManifest_UnresolvableMappingIsAMiss(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		fakeRecipe("core/helm/postgres-bitnami", "core/datastore/postgres"),
	)
	components := &types.MappingTier{}
	components.Type.Set("core/datastore/postgres", "core/helm/absent-recipe")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: components},
		},
	}
	lookup := c.RecipeLookupWithManifest(manifest)
	if r, ok := lookup.Recipe("", "core/datastore/postgres"); ok {
		t.Errorf("an unresolvable mapping value must miss; got %q", r.Metadata.Name)
	}
}

// TestVerifyManifestImplementsConsistency_PassOnMatch pins the
// consistency-check pass path: mappings whose resolved recipes carry
// matching spec.implements produce no errors.
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
	errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml", c.ComponentAncestry)
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
	errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml", c.ComponentAncestry)
	if len(errs) == 0 {
		t.Fatal("expected ErrManifestImplementsMismatch")
	}
	if !errors.Is(errs[0], ErrManifestImplementsMismatch()) {
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
	if errs := cPass.VerifyManifestImplementsConsistency(passManifest, "manifest.yaml", cPass.ComponentAncestry); len(errs) != 0 {
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
	errs := cFail.VerifyManifestImplementsConsistency(failManifest, "manifest.yaml", cFail.ComponentAncestry)
	if len(errs) == 0 {
		t.Fatal("caret out of range should fail with ErrManifestImplementsMismatch")
	}
	if !errors.Is(errs[0], ErrManifestImplementsMismatch()) {
		t.Errorf("expected ErrManifestImplementsMismatch in chain; got %v", errs[0])
	}
}

// The version truth table for the satisfaction rule itself moved with the rule,
// into the package that declares it. What stays here is the coverage over this
// method, which is the caller: the cases either side of this note drive the same
// table through the presentation this method builds, so the rule's own table and
// its use through a real catalog are both exercised.

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
	if errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml", c.ComponentAncestry); len(errs) != 0 {
		t.Errorf("bare key against versioned implements should be consistent; got %v", errs)
	}
}

// TestVerifyManifestImplementsConsistency_NilManifest pins boundary:
// nil manifest returns nil (no consistency errors to surface).
func TestVerifyManifestImplementsConsistency_NilManifest(t *testing.T) {
	t.Parallel()
	c := fakeCatalog()
	if errs := c.VerifyManifestImplementsConsistency(nil, "", c.ComponentAncestry); errs != nil {
		t.Errorf("expected nil on nil manifest; got %v", errs)
	}
}

// TestComponentRecipeURI_VersionTolerant pins the source-descent join's version
// tolerance: an authored component type resolves to its Manifest mapping recipe
// even when the authored type's version differs from (or omits) the mapping
// key's version — the idiomatic shape where the Aggregate authors a bare type
// and the Manifest pins a version. Both mismatch directions resolve, and the
// exact-version / unversioned-key fast paths are unchanged.
func TestComponentRecipeURI_VersionTolerant(t *testing.T) {
	t.Parallel()
	flat := &types.MappingTier{}
	flat.Type.Set("core/datastore/mariadb:1.0.0", "mariadb-bitnami:1.0.0") // versioned key
	flat.Type.Set("core/cache/redis", "redis-helm:1.0.0")                  // unversioned key
	flat.Type.Set("core/datastore/postgres:2.0.0", "postgres-helm:2.0.0")  // versioned key
	flat.Type.Set("core/queue/nats", "nats-helm:1.0.0")                    // unversioned key
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flat},
		},
	}

	cases := []struct {
		name, compType, wantURI string
		wantOK                  bool
	}{
		{"unversioned type, versioned key (the unblock)", "core/datastore/mariadb", "mariadb-bitnami:1.0.0", true},
		{"versioned type, unversioned key (reverse)", "core/cache/redis:3.1.0", "redis-helm:1.0.0", true},
		{"versioned type, versioned key (exact, unchanged)", "core/datastore/postgres:2.0.0", "postgres-helm:2.0.0", true},
		{"unversioned type, unversioned key (exact, unchanged)", "core/queue/nats", "nats-helm:1.0.0", true},
		{"version mismatch, base matches", "core/datastore/postgres:9.9.9", "postgres-helm:2.0.0", true},
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
	} else if !errors.Is(err, ErrAmbiguousTypeMapping()) {
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
	} else if !errors.Is(err, ErrAmbiguousTypeMapping()) {
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

// recipeYAML builds a minimal loadable Recipe: one artifact entry, the least a
// Recipe declares to pass load validation, on a capability no provider serves —
// enough for a resolution-only test, which renders nothing.
func recipeYAML(name, implements string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  capability: probe\n  implements: " + implements +
		"\n  artifacts:\n    probe.txt: \"probe\"\n"
}

func featureRecipeYAML(name, implements string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  capability: probe\n  implements: " + implements + "\n"
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
	if ids := recipesImplementing(c.Recipes, compType, nil); len(ids) != 2 {
		t.Fatalf("expected 2 recipes implementing %q to coexist after load; got %v", compType, ids)
	}
	if _, implementers := implementingFeatureRecipes(c.FeatureRecipes, featType, c.featureOfType); len(implementers) != 2 {
		ids := featureRecipeIdentities(implementers)
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
	if _, err := LoadAll("", dir); err == nil || !errors.Is(err, ErrDuplicateRecipeIdentity()) {
		t.Fatalf("expected ErrDuplicateRecipeIdentity for identity dup:1.0.0; got %v", err)
	}
}

// TestNodeRecipeResolution_SeparatesTheOutcomesARuleActsOn pins the catalog's
// half of the per-instance resolution: an unmapped type comes back unmapped with
// the implementing recipes offered as pin candidates; an unimplemented type comes
// back unmapped with none; a mapped type comes back mapped and present.
//
// It asserts the RESOLUTION and not a halt, because the halt is no longer this
// package's: the rule reading these outcomes is declared over the flattened
// composition and asked where the node set is complete, which is the only point
// at which a component a sub-aggregate contributed is in the set being walked.
// The halt it produces — once per node class, through a real compilation — is
// proved beside that rule's invocation site. What stays here is the half this
// package owns, which is that the four outcomes are told apart at all: a producer
// collapsing any pair of them would send an author to the wrong file, and no
// assertion over a joined error could see which pair had collapsed.
func TestNodeRecipeResolution_SeparatesTheOutcomesARuleActsOn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "r1.yaml", recipeYAML("db-a", "app/db"))
	writeCatalogEntity(t, dir, "r2.yaml", recipeYAML("db-b", "app/db"))
	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	empty := &types.Manifest{}

	unmapped := c.nodeRecipeResolution(empty, "db-x", "app/db", c.componentOfType)
	if unmapped.Mapped {
		t.Errorf("a type no mapping reaches must come back unmapped; got %+v", unmapped)
	}
	if len(unmapped.AmbiguousKeys) != 0 {
		t.Errorf("a type no mapping reaches is not a refused lookup; got keys %v", unmapped.AmbiguousKeys)
	}
	if !slices.Contains(unmapped.PinCandidates, "probe/db-a:1.0.0") ||
		!slices.Contains(unmapped.PinCandidates, "probe/db-b:1.0.0") {
		t.Errorf("the candidates must name both implementing recipes; got %v", unmapped.PinCandidates)
	}

	unimplemented := c.nodeRecipeResolution(empty, "x", "app/none", c.componentOfType)
	if unimplemented.Mapped || len(unimplemented.PinCandidates) != 0 {
		t.Errorf("a type nothing implements must come back unmapped with no candidate; got %+v", unimplemented)
	}

	comp := &types.MappingTier{}
	comp.Type.Set("app/db", "db-a:1.0.0")
	mapped := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp}}}
	resolved := c.nodeRecipeResolution(mapped, "db-x", "app/db", c.componentOfType)
	if !resolved.Mapped || !resolved.Present {
		t.Errorf("a mapped type whose recipe the catalog holds must come back mapped and present; got %+v", resolved)
	}
	if resolved.Reference != "db-a:1.0.0" {
		t.Errorf("reference = %q, want the mapping value verbatim", resolved.Reference)
	}
	// A mapped instance is offered no candidates: the author has already chosen,
	// and scanning the index for one would pay for a set nothing reads.
	if len(resolved.PinCandidates) != 0 {
		t.Errorf("a mapped instance must be offered no pin candidates; got %v", resolved.PinCandidates)
	}

	// The component arm of the joined producer now reports only a reference whose
	// shape no recipe reference can express, so a graph whose only fault is an
	// unmapped component reaches it clean.
	gUnmapped := &types.IRGraph{}
	gUnmapped.Spec.Nodes.Set("db-x", types.IRNode{Component: "app/db"})
	if err := c.VerifyRecipeResolution(empty, gUnmapped); err != nil {
		t.Errorf("the post-graph producer must not report an unmapped component a second time; got %v", err)
	}
}

// TestNodeRecipeResolution_RefusesAnUnpinnedInstanceAndResolvesAPinnedOne pins
// the catalog's half of the ambiguity refusal, in both directions.
//
// A mapping table holding one type at two distinct versions is legal authoring.
// What the lookup cannot do is choose between those keys for an instance whose
// authored type pins no version, and the keys it could not choose between travel
// with the refusal because they are the remedy.
func TestNodeRecipeResolution_RefusesAnUnpinnedInstanceAndResolvesAPinnedOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "r1.yaml", recipeYAML("db-a", "app/db"))
	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	comp := &types.MappingTier{}
	comp.Type.Set("app/db:1.0.0", "db-a:1.0.0")
	comp.Type.Set("app/db:2.0.0", "db-a:1.0.0")
	doubled := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp}}}

	refused := c.nodeRecipeResolution(doubled, "db-x", "app/db", c.componentOfType)
	if refused.Mapped {
		t.Errorf("a refused lookup names no reference; got %+v", refused)
	}
	if got := refused.AmbiguousKeys; len(got) != 2 ||
		!slices.Contains(got, "app/db:1.0.0") || !slices.Contains(got, "app/db:2.0.0") {
		t.Errorf("the refusal must carry both colliding keys; got %v", got)
	}
	// Candidates would suggest writing a mapping entry, which is the wrong edit for
	// a table that already carries two.
	if len(refused.PinCandidates) != 0 {
		t.Errorf("a refused lookup must offer no pin candidates; got %v", refused.PinCandidates)
	}

	pinned := c.nodeRecipeResolution(doubled, "db-x", "app/db:2.0.0", c.componentOfType)
	if !pinned.Mapped || !pinned.Present {
		t.Errorf("an exactly-pinned instance must resolve against the key it names; got %+v", pinned)
	}
	if len(pinned.AmbiguousKeys) != 0 {
		t.Errorf("an exact key match wins before any base match; got keys %v", pinned.AmbiguousKeys)
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
	if err := c.VerifyRecipeResolution(manifestFor("probe/present:1.0.0", "probe/cred:1.0.0"), graph); err != nil {
		t.Fatalf("capability-qualified references should resolve cleanly; got %v", err)
	}

	// Shape fault: one segment past the ceiling, naming a recipe that IS present.
	shapeErr := c.VerifyRecipeResolution(manifestFor("core/probe/present:1.0.0", "core/probe/cred:1.0.0"), graph)
	if shapeErr == nil {
		t.Fatalf("a reference past the segment ceiling must halt")
	}
	if !errors.Is(shapeErr, resolver.ErrMalformedRecipeReference()) {
		t.Errorf("expected ErrMalformedRecipeReference in chain; got %v", shapeErr)
	}
	if errors.Is(shapeErr, ErrNoRecipeResolved()) {
		t.Errorf("a shape fault must not report as no-recipe-resolved; got %v", shapeErr)
	}
	if strings.Contains(shapeErr.Error(), "not in the catalog") {
		t.Errorf("a shape fault must not claim the entity is absent; got %v", shapeErr)
	}
	for _, want := range []string{
		`"core/probe/present:1.0.0"`,           // the shape the author typed
		"3 slash-separated segments",           // and its segment count
		resolver.AcceptedRecipeReferenceShapes, // the shapes a reference accepts
		"correct the mapping value in the Manifest",
		`"core/probe/cred:1.0.0"`, // the feature twin fires too
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
	if !errors.Is(absentErr, ErrNoRecipeResolved()) {
		t.Errorf("expected ErrNoRecipeResolved in chain; got %v", absentErr)
	}
	if errors.Is(absentErr, resolver.ErrMalformedRecipeReference()) {
		t.Errorf("an absent entity must not report as a shape fault; got %v", absentErr)
	}
	// The FEATURE arm only. The component arm of the same discrimination is made
	// by the rule asked over the flattened composition, which reports an absent
	// recipe before this producer runs, so asserting it here would be asserting on
	// a reading that no longer happens.
	if want := `feature recipe "no-such-feature-recipe:1.0.0", which is not in the catalog`; !strings.Contains(absentErr.Error(), want) {
		t.Errorf("missing-entity diagnostic changed; expected %q in %v", want, absentErr)
	}
	// The component side of the same input, read through the producer that now
	// owns it: mapped, and absent from the catalog. Read here rather than left to
	// the rule's own proofs because it is the other half of THIS test's
	// discrimination — a shape fault and an absent entity are both mapped values,
	// and a producer that reported them the same way would be invisible from the
	// feature arm alone.
	componentAbsent := c.nodeRecipeResolution(
		manifestFor("no-such-recipe:1.0.0", "no-such-feature-recipe:1.0.0"), "db", compType, c.componentOfType)
	if !componentAbsent.Mapped || componentAbsent.Present {
		t.Errorf("a component mapped to an absent recipe must come back mapped and not present; got %+v", componentAbsent)
	}
	if componentAbsent.Reference != "no-such-recipe:1.0.0" {
		t.Errorf("reference = %q, want the mapping value verbatim", componentAbsent.Reference)
	}
	componentShape := c.nodeRecipeResolution(
		manifestFor("core/probe/present:1.0.0", "core/probe/cred:1.0.0"), "db", compType, c.componentOfType)
	if !componentShape.Mapped || componentShape.Present {
		t.Errorf("a component mapped past the segment ceiling must come back mapped and not present; got %+v", componentShape)
	}
}

// TestCatalogImmunity_UnmappedSingleImplementerStaysUnresolved pins the
// de-fallback permanently: an instance with NO Manifest mapping stays unresolved
// even when EXACTLY ONE recipe implements its type. Under the removed
// implements-search fallback the lone implementer resolved silently; resolution
// is mapping-only, so the instance comes back unmapped with that recipe offered
// as a pin candidate. This is red against the pre-removal code (which resolved a
// single implementer) and green after — the fallback is provably gone.
//
// It reads the resolution rather than a halt because the halt belongs to the rule
// asked over the flattened composition; what this pins is the input that rule
// reads, which is where the fallback would have to reappear to come back at all.
func TestCatalogImmunity_UnmappedSingleImplementerStaysUnresolved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "r.yaml", recipeYAML("only-db", "app/db"))
	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	resolution := c.nodeRecipeResolution(&types.Manifest{}, "db", "app/db", c.componentOfType)
	if resolution.Mapped || resolution.Present {
		t.Fatalf("an unmapped instance with exactly ONE implementer must stay unresolved; got %+v", resolution)
	}
	if !slices.ContainsFunc(resolution.PinCandidates, func(candidate string) bool {
		return strings.Contains(candidate, "only-db")
	}) {
		t.Errorf("the lone implementer must be offered as a pin candidate; got %v", resolution.PinCandidates)
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
