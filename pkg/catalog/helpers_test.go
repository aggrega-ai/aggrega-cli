// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"strings"
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

// TestDeprecationWarnings_ComponentsAndFeatures pins both deprecation
// paths: Components with deprecated secretNames + no secretsSchema;
// Features with the same shape. Empty maps produce no warnings.
func TestDeprecationWarnings_ComponentsAndFeatures(t *testing.T) {
	t.Parallel()
	// Empty catalog.
	c := &Catalog{
		Components: map[string]types.Component{},
		Features:   map[string]types.Feature{},
	}
	if got := c.DeprecationWarnings(); len(got) != 0 {
		t.Errorf("empty catalog should produce no warnings; got %v", got)
	}

	// Component with deprecated secretNames.
	depComp := types.Component{}
	depComp.Metadata.Name = "comp/deprecated"
	depComp.Spec.SecretNames = []string{"old-secret"}
	c.Components["comp/deprecated"] = depComp

	// Feature with deprecated secretNames.
	depFeat := types.Feature{}
	depFeat.Metadata.Name = "feat/deprecated"
	depFeat.Spec.SecretNames = []string{"old-secret"}
	c.Features["feat/deprecated"] = depFeat

	// Modern: secretsSchema populated → no deprecation warning.
	modernComp := types.Component{}
	modernComp.Metadata.Name = "comp/modern"
	modernComp.Spec.SecretNames = []string{"old-name"}
	modernComp.Spec.SecretsSchema.Set("slot", types.SecretSlot{})
	c.Components["comp/modern"] = modernComp

	got := c.DeprecationWarnings()
	if len(got) != 2 {
		t.Fatalf("expected 2 warnings; got %d (%v)", len(got), got)
	}
	saw := strings.Join(got, "\n")
	if !strings.Contains(saw, "comp/deprecated") {
		t.Errorf("component warning missing")
	}
	if !strings.Contains(saw, "feat/deprecated") {
		t.Errorf("feature warning missing")
	}
	if strings.Contains(saw, "comp/modern") {
		t.Errorf("modern component should not warn")
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
	r.Spec.Implements = "core/server/api-server"
	c.Recipes["core/server/api-server"] = r

	fr := types.FeatureRecipe{}
	fr.Spec.Implements = "feature/monitoring"
	c.FeatureRecipes["feature/monitoring"] = fr

	lookup := c.RecipeLookup()
	gotR, ok := lookup.Recipe("core/server/api-server")
	if !ok || gotR == nil {
		t.Errorf("recipe hit should return non-nil + true")
	}
	_, ok = lookup.Recipe("absent")
	if ok {
		t.Errorf("recipe miss should return false")
	}
	gotFR, ok := lookup.FeatureRecipe("feature/monitoring")
	if !ok || gotFR == nil {
		t.Errorf("feature-recipe hit should return non-nil + true")
	}
	_, ok = lookup.FeatureRecipe("absent")
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
	c.FeatureRecipes["feature/rewrite"] = fr

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
	if _, ok := flatFeatureURI(nil, "t"); ok {
		t.Errorf("nil manifest should miss")
	}
	if _, ok := flatFeatureURI(&types.Manifest{}, "t"); ok {
		t.Errorf("no features section should miss")
	}
	m := &types.Manifest{}
	mt := &types.MappingTier{}
	mt.Type.Set("feat-type", "feat-uri")
	m.Spec.Mappings.Features = mt
	uri, ok := flatFeatureURI(m, "feat-type")
	if !ok || uri != "feat-uri" {
		t.Errorf("hit: ok=%v uri=%q", ok, uri)
	}
	if _, ok := flatFeatureURI(m, "absent"); ok {
		t.Errorf("missing key should miss")
	}
}

// TestLegacyFeatureURI_AllBranches pins nil-manifest, no-providers,
// provider-without-features, miss across all providers, hit.
func TestLegacyFeatureURI_AllBranches(t *testing.T) {
	t.Parallel()
	if _, ok := legacyFeatureURI(nil, "t"); ok {
		t.Errorf("nil manifest should miss")
	}
	// No providers.
	if _, ok := legacyFeatureURI(&types.Manifest{}, "t"); ok {
		t.Errorf("no providers should miss")
	}
	// Provider without Features section.
	m := &types.Manifest{}
	m.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{})
	if _, ok := legacyFeatureURI(m, "t"); ok {
		t.Errorf("provider without features should miss")
	}
	// Provider with Features but no matching key.
	mt := &types.MappingTier{}
	mt.Type.Set("other-feat", "other-uri")
	m2 := &types.Manifest{}
	m2.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{Features: mt})
	if _, ok := legacyFeatureURI(m2, "absent"); ok {
		t.Errorf("absent key should miss")
	}
	// Hit.
	mt2 := &types.MappingTier{}
	mt2.Type.Set("feat-type", "feat-uri")
	m3 := &types.Manifest{}
	m3.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{Features: mt2})
	uri, ok := legacyFeatureURI(m3, "feat-type")
	if !ok || uri != "feat-uri" {
		t.Errorf("hit: ok=%v uri=%q", ok, uri)
	}
}

