// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Tests for the provider-scoped Recipe/FeatureRecipe identity: two providers
// shipping a recipe of the same name and version coexist (the catalog tree
// and registry identity are provider-scoped); a same-provider duplicate is
// a genuine collision. The two backings carry different duplicate postures
// by design — the eager loader halts loudly, the lazy enumerator
// skip-records the shadowed file (its tolerant blast-radius posture) — and
// each is pinned as it is.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// writeCatalogFile is the in-package twin of the external test package's
// writeYAMLToCatalog helper (which lives in catalog_test and is not
// importable here).
func writeCatalogFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

const twoProviderRecipeFmt = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: mariadb-rds
  version: 1.0.0
spec:
  capability: %s
  implements: core/datastore/mariadb:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      app:
        image: mariadb:11
        ports:
          db: 3306
    services:
      svc:
        type: ClusterIP
        ports:
          db: 3306
`

// TestLoad_TwoProvidersSameRecipeName mirrors the real hub catalog shape:
// terraform and cloudformation each ship mariadb-rds:1.0.0. Both load, each
// resolvable by its provider-qualified URI, and the retained bytes stay
// per-entity (the byte cache is keyed by full identity, so same-name
// recipes cannot shadow each other's digests).
func TestLoad_TwoProvidersSameRecipeName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tfBody := strings.Replace(twoProviderRecipeFmt, "%s", "terraform", 1)
	cfBody := strings.Replace(twoProviderRecipeFmt, "%s", "cloudformation", 1)
	writeCatalogFile(t, dir, "recipe/terraform/mariadb-rds/recipe.yaml", tfBody)
	writeCatalogFile(t, dir, "recipe/cloudformation/mariadb-rds/recipe.yaml", cfBody)

	cat, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Recipes) != 2 {
		t.Fatalf("expected 2 recipes, got %d (keys %v)", len(cat.Recipes), recipeKeys(cat))
	}

	tf, ok := cat.RecipeByURI("terraform/mariadb-rds:1.0.0")
	if !ok || tf.Spec.Capability != "terraform" {
		t.Errorf("qualified exact: got %+v ok=%v", tf.Spec.Capability, ok)
	}
	cf, ok := cat.RecipeByURI("cloudformation/mariadb-rds")
	if !ok || cf.Spec.Capability != "cloudformation" {
		t.Errorf("qualified unversioned: got %+v ok=%v", cf.Spec.Capability, ok)
	}

	// Bare references are ambiguous across the two providers: loud miss,
	// never a silent pick.
	if _, ok := cat.RecipeByURI("mariadb-rds:1.0.0"); ok {
		t.Error("bare versioned reference should miss on cross-provider ambiguity")
	}
	if _, ok := cat.RecipeByURI("mariadb-rds"); ok {
		t.Error("bare unversioned reference should miss on cross-provider ambiguity")
	}

	// Byte cache: each identity retains its own bytes.
	tfBytes, ok := cat.EntityBytes("Recipe", "terraform/mariadb-rds:1.0.0")
	if !ok || !strings.Contains(string(tfBytes), "capability: terraform") {
		t.Errorf("terraform bytes: ok=%v body-match=%v", ok, strings.Contains(string(tfBytes), "capability: terraform"))
	}
	cfBytes, ok := cat.EntityBytes("Recipe", "cloudformation/mariadb-rds:1.0.0")
	if !ok || !strings.Contains(string(cfBytes), "capability: cloudformation") {
		t.Errorf("cloudformation bytes: ok=%v body-match=%v", ok, strings.Contains(string(cfBytes), "capability: cloudformation"))
	}
}

// TestLoad_SameProviderDuplicateHalts pins the eager backing's posture: a
// genuine duplicate (same provider, name, version) halts with
// ErrDuplicateRecipeIdentity naming the provider-qualified identity.
func TestLoad_SameProviderDuplicateHalts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := strings.Replace(twoProviderRecipeFmt, "%s", "terraform", 1)
	writeCatalogFile(t, dir, "recipe/terraform/mariadb-rds/recipe.yaml", body)
	writeCatalogFile(t, dir, "recipe/terraform/mariadb-rds-copy/recipe.yaml", body)

	_, err := LoadAll("", dir)
	if err == nil {
		t.Fatal("expected duplicate-identity halt")
	}
	if !errors.Is(err, ErrDuplicateRecipeIdentity()) {
		t.Errorf("errors.Is(ErrDuplicateRecipeIdentity) = false; err = %v", err)
	}
	// The qualified identity is carried by the frame that wraps the
	// sentinel; the outermost frame's Error() renders only its own
	// message, so walk the chain.
	found := false
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), `"terraform/mariadb-rds:1.0.0"`) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no frame in the halt chain names the provider-qualified identity; got %v", err)
	}
}

// TestLazy_TwoProvidersCoexist_SameProviderShadows pins the lazy backing:
// cross-provider same-name recipes coexist (no skip), while a same-provider
// duplicate is skip-recorded as shadowed — the lazy enumerator's
// pre-existing tolerant posture, deliberately different from the eager halt.
func TestLazy_TwoProvidersCoexist_SameProviderShadows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCatalogFile(t, dir, "recipe/terraform/mariadb-rds/recipe.yaml",
		strings.Replace(twoProviderRecipeFmt, "%s", "terraform", 1))
	writeCatalogFile(t, dir, "recipe/cloudformation/mariadb-rds/recipe.yaml",
		strings.Replace(twoProviderRecipeFmt, "%s", "cloudformation", 1))

	l := NewLazyLookup(dir, ".aggrega/cache", []string{dir}, nil)
	cat := l.recipeCatalog()
	if len(cat.Recipes) != 2 {
		t.Fatalf("lazy: expected 2 recipes, got %d", len(cat.Recipes))
	}
	if len(l.recipeSkips) != 0 {
		t.Errorf("cross-provider pair should record no skips; got %v", l.recipeSkips)
	}

	// Same-provider duplicate: one of the two files is shadowed (walk-order
	// dependent which), recorded with the qualified identity.
	dir2 := t.TempDir()
	body := strings.Replace(twoProviderRecipeFmt, "%s", "terraform", 1)
	writeCatalogFile(t, dir2, "recipe/terraform/a/recipe.yaml", body)
	writeCatalogFile(t, dir2, "recipe/terraform/b/recipe.yaml", body)
	l2 := NewLazyLookup(dir2, ".aggrega/cache", []string{dir2}, nil)
	cat2 := l2.recipeCatalog()
	if len(cat2.Recipes) != 1 {
		t.Fatalf("same-provider dup: expected 1 surviving recipe, got %d", len(cat2.Recipes))
	}
	if len(l2.recipeSkips) != 1 {
		t.Fatalf("expected 1 shadow skip, got %v", l2.recipeSkips)
	}
	for _, reason := range l2.recipeSkips {
		if !strings.Contains(reason.Error(), "terraform/mariadb-rds:1.0.0") {
			t.Errorf("skip reason should carry the qualified identity; got %v", reason)
		}
	}
}

// TestRecipeByURI_BareSingleProvider pins bare-reference resolution when exactly
// one capability carries the name: an exact pin resolves that version, a caret
// pin the highest version in range, an unversioned reference the highest
// available, and a pin the catalog cannot satisfy a miss rather than a quiet
// substitution.
//
// Two assertions below REVERSE behaviours this test previously pinned. Both old
// behaviours were deliberate — each was stated as a contract in the lookup's own
// documentation and asserted here — so the reversals are recorded rather than
// dropped: a tested behaviour that vanishes without a trace leaves the next reader
// to re-derive the question.
//
// A version pin the catalog could not satisfy used to resolve to whatever single
// recipe carried the name. That tolerance was designed, not stumbled into: a
// versioned reference whose exact version was absent fell back to a version-agnostic
// scan so a versioned reference behaved like a bare one. It is a miss now, because
// a version pin is a request and answering it with a version the author did not
// write is a silent substitution — the identical typo on a component type has
// always halted, and one version rule across every entity kind is worth more than
// the tolerance it costs. The miss is not silent either: it reaches the operator as
// a loud resolution halt naming the mapping value.
//
// An unversioned reference across two versions of one recipe used to miss as
// ambiguous, on the reading that any tier finding more than one candidate must
// refuse rather than guess. It resolves to the highest version now, because
// choosing among the versions of ONE recipe is precisely what a version rule
// decides and involves no guess: highest-satisfying is the answer an unversioned
// component, feature, aggregate or plugin reference has always received. The
// refusal survives where it still answers a real ambiguity — across CAPABILITIES,
// where nothing in the reference selects one, pinned by
// TestLoad_TwoProvidersSameRecipeName.
func TestRecipeByURI_BareSingleProvider(t *testing.T) {
	t.Parallel()
	v2 := types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-rds", Version: "2.0.0"},
		Spec:     types.RecipeSpec{Capability: "terraform", Implements: "core/datastore/mariadb"},
	}
	v1 := types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-rds", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Capability: "terraform", Implements: "core/datastore/mariadb"},
	}

	one := fakeCatalog(v1)
	if r, ok := one.RecipeByURI("mariadb-rds:1.0.0"); !ok || r.Metadata.Version != "1.0.0" {
		t.Errorf("bare versioned single-provider: ok=%v", ok)
	}
	if r, ok := one.RecipeByURI("mariadb-rds"); !ok || r.Metadata.Version != "1.0.0" {
		t.Errorf("bare unversioned single-provider: ok=%v", ok)
	}
	// Reversed: see this test's own note on the retired version-agnostic fallback.
	if r, ok := one.RecipeByURI("mariadb-rds:9.9.9"); ok {
		t.Errorf("an unsatisfiable version pin must miss, not substitute another version; resolved %q", r.Metadata.Version)
	}

	two := fakeCatalog(v1, v2)
	if r, ok := two.RecipeByURI("mariadb-rds:2.0.0"); !ok || r.Metadata.Version != "2.0.0" {
		t.Errorf("version-exact across two versions: ok=%v", ok)
	}
	// Picking the highest must not swallow an exact pin on the LOWER version;
	// without this, "resolves the highest" would pass on an implementation that
	// ignored the pin entirely.
	if r, ok := two.RecipeByURI("mariadb-rds:1.0.0"); !ok || r.Metadata.Version != "1.0.0" {
		t.Errorf("exact pin on the lower of two versions: ok=%v version=%q", ok, r.Metadata.Version)
	}
	// Reversed: see this test's own note on the retired version-axis ambiguity
	// refusal.
	if r, ok := two.RecipeByURI("mariadb-rds"); !ok || r.Metadata.Version != "2.0.0" {
		t.Errorf("unversioned across two versions resolves the highest: ok=%v version=%q", ok, r.Metadata.Version)
	}
	if r, ok := two.RecipeByURI("terraform/mariadb-rds:2.0.0"); !ok || r.Metadata.Version != "2.0.0" {
		t.Errorf("qualified exact across two versions: ok=%v", ok)
	}
	// The caret form is now honoured on the recipe path rather than compared as a
	// literal version string: ^1.0.0 admits 1.0.0 and excludes 2.0.0.
	if r, ok := two.RecipeByURI("terraform/mariadb-rds:^1.0.0"); !ok || r.Metadata.Version != "1.0.0" {
		t.Errorf("qualified caret pins the major: ok=%v version=%q", ok, r.Metadata.Version)
	}
	if r, ok := two.RecipeByURI("terraform/mariadb-rds:^2.0.0"); !ok || r.Metadata.Version != "2.0.0" {
		t.Errorf("qualified caret at the higher major: ok=%v version=%q", ok, r.Metadata.Version)
	}
	// A named capability that holds the recipe but not the pinned version misses on
	// the pin; it does not fall through to a version-agnostic reading of the same name.
	if r, ok := two.RecipeByURI("terraform/mariadb-rds:9.9.9"); ok {
		t.Errorf("qualified unsatisfiable pin must miss; resolved %q", r.Metadata.Version)
	}
}

// TestFeatureRecipeByURI_CapabilityScopedVersion guards the FeatureRecipe side of
// the shared resolution ladder. Recipe and FeatureRecipe resolve through one
// implementation reached by two per-type adapters, so the algorithm itself is
// covered once (above) — but an adapter that read the wrong field, or read the
// capability and the name in the wrong order, would be invisible to a test whose
// fixtures leave the capability empty. These cases exercise the capability segment
// and the version segment together on the FeatureRecipe adapter specifically.
func TestFeatureRecipeByURI_CapabilityScopedVersion(t *testing.T) {
	t.Parallel()
	mk := func(capability, name, version string) types.FeatureRecipe {
		var fr types.FeatureRecipe
		fr.Metadata.Name = name
		fr.Metadata.Version = version
		fr.Spec.Capability = capability
		return fr
	}
	c := &Catalog{FeatureRecipes: map[string]types.FeatureRecipe{}}
	for _, fr := range []types.FeatureRecipe{
		mk("helm", "rewrite-host", "1.0.0"),
		mk("helm", "rewrite-host", "2.0.0"),
		mk("compose", "rewrite-host", "1.0.0"),
	} {
		c.FeatureRecipes[recipeIdentityKey(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version)] = fr
	}

	if fr, ok := c.FeatureRecipeByURI("helm/rewrite-host"); !ok || fr.Metadata.Version != "2.0.0" {
		t.Errorf("qualified unversioned resolves the highest in that capability: ok=%v version=%q", ok, fr.Metadata.Version)
	}
	if fr, ok := c.FeatureRecipeByURI("compose/rewrite-host"); !ok || fr.Spec.Capability != "compose" {
		t.Errorf("the capability segment selects the capability, not the name: ok=%v capability=%q", ok, fr.Spec.Capability)
	}
	if fr, ok := c.FeatureRecipeByURI("helm/rewrite-host:1.0.0"); !ok || fr.Metadata.Version != "1.0.0" {
		t.Errorf("qualified exact: ok=%v version=%q", ok, fr.Metadata.Version)
	}
	if fr, ok := c.FeatureRecipeByURI("helm/rewrite-host:9.9.9"); ok {
		t.Errorf("qualified unsatisfiable pin must miss; resolved %q", fr.Metadata.Version)
	}
	// Two capabilities offer the name, so a bare reference selects neither — the
	// refusal that survives on the capability axis.
	if fr, ok := c.FeatureRecipeByURI("rewrite-host"); ok {
		t.Errorf("bare reference across two capabilities must miss; resolved %q/%q", fr.Spec.Capability, fr.Metadata.Version)
	}
}

// TestRecipeLookupWithManifest_ProviderQualifiedMapping pins the Manifest
// surface: a flat mapping whose value is provider-qualified resolves the
// intended provider's recipe through layer 1.
func TestRecipeLookupWithManifest_ProviderQualifiedMapping(t *testing.T) {
	t.Parallel()
	tf := types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-rds", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Capability: "terraform", Implements: "core/datastore/mariadb"},
	}
	cf := types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-rds", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Capability: "cloudformation", Implements: "core/datastore/mariadb"},
	}
	c := fakeCatalog(tf, cf)

	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/datastore/mariadb", "terraform/mariadb-rds:1.0.0")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flatComponents},
		},
	}
	lookup := c.RecipeLookupWithManifest(manifest)
	r, ok := lookup.Recipe("", "core/datastore/mariadb")
	if !ok {
		t.Fatal("provider-qualified mapping should resolve")
	}
	if r.Spec.Capability != "terraform" {
		t.Errorf("resolved provider = %q; want terraform", r.Spec.Capability)
	}

	// Without a mapping, the implements-search sees BOTH recipes and
	// misses (the loud >1 ambiguity contract, surfaced upstream).
	bare := c.RecipeLookupWithManifest(nil)
	if _, ok := bare.Recipe("", "core/datastore/mariadb"); ok {
		t.Error("two implementing recipes with no mapping should miss (ambiguous)")
	}
}

func recipeKeys(c *Catalog) []string {
	keys := make([]string, 0, len(c.Recipes))
	for k := range c.Recipes {
		keys = append(keys, k)
	}
	return keys
}
