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

// ErrDuplicateEntityIdentity fires when two catalog entities of one kind share
// the same identity WITHIN A SINGLE catalog directory — an authoring collision,
// surfaced loudly (strict load) or recorded as a skip (tolerant load). It is the
// homogenized rule across every indexed kind (Component, Feature, Recipe,
// FeatureRecipe, Provider, SchemaLibrary, MappingLibrary, Aggregate): within one
// directory a duplicate identity halts; ACROSS directories the same identity is
// a first-win shadow (the earlier-listed directory wins), never an error.
var ErrDuplicateEntityIdentity = errors.New("duplicate entity identity within a single catalog directory")

// ErrDuplicateRecipeIdentity is the recipe-specific facet of
// ErrDuplicateEntityIdentity, preserved as an alias so callers matching the
// recipe collision via errors.Is keep working. The Recipe / FeatureRecipe load
// cases wrap it with the provider+name+version context; the wrapped error still
// satisfies errors.Is against both this name and ErrDuplicateEntityIdentity.
var ErrDuplicateRecipeIdentity = ErrDuplicateEntityIdentity

// ErrNoRecipeResolved fires when a component/feature instance has no explicit
// Manifest mapping for its type. Resolution is mapping-only — the catalog never
// chooses a recipe by spec.implements, so an unmapped instance halts regardless
// of how many recipes implement its type. The diagnostic names the implementing
// recipes as pin candidates: implements informs the choice, the author makes it,
// and the Manifest mapping records it. Also fires when a mapping names a recipe
// absent from the catalog (a dangling mapping).
var ErrNoRecipeResolved = errors.New("no recipe resolved for component/feature instance: add an explicit Manifest mapping for its type")

// recipeURIKey composes the bare "name:version" URI form Manifest mapping
// values and the closure walker use. A versionless entity keys on its bare
// name. The catalog's identity key wraps this with the provider segment —
// see recipeIdentityKey.
func recipeURIKey(name, version string) string {
	if version == "" {
		return name
	}
	return name + ":" + version
}

// recipeIdentityKey is the catalog's identity key for a recipe or feature
// recipe: "provider/name:version". The provider segment is the entity's
// spec.provider. Identity is provider-scoped because the catalog tree
// (recipe/{provider}/{name}/) and the registry identity
// (recipe/{provider}/{name}:{version}) both are: two providers may each
// ship a recipe of the same name and version, and the naming convention
// keeps the provider out of metadata.name. An entity with an empty
// provider keys on the flat "name:version" form, so a
// malformed-but-loading entity keeps a stable identity rather than
// gaining a leading slash.
func recipeIdentityKey(provider, name, version string) string {
	if provider == "" {
		return recipeURIKey(name, version)
	}
	return provider + "/" + recipeURIKey(name, version)
}

// entityIndexKey is the catalog index key for a by-name entity (Component,
// Feature, Provider, Aggregate): the bare metadata.name when version-less,
// "name:version" when versioned — the recipe identity key minus the provider
// segment. Keying by name:version lets multiple versions of one entity coexist
// in the index instead of silently overwriting by bare name (the semver-incorrect
// last-wins the bare-name index produced); resolution then selects among the
// coexisting versions via selectEntityVersion.
func entityIndexKey(name, version string) string {
	return recipeURIKey(name, version)
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
		// A version-ambiguous mapping error is swallowed to a miss here; the
		// loud ErrAmbiguousTypeMapping is the pull/source-descent contract
		// (ComponentRecipeURI), where a wrong pick mis-pins a source. A mapped
		// URI that does not resolve falls through to a miss and surfaces as a
		// CATALOG_ERROR at the resolution-completeness pass.
		if uri, ok, err := flatComponentURI(manifest, instanceName, componentType); err == nil && ok {
			if r, found := c.RecipeByURI(uri); found {
				return &r, true
			}
		}
		// Layer 2: legacy v2 per-provider mapping.
		if uri, ok, err := legacyComponentURI(manifest, instanceName, componentType); err == nil && ok {
			if r, found := c.RecipeByURI(uri); found {
				return &r, true
			}
		}
		// Resolution is mapping-only: an unmapped type is a miss here. The
		// catalog never chooses a recipe by spec.implements — an unmapped
		// instance halts loudly in the pre-render resolution-completeness pass
		// (resolveComponentRecipeForVerify), which names the implementing
		// recipes as pin candidates.
		return nil, false
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
		// Mapping-only resolution: an unmapped feature type is a miss here;
		// implements never resolves. Unmapped feature instances halt in the
		// pre-render resolution-completeness pass with pin candidates.
		return nil, false
	}
}

