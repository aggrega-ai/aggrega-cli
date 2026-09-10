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
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// manifestCategory is the authoring domain a fault on a Manifest is reported
// under. It is read from the one home of the kind-to-domain correspondence
// rather than written out here: a category named at a raise site is a second
// copy of that correspondence, and a copy agrees on the day it is written and
// is free to disagree the day either side moves.
//
// It panics on an unmapped kind, for the reason the validator's own resolution
// panics: the kind is a constant chosen in the compiler rather than read from
// an authored document, so an unmappable one means the correspondence stopped
// covering a kind that raises categorised faults. That is a mistake in the
// compiler, not in anything a user wrote, and it would otherwise surface as a
// fault quietly attributed to the wrong authoring domain.
func manifestCategory() cerrors.Category {
	category, ok := cerrors.ClassifyEntityKind(string(entitykind.Manifest))
	if !ok {
		panic("catalog: the Manifest kind belongs to no authoring domain, so a manifest-mapping " +
			"fault cannot be attributed; extend the entity-kind category mapping to cover it")
	}
	return category
}

// ErrManifestImplementsMismatch fires when a Manifest mapping entry's
// resolved Recipe (or FeatureRecipe) carries a spec.implements that
// doesn't match the Manifest's component-type / feature-type key for
// that entry. The mismatch is a structural consistency invariant per
// the schema-language migration's mapping authoring surface: the
// Manifest entry IS the Recipe URI; the Recipe's spec.implements is
// a consistency anchor, not a dispatch mechanism. Mismatches halt at
// workspace load.
func ErrManifestImplementsMismatch() error { return errManifestImplementsMismatch }

var errManifestImplementsMismatch = cerrors.Register("manifest-implements-mismatch", cerrors.Meta{
	ShortCode: "E0096",
	Message:   "manifest mapping entry's recipe spec.implements does not match the mapped component-type / feature-type key",
})

// ErrAmbiguousTypeMapping fires when a component/feature type resolves to more
// than one version-distinct Manifest mapping key under version-tolerant
// matching — the lookup cannot pick one without guessing, so it halts loudly
// rather than silently resolving the first.
func ErrAmbiguousTypeMapping() error { return errAmbiguousTypeMapping }

var errAmbiguousTypeMapping = cerrors.Register("ambiguous-type-mapping", cerrors.Meta{
	ShortCode: "E0093",
	Message:   "manifest maps multiple version-distinct keys to the same base type; the type-to-recipe lookup is ambiguous",
})

// ErrDuplicateEntityIdentity fires when two catalog entities of one kind share
// the same identity WITHIN A SINGLE catalog directory — an authoring collision,
// surfaced loudly (strict load) or recorded as a skip (tolerant load). It is the
// homogenized rule across every indexed kind (Component, Feature, Recipe,
// FeatureRecipe, Plugin, SchemaLibrary, MappingLibrary, Aggregate): within one
// directory a duplicate identity halts; ACROSS directories the same identity is
// a first-win shadow (the earlier-listed directory wins), never an error.
func ErrDuplicateEntityIdentity() error { return errDuplicateEntityIdentity }

var errDuplicateEntityIdentity = cerrors.Register("duplicate-entity-identity", cerrors.Meta{
	ShortCode: "E0094",
	Message:   "duplicate entity identity within a single catalog directory",
})

// ErrDuplicateRecipeIdentity is the recipe-specific facet of
// ErrDuplicateEntityIdentity, preserved as an alias so callers matching the
// recipe collision via errors.Is keep working. The Recipe / FeatureRecipe load
// cases wrap it with the provider+name+version context; the wrapped error still
// satisfies errors.Is against both this name and ErrDuplicateEntityIdentity.
func ErrDuplicateRecipeIdentity() error { return ErrDuplicateEntityIdentity() }

// ErrProviderVersionUnsatisfiable fires when a Recipe binds a version-pinned
// provider — "helm:1.0.0" or "helm:^1.0.0" — but no provider-rendering Plugin
// serving that provider satisfies the pin: either the provider has no serving
// Plugin at all, or the serving Plugin's available versions do not satisfy the
// exact/caret constraint. The version pin is an explicit request the compiler
// cannot honor, so the build halts loudly rather than silently ignoring the pin
// and rendering through whatever plugin version happens to serve the provider.
// The wrapped message names the provider and the pinned version so each provider
// yields a specific diagnostic. It is distinct
// from ErrUnsatisfiableEntityVersion, which covers a catalog ENTITY reference's
// version constraint — a Component, Feature, Aggregate, Plugin, Recipe or
// FeatureRecipe named at a version the catalog does not hold — rather than the
// selection of a render engine for a capability.
func ErrProviderVersionUnsatisfiable() error { return errProviderVersionUnsatisfiable }

var errProviderVersionUnsatisfiable = cerrors.Register("provider-version-unsatisfiable", cerrors.Meta{
	ShortCode: "E0101",
	Message:   "no provider-rendering plugin version satisfies the recipe's provider version pin",
})

