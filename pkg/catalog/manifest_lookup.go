// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ErrManifestImplementsMismatch fires when a Manifest mapping entry's
// resolved Recipe (or FeatureRecipe) carries a spec.implements that
// doesn't match the Manifest's component-type / feature-type key for
// that entry. The mismatch is a structural consistency invariant per
// the schema-language migration's mapping authoring surface: the
// Manifest entry IS the Recipe URI; the Recipe's spec.implements is
// a consistency anchor, not a dispatch mechanism. Mismatches halt at
// workspace load.
var ErrManifestImplementsMismatch = errors.New("manifest mapping entry's recipe spec.implements does not match the mapped component-type / feature-type key")

// ErrAmbiguousTypeMapping fires when a component/feature type resolves to more
// than one version-distinct Manifest mapping key under version-tolerant
// matching — the lookup cannot pick one without guessing, so it halts loudly
// rather than silently resolving the first.
var ErrAmbiguousTypeMapping = errors.New("manifest maps multiple version-distinct keys to the same base type; the type-to-recipe lookup is ambiguous")

// ErrDuplicateRecipeIdentity fires when two catalog recipes (or feature
// recipes) share the same identity — metadata.name plus version. The catalog is
// keyed by identity so multiple recipes implementing one component type coexist;
// a genuine duplicate identity is an authoring collision surfaced loudly at load
// rather than silently shadowed.
var ErrDuplicateRecipeIdentity = errors.New("duplicate recipe identity (metadata.name + version) in the catalog")

// ErrAmbiguousImplements fires when a component/feature type carries no Manifest
// mapping yet more than one catalog recipe implements it — the compiler cannot
// pick one without guessing, so it halts and names the colliding recipes. This
// is the implements-search analogue of ErrAmbiguousTypeMapping, possible only
// because the catalog (keyed by identity) holds multiple recipes per type.
var ErrAmbiguousImplements = errors.New("multiple recipes implement this type and the Manifest does not map it; pin the instance or type in the Manifest to disambiguate")

// ErrNoRecipeResolved fires when a component/feature instance resolves to no
// recipe at all — no Manifest mapping names it and no catalog recipe implements
// its type. The build halts rather than silently emitting a component with no
// rendered output.
var ErrNoRecipeResolved = errors.New("no recipe resolved for component/feature instance; map its type in the Manifest or add a recipe implementing it")

// recipeURIKey is the catalog's identity key for a recipe or feature recipe:
// metadata.name joined with version as "name:version" (the same URI form
// Manifest mapping values and the closure walker use). A versionless entity
// keys on its bare name.
func recipeURIKey(name, version string) string {
	if version == "" {
		return name
	}
	return name + ":" + version
}

// versionTolerantTypeGet resolves a component / feature TYPE to its mapped recipe
// URI in typeMap, tolerant of a version mismatch between the authored type and
// the mapping key. It tries an exact key match first (so an exact-version key
// wins), then falls back to a BASE match: the authored type and a mapping key
// resolve together when their stripVersion bases are equal.
//
// The tolerance is base-equality, NOT semver-range satisfaction — the
// type-to-recipe mapping only needs to find the recipe; the concrete version is
// pinned downstream by the closure's minimal-version selection. A caret key
// (`core/x:^1.0.0`) base-matches like any other; a single base match resolves; a
// base shared by multiple version-distinct keys is ambiguous and returns
// ErrAmbiguousTypeMapping (a Loud Guardrail), never a silent first-wins.
func versionTolerantTypeGet(typeMap types.OrderedMap[string], lookupType string) (string, bool, error) {
	if uri, ok := typeMap.Get(lookupType); ok {
		return uri, true, nil
	}
	base := stripVersion(lookupType)
	var matchURI string
	var matchedKeys []string
	for key, uri := range typeMap.All() {
		if stripVersion(key) == base {
			matchURI = uri
			matchedKeys = append(matchedKeys, key)
		}
	}
	switch len(matchedKeys) {
	case 0:
		return "", false, nil
	case 1:
		return matchURI, true, nil
	default:
		return "", false, fmt.Errorf("%w: type %q matches version-distinct keys %v — pin the authored type's version or remove the duplicate mapping keys", ErrAmbiguousTypeMapping, lookupType, matchedKeys)
	}
}