// RecipeByURI resolves a Manifest mapping value (a Recipe URI) to its catalog
// Recipe. The catalog is keyed by provider-scoped identity
// ("provider/name:version"), and the URI may name the recipe at any of three
// precision levels, tried in order:
//
//  1. Provider-qualified exact ("terraform/mariadb-rds:1.0.0") — a direct
//     map get on the identity key.
//  2. Provider-qualified by scan — the segment before the first "/" is
//     treated as a provider candidate; within that provider the name (and
//     version, when given) must match exactly one entity. A versioned URI
//     whose exact version is absent falls back to a version-blind scan
//     within the provider, preserving the version-tolerant posture the bare
//     form has always had.
//  3. Bare ("mariadb-rds:1.0.0" or "mariadb-rds") — a scan across every
//     provider, version-exact first, then version-blind; exactly one match
//     resolves.
//
// Any level that finds more than one candidate is a miss, never a silent
// pick — ambiguity surfaces as a loud resolution halt upstream (the same
// contract the unversioned-ambiguity path has always had, extended to the
// provider dimension). The "/" split cannot misread a bare reference:
// recipe names contain no "/" per the catalog naming convention, and a
// failed provider interpretation falls through to bare resolution anyway.
func (c *Catalog) RecipeByURI(uri string) (types.Recipe, bool) {
	if r, ok := c.Recipes[uri]; ok {
		return r, true
	}
	if provider, rest, found := strings.Cut(uri, "/"); found {
		name, version, _ := splitVersionedURI(rest)
		if r, ok := matchOneRecipe(c.Recipes, provider, name, version); ok {
			return r, true
		}
		if version != "" {
			if r, ok := matchOneRecipe(c.Recipes, provider, name, ""); ok {
				return r, true
			}
		}
	}
	name, version, _ := splitVersionedURI(uri)
	if r, ok := matchOneRecipe(c.Recipes, "", name, version); ok {
		return r, true
	}
	if version != "" {
		if r, ok := matchOneRecipe(c.Recipes, "", name, ""); ok {
			return r, true
		}
	}
	return types.Recipe{}, false
}

// matchOneRecipe scans the identity map for entities matching the given
// predicates (empty provider or version = wildcard) and returns the match
// only when it is unique. Map-iteration order is irrelevant: the result is
// gated on the count, not the encounter order.
func matchOneRecipe(rs map[string]types.Recipe, provider, name, version string) (types.Recipe, bool) {
	var match types.Recipe
	n := 0
	for _, r := range rs {
		if provider != "" && r.Spec.Provider != provider {
			continue
		}
		if r.Metadata.Name != name {
			continue
		}
		if version != "" && r.Metadata.Version != version {
			continue
		}
		match = r
		n++
	}
	return match, n == 1
}

// FeatureRecipeByURI is the FeatureRecipe analogue of RecipeByURI; the
// resolution ladder is identical.
func (c *Catalog) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	if fr, ok := c.FeatureRecipes[uri]; ok {
		return fr, true
	}
	if provider, rest, found := strings.Cut(uri, "/"); found {
		name, version, _ := splitVersionedURI(rest)
		if fr, ok := matchOneFeatureRecipe(c.FeatureRecipes, provider, name, version); ok {
			return fr, true
		}
		if version != "" {
			if fr, ok := matchOneFeatureRecipe(c.FeatureRecipes, provider, name, ""); ok {
				return fr, true
			}
		}
	}
	name, version, _ := splitVersionedURI(uri)
	if fr, ok := matchOneFeatureRecipe(c.FeatureRecipes, "", name, version); ok {
		return fr, true
	}
	if version != "" {
		if fr, ok := matchOneFeatureRecipe(c.FeatureRecipes, "", name, ""); ok {
			return fr, true
		}
	}
	return types.FeatureRecipe{}, false
}

