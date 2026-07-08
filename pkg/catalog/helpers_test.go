// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestLintWarnings_NilReceiverAndPopulated pins both arms: a nil
// Catalog returns nil; a populated lintWarnings slice round-trips.
func TestLintWarnings_NilReceiverAndPopulated(t *testing.T) {
	t.Parallel()
	var nilC *Catalog
	if got := nilC.LintWarnings(); got != nil {
		t.Errorf("nil receiver should return nil; got %v", got)
	}
	c := &Catalog{lintWarnings: []string{"warn-a", "warn-b"}}
	got := c.LintWarnings()
	if len(got) != 2 || got[0] != "warn-a" {
		t.Errorf("populated should round-trip; got %v", got)
	}
}

// TestDeprecationWarnings_NoActiveRules pins the attach-point contract:
// with no deprecation rules active, the function returns nothing for an
// empty catalog and for a populated one alike.
func TestDeprecationWarnings_NoActiveRules(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		Components: map[string]types.Component{},
		Features:   map[string]types.Feature{},
	}
	if got := c.DeprecationWarnings(); len(got) != 0 {
		t.Errorf("empty catalog should produce no warnings; got %v", got)
	}

	comp := types.Component{}
	comp.Metadata.Name = "comp/modern"
	comp.Spec.SecretsSchema.Set("slot", types.SecretSlot{})
	c.Components["comp/modern"] = comp

	if got := c.DeprecationWarnings(); len(got) != 0 {
		t.Errorf("no active rules: populated catalog should produce no warnings; got %v", got)
	}
}

// TestRecipeLookup_HitAndMiss pins the renderer.RecipeLookup contract.
func TestRecipeLookup_HitAndMiss(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		Recipes:        map[string]types.Recipe{},
		FeatureRecipes: map[string]types.FeatureRecipe{},
	}
	r := types.Recipe{}
	r.Metadata.Name = "helm-api-server"
	r.Spec.Implements = "core/compute/api-server"
	c.Recipes[recipeURIKey(r.Metadata.Name, r.Metadata.Version)] = r

	fr := types.FeatureRecipe{}
	fr.Metadata.Name = "helm-monitoring"
	fr.Spec.Implements = "feature/monitoring"
	c.FeatureRecipes[recipeURIKey(fr.Metadata.Name, fr.Metadata.Version)] = fr

	mtC := &types.MappingTier{}
	mtC.Type.Set("core/compute/api-server", "helm-api-server")
	mtF := &types.MappingTier{}
	mtF.Type.Set("feature/monitoring", "helm-monitoring")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: mtC, Features: mtF}}}
	lookup := c.RecipeLookupWithManifest(manifest)
	gotR, ok := lookup.Recipe("", "core/compute/api-server")
	if !ok || gotR == nil {
		t.Errorf("recipe hit should return non-nil + true")
	}
	_, ok = lookup.Recipe("", "absent")
	if ok {
		t.Errorf("recipe miss should return false")
	}
	gotFR, ok := lookup.FeatureRecipe("", "feature/monitoring")
	if !ok || gotFR == nil {
		t.Errorf("feature-recipe hit should return non-nil + true")
	}
	_, ok = lookup.FeatureRecipe("", "absent")
	if ok {
		t.Errorf("feature-recipe miss should return false")
	}
}

// TestFeatureRecipeByURI_AllBranches pins direct hit + miss.
func TestFeatureRecipeByURI_AllBranches(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		FeatureRecipes: map[string]types.FeatureRecipe{},
	}
	fr := types.FeatureRecipe{}
	fr.Metadata.Name = "myco/rewrite-host"
	fr.Spec.Implements = "feature/rewrite"
	c.FeatureRecipes[recipeURIKey(fr.Metadata.Name, fr.Metadata.Version)] = fr

	got, ok := c.FeatureRecipeByURI("myco/rewrite-host")
	if !ok || got.Metadata.Name != "myco/rewrite-host" {
		t.Errorf("hit: ok=%v got=%v", ok, got)
	}
	_, ok = c.FeatureRecipeByURI("absent")
	if ok {
		t.Errorf("miss should return false")
	}
}

// TestFlatFeatureURI_AllBranches pins nil-manifest, nil-features
// section, hit, miss.
func TestFlatFeatureURI_AllBranches(t *testing.T) {
	t.Parallel()
	if _, ok, _ := flatFeatureURI(nil, "", "t"); ok {
		t.Errorf("nil manifest should miss")
	}
	if _, ok, _ := flatFeatureURI(&types.Manifest{}, "", "t"); ok {
		t.Errorf("no features section should miss")
	}
	m := &types.Manifest{}
	mt := &types.MappingTier{}
	mt.Type.Set("feat-type", "feat-uri")
	m.Spec.Mappings.Features = mt
	uri, ok, _ := flatFeatureURI(m, "", "feat-type")
	if !ok || uri != "feat-uri" {
		t.Errorf("hit: ok=%v uri=%q", ok, uri)
	}
	if _, ok, _ := flatFeatureURI(m, "", "absent"); ok {
		t.Errorf("missing key should miss")
	}
}

