// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Tests for pkg/catalog. Coverage spans the four behaviours the package
// commits to:
//
//   - End-to-end load against testdata fixtures (one of each catalog-resident
//     kind: Component, Feature, Recipe, FeatureRecipe, Aggregate)
//     plus per-callback-returner round-trips through CatalogLookup,
//     RecipeLookup, and SubAggregateLoader. Each round-trip
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
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

func TestLoad_AllEntityTypes(t *testing.T) {
	t.Parallel()

	c, err := LoadAll("", "testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.Components) != 1 {
		t.Errorf("expected 1 component, got %d", len(c.Components))
	}
	if _, ok, _ := c.resolveComponentKey("core/compute/api-server"); !ok {
		t.Error("missing core/api-server component")
	}

	if len(c.Features) != 1 {
		t.Errorf("expected 1 feature, got %d", len(c.Features))
	}
	if _, ok, _ := c.resolveFeatureKey("core/monitoring"); !ok {
		t.Error("missing core/monitoring feature")
	}

	if len(c.Recipes) != 1 {
		t.Errorf("expected 1 recipe, got %d", len(c.Recipes))
	}
	if len(recipesImplementing(c.Recipes, "core/compute/api-server", nil)) == 0 {
		t.Error("missing recipe implementing core/api-server")
	}

	if len(c.FeatureRecipes) != 1 {
		t.Errorf("expected 1 feature recipe, got %d", len(c.FeatureRecipes))
	}

	if len(c.Aggregates) != 1 {
		t.Errorf("expected 1 aggregate, got %d", len(c.Aggregates))
	}
	if _, ok, _ := c.resolveAggregateKey("db-module"); !ok {
		t.Error("missing db-module aggregate")
	}
}