// ErrAmbiguousProviderPlugin fires at resolution when a capability is served
// by more than one distinct provider-rendering Plugin and no plugins.providers
// entry disambiguates which one renders it. The compiler never picks arbitrarily
// among competing render engines — a federated catalog where two publishers each
// ship a plugin for the same target is legal (the catalog loads), but a build or
// pull that reaches the target must be told which engine to use. The wrapped
// message names every candidate plugin, sorted, and points the operator at the
// plugins.providers surface that selects one. It is a point-of-use halt: it fires
// at pull (resolution) and at build (render-engine selection), not at catalog
// load, so an ambiguity outside a Release's reach never blocks an unrelated build.
func ErrAmbiguousProviderPlugin() error { return errAmbiguousProviderPlugin }

var errAmbiguousProviderPlugin = cerrors.Register("ambiguous-provider-plugin", cerrors.Meta{
	ShortCode: "E0092",
	Message:   "capability is served by more than one provider plugin; add a plugins.providers entry to select one",
})

// ErrProviderBindingUnsatisfied fires at resolution when a plugins.providers
// entry selects a plugin identity that no provider-rendering Plugin serving the
// target matches — a typo in the binding, or a binding pointing at a plugin that
// does not (or no longer) serves the target. The explicit selection is a request
// the catalog cannot honor, so it halts loudly rather than silently falling back
// to another engine.
func ErrProviderBindingUnsatisfied() error { return errProviderBindingUnsatisfied }

var errProviderBindingUnsatisfied = cerrors.Register("provider-binding-unsatisfied", cerrors.Meta{
	ShortCode: "E0100",
	Message:   "plugins.providers entry names a plugin no provider-rendering plugin serving the capability matches",
})

// ErrNoRecipeResolved fires when a component/feature instance has no explicit
// Manifest mapping for its type. Resolution is mapping-only — the catalog never
// chooses a recipe by spec.implements, so an unmapped instance halts regardless
// of how many recipes implement its type. The diagnostic names the implementing
// recipes as pin candidates: implements informs the choice, the author makes it,
// and the Manifest mapping records it. Also fires when a mapping value at an
// accepted reference shape names a recipe absent from the catalog (a dangling
// mapping) — including a recipe present under a different version than the one
// the value pins, and one whose bare name two capabilities both offer, since
// neither resolves to a recipe the author can be said to have chosen. A mapping
// value whose shape a reference cannot express is
// ErrMalformedRecipeReference instead, so the absent-from-the-catalog claim is
// only made about a reference that could have resolved.
func ErrNoRecipeResolved() error { return errNoRecipeResolved }

var errNoRecipeResolved = cerrors.Register("no-recipe-resolved", cerrors.Meta{
	ShortCode: "E0099",
	Message:   "no recipe resolved for component/feature instance: add an explicit Manifest mapping for its type",
})

// ErrMalformedRecipeReference fires when a Manifest mapping value carries more
// slash-separated segments than a Recipe / FeatureRecipe reference accepts. The
// verdict comes from the reference string alone, so the diagnostic names the
// shape the author typed and the shapes a reference accepts, instead of
// reporting the entity as absent: the recipe may be present in the catalog and
// correctly named, and only unreachable through the shape written. That is why
// it is distinct from ErrNoRecipeResolved, whose remedy — add a Manifest mapping
// for the type — is a step the author of a mis-shaped mapping has already taken.
// The accepted shape itself has a single home beside the resolution ladder that
// enforces it; see recipeReferenceMaxSegments.
func ErrMalformedRecipeReference() error { return errMalformedRecipeReference }

var errMalformedRecipeReference = cerrors.Register("malformed-recipe-reference", cerrors.Meta{
	ShortCode: "E0095",
	Message:   "malformed recipe reference in a Manifest mapping value",
})

// RecipeMappingURI composes the bare "name:version" URI form that Manifest
// mapping values and the closure walker use. A versionless entity keys on its
// bare name. It is the single home for this URI form, so a caller synthesizing
// a Manifest mapping value (for example a standalone-render harness) produces
// the exact string the resolver expects rather than reconstructing it.
func RecipeMappingURI(name, version string) string {
	if version == "" {
		return name
	}
	return name + ":" + version
}

// recipeURIKey composes the bare "name:version" URI form Manifest mapping
// values and the closure walker use. The catalog's identity key wraps this
// with the provider segment — see recipeIdentityKey.
func recipeURIKey(name, version string) string {
	return RecipeMappingURI(name, version)
}

// recipeIdentityKey is the catalog's identity key for a recipe or feature
// recipe: "provider/name:version". The provider segment is the entity's
// spec.capability. Identity is provider-scoped because the catalog tree
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
		return "", false, fmt.Errorf("%w: type %q matches version-distinct keys %v — pin the authored type's version or remove the duplicate mapping keys", ErrAmbiguousTypeMapping(), lookupType, matchedKeys)
	}
}