// TestLegacyFeatureURI_AllBranches pins nil-manifest, no-providers,
// provider-without-features, miss across all providers, hit.
func TestLegacyFeatureURI_AllBranches(t *testing.T) {
	t.Parallel()
	if _, ok, _ := legacyFeatureURI(nil, "", "t"); ok {
		t.Errorf("nil manifest should miss")
	}
	// No providers.
	if _, ok, _ := legacyFeatureURI(&types.Manifest{}, "", "t"); ok {
		t.Errorf("no providers should miss")
	}
	// Provider without Features section.
	m := &types.Manifest{}
	m.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{})
	if _, ok, _ := legacyFeatureURI(m, "", "t"); ok {
		t.Errorf("provider without features should miss")
	}
	// Provider with Features but no matching key.
	mt := &types.MappingTier{}
	mt.Type.Set("other-feat", "other-uri")
	m2 := &types.Manifest{}
	m2.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{Features: mt})
	if _, ok, _ := legacyFeatureURI(m2, "", "absent"); ok {
		t.Errorf("absent key should miss")
	}
	// Hit.
	mt2 := &types.MappingTier{}
	mt2.Type.Set("feat-type", "feat-uri")
	m3 := &types.Manifest{}
	m3.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{Features: mt2})
	uri, ok, _ := legacyFeatureURI(m3, "", "feat-type")
	if !ok || uri != "feat-uri" {
		t.Errorf("hit: ok=%v uri=%q", ok, uri)
	}
}

// TestLegacyComponentURI_NilAndNoComponents pins the nil-manifest and
// provider-without-components branches that the existing tests don't
// exercise.
func TestLegacyComponentURI_NilAndNoComponents(t *testing.T) {
	t.Parallel()
	if _, ok, _ := legacyComponentURI(nil, "", "t"); ok {
		t.Errorf("nil manifest should miss")
	}
	// Provider without Components section.
	m := &types.Manifest{}
	m.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{})
	if _, ok, _ := legacyComponentURI(m, "", "t"); ok {
		t.Errorf("provider without components should miss")
	}
}

// TestFeatureTypeToFeatureRecipeWithManifest_AllLayers pins the mapping-only
// cascade: flat mapping, legacy mapping, and miss. An unmapped feature type is
// a miss here — resolution never walks spec.implements.
func TestFeatureTypeToFeatureRecipeWithManifest_AllLayers(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		FeatureRecipes: map[string]types.FeatureRecipe{},
	}
	fr1 := types.FeatureRecipe{}
	fr1.Metadata.Name = "myco/rewrite-host"
	fr1.Spec.Implements = "feature/rewrite"
	c.FeatureRecipes[recipeURIKey(fr1.Metadata.Name, fr1.Metadata.Version)] = fr1

	fr2 := types.FeatureRecipe{}
	fr2.Metadata.Name = "myco/legacy-helper"
	fr2.Spec.Implements = "feature/legacy"
	c.FeatureRecipes[recipeURIKey(fr2.Metadata.Name, fr2.Metadata.Version)] = fr2

	// Layer 1: flat-shape mapping.
	manifest := &types.Manifest{}
	mt := &types.MappingTier{}
	mt.Type.Set("feature/rewrite", "myco/rewrite-host")
	manifest.Spec.Mappings.Features = mt
	lookup := c.featureTypeToFeatureRecipeWithManifest(manifest)
	got, ok := lookup("", "feature/rewrite")
	if !ok || got == nil || got.Metadata.Name != "myco/rewrite-host" {
		t.Errorf("layer 1 flat hit: ok=%v got=%v", ok, got)
	}

	// Layer 2: legacy mapping.
	manifest2 := &types.Manifest{}
	legacyMT := &types.MappingTier{}
	legacyMT.Type.Set("feature/legacy", "myco/legacy-helper")
	manifest2.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{Features: legacyMT})
	lookup2 := c.featureTypeToFeatureRecipeWithManifest(manifest2)
	got, ok = lookup2("", "feature/legacy")
	if !ok || got == nil || got.Metadata.Name != "myco/legacy-helper" {
		t.Errorf("layer 2 legacy hit: ok=%v got=%v", ok, got)
	}

	// An unmapped type is a miss: resolution is mapping-only and never
	// walks spec.implements, even when a recipe implements the type.
	_, ok = lookup2("", "feature/rewrite")
	if ok {
		t.Errorf("unmapped type must miss under mapping-only resolution")
	}

	// Miss.
	_, ok = lookup2("", "absent/type")
	if ok {
		t.Errorf("absent type should miss")
	}
}

