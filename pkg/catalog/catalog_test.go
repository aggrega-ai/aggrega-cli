// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
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
