// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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

const twoProviderRecipeFmt = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: mariadb-rds
  version: 1.0.0
spec:
  provider: %s
  implements: core/database/mariadb:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1alpha1
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

	cat, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Recipes) != 2 {
		t.Fatalf("expected 2 recipes, got %d (keys %v)", len(cat.Recipes), recipeKeys(cat))
	}

	tf, ok := cat.RecipeByURI("terraform/mariadb-rds:1.0.0")
	if !ok || tf.Spec.Provider != "terraform" {
		t.Errorf("qualified exact: got %+v ok=%v", tf.Spec.Provider, ok)
	}
	cf, ok := cat.RecipeByURI("cloudformation/mariadb-rds")
	if !ok || cf.Spec.Provider != "cloudformation" {
		t.Errorf("qualified unversioned: got %+v ok=%v", cf.Spec.Provider, ok)
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
	if !ok || !strings.Contains(string(tfBytes), "provider: terraform") {
		t.Errorf("terraform bytes: ok=%v body-match=%v", ok, strings.Contains(string(tfBytes), "provider: terraform"))
	}
	cfBytes, ok := cat.EntityBytes("Recipe", "cloudformation/mariadb-rds:1.0.0")
	if !ok || !strings.Contains(string(cfBytes), "provider: cloudformation") {
		t.Errorf("cloudformation bytes: ok=%v body-match=%v", ok, strings.Contains(string(cfBytes), "provider: cloudformation"))
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

	_, err := Load("", dir)
	if err == nil {
		t.Fatal("expected duplicate-identity halt")
	}
	if !errors.Is(err, ErrDuplicateRecipeIdentity) {
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

// TestRecipeByURI_BareSingleProvider pins bare-reference resolution when
// exactly one provider carries the name (versioned, version-tolerant, and
// unversioned forms), plus version-exact preference when one provider holds
// multiple versions.
func TestRecipeByURI_BareSingleProvider(t *testing.T) {
	t.Parallel()
	v2 := types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-rds", Version: "2.0.0"},
		Spec:     types.RecipeSpec{Provider: "terraform", Implements: "core/database/mariadb"},
	}
	v1 := types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-rds", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Provider: "terraform", Implements: "core/database/mariadb"},
	}

	one := fakeCatalog(v1)
	if r, ok := one.RecipeByURI("mariadb-rds:1.0.0"); !ok || r.Metadata.Version != "1.0.0" {
		t.Errorf("bare versioned single-provider: ok=%v", ok)
	}
	if r, ok := one.RecipeByURI("mariadb-rds"); !ok || r.Metadata.Version != "1.0.0" {
		t.Errorf("bare unversioned single-provider: ok=%v", ok)
	}
	if _, ok := one.RecipeByURI("mariadb-rds:9.9.9"); !ok {
		t.Error("version-tolerant fallback (single carrier) should resolve")
	}

	two := fakeCatalog(v1, v2)
	if r, ok := two.RecipeByURI("mariadb-rds:2.0.0"); !ok || r.Metadata.Version != "2.0.0" {
		t.Errorf("version-exact across two versions: ok=%v", ok)
	}
	if _, ok := two.RecipeByURI("mariadb-rds"); ok {
		t.Error("unversioned across two versions should miss (ambiguous)")
	}
	if r, ok := two.RecipeByURI("terraform/mariadb-rds:2.0.0"); !ok || r.Metadata.Version != "2.0.0" {
		t.Errorf("qualified exact across two versions: ok=%v", ok)
	}
}

// TestRecipeLookupWithManifest_ProviderQualifiedMapping pins the Manifest
// surface: a flat mapping whose value is provider-qualified resolves the
// intended provider's recipe through layer 1.
func TestRecipeLookupWithManifest_ProviderQualifiedMapping(t *testing.T) {
	t.Parallel()
	tf := types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-rds", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Provider: "terraform", Implements: "core/database/mariadb"},
	}
	cf := types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-rds", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Provider: "cloudformation", Implements: "core/database/mariadb"},
	}
	c := fakeCatalog(tf, cf)

	flatComponents := &types.MappingTier{}
	flatComponents.Type.Set("core/database/mariadb", "terraform/mariadb-rds:1.0.0")
	manifest := &types.Manifest{
		Spec: types.ManifestSpec{
			Mappings: types.ManifestMappings{Components: flatComponents},
		},
	}
	lookup := c.RecipeLookupWithManifest(manifest)
	r, ok := lookup.Recipe("", "core/database/mariadb")
	if !ok {
		t.Fatal("provider-qualified mapping should resolve")
	}
	if r.Spec.Provider != "terraform" {
		t.Errorf("resolved provider = %q; want terraform", r.Spec.Provider)
	}

	// Without a mapping, the implements-search sees BOTH recipes and
	// misses (the loud >1 ambiguity contract, surfaced upstream).
	bare := c.RecipeLookupWithManifest(nil)
	if _, ok := bare.Recipe("", "core/database/mariadb"); ok {
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
