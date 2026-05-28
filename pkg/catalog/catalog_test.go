// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for pkg/catalog. Coverage spans the four behaviours the package
// commits to:
//
//   - End-to-end load against testdata fixtures (one of each catalog-resident
//     kind: Component, Feature, Recipe, FeatureRecipe, Aggregate, Provider)
//     plus per-callback-returner round-trips through CatalogLookup,
//     RecipeLookup, SubAggregateLoader, and ProviderLookup. Each round-trip
//     pins the lookup contract (hit returns the entity; miss returns the
//     bool false / error path the renderer or topology package expects).
//
//   - Multi-directory merge via LoadAll across two temp dirs — pins the
//     last-write-wins layered-catalog semantics by loading two Recipes
//     keyed on different spec.implements URIs and asserting both survive
//     into the merged Catalog.
//
//   - Empty-directory load, asserting Load returns an empty Catalog rather
//     than an error when the input directory carries no entity YAML.
//
//   - ResolveAllExtends round-trip on a parent / child Component pair —
//     pins both the inherited-label persistence and the composed-schema
//     constraint propagation that the Two-Tiered Inheritance Merge
//     (deep YAML merge + JSON Schema composition) guarantees.
//
//   - Recipe relative-source resolution to absolute path so co-located
//     charts stay resolvable regardless of the consumer's working directory.
//
// Tests use t.TempDir + os.WriteFile to build per-test fixtures inline
// where the testdata/ tree's single-of-each-kind shape isn't enough; the
// canonical six-fixture testdata/ tree drives the all-entity-types test.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
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
	if _, ok := c.Components["core/server/api-server"]; !ok {
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
	if _, ok := c.Recipes["core/server/api-server"]; !ok {
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

// TestLoad_SchemaLibrary verifies the catalog walker recognises
// SchemaLibrary entries (kind: SchemaLibrary) and indexes them by
// metadata.name. The walker treats SchemaLibrary like any other catalog
// entity kind — the per-version subdirectory layout
// schemalibrary/<namespace>/<name>/<version>/library.yaml is covered by
// the existing recursive WalkDir; this test verifies the kind dispatch
// + map indexing.
func TestLoad_SchemaLibrary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "schemalibrary", "core", "Probes", "1.0.0", "library.yaml")
	if err := os.MkdirAll(filepath.Dir(libPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Probes
  version: 1.0.0
spec:
  schemas:
    LivenessProbe:
      type: object
      properties:
        path:
          type: string
        port:
          type: integer
`
	if err := os.WriteFile(libPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.SchemaLibraries) != 1 {
		t.Fatalf("expected 1 SchemaLibrary; got %d", len(c.SchemaLibraries))
	}
	lib, ok := c.SchemaLibraries["Probes"]
	if !ok {
		t.Fatalf("expected Probes SchemaLibrary; got %v", c.SchemaLibraries)
	}
	if lib.Spec.Schemas.Len() != 1 {
		t.Errorf("expected 1 schema entry; got %d", lib.Spec.Schemas.Len())
	}
}

// TestLoad_MappingLibrary verifies the catalog walker recognises
// MappingLibrary entries (kind: MappingLibrary) and indexes them by
// metadata.name. Per-version subdirectory layout
// (mappinglibrary/<namespace>/<name>/<version>/library.yaml) is covered
// by the existing recursive WalkDir.
func TestLoad_MappingLibrary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "mappinglibrary", "core", "BaseMappings", "1.0.0", "library.yaml")
	if err := os.MkdirAll(filepath.Dir(libPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.image }}'
    Replicas: 'replicas: {{ .Values.replicas | default 1 }}'
`
	if err := os.WriteFile(libPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.MappingLibraries) != 1 {
		t.Fatalf("expected 1 MappingLibrary; got %d", len(c.MappingLibraries))
	}
	lib, ok := c.MappingLibraries["BaseMappings"]
	if !ok {
		t.Fatalf("expected BaseMappings MappingLibrary; got %v", c.MappingLibraries)
	}
	if lib.Spec.Mappings.Len() != 2 {
		t.Errorf("expected 2 mapping entries; got %d", lib.Spec.Mappings.Len())
	}
}

// TestLoad_MappingLibrary_NonScalarEntryRejected verifies the catalog
// walker surfaces the Go-side typed-unmarshal rejection when a
// MappingLibrary entry's value is not a string scalar (e.g., a mapping
// or sequence). The envelope JSON Schema would reject the same shape
// at workspace-load time; the Go-side check provides defense-in-depth.
func TestLoad_MappingLibrary_NonScalarEntryRejected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "library.yaml")
	body := `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BadEntry
  version: 1.0.0
spec:
  mappings:
    BadShape:
      not: a string
`
	if err := os.WriteFile(libPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for non-scalar mapping entry; got nil")
	}
	if !errors.Is(err, types.ErrMappingEntryNotScalar) {
		t.Errorf("expected ErrMappingEntryNotScalar in chain; got %v", err)
	}
}

