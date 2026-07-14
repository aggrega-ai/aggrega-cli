// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestLazyRecipeIndex_TolerantSkipsBroken is the blast-radius fix at the recipe
// surface: a broken recipe file in the catalog directory is SKIPPED (with a
// recorded reason), not fatal — so the valid recipes still enumerate and resolve.
// The eager catalog.Load, by contrast, aborts the whole walk on the first broken
// recipe; that is the defect this tolerance removes.
func TestLazyRecipeIndex_TolerantSkipsBroken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "good-a.yaml", recipeYAML("core/recipe-a", "core/a"))
	writeFixture(t, dir, "good-b.yaml", recipeYAML("core/recipe-b", "core/b"))
	// A recipe whose body fails load-time shape validation (no implements, no
	// input kind under a non-eval provider is rejected by ValidateRecipe).
	writeFixture(t, dir, "broken.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: core/recipe-broken
  version: 1.0.0
spec:
  capability: helm
`)

	ll := NewLazyLookup(dir, "", []string{dir}, nil)
	ll.buildRecipeIndex()

	if len(ll.recipes) != 2 {
		t.Fatalf("expected 2 valid recipes enumerated, got %d (%v)", len(ll.recipes), keysOf(ll.recipes))
	}
	if len(ll.recipeSkips) != 1 {
		t.Fatalf("expected the broken recipe to be skipped-with-reason, got %d skips", len(ll.recipeSkips))
	}
	// The valid recipes are still enumerated as candidates by implements.
	if len(recipesImplementing(ll.recipes, "core/a")) == 0 {
		t.Errorf("core/a should enumerate its recipe despite the unrelated broken recipe")
	}
	if len(recipesImplementing(ll.recipes, "core/b")) == 0 {
		t.Errorf("core/b should enumerate its recipe despite the unrelated broken recipe")
	}
}

// TestLazyRecipe_EagerLazyParity proves the suggestion enumerator over the
// lazily-enumerated recipe set yields the SAME candidate id-list as over the
// eager *Catalog's for the same VALID catalog — the enumerator, fed lazily,
// names the same implementing recipes. Non-vacuous: it asserts a non-empty
// candidate list on both sides and list equality.
func TestLazyRecipe_EagerLazyParity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "a.yaml", recipeYAML("core/recipe-a", "core/a"))
	writeFixture(t, dir, "b.yaml", recipeYAML("core/recipe-b", "core/b"))
	writeFixture(t, dir, "fr.yaml", featureRecipeYAML("sec/fr-x", "sec/x"))

	eager, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("eager Load: %v", err)
	}
	ll := NewLazyLookup(dir, "", []string{dir}, nil)
	ll.buildRecipeIndex()

	for _, typ := range []string{"core/a", "core/b"} {
		eids := recipesImplementing(eager.Recipes, typ)
		lids := recipesImplementing(ll.recipes, typ)
		if len(eids) == 0 || len(lids) == 0 {
			t.Fatalf("type %q: eager ids=%v lazy ids=%v (expected both non-empty)", typ, eids, lids)
		}
		if !slices.Equal(eids, lids) {
			t.Errorf("type %q candidate id-list differs: eager %v vs lazy %v", typ, eids, lids)
		}
	}
	efr := featureRecipesImplementing(eager.FeatureRecipes, "sec/x")
	lfr := featureRecipesImplementing(ll.featureRecipes, "sec/x")
	if len(efr) == 0 || len(lfr) == 0 || !slices.Equal(efr, lfr) {
		t.Errorf("feature recipe sec/x candidate parity failed: eager %v vs lazy %v", efr, lfr)
	}
}

// TestLazyRecipe_EnumeratesAllImplementers proves the suggestion enumerator over
// the lazy set names every implementer: two recipes implementing one type both
// appear in the candidate id-list. The suggestion enumerator must hold all
// implementers for a complete candidate list — a coverage gap would drop one
// from the pin suggestions surfaced on an unmapped-type halt.
func TestLazyRecipe_EnumeratesAllImplementers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "one.yaml", recipeYAML("core/recipe-one", "core/dup"))
	writeFixture(t, dir, "two.yaml", recipeYAML("core/recipe-two", "core/dup"))

	ll := NewLazyLookup(dir, "", []string{dir}, nil)
	ll.buildRecipeIndex()

	ids := recipesImplementing(ll.recipes, "core/dup")
	if len(ids) != 2 {
		t.Errorf("expected 2 colliding recipe identities, got %d (%v)", len(ids), ids)
	}
}

// TestValidateClosurePillars_ScopesToSuppliedLibraries proves the closure-scoped
// Pass-2 validates against the SUPPLIED library map: a Component whose $ref is
// satisfied by the supplied libraries passes, and the same Component validated
// against an empty library map fails (the hard in-closure library-reject — an
// in-closure $ref to an absent library is red). Non-vacuous by the pass/fail pair
// over one Component.
func TestValidateClosurePillars_ScopesToSuppliedLibraries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "library.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`)
	writeFixture(t, dir, "consumer.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "DbSizing#/schemas/Storage"
`)
	// Eager full load (incl. Pass-2) must succeed — the fixture is valid catalog,
	// so the closure-pillars pass is being measured against a real baseline.
	cat, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("eager LoadAll (with DbSizing present): %v", err)
	}

	// In-closure libraries present → passes, exactly as eager.
	if err := ValidateClosurePillars(cat.Components, cat.componentSources, nil, nil, cat.SchemaLibraries); err != nil {
		t.Fatalf("closure pillars should pass when the referenced library is in scope: %v", err)
	}
	// Same Component, empty library scope → the $ref cannot resolve → red.
	if err := ValidateClosurePillars(cat.Components, cat.componentSources, nil, nil, map[string]types.SchemaLibrary{}); err == nil {
		t.Fatal("closure pillars must fail when the referenced library is out of scope (in-closure $ref unresolvable)")
	}
}

// TestBrokenLibraryRecordedInSkips proves a broken schema library is tolerated
// (skipped, recorded in the shared skip map) rather than fatal — so a broken
// UNUSED library never fails an unrelated build, and the daemon can surface WHY a
// referenced one went missing. Non-vacuous: a clean library produces no skip.
func TestBrokenLibraryRecordedInSkips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "bad-library.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Bad
  version: 1.0.0
spec:
  schemas:
    Broken:
      $dynamicAnchor: forbidden
      type: string
`)
	ll := NewLazyLookup(dir, "", []string{dir}, nil)
	skips := ll.Skips()
	if len(skips) != 1 {
		t.Fatalf("expected the invalid library to be skipped-and-recorded; got %d skips", len(skips))
	}

	clean := t.TempDir()
	writeFixture(t, clean, "good-library.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Good
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`)
	if got := NewLazyLookup(clean, "", []string{clean}, nil).Skips(); len(got) != 0 {
		t.Errorf("a valid library must produce no skip; got %d", len(got))
	}
}

// TestLazyShell_CachedAndResetInvalidated pins the F1 binding note: recipeCatalog()
// returns the SAME cached shell across calls, and Reset invalidates it so a changed
// workspace re-walks and a fresh shell is built.
func TestLazyShell_CachedAndResetInvalidated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "a.yaml", recipeYAML("core/recipe-a", "core/a"))

	ll := NewLazyLookup(dir, "", []string{dir}, nil)
	c1 := ll.recipeCatalog()
	c2 := ll.recipeCatalog()
	if c1 != c2 {
		t.Fatal("recipeCatalog must return the cached shell across calls")
	}
	ll.Reset()
	c3 := ll.recipeCatalog()
	if c3 == c1 {
		t.Fatal("Reset must invalidate the cached shell so an edited workspace re-walks")
	}
}

// TestLazySubAggregateLoader proves the re-homed SubAggregateLoader resolves a
// lazily-enumerated Aggregate and reports a miss as a fatal error (its contract).
func TestLazySubAggregateLoader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "sub.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo/sub
  version: 1.0.0
spec:
  components: {}
`)
	ll := NewLazyLookup(dir, "", []string{dir}, nil)
	load := ll.SubAggregateLoader()

	agg, _, _, err := load("demo/sub", nil)
	if err != nil || agg == nil {
		t.Fatalf("SubAggregateLoader should resolve the enumerated aggregate; err=%v", err)
	}
	if _, _, _, err := load("demo/missing", nil); err == nil {
		t.Error("a missing sub-aggregate must be a fatal error per the loader contract")
	}
}

