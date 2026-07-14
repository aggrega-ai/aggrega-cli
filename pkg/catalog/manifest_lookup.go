// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"fmt"
	"slices"
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
// FeatureRecipe, Plugin, SchemaLibrary, MappingLibrary, Aggregate): within one
// directory a duplicate identity halts; ACROSS directories the same identity is
// a first-win shadow (the earlier-listed directory wins), never an error.
var ErrDuplicateEntityIdentity = errors.New("duplicate entity identity within a single catalog directory")

// ErrDuplicateRecipeIdentity is the recipe-specific facet of
// ErrDuplicateEntityIdentity, preserved as an alias so callers matching the
// recipe collision via errors.Is keep working. The Recipe / FeatureRecipe load
// cases wrap it with the provider+name+version context; the wrapped error still
// satisfies errors.Is against both this name and ErrDuplicateEntityIdentity.
var ErrDuplicateRecipeIdentity = ErrDuplicateEntityIdentity

// ErrProviderVersionUnsatisfiable fires when a Recipe binds a version-pinned
// provider — "helm:1.0.0" or "helm:^1.0.0" — but no provider-rendering Plugin
// serving that provider satisfies the pin: either the provider has no serving
// Plugin at all, or the serving Plugin's available versions do not satisfy the
// exact/caret constraint. The version pin is an explicit request the compiler
// cannot honor, so the build halts loudly rather than silently ignoring the pin
// and rendering through whatever plugin version happens to serve the provider.
// The wrapped message names the provider and the pinned version so each provider
// yields a specific diagnostic. It is distinct
// from ErrUnsatisfiableEntityVersion, which covers Component / Feature / Aggregate
// version references, not provider-engine selection.
var ErrProviderVersionUnsatisfiable = errors.New("no provider-rendering plugin version satisfies the recipe's provider version pin")

// ErrAmbiguousProviderPlugin fires at resolution when a render target is served
// by more than one distinct provider-rendering Plugin and no plugins.providers
// entry disambiguates which one renders it. The compiler never picks arbitrarily
// among competing render engines — a federated catalog where two publishers each
// ship a plugin for the same target is legal (the catalog loads), but a build or
// pull that reaches the target must be told which engine to use. The wrapped
// message names every candidate plugin, sorted, and points the operator at the
// plugins.providers surface that selects one. It is a point-of-use halt: it fires
// at pull (resolution) and at build (render-engine selection), not at catalog
// load, so an ambiguity outside a Release's reach never blocks an unrelated build.
var ErrAmbiguousProviderPlugin = errors.New("render target is served by more than one provider plugin; add a plugins.providers entry to select one")