// TestLegacyComponentURI_NilAndNoComponents pins the nil-manifest and
// provider-without-components branches that the existing tests don't
// exercise.
func TestLegacyComponentURI_NilAndNoComponents(t *testing.T) {
	t.Parallel()
	if _, ok := legacyComponentURI(nil, "t"); ok {
		t.Errorf("nil manifest should miss")
	}
	// Provider without Components section.
	m := &types.Manifest{}
	m.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{})
	if _, ok := legacyComponentURI(m, "t"); ok {
		t.Errorf("provider without components should miss")
	}
}

// TestFeatureTypeToFeatureRecipeWithManifest_AllLayers pins the three-
// layer cascade: flat mapping, legacy mapping, matchImplements fallback,
// and miss.
func TestFeatureTypeToFeatureRecipeWithManifest_AllLayers(t *testing.T) {
	t.Parallel()
	c := &Catalog{
		FeatureRecipes: map[string]types.FeatureRecipe{},
	}
	fr1 := types.FeatureRecipe{}
	fr1.Metadata.Name = "myco/rewrite-host"
	fr1.Spec.Implements = "feature/rewrite"
	c.FeatureRecipes["feature/rewrite"] = fr1

	fr2 := types.FeatureRecipe{}
	fr2.Metadata.Name = "myco/legacy-helper"
	fr2.Spec.Implements = "feature/legacy"
	c.FeatureRecipes["feature/legacy"] = fr2

	// Layer 1: flat-shape mapping.
	manifest := &types.Manifest{}
	mt := &types.MappingTier{}
	mt.Type.Set("feature/rewrite", "myco/rewrite-host")
	manifest.Spec.Mappings.Features = mt
	lookup := c.featureTypeToFeatureRecipeWithManifest(manifest)
	got, ok := lookup("feature/rewrite")
	if !ok || got == nil || got.Metadata.Name != "myco/rewrite-host" {
		t.Errorf("layer 1 flat hit: ok=%v got=%v", ok, got)
	}

	// Layer 2: legacy mapping.
	manifest2 := &types.Manifest{}
	legacyMT := &types.MappingTier{}
	legacyMT.Type.Set("feature/legacy", "myco/legacy-helper")
	manifest2.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{Features: legacyMT})
	lookup2 := c.featureTypeToFeatureRecipeWithManifest(manifest2)
	got, ok = lookup2("feature/legacy")
	if !ok || got == nil || got.Metadata.Name != "myco/legacy-helper" {
		t.Errorf("layer 2 legacy hit: ok=%v got=%v", ok, got)
	}

	// Layer 3: matchImplements fallback (no manifest mapping for this type).
	got, ok = lookup2("feature/rewrite") // type maps via spec.implements walking
	if !ok || got == nil {
		t.Errorf("layer 3 fallback: ok=%v got=%v", ok, got)
	}

	// Miss.
	_, ok = lookup2("absent/type")
	if ok {
		t.Errorf("absent type should miss")
	}
}