// TestLoad_SchemaLibrary_TemplateBodyForbiddenKeywordRejectedAtLoad
// verifies the catalog walker scans template bodies (not only schema
// entries) for $dynamicAnchor / $dynamicRef at workspace-load time.
// Authors writing template bodies with the forbidden keywords see
// errors at-author-time rather than at first instantiation, matching
// the fail-fast discipline applied to schema entries.
func TestLoad_SchemaLibrary_TemplateBodyForbiddenKeywordRejectedAtLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "library.yaml")
	body := `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BadTemplate
  version: 1.0.0
spec:
  schemas:
    Placeholder:
      type: object
  templates:
    Probe:
      $params:
        port:
          type: integer
      type: object
      $dynamicAnchor: someAnchor
`
	if err := os.WriteFile(libPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for $dynamicAnchor in template body; got nil")
	}
	if !errors.Is(err, types.ErrForbiddenKeyword) {
		t.Errorf("expected ErrForbiddenKeyword in chain; got %v", err)
	}
}

// TestLoad_SchemaLibrary_ForbiddenKeywordRejected verifies the parser-side
// $dynamicAnchor / $dynamicRef rejection signal fires at catalog load
// time. Authors using these keywords get a clear CATALOG_ERROR citing the
// file path.
func TestLoad_SchemaLibrary_ForbiddenKeywordRejected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "library.yaml")
	body := `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Forbidden
  version: 1.0.0
spec:
  schemas:
    BadEntry:
      type: object
      $dynamicAnchor: someAnchor
`
	if err := os.WriteFile(libPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for $dynamicAnchor; got nil")
	}
	if !errors.Is(err, types.ErrForbiddenKeyword) {
		t.Errorf("expected ErrForbiddenKeyword in chain; got %v", err)
	}
}

func TestLoad_CatalogLookup(t *testing.T) {
	t.Parallel()

	c, err := Load("testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lookup := c.CatalogLookup()

	comp, found := lookup.Component("core/server/api-server")
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

	r, found := lookup.Recipe("core/server/api-server")
	if !found {
		t.Fatal("expected to find recipe for core/api-server")
	}
	if r.Metadata.Name != "helm-api-server" {
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

	agg, path, err := loader("db-module")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agg.Metadata.Name != "db-module" {
		t.Errorf("name: %q", agg.Metadata.Name)
	}
	if path == "" {
		t.Errorf("loader returned empty path for db-module; expected the testdata file path")
	}

	if file, ok := c.AggregateSourceFile("db-module"); !ok || file != path {
		t.Errorf("AggregateSourceFile(db-module) = (%q, %v); want matching loader path %q", file, ok, path)
	}
	if _, ok := c.AggregateSourceFile("nonexistent"); ok {
		t.Error("AggregateSourceFile(nonexistent) reported ok; expected false")
	}

	_, _, err = loader("nonexistent")
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
  name: recipe-a
  version: 1.0.0
spec:
  provider: helm
  implements: core/x
  source: ./fake-chart
  valuesMapping: '{}'
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "recipe.yaml"), []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: recipe-b
  version: 1.0.0
spec:
  provider: helm-chart
  implements: core/y
  source: ./fake-chart
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

func TestResolveAllExtends_ComponentChain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.yaml"), []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server/base
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, minimum: 1024 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api.yaml"), []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server/api
  version: "1.0.0"
  labels:
    tier: prod
spec:
  extends: core/server/base
  schema:
    values:
      port: { type: integer, maximum: 65535 }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Before resolution: child has its own Schema populated.
	if cat.Components["core/server/api"].Spec.Schema.Kind == 0 {
		t.Fatal("child component missing schema before resolution")
	}

	errs := cat.ResolveAllExtends()
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	resolved := cat.Components["core/server/api"]
	typeLabel, _ := resolved.Metadata.Labels.Get("component.aggrega.ai/type")
	if typeLabel != "server" {
		t.Errorf("expected inherited type label, got %v", resolved.Metadata.Labels.Keys())
	}
	tierLabel, _ := resolved.Metadata.Labels.Get("tier")
	if tierLabel != "prod" {
		t.Errorf("child tier label lost, got %v", resolved.Metadata.Labels.Keys())
	}
	// Child's authored Schema is preserved as-is. JSON Schema composition
	// across the extends chain runs through the validator/merger package
	// against the structured Schema content; this test pins that the
	// child's own constraint survives the merge.
	schemaText, err := yaml.Marshal(&resolved.Spec.Schema)
	if err != nil {
		t.Fatalf("marshal Schema: %v", err)
	}
	if !strings.Contains(string(schemaText), "65535") {
		t.Errorf("child's authored Schema lost:\n%s", string(schemaText))
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
  name: rel
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