// ErrProviderBindingUnsatisfied fires at resolution when a plugins.providers
// entry selects a plugin identity that no provider-rendering Plugin serving the
// target matches — a typo in the binding, or a binding pointing at a plugin that
// does not (or no longer) serves the target. The explicit selection is a request
// the catalog cannot honor, so it halts loudly rather than silently falling back
// to another engine.
var ErrProviderBindingUnsatisfied = errors.New("plugins.providers entry names a plugin no provider-rendering plugin serving the target matches")

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
// Feature, Aggregate): the bare metadata.name when version-less,
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
//
// Resolution is mapping-only: a type with no flat-shape or legacy mapping
// is a miss here — the catalog never chooses a recipe by spec.implements.
// An unmapped instance halts loudly in the pre-render resolution-completeness
// pass, which names the implementing recipes as pin candidates. When manifest
// is nil, every type is a miss.
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
		if provider != "" && r.Spec.Target != provider {
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
		if provider != "" && fr.Spec.Target != provider {
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

// PluginByURI returns the Plugin entity whose identity matches uri, plus
// a found flag. A Plugin is a by-name entity (keyed metadata.name:version,
// like Component), not a provider-qualified one, so resolution goes
// through the shared three-form version select (exact / caret / unversioned ->
// highest-satisfying) rather than the provider-scan ladder RecipeByURI uses. The
// returned entity's .wasm artifact reference is derived from its bare identity;
// resolving that reference to a pinned artifact is a separate concern (the
// closure's artifact resolution), not this accessor's job — this serves the
// entity struct only.
//
// Served by both catalog backings: the lazy backing exposes its enumerated
// Plugin set through the recipeCatalog() *Catalog shell, so its
// PluginByURI delegates here against the same matcher (no per-backing
// reimplementation).
func (c *Catalog) PluginByURI(uri string) (types.Plugin, bool) {
	key, ok, _ := c.resolvePluginKey(uri)
	if !ok {
		return types.Plugin{}, false
	}
	return c.Plugins[key], true
}

// ProviderPluginForTarget resolves a render target to the provider-rendering
// Plugin entity that renders it, plus a found flag and an error, honoring an
// explicit plugins.providers selection. The target ref is the bare engine name
// ("helm") or a version-pinned form ("helm:1.0.0", "helm:^1.0.0") a Recipe binds
// through spec.target; the bare name (before any pin) is matched against
// targets. A pluginType: provider Plugin serves the target names listed in
// its targets; a linter or frp_step plugin whose targets happens to list the name
// is excluded — that list carries a different meaning for a non-provider plugin.
//
// The bindings map is the workspace-config (at pull) or lockfile-derived (at
// build) provider selections, keyed by bare target name; each value is a plugin's
// bare identity (a Plugin's metadata.name) with an optional trailing ":version".
// When an entry is present for the target, the candidate set is filtered to
// plugins whose identity matches that value — so the binding SELECTS the plugin
// repository among competing publishers. A nil
// or empty bindings map disables that filter (every provider plugin serving the
// target is a candidate). Version coexistence is orthogonal to the binding: the
// binding selects the repository, and the version is chosen by the recipe's pin,
// else the binding's optional version, else the highest — via the same three-form
// version select the other by-name lookups use.
//
// Return contract:
//   - (plugin, true, nil) on a satisfying match;
//   - (_, false, nil) on a bare miss (no serving plugin and no binding, no
//     version pin) — left for the caller to handle (build halts loudly at render);
//   - (_, false, ErrAmbiguousProviderPlugin) when more than one distinct provider
//     plugin serves the target and no binding disambiguates — a loud point-of-use
//     halt naming the candidates;
//   - (_, false, ErrProviderBindingUnsatisfied) when a binding selects a plugin no
//     serving candidate matches — a loud halt on an unfulfillable explicit choice;
//   - (_, false, ErrProviderVersionUnsatisfiable) when a version pin is present but
//     no serving plugin version satisfies it — a loud halt on the pin.
//
// Determinism (byte-identical output): matches collect into a sub-index that the
// shared version select sorts before choosing, and the candidate names sort before
// they enter a diagnostic, so no outcome depends on map-iteration order.
//
// Served by both catalog backings: the lazy backing exposes its enumerated Plugin
// set through the recipeCatalog() *Catalog shell, so its ProviderPluginForTarget
// delegates here against the same matcher (no per-backing reimplementation).
func (c *Catalog) ProviderPluginForTarget(ref string, bindings map[string]string) (types.Plugin, bool, error) {
	name, verSeg, hasVer := splitVersionedURI(ref)

	var bindBase, bindVer string
	var bindHasVer, bound bool
	if bv, ok := bindings[name]; ok && bv != "" {
		bound = true
		bindBase, bindVer, bindHasVer = splitPluginRefVersion(bv)
	}

	matches := map[string]types.Plugin{}
	distinctNames := map[string]struct{}{}
	for key, p := range c.Plugins {
		if p.Spec.PluginType != "provider" {
			continue
		}
		if !slices.Contains(p.Spec.Targets, name) {
			continue
		}
		// A binding selects the plugin repository by its bare identity
		// (metadata.name), filtering out every competing publisher's plugin for
		// this target.
		if bound && p.Metadata.Name != bindBase {
			continue
		}
		matches[key] = p
		distinctNames[p.Metadata.Name] = struct{}{}
	}

	// The version pin priority: the recipe's target pin, else the binding's
	// optional version. Threaded into the diagnostic and the version select alike.
	pin := ""
	if hasVer {
		pin = verSeg
	} else if bound && bindHasVer {
		pin = bindVer
	}

	switch len(distinctNames) {
	case 0:
		if bound {
			return types.Plugin{}, false, fmt.Errorf("%w: target %q is bound to plugin %q, but no provider-rendering plugin serving %q matches that identity", ErrProviderBindingUnsatisfied, name, bindBase, name)
		}
		if hasVer {
			return types.Plugin{}, false, fmt.Errorf("%w: provider %q pins %q but no provider-rendering plugin serves it", ErrProviderVersionUnsatisfiable, name, verSeg)
		}
		// No serving plugin, no binding, no pin — a bare miss the caller resolves.
		return types.Plugin{}, false, nil
	case 1:
		// Exactly one repository — fall through to version select.
	default:
		names := make([]string, 0, len(distinctNames))
		for n := range distinctNames {
			names = append(names, n)
		}
		sort.Strings(names)
		return types.Plugin{}, false, fmt.Errorf("%w: target %q is served by more than one provider plugin (%s); add a plugins.providers entry for %q selecting one", ErrAmbiguousProviderPlugin, name, strings.Join(names, ", "), name)
	}

	var base string
	for n := range distinctNames {
		base = n
	}
	// Thread the version constraint into the shared three-form select by rebuilding
	// the caller URI against the PLUGIN's identity name (which differs from the
	// target name — e.g. target "helm" served by plugin "helm-renderer").
	callerURI := base
	if pin != "" {
		callerURI = base + ":" + pin
	}
	key, ok, err := selectEntityVersion(matches, callerURI, func(v types.Plugin) (string, string) {
		return v.Metadata.Name, v.Metadata.Version
	})
	if err != nil {
		// selectEntityVersion signals an unsatisfiable exact/caret constraint;
		// translate it to the provider-specific halt naming the pin.
		return types.Plugin{}, false, fmt.Errorf("%w: provider %q pins %q but no serving plugin version satisfies it", ErrProviderVersionUnsatisfiable, name, pin)
	}
	if !ok {
		return types.Plugin{}, false, nil
	}
	return matches[key], true, nil
}

// ProviderPluginCandidates returns every provider-rendering Plugin serving the
// bare render target, sorted by metadata name then version, for a diagnostic or
// an interactive disambiguation prompt. It applies no binding filter and no
// version select — it is the raw candidate set the ambiguity halt reports over.
// The sort makes the returned order byte-identical across runs.
func (c *Catalog) ProviderPluginCandidates(target string) []types.Plugin {
	var out []types.Plugin
	for _, p := range c.Plugins {
		if p.Spec.PluginType != "provider" {
			continue
		}
		if !slices.Contains(p.Spec.Targets, target) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Metadata.Name != out[j].Metadata.Name {
			return out[i].Metadata.Name < out[j].Metadata.Name
		}
		return out[i].Metadata.Version < out[j].Metadata.Version
	})
	return out
}

// ProviderPluginByName resolves a render target to its provider-rendering Plugin
// entity with no explicit binding — the zero-config path where a target served by
// exactly one provider plugin resolves to it, and an ambiguously-served target
// halts with ErrAmbiguousProviderPlugin. It is ProviderPluginForTarget with a nil
// bindings map; the binding-aware callers (pull, build render) pass their bindings
// directly. Retained as the by-name accessor for the catalog-wide capability
// checks and the eager CatalogLookup, which resolve without a binding surface.
func (c *Catalog) ProviderPluginByName(ref string) (types.Plugin, bool, error) {
	return c.ProviderPluginForTarget(ref, nil)
}

// implementsSatisfies reports whether a recipe declaring spec.implements==impl
// satisfies a request for componentType, under the three-form matching rule
// (exact, caret-range, unversioned base). It is expressed per-entry so the
// implements SEARCH can collect every matching recipe (the catalog now holds
// more than one recipe per type), and it is the single caret-aware implements
// matcher — the manifest-mapping consistency check consumes it too, so mapping
// validation honours caret ranges rather than comparing version-stripped bases.
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

// manifestMappingConsistent reports whether a Manifest mapping key and the
// mapped recipe's spec.implements refer to the same type at compatible
// versions. It is bidirectional over the version dimension:
//
//   - A version-pinned key (exact or caret) is judged by implementsSatisfies,
//     which honours an exact version, a caret range, and an unversioned
//     implements (a recipe implementing any version of the base).
//   - A bare (versionless) key imposes no version constraint, so a base match
//     alone is consistent — including a bare key against a version-pinned
//     implements. implementsSatisfies rejects that direction because it treats
//     a versionless request as unmatchable, so the bare-key case is handled
//     here rather than by loosening implementsSatisfies (whose other caller
//     passes a concrete component version, where a bare request never arises).
func manifestMappingConsistent(impl, key string) bool {
	if implementsSatisfies(impl, key) {
		return true
	}
	if _, _, keyHasVer := splitVersionedURI(key); !keyHasVer {
		return stripVersion(impl) == key
	}
	return false
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
			ids = append(ids, recipeIdentityKey(r.Spec.Target, r.Metadata.Name, r.Metadata.Version))
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
			ids = append(ids, recipeIdentityKey(fr.Spec.Target, fr.Metadata.Name, fr.Metadata.Version))
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
			if !manifestMappingConsistent(r.Spec.Implements, compType) {
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
			if !manifestMappingConsistent(fr.Spec.Implements, featType) {
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