// nameThenType resolves a mapping tier with name-wins-over-type precedence:
// an exact instance-name hit short-circuits all further lookup; otherwise the
// version-tolerant type match applies. This is the single expression of the
// Manifest two-tier precedence per entity kind — flat-shape and legacy callers
// both route through it, so the cascade entry points stay dumb forwarders and
// the precedence cannot drift between resolution paths. An empty instanceName
// (no per-instance name in scope at a call site) cleanly skips the name tier.
func nameThenType(tier *types.MappingTier, instanceName, lookupType string) (string, bool, error) {
	if tier == nil {
		return "", false, nil
	}
	if instanceName != "" {
		if uri, ok := tier.Name.Get(instanceName); ok {
			return uri, true, nil
		}
	}
	return versionTolerantTypeGet(tier.Type, lookupType)
}

// RecipeLookupWithManifest returns a renderer.RecipeLookup whose Recipe
// and FeatureRecipe closures dispatch through a three-layer cascade
// when a Manifest is supplied:
//
//  1. Flat-shape lookup (manifest.Spec.Mappings.Components.Type and
//     .Features.Type) — when populated, resolve the mapping entry's
//     Recipe URI directly against the catalog by Recipe URI (not by
//     spec.implements).
//  2. Legacy v2 per-provider lookup (manifest.Spec.Mappings.Legacy) —
//     unchanged from pre-migration behavior; included as a transitional
//     fallback while fixtures still author v2 shape.
//  3. matchImplements walking spec.implements — final fallback for
//     fixtures that don't author Manifest mappings at all.
//
// Each layer is active for the fixtures that opt in. When manifest is
// nil, the cascade collapses to layer 3 only — semantically identical
// to RecipeLookup().
func (c *Catalog) RecipeLookupWithManifest(manifest *types.Manifest) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe:        c.componentTypeToRecipeWithManifest(manifest),
		FeatureRecipe: c.featureTypeToFeatureRecipeWithManifest(manifest),
	}
}

func (c *Catalog) componentTypeToRecipeWithManifest(manifest *types.Manifest) func(string, string) (*types.Recipe, bool) {
	return func(instanceName, componentType string) (*types.Recipe, bool) {
		// Layer 1: flat-shape mapping (name-wins-over-type via flatComponentURI).
		// A version-ambiguous mapping error is swallowed to a miss here — render
		// is resilient via the Layer-3 implements fallback below; the loud
		// ErrAmbiguousTypeMapping is the pull/source-descent contract
		// (ComponentRecipeURI), where a wrong pick mis-pins a source.
		if uri, ok, err := flatComponentURI(manifest, instanceName, componentType); err == nil && ok {
			if r, found := c.RecipeByURI(uri); found {
				return &r, true
			}
			// Manifest mapped this type to a URI that doesn't resolve;
			// fall through to subsequent layers so legacy fixtures don't
			// break, but in production this would surface as a CATALOG_ERROR
			// at the consistency-check step.
		}
		// Layer 2: legacy v2 per-provider mapping.
		if uri, ok, err := legacyComponentURI(manifest, instanceName, componentType); err == nil && ok {
			if r, found := c.RecipeByURI(uri); found {
				return &r, true
			}
		}
		// Layer 3: implements-search (the name tier is a Manifest-mapping
		// concern, not an implements axis). A type with no mapping and >1
		// implementing recipe misses here and halts loudly in the pre-render
		// resolution-completeness pass — render never silently picks one.
		r, _, ok := matchRecipeByImplements(c.Recipes, componentType)
		if !ok {
			return nil, false
		}
		return &r, true
	}
}