// TestVerifyManifestImplementsConsistency_EveryArm pins each documented
// arm: missing recipe, mismatched recipe, matching recipe, missing FR,
// mismatched FR, matching FR.
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
	c.Recipes["core/different"] = rMismatch

	// Recipe that matches.
	rMatch := types.Recipe{}
	rMatch.Metadata.Name = "myco/matching"
	rMatch.Spec.Implements = "core/match"
	c.Recipes["core/match"] = rMatch

	// FeatureRecipe with the same shapes.
	frMismatch := types.FeatureRecipe{}
	frMismatch.Metadata.Name = "myco/fr-mismatch"
	frMismatch.Spec.Implements = "feature/different"
	c.FeatureRecipes["feature/different"] = frMismatch
	frMatch := types.FeatureRecipe{}
	frMatch.Metadata.Name = "myco/fr-match"
	frMatch.Spec.Implements = "feature/match"
	c.FeatureRecipes["feature/match"] = frMatch

	manifest := &types.Manifest{}
	mt := &types.MappingTier{}
	mt.Type.Set("core/missing", "myco/never")          // missing recipe
	mt.Type.Set("core/mismatch", "myco/mismatch")       // mismatched recipe
	mt.Type.Set("core/match", "myco/matching")          // matching recipe
	manifest.Spec.Mappings.Components = mt

	mtF := &types.MappingTier{}
	mtF.Type.Set("feature/missing", "myco/never")
	mtF.Type.Set("feature/mismatch", "myco/fr-mismatch")
	mtF.Type.Set("feature/match", "myco/fr-match")
	manifest.Spec.Mappings.Features = mtF

	errs := c.VerifyManifestImplementsConsistency(manifest, "manifest.yaml")
	if len(errs) != 4 {
		t.Errorf("expected 4 violations (2 missing + 2 mismatched); got %d: %v", len(errs), errs)
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
		Providers:        map[string]types.Provider{},
		SchemaLibraries:  map[string]types.SchemaLibrary{},
		MappingLibraries: map[string]types.MappingLibrary{},
		componentSources: map[string]string{},
		featureSources:   map[string]string{},
		providerSources:  map[string]string{},
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
		Providers:        map[string]types.Provider{},
		SchemaLibraries:  map[string]types.SchemaLibrary{},
		MappingLibraries: map[string]types.MappingLibrary{},
		componentSources: map[string]string{},
		featureSources:   map[string]string{},
		providerSources:  map[string]string{},
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
	_, err := Load("/nonexistent/path/nope")
	if err == nil {
		t.Errorf("missing dir should error")
	}
}

// TestLoadAll_NoDirectories pins the empty-input branch.
func TestLoadAll_NoDirectories(t *testing.T) {
	t.Parallel()
	c, err := LoadAll()
	if err != nil {
		t.Errorf("no dirs: %v", err)
	}
	if c == nil {
		t.Errorf("LoadAll() with no args should return empty catalog, not nil")
	}
}

// TestLoadAll_OneInvalidDir pins error propagation across multiple dirs.
func TestLoadAll_OneInvalidDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	_, err := LoadAll(tmp, "/nonexistent/path")
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
  provider: helm-chart
  source: ./src
`
	if err := os.WriteFile(filepath.Join(tmp, "fr.yaml"), []byte(frYAML), 0o644); err != nil {
		t.Fatalf("write fr: %v", err)
	}
	c, err := Load(tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	fr, ok := c.FeatureRecipes["feature/example"]
	if !ok {
		t.Fatalf("FR not loaded")
	}
	src, _ := fr.Spec.Source.(string)
	if !filepath.IsAbs(src) {
		t.Errorf("relative source should be made absolute; got %q", src)
	}
}

// TestProviderLookup_MissAndHit pins the contract.
func TestProviderLookup_MissAndHit(t *testing.T) {
	t.Parallel()
	c := &Catalog{Providers: map[string]types.Provider{}}
	if _, ok := c.ProviderLookup("absent"); ok {
		t.Errorf("absent → false")
	}
	c.Providers["helm"] = types.Provider{}
	if _, ok := c.ProviderLookup("helm"); !ok {
		t.Errorf("present → true")
	}
}