// nameThenType resolves a mapping tier with name-wins-over-type precedence:
// an exact instance-name hit short-circuits all further lookup; otherwise the
// version-tolerant type match applies. This is the single expression of the
// Manifest two-tier precedence per entity kind — the render lookup and the
// pull/source-descent lookup both route through it, so those entry points stay
// dumb forwarders and the precedence cannot drift between the two resolution
// paths. An empty instanceName
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
// and FeatureRecipe closures resolve through the Manifest's mappings when a
// Manifest is supplied: a mapping entry under manifest.Spec.Mappings.Components
// or .Features resolves its Recipe URI directly against the catalog by Recipe
// URI, never by spec.implements.
//
// A MAPPING WINS ON EXISTENCE, NOT ON RESOLVABILITY, and that is the whole
// precedence rule — the single home for it, which both consumption paths read.
// A mapping entry is the author's answer for that type, so a value naming a
// recipe the catalog does not hold is a miss here rather than an invitation to
// resolve by some other route. Answering any other way would make this lookup
// disagree with ComponentRecipeURI / FeatureRecipeURI, which the source-descent
// walk and the resolution-completeness pass both read — and a build whose
// rendered output named one recipe while its own reported fault named another
// is a divergence no author could act on.
//
// Resolution is mapping-only: a type the Manifest does not map is a miss here —
// the catalog never chooses a recipe by spec.implements. An unmapped instance
// halts loudly in the pre-render resolution-completeness pass, which names the
// implementing recipes as pin candidates. When manifest is nil, every type is a
// miss.
func (c *Catalog) RecipeLookupWithManifest(manifest *types.Manifest) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe:        c.componentTypeToRecipeWithManifest(manifest),
		FeatureRecipe: c.featureTypeToFeatureRecipeWithManifest(manifest),
	}
}

func (c *Catalog) componentTypeToRecipeWithManifest(manifest *types.Manifest) func(string, string) (*types.Recipe, bool) {
	return func(instanceName, componentType string) (*types.Recipe, bool) {
		// Name-wins-over-type resolution via componentMappingURI. A
		// version-ambiguous mapping error is swallowed to a miss here; the
		// loud ErrAmbiguousTypeMapping is the pull/source-descent contract
		// (ComponentRecipeURI), where a wrong pick mis-pins a source.
		//
		// A mapping entry is answered whether or not its URI resolves — the
		// precedence rule is stated once, on RecipeLookupWithManifest. The
		// unresolvable case is a miss here and surfaces as a CATALOG_ERROR at
		// the resolution-completeness pass, which names the mapping value the
		// author wrote.
		if uri, ok, err := componentMappingURI(manifest, instanceName, componentType); err == nil && ok {
			r, found := c.RecipeByURI(uri)
			if !found {
				return nil, false
			}
			return &r, true
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
		// A mapping entry is answered whether or not its URI resolves, exactly
		// as it is for a component: same precedence rule, stated once on
		// RecipeLookupWithManifest.
		if uri, ok, err := featureMappingURI(manifest, instanceName, featureType); err == nil && ok {
			fr, found := c.FeatureRecipeByURI(uri)
			if !found {
				return nil, false
			}
			return &fr, true
		}
		// Mapping-only resolution: an unmapped feature type is a miss here;
		// implements never resolves. Unmapped feature instances halt in the
		// pre-render resolution-completeness pass with pin candidates.
		return nil, false
	}
}

// The shape a Recipe / FeatureRecipe reference accepts has ONE home: these two
// declarations, sitting beside the resolution ladder that enforces them. The
// ladder below reads the ceiling, RecipeByURI's godoc points here instead of
// restating it, and the shape diagnostic quotes the shape statement verbatim.
// Keeping it single-sourced is load-bearing rather than tidy: a ladder that
// grows a reference position beyond the capability one widens the accepted
// shape, and a count or a shape list restated anywhere else would go quietly
// false, because no gate reads prose. Whoever adds the position edits these
// two lines and every reader follows.
const (
	recipeReferenceMaxSegments = 2

	acceptedRecipeReferenceShapes = `"name", "name:version", "capability/name" or "capability/name:version"`
)

// recipeReferenceSegments counts the slash-separated segments in a Recipe /
// FeatureRecipe reference and reports whether that count exceeds what the
// resolution ladder accepts. It reads the reference string only — no catalog
// lookup — which is what lets a mis-shaped reference be reported as a shape
// fault rather than as an entity the catalog does not hold.
func recipeReferenceSegments(uri string) (int, bool) {
	n := strings.Count(uri, "/") + 1
	return n, n > recipeReferenceMaxSegments
}

// RecipeByURI resolves a Manifest mapping value (a Recipe URI) to its catalog
// Recipe. The catalog is keyed by capability-scoped identity
// ("capability/name:version"), and the URI may name the recipe at any of
// three precision levels, tried in order:
//
//  1. Capability-qualified exact ("terraform/mariadb-rds:1.0.0") — a direct
//     map get on the identity key.
//  2. Capability-qualified ("terraform/mariadb-rds:^1.0.0",
//     "terraform/mariadb-rds") — the segment before the first "/" is matched
//     against each Recipe's Spec.Capability, and the version is selected among
//     that capability's recipes of that name.
//  3. Bare ("mariadb-rds:1.0.0" or "mariadb-rds") — a scan across every
//     capability, with the version selected within each one.
//
// A Recipe URI therefore carries at most recipeReferenceMaxSegments segments,
// whose declaration above is the single home for the accepted shape: strings.Cut
// splits on the FIRST "/" only, so a segment past the ceiling stays inside the
// name candidate and matches no Metadata.Name.
//
// Version selection is the shared three-form rule every by-name catalog lookup
// applies: an exact pin resolves that version, a caret pin the highest in range,
// an unversioned reference the highest available. A pin the catalog cannot satisfy
// is a miss here — it is never quietly widened to a version the author did not
// ask for — and the build's resolution-completeness pass turns that miss into a
// loud halt naming the mapping value, exactly as it does for a component type
// pinned at an absent version.
//
// Ambiguity across CAPABILITIES is a miss, never a silent pick: a bare reference
// two capabilities can both answer carries nothing that selects either, so it
// resolves to neither and surfaces as a loud resolution halt upstream. Qualify
// the reference with its capability to select one. The "/" split cannot misread a
// bare reference: recipe names contain no "/" per the catalog naming convention,
// and a failed capability interpretation falls through to bare resolution anyway.
func (c *Catalog) RecipeByURI(uri string) (types.Recipe, bool) {
	// The unsatisfiable-version sentinel is dropped rather than surfaced: this
	// accessor's (entity, found) shape is the one every consumer of the recipe
	// lookup shares, and an unsatisfied pin is a miss to all of them. The loud
	// halt is the resolution-completeness pass's, which names the mapping value.
	key, ok, _ := selectCapabilityScopedVersion(c.Recipes, uri, func(r types.Recipe) (string, string, string) {
		return r.Spec.Capability, r.Metadata.Name, r.Metadata.Version
	})
	if !ok {
		return types.Recipe{}, false
	}
	return c.Recipes[key], true
}

// FeatureRecipeByURI is the FeatureRecipe analogue of RecipeByURI; the
// resolution ladder, the version rule and the capability-ambiguity refusal are
// identical, over the FeatureRecipe index.
func (c *Catalog) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	key, ok, _ := selectCapabilityScopedVersion(c.FeatureRecipes, uri, func(fr types.FeatureRecipe) (string, string, string) {
		return fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version
	})
	if !ok {
		return types.FeatureRecipe{}, false
	}
	return c.FeatureRecipes[key], true
}