func (c *Catalog) featureTypeToFeatureRecipeWithManifest(manifest *types.Manifest) func(string, string) (*types.FeatureRecipe, bool) {
	return func(instanceName, featureType string) (*types.FeatureRecipe, bool) {
		if uri, ok, err := flatFeatureURI(manifest, instanceName, featureType); err == nil && ok {
			if fr, found := c.FeatureRecipeByURI(uri); found {
				return &fr, true
			}
		}
		if uri, ok, err := legacyFeatureURI(manifest, instanceName, featureType); err == nil && ok {
			if fr, found := c.FeatureRecipeByURI(uri); found {
				return &fr, true
			}
		}
		fr, _, ok := matchFeatureRecipeByImplements(c.FeatureRecipes, featureType)
		if !ok {
			return nil, false
		}
		return &fr, true
	}
}

// RecipeByURI resolves a Manifest mapping value (a Recipe URI) to its catalog
// Recipe. The catalog is keyed by identity (metadata.name:version), so an exact
// `name:version` URI is a direct map get; an unversioned URI (or a versioned URI
// whose exact version is absent) resolves by recipe NAME when exactly one
// version carries that name. Two versions under one name make an unversioned URI
// ambiguous — that misses here and is surfaced as a loud resolution halt
// upstream rather than silently picking a version.
func (c *Catalog) RecipeByURI(uri string) (types.Recipe, bool) {
	if r, ok := c.Recipes[uri]; ok {
		return r, true
	}
	name := stripVersion(uri)
	var match types.Recipe
	n := 0
	for _, r := range c.Recipes {
		if r.Metadata.Name == name {
			match = r
			n++
		}
	}
	if n == 1 {
		return match, true
	}
	return types.Recipe{}, false
}

// FeatureRecipeByURI is the FeatureRecipe analogue of RecipeByURI.
func (c *Catalog) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	if fr, ok := c.FeatureRecipes[uri]; ok {
		return fr, true
	}
	name := stripVersion(uri)
	var match types.FeatureRecipe
	n := 0
	for _, fr := range c.FeatureRecipes {
		if fr.Metadata.Name == name {
			match = fr
			n++
		}
	}
	if n == 1 {
		return match, true
	}
	return types.FeatureRecipe{}, false
}

// implementsSatisfies reports whether a recipe declaring spec.implements==impl
// satisfies a request for componentType, under the three-form matching rule
// (exact, caret-range, unversioned base) — the same precedence matchImplements
// applies, expressed per-entry so the implements SEARCH can collect every
// matching recipe (the catalog now holds more than one recipe per type).
func implementsSatisfies(impl, componentType string) bool {
	if impl == componentType {
		return true
	}
	cBase, cVer, cHasVer := splitVersionedURI(componentType)
	if !cHasVer {
		return false
	}
	iBase, iVer, iHasVer := splitVersionedURI(impl)
	if iHasVer && iBase == cBase && strings.HasPrefix(iVer, "^") {
		if cv, err := semver.NewVersion(cVer); err == nil {
			if con, cerr := semver.NewConstraint(iVer); cerr == nil && con.Check(cv) {
				return true
			}
		}
		return false
	}
	return impl == cBase
}

// matchRecipeByImplements searches the catalog for recipes whose spec.implements
// satisfies componentType. It returns the single match when exactly one recipe
// implements the type; the sorted identity list of all matches is returned for
// the caller's diagnostics. Zero or more than one return ok=false — the
// resolution-completeness pass turns those into a loud halt (no recipe / the
// ambiguity the Manifest must disambiguate), so render never silently picks.
func matchRecipeByImplements(recipes map[string]types.Recipe, componentType string) (types.Recipe, []string, bool) {
	var match types.Recipe
	var ids []string
	for _, r := range recipes {
		if implementsSatisfies(r.Spec.Implements, componentType) {
			match = r
			ids = append(ids, recipeURIKey(r.Metadata.Name, r.Metadata.Version))
		}
	}
	sort.Strings(ids)
	if len(ids) == 1 {
		return match, ids, true
	}
	return types.Recipe{}, ids, false
}