// matchOneFeatureRecipe is the FeatureRecipe twin of matchOneRecipe.
func matchOneFeatureRecipe(frs map[string]types.FeatureRecipe, provider, name, version string) (types.FeatureRecipe, bool) {
	var match types.FeatureRecipe
	n := 0
	for _, fr := range frs {
		if provider != "" && fr.Spec.Provider != provider {
			continue
		}
		if fr.Metadata.Name != name {
			continue
		}
		if version != "" && fr.Metadata.Version != version {
			continue
		}
		match = fr
		n++
	}
	return match, n == 1
}

// WasmPluginByURI returns the WasmPlugin entity whose identity matches uri, plus
// a found flag. A WasmPlugin is a by-name entity (keyed metadata.name:version,
// like Provider / Component), not a provider-qualified one, so resolution goes
// through the shared three-form version select (exact / caret / unversioned ->
// highest-satisfying) rather than the provider-scan ladder RecipeByURI uses. The
// returned entity carries spec.plugin (the OCI artifact reference); resolving that
// reference to a pinned artifact is a separate concern (the closure's artifact
// resolution), not this accessor's job — this serves the entity struct only.
//
// Served by both catalog backings: the lazy backing exposes its enumerated
// WasmPlugin set through the recipeCatalog() *Catalog shell, so its
// WasmPluginByURI delegates here against the same matcher (no per-backing
// reimplementation).
func (c *Catalog) WasmPluginByURI(uri string) (types.WasmPlugin, bool) {
	key, ok, _ := c.resolveWasmPluginKey(uri)
	if !ok {
		return types.WasmPlugin{}, false
	}
	return c.WasmPlugins[key], true
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

// recipesImplementing returns the sorted, provider-qualified identity keys of
// every catalog recipe whose spec.implements satisfies componentType. It is a
// SUGGESTION provider only — resolution is mapping-only and never consults it.
// Its readers are the no-mapping diagnostic (pin candidates) and the Manifest
// recipe-reference completion (implements-aware ranking).
func recipesImplementing(recipes map[string]types.Recipe, componentType string) []string {
	var ids []string
	for _, r := range recipes {
		if implementsSatisfies(r.Spec.Implements, componentType) {
			ids = append(ids, recipeIdentityKey(r.Spec.Provider, r.Metadata.Name, r.Metadata.Version))
		}
	}
	sort.Strings(ids)
	return ids
}

// featureRecipesImplementing is the FeatureRecipe analogue of recipesImplementing.
func featureRecipesImplementing(frs map[string]types.FeatureRecipe, featureType string) []string {
	var ids []string
	for _, fr := range frs {
		if implementsSatisfies(fr.Spec.Implements, featureType) {
			ids = append(ids, recipeIdentityKey(fr.Spec.Provider, fr.Metadata.Name, fr.Metadata.Version))
		}
	}
	sort.Strings(ids)
	return ids
}

// resolveComponentRecipeForVerify verifies that one component instance resolves
// through an explicit Manifest mapping. Resolution is mapping-only: a mapped
// instance whose recipe is in the catalog resolves cleanly; a mapping to an
// absent recipe (dangling) or no mapping at all halts loudly. An unmapped
// instance always halts regardless of how many recipes implement its type — the
// diagnostic lists those recipes as pin candidates (implements suggests; the
// author chooses; the Manifest records). nil means the instance resolves cleanly.
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
	if candidates := recipesImplementing(c.Recipes, componentType); len(candidates) > 0 {
		return fmt.Errorf("%w: component %q (type %q); recipes implementing it: %v; pin one in the Manifest", ErrNoRecipeResolved, instanceName, componentType, candidates)
	}
	return fmt.Errorf("%w: component %q (type %q); no recipe implements this type", ErrNoRecipeResolved, instanceName, componentType)
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
	if candidates := featureRecipesImplementing(c.FeatureRecipes, featureType); len(candidates) > 0 {
		return fmt.Errorf("%w: feature %q (type %q); feature recipes implementing it: %v; pin one in the Manifest", ErrNoRecipeResolved, instanceName, featureType, candidates)
	}
	return fmt.Errorf("%w: feature %q (type %q); no feature recipe implements this type", ErrNoRecipeResolved, instanceName, featureType)
}