// PluginByURI returns the Plugin entity whose identity matches uri, plus
// a found flag. A Plugin is keyed by its coordinate — the `<pluginType>/<name>`
// pair — not by a provider-qualified name, so resolution goes through the
// shared three-form version select (exact / caret / unversioned ->
// highest-satisfying) rather than the provider-scan ladder RecipeByURI uses. The
// returned entity's .wasm artifact reference is that same coordinate; resolving
// that reference to a pinned artifact is a separate concern (the closure's
// artifact resolution), not this accessor's job — this serves the entity struct
// only.
//
// Resolution is local-first with an embedded fallback. An explicit entity-URI
// reference resolves against the loaded catalog first, so an exact-version
// reference to a present local plugin (a pipeline step's `mutator/x:1.0.0`, a
// linter's) is honored verbatim — never shadowed by the binary's embedded core,
// whose build version differs. Only when the local catalog cannot resolve the
// reference does a production workspace view fall back to the embedded core
// entities, so a no-local-catalog build can still resolve a core plugin entity by
// URI. (Embedded-wins-over-local is a CAPABILITY-resolution behavior — see
// pluginForCapability — not an entity-URI one: a bare provider name has no
// author-declared version to honor, an explicit URI does.)
//
// Served by both catalog backings: the lazy backing exposes its enumerated
// Plugin set through the recipeCatalog() *Catalog shell, so its
// PluginByURI delegates here against the same matcher (no per-backing
// reimplementation).
func (c *Catalog) PluginByURI(uri string) (types.Plugin, bool) {
	if key, ok, _ := c.resolvePluginKey(uri); ok {
		return c.Plugins[key], true
	}
	if !c.resolveEmbeddedProviders {
		return types.Plugin{}, false
	}
	embeds, _ := embeddedPluginEntities()
	if len(embeds) == 0 {
		return types.Plugin{}, false
	}
	embedMap := make(map[string]types.Plugin, len(embeds))
	for _, p := range embeds {
		embedMap[entityIndexKey(p.Coordinate(), p.Metadata.Version)] = p
	}
	key, ok, _ := selectEntityVersion(embedMap, uri, func(v types.Plugin) (string, string) {
		return v.Coordinate(), v.Metadata.Version
	})
	if !ok {
		return types.Plugin{}, false
	}
	return embedMap[key], true
}

// ProviderPluginForTarget resolves a render-engine capability to the
// provider-rendering Plugin entity that serves it, plus a found flag and an
// error, honoring an explicit plugins.providers selection. The ref is the bare
// capability name ("helm") or a version-pinned form ("helm:1.0.0",
// "helm:^1.0.0") a Recipe binds through spec.capability; the bare name (before
// any pin) is matched against each plugin's served capabilities. A pluginType:
// provider Plugin serves the capabilities listed in its spec.provider block; a
// pipeline-step plugin whose own block happens to list the name is excluded —
// each plugin type keeps its capabilities in its own block precisely because the
// list means a different thing per type.
//
// The bindings map is the workspace-config (at pull) or lockfile-derived (at
// build) provider selections, keyed by bare capability name; each value is a
// plugin's bare identity (a Plugin's metadata.name) with an optional trailing
// ":version". When an entry is present for the capability, the candidate set is
// filtered to plugins whose identity matches that value — so the binding SELECTS
// the plugin repository among competing publishers. A nil
// or empty bindings map disables that filter (every provider plugin serving the
// capability is a candidate). Version coexistence is orthogonal to the binding: the
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
	capability, verSeg, hasVer := SplitVersionedURI(ref)
	return c.pluginForCapability("provider", capability, verSeg, hasVer, bindings, providerCapabilityDiag)
}