// matchFeatureRecipeByImplements is the FeatureRecipe analogue of
// matchRecipeByImplements.
func matchFeatureRecipeByImplements(frs map[string]types.FeatureRecipe, featureType string) (types.FeatureRecipe, []string, bool) {
	var match types.FeatureRecipe
	var ids []string
	for _, fr := range frs {
		if implementsSatisfies(fr.Spec.Implements, featureType) {
			match = fr
			ids = append(ids, recipeURIKey(fr.Metadata.Name, fr.Metadata.Version))
		}
	}
	sort.Strings(ids)
	if len(ids) == 1 {
		return match, ids, true
	}
	return types.FeatureRecipe{}, ids, false
}

// resolveComponentRecipeForVerify runs the renderer's component cascade
// (Manifest name → type → legacy mapping, then the implements search) for one
// instance and returns a loud error when it does not resolve to exactly one
// recipe. nil means the instance resolves cleanly.
func (c *Catalog) resolveComponentRecipeForVerify(manifest *types.Manifest, instanceName, componentType string) error {
	uri, ok, err := ComponentRecipeURI(manifest, instanceName, componentType)
	if err != nil {
		return err // ambiguous version-distinct type mapping (already a loud sentinel)
	}
	if ok {
		if _, found := c.RecipeByURI(uri); !found {
			return fmt.Errorf("%w: component %q (type %q): the Manifest maps it to recipe %q, which is not in the catalog", ErrNoRecipeResolved, instanceName, componentType, uri)
		}
		return nil
	}
	_, ids, single := matchRecipeByImplements(c.Recipes, componentType)
	if single {
		return nil
	}
	if len(ids) == 0 {
		return fmt.Errorf("%w: component %q (type %q): no Manifest mapping and no recipe implements it", ErrNoRecipeResolved, instanceName, componentType)
	}
	return fmt.Errorf("%w: component %q (type %q): recipes %v all implement it", ErrAmbiguousImplements, instanceName, componentType, ids)
}

// resolveFeatureRecipeForVerify is the FeatureRecipe analogue of
// resolveComponentRecipeForVerify.
func (c *Catalog) resolveFeatureRecipeForVerify(manifest *types.Manifest, instanceName, featureType string) error {
	uri, ok, err := FeatureRecipeURI(manifest, instanceName, featureType)
	if err != nil {
		return err
	}
	if ok {
		if _, found := c.FeatureRecipeByURI(uri); !found {
			return fmt.Errorf("%w: feature %q (type %q): the Manifest maps it to feature recipe %q, which is not in the catalog", ErrNoRecipeResolved, instanceName, featureType, uri)
		}
		return nil
	}
	_, ids, single := matchFeatureRecipeByImplements(c.FeatureRecipes, featureType)
	if single {
		return nil
	}
	if len(ids) == 0 {
		return fmt.Errorf("%w: feature %q (type %q): no Manifest mapping and no feature recipe implements it", ErrNoRecipeResolved, instanceName, featureType)
	}
	return fmt.Errorf("%w: feature %q (type %q): feature recipes %v all implement it", ErrAmbiguousImplements, instanceName, featureType, ids)
}