// TestEagerLazyClosureLookupDigestParity is the digest-fidelity guard for serve's
// lazy closure walk: the lazy ClosureLookup must return the SAME resolved version
// + content digest as the eager *Catalog.ClosureLookupWithExternals for every kind
// — Component (cascade), Recipe (shell), Aggregate (shell) — so a daemon drift
// verify compares like-for-like against a lockfile a build/pull wrote. A digest
// mismatch here is exactly the spurious-drift bug the retained-entityBytes design
// prevents. Non-vacuous: digests are asserted non-empty and equal.
func TestEagerLazyClosureLookupDigestParity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "comp.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeFixture(t, dir, "recipe.yaml", recipeYAML("server-recipe", "core/server"))
	writeFixture(t, dir, "agg.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo/agg
  version: "1.0.0"
spec:
  components: {}
`)

	eager, err := LoadAll(dir, dir)
	if err != nil {
		t.Fatalf("eager LoadAll: %v", err)
	}
	eagerLk := eager.ClosureLookupWithExternals(nil, dir, "", nil)
	lazyLk := NewLazyLookup(dir, "", []string{dir}, nil).ClosureLookup(nil, dir, "", nil)

	for _, uri := range []string{"core/server", "server-recipe:1.0.0", "demo/agg"} {
		er, eerr := eagerLk.ResolveCatalogEntity(uri, "")
		lr, lerr := lazyLk.ResolveCatalogEntity(uri, "")
		if (eerr == nil) != (lerr == nil) {
			t.Fatalf("%q: presence differs — eager err=%v lazy err=%v", uri, eerr, lerr)
		}
		if er.Digest == "" {
			t.Fatalf("%q: eager digest empty; parity check would be vacuous", uri)
		}
		if er.Digest != lr.Digest {
			t.Errorf("%q: digest differs — eager %s lazy %s", uri, er.Digest, lr.Digest)
		}
		if er.ResolvedVersion != lr.ResolvedVersion {
			t.Errorf("%q: resolved version differs — eager %q lazy %q", uri, er.ResolvedVersion, lr.ResolvedVersion)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