// capabilityDiag carries a capability-resolving socket's own diagnostic
// vocabulary, so the shared select emits each socket's phrasing rather than a
// single hardcoded one. noun is woven into the binding-unsatisfied and
// version-no-serving-plugin halts; bindingUnsatisfied and versionUnsatisfiable
// are the sentinels those two halts (and the unsatisfiable-version-select halt)
// wrap; ambiguous builds the more-than-one-serving-plugin halt, whose phrasing
// is socket-specific — the provider names its plugins.providers binding surface,
// a format-routed socket names the format — so it is a closure rather than a
// noun-substituted template.
type capabilityDiag struct {
	noun                 string
	bindingUnsatisfied   error
	versionUnsatisfiable error
	ambiguous            func(capability string, candidates []string) error
}

// providerCapabilityDiag is the provider socket's diagnostic set. Its phrasing
// reproduces the provider wording verbatim, so provider resolution's halts stay
// byte-identical now that the shared select is generalized across sockets.
var providerCapabilityDiag = capabilityDiag{
	noun:                 "provider-rendering plugin",
	bindingUnsatisfied:   ErrProviderBindingUnsatisfied(),
	versionUnsatisfiable: ErrProviderVersionUnsatisfiable(),
	ambiguous: func(capability string, candidates []string) error {
		return fmt.Errorf("%w: capability %q is served by more than one provider plugin (%s); add a plugins.providers entry for %q selecting one", ErrAmbiguousProviderPlugin(), capability, strings.Join(candidates, ", "), capability)
	},
}

