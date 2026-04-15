// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_AllEntityTypes(t *testing.T) {
	t.Parallel()

	c, err := Load("testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.Components) != 1 {
		t.Errorf("expected 1 component, got %d", len(c.Components))
	}
	if _, ok := c.Components["core/api-server"]; !ok {
		t.Error("missing core/api-server component")
	}

	if len(c.Features) != 1 {
		t.Errorf("expected 1 feature, got %d", len(c.Features))
	}
	if _, ok := c.Features["core/monitoring"]; !ok {
		t.Error("missing core/monitoring feature")
	}

	if len(c.Recipes) != 1 {
		t.Errorf("expected 1 recipe, got %d", len(c.Recipes))
	}
	if _, ok := c.Recipes["core/api-server"]; !ok {
		t.Error("missing recipe implementing core/api-server")
	}

	if len(c.FeatureRecipes) != 1 {
		t.Errorf("expected 1 feature recipe, got %d", len(c.FeatureRecipes))
	}

	if len(c.Aggregates) != 1 {
		t.Errorf("expected 1 aggregate, got %d", len(c.Aggregates))
	}
	if _, ok := c.Aggregates["db-module"]; !ok {
		t.Error("missing db-module aggregate")
	}

	if len(c.Providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(c.Providers))
	}
	if _, ok := c.Providers["helm-chart"]; !ok {
		t.Error("missing helm-chart provider")
	}
}

func TestProviderLookup(t *testing.T) {
	t.Parallel()

	c, err := Load("testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p, ok := c.ProviderLookup("helm-chart")
	if !ok {
		t.Fatal("expected helm-chart provider")
	}
	if p.Spec.BaseSchema == "" {
		t.Error("expected baseSchema to be populated")
	}

	_, ok = c.ProviderLookup("nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestLoad_CatalogLookup(t *testing.T) {
	t.Parallel()

	c, err := Load("testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lookup := c.CatalogLookup()

	comp, found := lookup.Component("core/api-server")
	if !found {
		t.Fatal("expected to find core/api-server")
	}
	if comp.Metadata.Version != "1.0.0" {
		t.Errorf("version: %q", comp.Metadata.Version)
	}

	_, found = lookup.Component("nonexistent")
	if found {
		t.Error("expected not found for nonexistent component")
	}

	feat, found := lookup.Feature("core/monitoring")
	if !found {
		t.Fatal("expected to find core/monitoring")
	}
	if feat.Metadata.Name != "core/monitoring" {
		t.Errorf("name: %q", feat.Metadata.Name)
	}
}

func TestLoad_RecipeLookup(t *testing.T) {
	t.Parallel()

	c, err := Load("testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lookup := c.RecipeLookup()

	r, found := lookup.Recipe("core/api-server")
	if !found {
		t.Fatal("expected to find recipe for core/api-server")
	}
	if r.Metadata.Name != "ops/helm-api-server" {
		t.Errorf("recipe name: %q", r.Metadata.Name)
	}

	_, found = lookup.Recipe("nonexistent")
	if found {
		t.Error("expected not found")
	}
}

func TestLoad_SubAggregateLoader(t *testing.T) {
	t.Parallel()

	c, err := Load("testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loader := c.SubAggregateLoader()

	agg, err := loader("db-module")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agg.Metadata.Name != "db-module" {
		t.Errorf("name: %q", agg.Metadata.Name)
	}

	_, err = loader("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent aggregate")
	}
}

func TestLoad_EmptyDir(t *testing.T) {
	t.Parallel()

	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.Components) != 0 || len(c.Features) != 0 {
		t.Error("expected empty catalog")
	}
}

func TestLoadAll_MergesAcrossDirs(t *testing.T) {
	t.Parallel()

	dirA := t.TempDir()
	dirB := t.TempDir()

	if err := os.WriteFile(filepath.Join(dirA, "recipe.yaml"), []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: ops/recipe-a
  version: 1.0.0
spec:
  provider: helm
  implements: core/x
  valuesMapping: '{}'
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "recipe.yaml"), []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: ops/recipe-b
  version: 1.0.0
spec:
  provider: helm-chart
  implements: core/y
  valuesMapping: '{}'
`), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadAll(dirA, dirB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := c.Recipes["core/x"]; !ok {
		t.Error("missing core/x from dirA")
	}
	if _, ok := c.Recipes["core/y"]; !ok {
		t.Error("missing core/y from dirB")
	}
}

func TestLoad_RelativeRecipeSourceResolvedToAbsPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	recipeDir := filepath.Join(dir, "nested")
	if err := os.MkdirAll(filepath.Join(recipeDir, "charts", "my-chart"), 0o755); err != nil {
		t.Fatal(err)
	}
	recipePath := filepath.Join(recipeDir, "recipe.yaml")
	if err := os.WriteFile(recipePath, []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: ops/rel
  version: 1.0.0
spec:
  provider: helm
  implements: core/rel
  source: ./charts/my-chart
  valuesMapping: '{}'
`), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := c.Recipes["core/rel"]
	if !ok {
		t.Fatal("recipe not loaded")
	}
	s, ok := r.Spec.Source.(string)
	if !ok {
		t.Fatalf("source not a string: %T", r.Spec.Source)
	}
	if !filepath.IsAbs(s) {
		t.Errorf("source not absolute: %q", s)
	}
	if !strings.HasSuffix(filepath.ToSlash(s), "/nested/charts/my-chart") {
		t.Errorf("source path not resolved against recipe dir: %q", s)
	}
}