// VerifyRecipeResolution is the pre-render resolution-completeness pass: every
// component and feature instance in the IR must resolve to exactly one recipe.
// It runs the same cascade the renderer uses and halts loudly — rather than
// letting a no-recipe instance silently render nothing or an ambiguous type
// silently pick one — naming the offending instance and (for ambiguity) the
// colliding recipes. Errors across all instances are collected (no
// short-circuit) and joined in IR author order. Returns nil when every instance
// resolves cleanly. Signature matches pipeline.Options.RecipeResolutionVerifier.
func (c *Catalog) VerifyRecipeResolution(manifest *types.Manifest, graph *types.IRGraph) error {
	if graph == nil {
		return nil
	}
	var errs []error
	for _, nodeID := range graph.Spec.Nodes.Keys() {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		if err := c.resolveComponentRecipeForVerify(manifest, nodeID, node.Component); err != nil {
			errs = append(errs, err)
		}
	}
	for nodeID, feats := range graph.Spec.FeatureValuesMapping.All() {
		_ = nodeID
		for instName, entry := range feats.All() {
			if err := c.resolveFeatureRecipeForVerify(manifest, instName, entry.Type); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for nodeID, secs := range graph.Spec.Secrets.All() {
		_ = nodeID
		for instName, entry := range secs.All() {
			if err := c.resolveFeatureRecipeForVerify(manifest, instName, entry.Type); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for bp, feats := range graph.Spec.BoundaryFeatures.All() {
		_ = bp
		for instName, bf := range feats.All() {
			if err := c.resolveFeatureRecipeForVerify(manifest, instName, bf.Type); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// VerifyManifestImplementsConsistency walks every populated mapping
// entry in the Manifest and verifies the resolved Recipe / FeatureRecipe's
// spec.implements matches the Manifest's component-type / feature-type
// key for that entry. Mismatches produce ErrManifestImplementsMismatch
// wrapped with file:line provenance on both sides; the function
// collects all violations rather than short-circuiting on the first.
//
// Returns nil when manifest is nil, when no flat-shape mappings are
// populated, or when every populated mapping resolves consistently.
// Legacy v2 mappings are not consistency-checked at this surface —
// they keep pre-migration semantics (Recipe lookup via spec.implements
// walking) where the consistency is implicit in the matching rule.
func (c *Catalog) VerifyManifestImplementsConsistency(manifest *types.Manifest, manifestPath string) []error {
	if manifest == nil {
		return nil
	}
	var errs []error
	// Component mappings.
	if manifest.Spec.Mappings.Components != nil {
		for compType, recipeURI := range manifest.Spec.Mappings.Components.Type.All() {
			r, ok := c.RecipeByURI(recipeURI)
			if !ok {
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch, cerrors.CatalogError, cerrors.SourceLocation{File: manifestPath}, fmt.Sprintf("manifest mapping %q -> %q: recipe not found in catalog", compType, recipeURI)))
				continue
			}
			recipeImplements := stripVersion(r.Spec.Implements)
			expected := stripVersion(compType)
			if recipeImplements != expected {
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch, cerrors.CatalogError, cerrors.SourceLocation{File: manifestPath}, fmt.Sprintf("manifest mapping %q -> %q: recipe spec.implements is %q (expected %q)", compType, recipeURI, r.Spec.Implements, compType)))
			}
		}
	}
	// Feature mappings.
	if manifest.Spec.Mappings.Features != nil {
		for featType, frURI := range manifest.Spec.Mappings.Features.Type.All() {
			fr, ok := c.FeatureRecipeByURI(frURI)
			if !ok {
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch, cerrors.CatalogError, cerrors.SourceLocation{File: manifestPath}, fmt.Sprintf("manifest feature mapping %q -> %q: featurerecipe not found in catalog", featType, frURI)))
				continue
			}
			frImplements := stripVersion(fr.Spec.Implements)
			expected := stripVersion(featType)
			if frImplements != expected {
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch, cerrors.CatalogError, cerrors.SourceLocation{File: manifestPath}, fmt.Sprintf("manifest feature mapping %q -> %q: featurerecipe spec.implements is %q (expected %q)", featType, frURI, fr.Spec.Implements, featType)))
			}
		}
	}
	return errs
}

// flatComponentURI returns the Recipe URI mapped to the given component
// instance in the Manifest's flat-shape mapping, when populated. Resolution
// is name-wins-over-type: a `name:` override on instanceName takes precedence
// over the (version-tolerant) `type:` match on componentType.
func flatComponentURI(manifest *types.Manifest, instanceName, componentType string) (string, bool, error) {
	if manifest == nil || manifest.Spec.Mappings.Components == nil {
		return "", false, nil
	}
	return nameThenType(manifest.Spec.Mappings.Components, instanceName, componentType)
}