// pluginForCapability is the single generalized capability-resolution core the
// per-socket resolvers share. Given the plugin type a socket expects and a
// capability reference (the bare capability name, optionally version-pinned),
// it filters the catalog to plugins of that type serving the capability,
// disambiguates the survivors by an optional binding (bare plugin identity),
// halts loudly on an unresolved ambiguity or an unsatisfiable version pin, and
// otherwise selects the version via the shared three-form version select.
//
// It is the one home for the filter → disambiguate → ambiguity-halt →
// version-pin sequence: ProviderPluginForTarget is a thin pluginType="provider"
// specialization delegating here, so the provider socket's behavior is defined
// by this core rather than a parallel copy. A plugin type whose plugins declare
// no served capabilities matches nothing here — those sockets attach by URI
// (direct attach) instead, so routing them through capability resolution is a
// clean structural miss, not an error.
//
// The candidate set is the render-time plugin set (providerResolutionPlugins):
// the loaded catalog plugins overlaid with the binary's embedded core-provider
// entities, embedded-wins on a shared coordinate. An explicit binding still
// selects among the survivors, so a bound custom provider overrides the embedded
// default while a local core entity of the same coordinate is shadowed by it.
//
// The capability is the already-separated capability string (the provider passes
// the pre-split bare name; a format-routed socket passes its full "prefix:value"
// capability, which may itself carry a colon), and refPin/refHasPin is the
// caller's optional version constraint on the resolved plugin. The diagnostics
// are threaded via diag, so each socket surfaces its own sentinels and phrasing
// rather than a hardcoded provider one — the provider socket passes
// providerCapabilityDiag to keep its halts byte-identical.
func (c *Catalog) pluginForCapability(pluginType, capability, refPin string, refHasPin bool, bindings map[string]string, diag capabilityDiag) (types.Plugin, bool, error) {
	var bindBase, bindVer string
	var bindHasVer, bound bool
	if bv, ok := bindings[capability]; ok && bv != "" {
		bound = true
		bindBase, bindVer, bindHasVer = ociref.SplitVersionTag(bv)
	}

	matches := map[string]types.Plugin{}
	distinctNames := map[string]struct{}{}
	for key, p := range c.providerResolutionPlugins() {
		if p.Metadata.PluginType != pluginType {
			continue
		}
		if !slices.Contains(p.DeclaredCapabilities(), capability) {
			continue
		}
		// A binding selects the plugin repository by its coordinate identity
		// (<pluginType>/<name>) — the same coordinate the lockfile pins — filtering
		// out every competing publisher's plugin for this target.
		if bound && p.Coordinate() != bindBase {
			continue
		}
		matches[key] = p
		distinctNames[p.Metadata.Name] = struct{}{}
	}

	// The version pin priority: the caller's pin, else the binding's optional
	// version. Threaded into the diagnostic and the version select alike.
	pin := ""
	if refHasPin {
		pin = refPin
	} else if bound && bindHasVer {
		pin = bindVer
	}

	switch len(distinctNames) {
	case 0:
		if bound {
			return types.Plugin{}, false, fmt.Errorf("%w: capability %q is bound to plugin %q, but no %s serving %q matches that identity", diag.bindingUnsatisfied, capability, bindBase, diag.noun, capability)
		}
		if refHasPin {
			return types.Plugin{}, false, fmt.Errorf("%w: capability %q pins %q but no %s serves it", diag.versionUnsatisfiable, capability, refPin, diag.noun)
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
		return types.Plugin{}, false, diag.ambiguous(capability, names)
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
		// translate it to the socket's version halt naming the pin.
		return types.Plugin{}, false, fmt.Errorf("%w: capability %q pins %q but no serving plugin version satisfies it", diag.versionUnsatisfiable, capability, pin)
	}
	if !ok {
		return types.Plugin{}, false, nil
	}
	return matches[key], true, nil
}

// ProviderPluginCandidates returns every provider-rendering Plugin serving the
// bare capability, sorted by metadata name then version, for a diagnostic or
// an interactive disambiguation prompt. It applies no binding filter and no
// version select — it is the raw candidate set the ambiguity halt reports over.
// The sort makes the returned order byte-identical across runs.
func (c *Catalog) ProviderPluginCandidates(target string) []types.Plugin {
	var out []types.Plugin
	for _, p := range c.providerResolutionPlugins() {
		if p.Metadata.PluginType != "provider" {
			continue
		}
		if !slices.Contains(p.DeclaredCapabilities(), target) {
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

// ProviderPluginByName resolves a capability to its provider-rendering Plugin
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
	cBase, cVer, cHasVer := SplitVersionedURI(componentType)
	if !cHasVer {
		return false
	}
	iBase, iVer, iHasVer := SplitVersionedURI(impl)
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
	if _, _, keyHasVer := SplitVersionedURI(key); !keyHasVer {
		return stripVersion(impl) == key
	}
	return false
}

// ComponentAncestryResolver reports the extends ancestry of a component type:
// the Manifest-key form of every base type it descends from, nearest parent
// first. It is the seam through which a Component index reaches the
// mapping-consistency gate, which holds recipes but not necessarily Components.
//
// A type the resolver cannot find has no ancestry and no error — "the mapped
// type is absent from the catalog" belongs to the resolution path's diagnostics,
// not to a consistency judgement. A malformed chain returns the chain's own
// fault so a caller reports the chain rather than a mapping mismatch.
type ComponentAncestryResolver func(componentType string) ([]string, error)

// componentAncestryOver is the single home for the ancestry both catalog
// backings expose: the eagerly-loaded Component index and the lazy
// Tier-1/2/3 cascade differ only in the lookup they hand it. Each ancestor is
// rendered as the mapping key an author would write for that base type so the
// satisfaction matcher receives ancestry in exactly the form it receives the
// mapped key itself.
func componentAncestryOver(componentType string, lookup validator.ComponentLookup) ([]string, error) {
	comp, ok := lookup(componentType)
	if !ok {
		return nil, nil
	}
	chain, vErrs := validator.ComponentAncestry(comp, lookup)
	if len(vErrs) > 0 {
		return nil, vErrs[0]
	}
	keys := make([]string, 0, len(chain))
	for _, anc := range chain {
		// versionedSlotURI is the existing single home for the
		// bare-name-plus-version join, including its double-append guard.
		keys = append(keys, versionedSlotURI(anc.Metadata.Name, anc.Metadata.Version))
	}
	return keys, nil
}

// ComponentAncestry resolves componentType's extends ancestry over this
// catalog's OWN Component index. It is the ComponentAncestryResolver an
// eagerly-loaded catalog passes to its own consistency gate.
//
// A catalog shell assembled over a different backing — one whose Components
// index is empty by construction — must pass that backing's resolver instead.
// Passing this method from such a shell resolves no ancestry at all and the gate
// then refuses every mapping that relies on it, which is why the resolver is an
// argument of the gate rather than a field read off the receiver.
func (c *Catalog) ComponentAncestry(componentType string) ([]string, error) {
	return componentAncestryOver(componentType, func(uri string) (types.Component, bool) {
		key, ok, _ := c.resolveComponentKey(uri)
		if !ok {
			return types.Component{}, false
		}
		return c.Components[key], true
	})
}

// manifestMappingSatisfiedByAncestry reports whether the mapped recipe's
// spec.implements satisfies any base the mapped type extends. Ancestry enters as
// additional INPUTS to manifestMappingConsistent — each base is offered exactly
// as the mapped key itself is — never as a second matching rule, so one
// satisfaction rule keeps answering for every caller while a Recipe written
// against a base serves everything that extends it.
func manifestMappingSatisfiedByAncestry(impl string, ancestry []string) bool {
	for _, base := range ancestry {
		if manifestMappingConsistent(impl, base) {
			return true
		}
	}
	return false
}

// recipesImplementing returns the sorted, provider-qualified identity keys of
// every catalog recipe whose spec.implements satisfies componentType. It is a
// SUGGESTION provider only — resolution is mapping-only and never consults it.
// Its readers are the no-mapping diagnostic (pin candidates) and the Manifest
// recipe-reference completion (implements-aware ranking).
//
// Deliberately ancestry-blind, unlike the mapping-consistency check: extends
// ancestry is handled in that check's outer layer rather than inside
// implementsSatisfies, so it reaches only the caller that resolves it. Widening
// the suggestion set to recipes implementing a base of the requested type would
// change which candidates a diagnostic lists and how completion ranks them —
// user-visible output whose shape is a separate choice from whether an
// operator-written mapping is judged consistent.
func recipesImplementing(recipes map[string]types.Recipe, componentType string) []string {
	var ids []string
	for _, r := range recipes {
		if implementsSatisfies(r.Spec.Implements, componentType) {
			ids = append(ids, recipeIdentityKey(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version))
		}
	}
	sort.Strings(ids)
	return ids
}

// featureRecipesImplementing is the FeatureRecipe analogue of
// recipesImplementing, ancestry-blind for the same reason: it provides
// suggestions, and extends ancestry reaches only the mapping-consistency check
// that resolves it. Feature extends carries its own composition semantics, which
// this surface takes no position on.
func featureRecipesImplementing(frs map[string]types.FeatureRecipe, featureType string) []string {
	var ids []string
	for _, fr := range frs {
		if implementsSatisfies(fr.Spec.Implements, featureType) {
			ids = append(ids, recipeIdentityKey(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version))
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
			// A reference the shape cannot express is reported as a shape fault:
			// the recipe it names may well be in the catalog, so calling it
			// absent sends the author looking for the wrong thing. The check
			// runs only after the lookup misses, so it never changes which
			// references resolve.
			//
			// DELIBERATE second reading, not a leftover: ManifestRecipeReferenceFaults
			// judges the same shape earlier, before any closure walk, and halts on the
			// first fault. This pass is the RESILIENT surface — the one an editor maps
			// to diagnostics — and it enumerates every fault rather than stopping at
			// one, so removing it would cost an author authoring several mis-shaped
			// mappings all but the first squiggle. Do not "unify" the two: the grammar
			// itself is single-sourced in the constant pair above, which is what keeps
			// them from disagreeing.
			if n, tooMany := recipeReferenceSegments(uri); tooMany {
				return fmt.Errorf("%w: component %q (type %q): the Manifest maps it to %q, which carries %d slash-separated segments; a recipe reference accepts %s; correct the mapping value in the Manifest", ErrMalformedRecipeReference(), instanceName, componentType, uri, n, acceptedRecipeReferenceShapes)
			}
			return fmt.Errorf("%w: component %q (type %q): the Manifest maps it to recipe %q, which is not in the catalog", ErrNoRecipeResolved(), instanceName, componentType, uri)
		}
		return nil
	}
	if candidates := recipesImplementing(c.Recipes, componentType); len(candidates) > 0 {
		return fmt.Errorf("%w: component %q (type %q); recipes implementing it: %v; pin one in the Manifest", ErrNoRecipeResolved(), instanceName, componentType, candidates)
	}
	return fmt.Errorf("%w: component %q (type %q); no recipe implements this type", ErrNoRecipeResolved(), instanceName, componentType)
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
			// Deliberate second reading for the same reason the component branch
			// records: this pass enumerates every fault where the load-time check
			// halts on the first, and the grammar is single-sourced either way.
			if n, tooMany := recipeReferenceSegments(uri); tooMany {
				return fmt.Errorf("%w: feature %q (type %q): the Manifest maps it to %q, which carries %d slash-separated segments; a feature recipe reference accepts %s; correct the mapping value in the Manifest", ErrMalformedRecipeReference(), instanceName, featureType, uri, n, acceptedRecipeReferenceShapes)
			}
			return fmt.Errorf("%w: feature %q (type %q): the Manifest maps it to feature recipe %q, which is not in the catalog", ErrNoRecipeResolved(), instanceName, featureType, uri)
		}
		return nil
	}
	if candidates := featureRecipesImplementing(c.FeatureRecipes, featureType); len(candidates) > 0 {
		return fmt.Errorf("%w: feature %q (type %q); feature recipes implementing it: %v; pin one in the Manifest", ErrNoRecipeResolved(), instanceName, featureType, candidates)
	}
	return fmt.Errorf("%w: feature %q (type %q); no feature recipe implements this type", ErrNoRecipeResolved(), instanceName, featureType)
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
// A component mapping is also consistent when the recipe implements a base the
// mapped type extends, at any depth of the chain: a Recipe written against a base
// serves everything extending that base, so a specialization needs a mapping
// entry but no Recipe of its own. The ancestry arrives through the
// ComponentAncestryResolver argument, which each backing supplies over the index
// its own Components live in — this catalog's own index via
// (*Catalog).ComponentAncestry, or the entity cascade a lazily-enumerated view
// reads. A nil resolver declares that the caller's catalog resolves no ancestry,
// which is honest for a surface holding no Components and wrong for one that
// does. Ancestry is resolved only after the direct comparison fails, so a
// mapping that matches outright costs no component lookup, and a chain that
// cannot be walked is reported as the chain fault it is rather than as a mapping
// mismatch.
//
// Feature mappings are judged on the direct comparison alone. Feature extends
// composes its pillars rather than inheriting them, so whether a FeatureRecipe
// written against a base serves an extending Feature is a separate question this
// surface does not answer.
//
// A mapping whose recipe URI does not resolve in the catalog is skipped here,
// not reported: a missing/unresolvable recipe is a resolution concern owned by
// the recipe-resolution path (VerifyRecipeResolution and the catalog lookup),
// which surfaces it with richer context — including, for an entity skipped at
// tolerant load, a replay of WHY it went missing. Reporting "not found" here
// too would preempt that better diagnostic. This function is therefore the
// found-but-mismatched check only.
//
// Returns nil when manifest is nil, when no mappings are populated, or when
// every resolved mapping is consistent.
func (c *Catalog) VerifyManifestImplementsConsistency(manifest *types.Manifest, manifestPath string, ancestry ComponentAncestryResolver) []error {
	if manifest == nil {
		return nil
	}
	if ancestry == nil {
		ancestry = func(string) ([]string, error) { return nil, nil }
	}
	var errs []error
	// Component mappings.
	if manifest.Spec.Mappings.Components != nil {
		for compType, recipeURI := range manifest.Spec.Mappings.Components.Type.All() {
			r, ok := c.RecipeByURI(recipeURI)
			if !ok {
				continue // missing recipe → resolution path's concern, not a consistency violation
			}
			if manifestMappingConsistent(r.Spec.Implements, compType) {
				continue
			}
			bases, aerr := ancestry(compType)
			if aerr != nil {
				errs = append(errs, cerrors.Wrap(aerr, manifestCategory(), cerrors.SourceLocation{File: manifestPath}, fmt.Sprintf("manifest mapping %q -> %q: the mapped component type's extends chain cannot be walked, so the mapping cannot be judged", compType, recipeURI)))
				continue
			}
			if !manifestMappingSatisfiedByAncestry(r.Spec.Implements, bases) {
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch(), manifestCategory(), cerrors.SourceLocation{File: manifestPath},
					cerrors.ComposeMessage(ErrManifestImplementsMismatch(), []string{
						fmt.Sprintf("manifest mapping %q -> %q", compType, recipeURI),
						fmt.Sprintf("recipe spec.implements is %q", r.Spec.Implements),
						fmt.Sprintf("expected %q, or a type %q extends", compType, compType),
					}, "")))
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
				errs = append(errs, cerrors.Wrap(ErrManifestImplementsMismatch(), manifestCategory(), cerrors.SourceLocation{File: manifestPath},
					cerrors.ComposeMessage(ErrManifestImplementsMismatch(), []string{
						fmt.Sprintf("manifest feature mapping %q -> %q", featType, frURI),
						fmt.Sprintf("featurerecipe spec.implements is %q", fr.Spec.Implements),
						fmt.Sprintf("expected %q", featType),
					}, "")))
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
//
// ancestry carries the same contract it carries on
// VerifyManifestImplementsConsistency: it is the caller's Component index, and a
// caller whose catalog holds Components passes one so a mapping onto a base's
// Recipe is judged consistent.
func (c *Catalog) AssertManifestImplementsConsistency(manifest *types.Manifest, manifestPath string, ancestry ComponentAncestryResolver) error {
	errs := c.VerifyManifestImplementsConsistency(manifest, manifestPath, ancestry)
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// componentMappingURI returns the Recipe URI the Manifest maps the given
// component instance to, when the components mapping is populated. Resolution
// is name-wins-over-type: a `name:` override on instanceName takes precedence
// over the (version-tolerant) `type:` match on componentType.
func componentMappingURI(manifest *types.Manifest, instanceName, componentType string) (string, bool, error) {
	if manifest == nil || manifest.Spec.Mappings.Components == nil {
		return "", false, nil
	}
	return nameThenType(manifest.Spec.Mappings.Components, instanceName, componentType)
}

// featureMappingURI is the Feature analogue of componentMappingURI:
// name-wins-over-type resolution of a feature instance against the features
// mapping.
func featureMappingURI(manifest *types.Manifest, instanceName, featureType string) (string, bool, error) {
	if manifest == nil || manifest.Spec.Mappings.Features == nil {
		return "", false, nil
	}
	return nameThenType(manifest.Spec.Mappings.Features, instanceName, featureType)
}

// ComponentRecipeURI returns the Recipe URI the Manifest maps componentType
// to. The returned URI is the same verbatim string the dependency-closure
// walker records as a source-descent entity, so callers keying per-component
// data by this value join cleanly against the closure's entities (regardless of
// whether the author wrote the mapping value in short form or full-path form).
//
// Returns ("", false) when no Manifest mapping names componentType. Such a
// component carries no source-descent entity — it would be selected for render
// by spec.implements, which is a render-selection path, not a
// source-acquisition path — so it correctly contributes no source artifact.
func ComponentRecipeURI(manifest *types.Manifest, instanceName, componentType string) (string, bool, error) {
	return componentMappingURI(manifest, instanceName, componentType)
}

// FeatureRecipeURI is the FeatureRecipe analogue of ComponentRecipeURI: it
// returns the FeatureRecipe URI the Manifest maps featureType to. The returned
// URI is the same verbatim string the dependency-closure walker records as a
// feature_recipe source-descent entity, so callers keying per-feature-instance
// data by this value join cleanly against the closure's entities.
//
// Returns ("", false) when no Manifest mapping names featureType.
func FeatureRecipeURI(manifest *types.Manifest, instanceName, featureType string) (string, bool, error) {
	return featureMappingURI(manifest, instanceName, featureType)
}

// stripVersion removes the :version segment from a URI, returning the
// base. Used by the consistency check to compare base-URI portions
// independent of version form.
func stripVersion(uri string) string {
	base, _, _ := SplitVersionedURI(uri)
	return base
}