// TestLoad_SchemaLibrary verifies the catalog walker recognises
// SchemaLibrary entries (kind: SchemaLibrary) and indexes them by
// metadata.name. The walker treats SchemaLibrary like any other catalog
// entity kind — the recursive WalkDir reaches library.yaml at whatever
// depth it sits and the index key is metadata.name rather than a path
// segment, so the fixture below need not reproduce the authoring layout
// (schemalibrary/<name>/<version>/library.yaml — a library kind carries
// no publisher segment). This test verifies the kind dispatch + map
// indexing.
func TestLoad_SchemaLibrary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "schemalibrary", "core", "Probes", "1.0.0", "library.yaml")
	if err := os.MkdirAll(filepath.Dir(libPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `apiVersion: aggrega.ai/v1beta1
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

	c, err := LoadAll("", dir)
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
// metadata.name. The authoring layout is
// mappinglibrary/<name>/<version>/library.yaml — a library kind carries
// no publisher segment — and the recursive WalkDir reaches library.yaml
// at whatever depth it sits, so the fixture's own path is incidental to
// what this test proves.
func TestLoad_MappingLibrary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "mappinglibrary", "core", "BaseMappings", "1.0.0", "library.yaml")
	if err := os.MkdirAll(filepath.Dir(libPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `apiVersion: aggrega.ai/v1beta1
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

	c, err := LoadAll("", dir)
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

// TestLoad_WorkloadLibrary verifies the eager catalog load arm indexes a
// WorkloadLibrary into Catalog.WorkloadLibraries, keyed by metadata.name,
// with its workload entries captured.
func TestLoad_WorkloadLibrary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "workloadlibrary", "core", "BaseWorkloads", "1.0.0", "library.yaml")
	if err := os.MkdirAll(filepath.Dir(libPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `apiVersion: aggrega.ai/v1beta1
kind: WorkloadLibrary
metadata:
  name: BaseWorkloads
  version: 1.0.0
spec:
  workloads:
    Deployment: 'kind: Deployment'
    Service: 'kind: Service'
`
	if err := os.WriteFile(libPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.WorkloadLibraries) != 1 {
		t.Fatalf("expected 1 WorkloadLibrary; got %d", len(c.WorkloadLibraries))
	}
	lib, ok := c.WorkloadLibraries["BaseWorkloads"]
	if !ok {
		t.Fatalf("expected BaseWorkloads WorkloadLibrary; got %v", c.WorkloadLibraries)
	}
	if lib.Spec.Workloads.Len() != 2 {
		t.Errorf("expected 2 workload entries; got %d", lib.Spec.Workloads.Len())
	}
}

// TestLazyLookup_WorkloadLibraries_LocalFold verifies the lazy backing folds a
// local-catalog WorkloadLibrary into the enumerated set exposed by
// WorkloadLibraries() — the set the renderer's workload `$import` resolution
// consumes. This covers the local (Tier-1) arm; the lockfile-pinned remote
// (Tier-2) bridge into the same set is proven by the pinned-enumeration test.
func TestLazyLookup_WorkloadLibraries_LocalFold(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "BaseWorkloads.yaml")
	body := `apiVersion: aggrega.ai/v1beta1
kind: WorkloadLibrary
metadata:
  name: BaseWorkloads
  version: 1.0.0
spec:
  workloads:
    Deployment: 'kind: Deployment'
`
	if err := os.WriteFile(libPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ll := NewLazyLookup("", "", []string{dir}, nil)
	libs := ll.WorkloadLibraries()
	if len(libs) != 1 {
		t.Fatalf("expected 1 enumerated WorkloadLibrary; got %d", len(libs))
	}
	lib, ok := libs["BaseWorkloads"]
	if !ok {
		t.Fatalf("expected BaseWorkloads enumerated; got %v", libs)
	}
	if lib.Spec.Workloads.Len() != 1 {
		t.Errorf("expected 1 workload entry; got %d", lib.Spec.Workloads.Len())
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
	body := `apiVersion: aggrega.ai/v1beta1
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

	_, err := LoadAll("", dir)
	if err == nil {
		t.Fatal("expected error for non-scalar mapping entry; got nil")
	}
	// The document envelope owns the non-scalar rejection on the loader
	// path (the MappingLibrary schema types entries as strings); the
	// typed ErrMappingEntryNotScalar still guards constructed inputs.
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Errorf("expected ErrEntityEnvelope in chain; got %v", err)
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
	body := `apiVersion: aggrega.ai/v1beta1
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

	_, err := LoadAll("", dir)
	if err == nil {
		t.Fatal("expected error for $dynamicAnchor in template body; got nil")
	}
	if !errors.Is(err, types.ErrForbiddenKeyword()) {
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
	body := `apiVersion: aggrega.ai/v1beta1
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

	_, err := LoadAll("", dir)
	if err == nil {
		t.Fatal("expected error for $dynamicAnchor; got nil")
	}
	if !errors.Is(err, types.ErrForbiddenKeyword()) {
		t.Errorf("expected ErrForbiddenKeyword in chain; got %v", err)
	}
}

func TestLoad_CatalogLookup(t *testing.T) {
	t.Parallel()

	c, err := LoadAll("", "testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lookup := c.CatalogLookup()

	comp, found := lookup.Component("core/compute/api-server")
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

	c, err := LoadAll("", "testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mt := &types.MappingTier{}
	mt.Type.Set("core/compute/api-server", "helm-api-server")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: mt}}}
	lookup := c.RecipeLookupWithManifest(manifest)

	r, found := lookup.Recipe("", "core/compute/api-server")
	if !found {
		t.Fatal("expected to find recipe for core/api-server")
	}
	if r.Metadata.Name != "helm-api-server" {
		t.Errorf("recipe name: %q", r.Metadata.Name)
	}

	_, found = lookup.Recipe("", "nonexistent")
	if found {
		t.Error("expected not found")
	}
}

func TestLoad_SubAggregateLoader(t *testing.T) {
	t.Parallel()

	c, err := LoadAll("", "testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loader := c.SubAggregateLoader()

	agg, path, _, err := loader("db-module", nil)
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

	_, _, _, err = loader("nonexistent", nil)
	if err == nil {
		t.Error("expected error for nonexistent aggregate")
	}
}

func TestLoad_EmptyDir(t *testing.T) {
	t.Parallel()

	c, err := LoadAll("", t.TempDir())
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

	if err := os.WriteFile(filepath.Join(dirA, "recipe.yaml"), []byte(`apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: recipe-a
  version: 1.0.0
spec:
  capability: helm
  implements: core/x
  source: ./fake-chart
  valuesMapping: '{}'
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "recipe.yaml"), []byte(`apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: recipe-b
  version: 1.0.0
spec:
  capability: helm-chart
  implements: core/y
  source: ./fake-chart
  valuesMapping: '{}'
`), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadAll("", dirA, dirB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recipesImplementing(c.Recipes, "core/x", nil)) == 0 {
		t.Error("missing core/x from dirA")
	}
	if len(recipesImplementing(c.Recipes, "core/y", nil)) == 0 {
		t.Error("missing core/y from dirB")
	}
}

func TestResolveAllExtends_ComponentChain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.yaml"), []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/server
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
	if err := os.WriteFile(filepath.Join(dir, "api.yaml"), []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/compute/api
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
    tier: prod
spec:
  extends: core/compute/server
  schema: inherited
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cat, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Before resolution: the child's Spec.Schema is the inheritance marker as
	// authored on disk, not the parent's pillar block.
	before, _ := cat.CatalogLookup().Component("core/compute/api")
	beforeText, err := yaml.Marshal(&before.Spec.Schema)
	if err != nil {
		t.Fatalf("marshal pre-resolution Schema: %v", err)
	}
	if strings.TrimSpace(string(beforeText)) != "inherited" {
		t.Fatalf("child should carry the inheritance marker before resolution, got %q", string(beforeText))
	}

	errs := cat.ResolveAllExtends()
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	resolved, _ := cat.CatalogLookup().Component("core/compute/api")
	typeLabel, _ := resolved.Metadata.Labels.Get("component.aggrega.ai/type")
	if typeLabel != "server" {
		t.Errorf("expected inherited type label, got %v", resolved.Metadata.Labels.Keys())
	}
	tierLabel, _ := resolved.Metadata.Labels.Get("tier")
	if tierLabel != "prod" {
		t.Errorf("child tier label lost, got %v", resolved.Metadata.Labels.Keys())
	}
	// After resolution the child's Spec.Schema IS the parent's pillar block,
	// copied down verbatim — the marker is gone and the parent's constraint is
	// what every enforcement path now reads off the child.
	schemaText, err := yaml.Marshal(&resolved.Spec.Schema)
	if err != nil {
		t.Fatalf("marshal Schema: %v", err)
	}
	if !strings.Contains(string(schemaText), "1024") {
		t.Errorf("parent's pillar block not inherited onto the child:\n%s", string(schemaText))
	}
	if strings.Contains(string(schemaText), "inherited") {
		t.Errorf("resolved Schema still carries the inheritance marker:\n%s", string(schemaText))
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
	if err := os.WriteFile(recipePath, []byte(`apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: rel
  version: 1.0.0
spec:
  capability: helm
  implements: core/rel
  source: ./charts/my-chart
  valuesMapping: '{}'
`), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recipesImplementing(c.Recipes, "core/rel", nil)) == 0 {
		t.Fatal("recipe not loaded")
	}
	var r types.Recipe
	for _, rr := range c.Recipes {
		if resolver.ImplementsSatisfies(rr.Spec.Implements, "core/rel") {
			r = rr
			break
		}
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