// TestVerifyManifestImplementsConsistency_EveryArm pins each documented
// arm: a missing recipe is SKIPPED (the resolution path owns missing recipes
// with richer context), a mismatched recipe is reported, a matching recipe
// passes — same three arms for FeatureRecipes. So a manifest carrying one
// missing + one mismatched + one matching mapping per kind yields exactly two
// violations (the two mismatches).
func TestVerifyManifestImplementsConsistency_EveryArm(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		Recipes:        map[string]types.Recipe{},
		FeatureRecipes: map[string]types.FeatureRecipe{},
	}
	// Recipe that doesn't match the manifest's component-type key.
	rMismatch := types.Recipe{}
	rMismatch.Metadata.Name = "myco/mismatch"
	rMismatch.Spec.Implements = "core/different"
	c.Recipes[recipeURIKey(rMismatch.Metadata.Name, rMismatch.Metadata.Version)] = rMismatch

	// Recipe that matches.
	rMatch := types.Recipe{}
	rMatch.Metadata.Name = "myco/matching"
	rMatch.Spec.Implements = "core/match"
	c.Recipes[recipeURIKey(rMatch.Metadata.Name, rMatch.Metadata.Version)] = rMatch

	// FeatureRecipe with the same shapes.
	frMismatch := types.FeatureRecipe{}
	frMismatch.Metadata.Name = "myco/fr-mismatch"
	frMismatch.Spec.Implements = "feature/different"
	c.FeatureRecipes[recipeURIKey(frMismatch.Metadata.Name, frMismatch.Metadata.Version)] = frMismatch
	frMatch := types.FeatureRecipe{}
	frMatch.Metadata.Name = "myco/fr-match"
	frMatch.Spec.Implements = "feature/match"
	c.FeatureRecipes[recipeURIKey(frMatch.Metadata.Name, frMatch.Metadata.Version)] = frMatch

	manifest := &types.Manifest{}
	mt := &types.MappingTier{}
	mt.Type.Set("core/missing", "myco/never")     // missing recipe
	mt.Type.Set("core/mismatch", "myco/mismatch") // mismatched recipe
	mt.Type.Set("core/match", "myco/matching")    // matching recipe
	manifest.Spec.Mappings.Components = mt

	mtF := &types.MappingTier{}
	mtF.Type.Set("feature/missing", "myco/never")
	mtF.Type.Set("feature/mismatch", "myco/fr-mismatch")
	mtF.Type.Set("feature/match", "myco/fr-match")
	manifest.Spec.Mappings.Features = mtF

	errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml")
	if len(errs) != 2 {
		t.Errorf("expected 2 violations (2 mismatched; the 2 missing are skipped for the resolution path); got %d: %v", len(errs), errs)
	}
}

// TestResolveAllExtends_NoExtends pins the no-op path: a catalog with
// no extends entries returns empty errors and no mutation.
func TestResolveAllExtends_NoExtends(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		Components: map[string]types.Component{},
		Features:   map[string]types.Feature{},
	}
	plainComp := types.Component{}
	plainComp.Metadata.Name = "comp/plain"
	c.Components["comp/plain"] = plainComp

	plainFeat := types.Feature{}
	plainFeat.Metadata.Name = "feat/plain"
	c.Features["feat/plain"] = plainFeat

	errs := c.ResolveAllExtends()
	if len(errs) != 0 {
		t.Errorf("no extends should produce no errors; got %v", errs)
	}
}

// TestResolveAllExtends_FeatureChain pins the Feature extends path
// — Component chain is already covered by TestResolveAllExtends_ComponentChain.
func TestResolveAllExtends_FeatureChain(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		Components: map[string]types.Component{},
		Features:   map[string]types.Feature{},
	}
	parent := types.Feature{}
	parent.APIVersion = "aggrega.ai/v1alpha1"
	parent.Kind = "Feature"
	parent.Metadata.Name = "feat/parent"
	c.Features["feat/parent"] = parent

	child := types.Feature{}
	child.APIVersion = "aggrega.ai/v1alpha1"
	child.Kind = "Feature"
	child.Metadata.Name = "feat/child"
	child.Spec.Extends = "feat/parent"
	c.Features["feat/child"] = child

	errs := c.ResolveAllExtends()
	if len(errs) != 0 {
		t.Errorf("feature extends should resolve; got errors: %v", errs)
	}
}