// ResolutionFindings is the shared resolution-completeness producer: it walks
// every component and feature instance in the IR and returns one error per
// instance that does NOT resolve through an explicit Manifest mapping (unmapped
// — naming the implementing recipes as pin candidates — or mapped to a recipe
// absent from the catalog). The slice is in IR author order; an empty slice
// means every instance resolves cleanly. Both surfaces consume this single
// routine so they cannot drift: the CLI build joins the slice into a loud halt
// (VerifyRecipeResolution); the serve daemon maps each finding to a resilient
// diagnostic. The mapping-only cascade matches the renderer's — the catalog
// never chooses a recipe by spec.implements.
func (c *Catalog) ResolutionFindings(manifest *types.Manifest, graph *types.IRGraph) []error {
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
	return errs
}

// VerifyRecipeResolution is the CLI build's pre-render resolution-completeness
// halt: it joins ResolutionFindings into a single error so an unmapped instance
// halts the build loudly rather than silently rendering nothing. Returns nil
// when every instance resolves cleanly. Signature matches
// pipeline.Options.RecipeResolutionVerifier.
func (c *Catalog) VerifyRecipeResolution(manifest *types.Manifest, graph *types.IRGraph) error {
	return errors.Join(c.ResolutionFindings(manifest, graph)...)
}

// VerifyManifestImplementsConsistency walks every populated mapping
// entry in the Manifest and verifies the resolved Recipe / FeatureRecipe's
// spec.implements matches the Manifest's component-type / feature-type
// key for that entry. Mismatches produce ErrManifestImplementsMismatch
// wrapped with file:line provenance on both sides; the function
// collects all violations rather than short-circuiting on the first.
//
// A mapping whose recipe URI does not resolve in the catalog is skipped here,
// not reported: a missing/unresolvable recipe is a resolution concern owned by
// the recipe-resolution path (VerifyRecipeResolution and the catalog lookup),
// which surfaces it with richer context — including, for an entity skipped at
// tolerant load, a replay of WHY it went missing. Reporting "not found" here
// too would preempt that better diagnostic. This function is therefore the
// found-but-mismatched check only.
//
// Returns nil when manifest is nil, when no flat-shape mappings are
// populated, or when every resolved mapping is consistent. Legacy v2 mappings
// are not consistency-checked at this surface — they keep pre-migration
// semantics (Recipe lookup via spec.implements walking) where the consistency
// is implicit in the matching rule.
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
				continue // missing recipe → resolution path's concern, not a consistency violation
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
				continue // missing featurerecipe → resolution path's concern
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

// AssertManifestImplementsConsistency runs VerifyManifestImplementsConsistency
// and joins any violations into a single error (nil when consistent). It is the
// production halt surface: every caller that gates a build (the CLI build and
// pull commands, and the build-faithful pipeline harness) calls this so the
// halt semantics — collect-all-then-join — are identical across all of them.
// The check is mapping-table-level (mapped type vs the resolved recipe's
// spec.implements) and runs before IR construction, complementing the post-IR
// per-node VerifyRecipeResolution.
func (c *Catalog) AssertManifestImplementsConsistency(manifest *types.Manifest, manifestPath string) error {
	errs := c.VerifyManifestImplementsConsistency(manifest, manifestPath)
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
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