// legacyComponentURI walks the legacy v2 per-provider mappings looking
// for componentType. Returns the first match across providers in
// author order. Pre-migration semantics: the renderer dispatched per
// (path, provider) and the right provider's mapping won; for the
// transitional overlay, we accept the first match as a sufficient
// fallback (the legacy fixtures with populated mappings all carry
// the single-provider shape).
func legacyComponentURI(manifest *types.Manifest, instanceName, componentType string) (string, bool, error) {
	if manifest == nil {
		return "", false, nil
	}
	for _, pm := range manifest.Spec.Mappings.Legacy.All() {
		if pm.Components == nil {
			continue
		}
		uri, ok, err := nameThenType(pm.Components, instanceName, componentType)
		if err != nil {
			return "", false, err
		}
		if ok {
			return uri, true, nil
		}
	}
	return "", false, nil
}

// ComponentRecipeURI returns the Recipe URI the Manifest maps componentType
// to — the flat-shape mapping first, then the legacy per-provider shape. The
// returned URI is the same verbatim string the dependency-closure walker
// records as a source-descent entity, so callers keying per-component data by
// this value join cleanly against the closure's entities (regardless of
// whether the author wrote the mapping value in short form or full-path form).
//
// Returns ("", false) when no Manifest mapping names componentType. Such a
// component carries no source-descent entity — it would be selected for render
// by spec.implements, which is a render-selection path, not a
// source-acquisition path — so it correctly contributes no source artifact.
func ComponentRecipeURI(manifest *types.Manifest, instanceName, componentType string) (string, bool, error) {
	if uri, ok, err := flatComponentURI(manifest, instanceName, componentType); err != nil {
		return "", false, err
	} else if ok {
		return uri, true, nil
	}
	return legacyComponentURI(manifest, instanceName, componentType)
}

// FeatureRecipeURI is the FeatureRecipe analogue of ComponentRecipeURI: it
// returns the FeatureRecipe URI the Manifest maps featureType to — the
// flat-shape mapping first, then the legacy per-provider shape. The returned URI
// is the same verbatim string the dependency-closure walker records as a
// feature_recipe source-descent entity, so callers keying per-feature-instance
// data by this value join cleanly against the closure's entities.
//
// Returns ("", false) when no Manifest mapping names featureType.
func FeatureRecipeURI(manifest *types.Manifest, instanceName, featureType string) (string, bool, error) {
	if uri, ok, err := flatFeatureURI(manifest, instanceName, featureType); err != nil {
		return "", false, err
	} else if ok {
		return uri, true, nil
	}
	return legacyFeatureURI(manifest, instanceName, featureType)
}

// flatFeatureURI is the Feature analogue of flatComponentURI: name-wins-over-type
// resolution of a feature instance against the flat-shape mapping.
func flatFeatureURI(manifest *types.Manifest, instanceName, featureType string) (string, bool, error) {
	if manifest == nil || manifest.Spec.Mappings.Features == nil {
		return "", false, nil
	}
	return nameThenType(manifest.Spec.Mappings.Features, instanceName, featureType)
}

// legacyFeatureURI is the Feature analogue of legacyComponentURI.
func legacyFeatureURI(manifest *types.Manifest, instanceName, featureType string) (string, bool, error) {
	if manifest == nil {
		return "", false, nil
	}
	for _, pm := range manifest.Spec.Mappings.Legacy.All() {
		if pm.Features == nil {
			continue
		}
		uri, ok, err := nameThenType(pm.Features, instanceName, featureType)
		if err != nil {
			return "", false, err
		}
		if ok {
			return uri, true, nil
		}
	}
	return "", false, nil
}

// stripVersion removes the :version segment from a URI, returning the
// base. Used by the consistency check to compare base-URI portions
// independent of version form.
func stripVersion(uri string) string {
	base, _, _ := splitVersionedURI(uri)
	return base
}