// TestIndexFile_NonEntityYAML pins the silent-skip arm: a YAML file
// that's syntactically valid but not a catalog entity (no `kind:` field)
// is skipped without error.
func TestIndexFile_NonEntityYAML(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		Components:       map[string]types.Component{},
		Features:         map[string]types.Feature{},
		Recipes:          map[string]types.Recipe{},
		FeatureRecipes:   map[string]types.FeatureRecipe{},
		Aggregates:       map[string]types.Aggregate{},
		SchemaLibraries:  map[string]types.SchemaLibrary{},
		MappingLibraries: map[string]types.MappingLibrary{},
		componentSources: map[string]string{},
		featureSources:   map[string]string{},
		aggregateSources: map[string]string{},
	}
	// Non-entity YAML.
	if err := c.indexFile([]byte("not: an entity\n"), "p"); err != nil {
		t.Errorf("non-entity YAML should be skipped silently; got %v", err)
	}
	if len(c.Components) != 0 {
		t.Errorf("nothing should be indexed; got %v", c.Components)
	}
	// Malformed YAML — also skipped (yaml.Unmarshal of header returns error).
	if err := c.indexFile([]byte("::: not yaml :::"), "p"); err != nil {
		t.Errorf("malformed YAML should be skipped silently; got %v", err)
	}
}

// TestIndexFile_UnknownKind pins the silent-skip on unknown Kind:
// Manifest / Release are handled separately, but other unknown kinds
// also pass through.
func TestIndexFile_UnknownKind(t *testing.T) {
	t.Parallel()
	c := makeEmptyCatalog()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: Unknown
metadata:
  name: thing
`
	if err := c.indexFile([]byte(src), "p"); err != nil {
		t.Errorf("unknown kind should skip; got %v", err)
	}
}

func makeEmptyCatalog() *Catalog {
	return &Catalog{
		Components:       map[string]types.Component{},
		Features:         map[string]types.Feature{},
		Recipes:          map[string]types.Recipe{},
		FeatureRecipes:   map[string]types.FeatureRecipe{},
		Aggregates:       map[string]types.Aggregate{},
		SchemaLibraries:  map[string]types.SchemaLibrary{},
		MappingLibraries: map[string]types.MappingLibrary{},
		componentSources: map[string]string{},
		featureSources:   map[string]string{},
		aggregateSources: map[string]string{},
		entityBytes:      map[string][]byte{},
	}
}

// TestIndexFile_ComponentUnmarshalError pins the structured-error arm:
// a Component-kind YAML with malformed body returns a wrapped error.
func TestIndexFile_ComponentUnmarshalError(t *testing.T) {
	t.Parallel()
	c := makeEmptyCatalog()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: Component
spec: [not, a, mapping]
`
	if err := c.indexFile([]byte(src), "p"); err == nil {
		t.Errorf("malformed Component body should error")
	}
}

// TestLoad_NoSuchDirectory pins the directory-walk error path.
func TestLoad_NoSuchDirectory(t *testing.T) {
	t.Parallel()
	_, err := LoadAll("", "/nonexistent/path/nope")
	if err == nil {
		t.Errorf("missing dir should error")
	}
}

// TestLoadAll_NoDirectories pins the empty-input branch.
func TestLoadAll_NoDirectories(t *testing.T) {
	t.Parallel()
	c, err := LoadAll("")
	if err != nil {
		t.Errorf("no dirs: %v", err)
	}
	if c == nil {
		t.Errorf("LoadAll with no dirs should return empty catalog, not nil")
	}
}

// TestLoadAll_OneInvalidDir pins error propagation across multiple dirs.
func TestLoadAll_OneInvalidDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	_, err := LoadAll("", tmp, "/nonexistent/path")
	if err == nil {
		t.Errorf("invalid second dir should error")
	}
}

// TestLoad_RelativeFeatureRecipeSource pins the FR source-rewrite path
// (mirrors Recipe source-rewrite, less commonly tested).
func TestLoad_RelativeFeatureRecipeSource(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	// Subdir for the source target.
	srcDir := filepath.Join(tmp, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "Chart.yaml"), []byte("apiVersion: v2\nname: x\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write chart: %v", err)
	}
	frYAML := `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: myco/fr
  version: 1.0.0
spec:
  implements: feature/example
  target: helm-chart
  source: ./src
`
	if err := os.WriteFile(filepath.Join(tmp, "fr.yaml"), []byte(frYAML), 0o644); err != nil {
		t.Fatalf("write fr: %v", err)
	}
	c, err := LoadAll("", tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(featureRecipesImplementing(c.FeatureRecipes, "feature/example")) == 0 {
		t.Fatalf("FR not loaded")
	}
	var fr types.FeatureRecipe
	for _, ffr := range c.FeatureRecipes {
		if implementsSatisfies(ffr.Spec.Implements, "feature/example") {
			fr = ffr
			break
		}
	}
	src, _ := fr.Spec.Source.(string)
	if !filepath.IsAbs(src) {
		t.Errorf("relative source should be made absolute; got %q", src)
	}
}
