// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ProviderPluginResolver resolves a Recipe / FeatureRecipe spec.capability binding
// — a bare engine name or a version-pinned form ("helm", "helm:1.0.0",
// "helm:^1.0.0") — to the provider-rendering Plugin entity serving it, plus a
// found flag and an error. It backs the lock-time provider pin: the descent
// reads the resolved Plugin's identity to pin the provider-rendering Plugin
// entity and its .wasm artifact into the closure, so a build resolves the same
// provider-plugin version the pin recorded.
//
// A bare binding no provider-rendering plugin serves returns (_, false, nil), so
// the descent leaves the provider to build-time engine resolution rather than
// halting. A version-pinned binding no serving plugin version satisfies returns
// a non-nil error, so the descent halts the pull loudly on a pin the catalog
// cannot honor. A nil resolver disables provider-plugin descent.
type ProviderPluginResolver func(providerRef string) (types.Plugin, bool, error)

// providerPluginEntityURI builds the catalog URI identifying a resolved
// provider-rendering Plugin entity — its identity coordinate (<pluginType>/<name>)
// pinned to the resolved version. The lock-time provider pin keys on this URI so a
// build resolves the same provider-plugin version the pin recorded. A version-less
// resolution (empty metadata.version) yields the bare coordinate, which resolves
// through the three-form unversioned rule.
func providerPluginEntityURI(p types.Plugin) string {
	if p.Metadata.Version == "" {
		return p.Coordinate()
	}
	return p.Coordinate() + ":" + p.Metadata.Version
}

// pluginKindHint marks a closure entry emitted for a pipeline plugin artifact
// (the .wasm reference derived from a Plugin entity's bare identity). The plugin
// descent sets it so the pinned plugin artifact carries the same provenance hint
// the workspace-config plugin axis records, distinguishing a pipeline-plugin pin
// from a catalog-entity pin during closure assembly. Like the other
// ResolvedRef.KindHint values it is an in-memory provenance hint, not a lockfile
// field; the lockfile writer copies only the wire-shape fields.
const pluginKindHint = "plugin"

// resolvedInLocalCatalog reports whether a resolution came from a local catalog
// directory (or a workspace-relative path) rather than from a registry pull. It is
// the universal leg of the registry-only rule: no plugin type is pinned when it
// resolves this way, because the build resolves it again through the same local
// catalog walk, and a pin would strand a lockfile entry the hermetic build cannot
// reconstruct. Both pin-decision helpers below compose it, so the rule has one home.
func resolvedInLocalCatalog(resolutionPath string) bool {
	return resolutionPath == ResolutionPathCatalog
}

// entityNeedsNoPin reports whether a resolved plugin ENTITY is left out of the
// closure by a pull-side descent: it resolved from a local catalog, or it resolved
// from the copy that ships inside the compiler binary.
//
// The from-the-binary leg inspects no plugin type because it cannot need to: on the
// catalog-entity axis a resolver reports the from-the-binary path for a core
// PROVIDER entity only, and every other plugin type keeps its ordinary cascade. That
// scoping lives in the resolver, beside the entity whose declared plugin type
// answers the question, rather than being re-derived here from a URI — a plugin's
// identity coordinate is built from its (pluginType, name) pair and is never
// re-parsed to recover either half.
func entityNeedsNoPin(resolutionPath string) bool {
	return resolvedInLocalCatalog(resolutionPath) || resolutionPath == ResolutionPathEmbedded
}

// artifactNeedsNoPin reports whether a resolved plugin .wasm ARTIFACT is left out of
// the closure by a pull-side descent, given the plugin type its entity declares.
//
// Unlike the entity axis, the artifact axis resolves EVERY core plugin the binary
// carries from the binary — a governance guest's .wasm as readily as a provider's —
// so the plugin type is what separates the two outcomes here, and the entity being
// loaded at this point is what makes its declared type available to ask.
//
// A core provider resolved from the binary is not pinned: the build selects and
// loads its rendering engine from the same binary, so a pin would record a digest
// the pull never fetched and tie the closure to whichever binary wrote it.
//
// Every other declared type IS pinned, even when the binary carries it. The plugins
// this axis reaches — a recipe's pipeline steps and an output-shaping workspace-socket
// attachment — put their bytes into the rendered output, and the lockfile pin is what
// holds those bytes still until the next pull. Two things break without it: such a
// plugin is adopted the moment it changes rather than at a pull, and the
// workspace-socket reconstruction the build performs from the lockfile pins the
// artifact regardless, so a pull that declined it writes a lockfile the very next
// build reports as drifted.
func artifactNeedsNoPin(resolutionPath string, declaredType plugintype.Type) bool {
	if resolvedInLocalCatalog(resolutionPath) {
		return true
	}
	return resolutionPath == ResolutionPathEmbedded && declaredType == plugintype.Provider
}

// DescendRecipePluginEntities is the entity descent of the two-pass
// pipeline-plugin descent:
// it discovers every Plugin ENTITY a closure's Recipes / FeatureRecipes
// reference through their spec.pipeline[].plugin URIs (plus any workspace-socket
// plugin entity ComputeClosure already pinned on the entity axis) and pins each
// registry-resolved entity onto the catalog-entity axis (c.Entities) through the
// same ResolveCatalogEntity path the provider axis uses. It does NOT load any
// entity body and does NOT resolve any .wasm artifact — that is the artifact
// descent (DescendRecipePluginArtifacts).
//
// It also discovers the provider-rendering Plugin ENTITY each Recipe /
// FeatureRecipe binds through spec.capability. When a providerResolver is supplied,
// each non-empty spec.capability binding resolves to its serving Plugin identity,
// and that Plugin entity is pinned on the same catalog-entity axis and descended
// to its .wasm artifact in the artifact descent exactly like a pipeline plugin — so the
// provider a build renders through is pinned to a resolved version and content
// digest at lock time. A provider Plugin that is also referenced as a pipeline
// plugin dedupes by URI (its referenced-from sites merge; it is not recorded
// twice). A binding no plugin serves is left to build-time engine resolution; a
// version pin no serving plugin version satisfies halts the pull. A nil
// providerResolver disables provider-plugin descent.
//
// The split exists because a freshly-pulled plugin entity is pinned here but is
// not yet visible to the entity-front the artifact-load phase reads: the caller
// must re-fold the lazy index from the now-augmented closure between the two
// phases so the artifact descent's LoadPlugin resolves the just-pinned entity. A
// single-pass descent loads the body against a pin set that predates the
// entity's own pin and misses it.
//
// Both phases are pull-side augmentations, run after ComputeClosure (and the
// sub-aggregate / library discovery passes) at lock time — never
// inside ComputeClosure, which also runs on the hermetic build and serve paths
// where no registry pull may occur. The shape mirrors DescendSubAggregates and
// DescendRecipeSources: a separate pass over the resolved closure driven by an
// injected loader, producing the same Entities entry representation the other
// axes emit rather than a parallel pin.
//
// Registry-only pinning: an entity is emitted into the closure ONLY when it is a
// pulled artifact (the registry-backed cache / pull tiers). An entity authored in a
// local catalog directory or addressed by a workspace-relative path is left out and
// resolves at build through the local catalog walk. So is a core PROVIDER entity the
// binary carries, which resolves at build from the binary — the only plugin type an
// entity resolver reports that way, because the build likewise selects its rendering
// engine from the binary. Every other socket's plugin entity keeps its ordinary
// cascade and is pinned when a registry serves it. A reference that resolves through
// no tier (NotFound) is skipped rather than halting the pull: a local-path entity the
// lock-time lookup does not see resolves at build, and the build-time loader gate
// halts on a genuinely-absent reference.
//
// Newly-discovered entities are appended to c.Entities (deduped by URI against
// what is already present, accumulating referenced-from sites). A nil loader is
// a no-op (the caller wired no recipe-body access).
func DescendRecipePluginEntities(ctx context.Context, c *Closure, loader RecipeLoader, lookup Lookup, providerResolver ProviderPluginResolver) error {
	if c == nil || loader == nil {
		return nil
	}

	entityIndex := make(map[string]int, len(c.Entities))
	for i, e := range c.Entities {
		entityIndex[e.URI] = i
	}

	recipeRefs, entityURIs, err := discoverPluginEntities(c, loader, providerResolver)
	if err != nil {
		return err
	}

	for _, entityURI := range entityURIs {
		// Resolve and pin the Plugin ENTITY onto the catalog-entity axis —
		// exactly as the provider axis pins a Plugin. A recipe-referenced entity
		// is pinned here with the referencing recipe URIs; a workspace-socket entity
		// was already pinned by ComputeClosure and keeps its socket referenced-from.
		recipeFromSet := recipeRefs[entityURI]
		entRes, err := lookup.ResolveCatalogEntity(ctx, entityURI, pluginKindHint)
		if err != nil {
			return fmt.Errorf("resolve: resolve wasm plugin entity %q: %w", entityURI, err)
		}
		// A plugin entity referenced by a recipe but resolvable through no tier is not
		// a closure gap here: it may resolve at build through a local catalog the
		// lock-time lookup does not see, and the build-time loader gate halts on a
		// genuinely-absent entity. Skip an unresolved entity rather than halt the pull.
		// (A workspace-socket entity already passed ComputeClosure's fatal resolve.)
		if entRes.NotFound {
			continue
		}
		// Registry-only pinning: an entity already present on the machine is not
		// pinned — it resolves at build from the local catalog, or (a core provider
		// only, which is what the entity resolver reports this way) from the binary.
		if entityNeedsNoPin(entRes.ResolutionPath) {
			continue
		}
		if len(recipeFromSet) > 0 {
			refs := make([]string, 0, len(recipeFromSet))
			for r := range recipeFromSet {
				refs = append(refs, r)
			}
			entRef := ResolvedRef{
				URI:             entityURI,
				ResolvedVersion: entRes.ResolvedVersion,
				Digest:          entRes.Digest,
				ManifestDigest:  entRes.ManifestDigest,
				ResolutionPath:  entRes.ResolutionPath,
				ReferencedFrom:  refs,
				KindHint:        pluginKindHint,
			}
			if idx, dup := entityIndex[entityURI]; dup {
				c.Entities[idx].ReferencedFrom = mergeReferencedFrom(c.Entities[idx].ReferencedFrom, entRef.ReferencedFrom)
			} else {
				c.Entities = append(c.Entities, entRef)
				entityIndex[entityURI] = len(c.Entities) - 1
			}
		}
	}

	canonicaliseClosure(c)
	return nil
}

// DescendRecipePluginArtifacts is the artifact descent of the two-pass
// pipeline-plugin descent:
// it rediscovers the Plugin entities the entity descent pinned (by walking
// c.Entities for
// KindHint==pluginKindHint), loads each entity body to derive its .wasm artifact
// reference from its identity, resolves that reference through ResolvePlugin, and pins
// the artifact onto the plugin axis (c.Plugins). It runs AFTER the caller has
// re-folded the lazy index from the entity-descent-augmented closure, so LoadPlugin
// resolves a just-pinned plugin entity that the entity descent discovered.
//
// Rediscovery is from c.Entities rather than from a re-walk of the recipes: the
// entity descent
// already pinned every registry-resolved plugin entity (recipe-referenced and
// workspace-socket) with KindHint==pluginKindHint, so the entity axis is the
// authoritative set of artifacts to descend, and an entity the entity descent
// declined to pin
// because it was already present on the machine is correctly absent (its artifact is
// not lockfile-pinned either).
//
// The artifact is referenced from its Plugin entity. NotFound-skip mirrors the
// entity descent.
// The registry-only rule does NOT: the artifact axis resolves every core plugin the
// binary carries from the binary, not just the providers the entity axis does, so
// which of those is pinned depends on the entity's declared plugin type —
// artifactNeedsNoPin holds that rule. Newly-discovered artifacts are appended to
// c.Plugins (deduped by URI, accumulating referenced-from sites); the closure
// is re-canonicalised before return, so the pinned set — and the lockfile written
// from it — is byte-identical across runs regardless of descent order. A nil loader
// is a no-op.
func DescendRecipePluginArtifacts(ctx context.Context, c *Closure, loader RecipeLoader, lookup Lookup) error {
	if c == nil || loader == nil {
		return nil
	}

	pluginIndex := make(map[string]int, len(c.Plugins))
	for i, e := range c.Plugins {
		pluginIndex[e.URI] = i
	}

	// Rediscover the plugin entities the entity descent pinned. Collect first (sorted) so the
	// artifact-descent order is deterministic and independent of c.Entities order.
	var entityURIs []string
	for _, ent := range c.Entities {
		if ent.KindHint == pluginKindHint {
			entityURIs = append(entityURIs, ent.URI)
		}
	}
	sort.Strings(entityURIs)

	for _, entityURI := range entityURIs {
		registryOnly := true
		if err := pinPluginArtifact(ctx, c, loader, lookup, entityURI, pluginIndex, registryOnly); err != nil {
			return err
		}
	}

	canonicaliseClosure(c)
	return nil
}

// pinPluginArtifact loads a Plugin entity body, derives its .wasm artifact
// reference from its identity, resolves that reference through ResolvePlugin, and
// pins the artifact onto the plugin axis (c.Plugins), deduping by URI against
// pluginIndex and accumulating referenced-from sites. The derivation runs through
// the single entity → artifact-reference helper (Plugin.ArtifactReference) so the
// lock-time reference and the load-time entity-front derive the same bare identity.
//
// A NotFound artifact is skipped (it may resolve at build through a local path the
// lock-time lookup does not see, and the build-time loader gate halts on a
// truly-missing artifact). registryOnly controls whether the registry-only rule
// applies at all: the pull-side pipeline descent passes true, so an artifact
// artifactNeedsNoPin reports is left out; the workspace-socket reconstruction passes
// false, because a socket artifact the lockfile already pins can surface as
// locally-resolved once the pinned cache slot is folded into the hermetic view's local
// enumeration, and dropping it is exactly the reconstruction gap that surfaces as
// spurious drift.
func pinPluginArtifact(ctx context.Context, c *Closure, loader RecipeLoader, lookup Lookup, entityURI string, pluginIndex map[string]int, registryOnly bool) error {
	wp, err := loader.LoadPlugin(entityURI)
	if err != nil {
		return fmt.Errorf("resolve: load wasm plugin entity %q for artifact descent: %w", entityURI, err)
	}
	artifactURI := wp.ArtifactReference()
	if artifactURI == "" {
		return nil
	}
	// The .wasm rides inline in the entity's single manifest, tagged by the
	// entity's version; pass that version so a digest-free reference resolves
	// the layer from the right manifest.
	artRes, err := lookup.ResolvePlugin(ctx, artifactURI, wp.Metadata.Version)
	if err != nil {
		return fmt.Errorf("resolve: resolve pipeline plugin artifact %q: %w", artifactURI, err)
	}
	if artRes.NotFound {
		return nil
	}
	if registryOnly && artifactNeedsNoPin(artRes.ResolutionPath, plugintype.Type(wp.Metadata.PluginType)) {
		return nil
	}
	artRef := ResolvedRef{
		URI:             artifactURI,
		ResolvedVersion: artRes.ResolvedVersion,
		Digest:          artRes.Digest,
		ManifestDigest:  artRes.ManifestDigest,
		ResolutionPath:  artRes.ResolutionPath,
		ReferencedFrom:  []string{entityURI},
		KindHint:        pluginKindHint,
	}
	if idx, dup := pluginIndex[artifactURI]; dup {
		c.Plugins[idx].ReferencedFrom = mergeReferencedFrom(c.Plugins[idx].ReferencedFrom, artRef.ReferencedFrom)
	} else {
		c.Plugins = append(c.Plugins, artRef)
		pluginIndex[artifactURI] = len(c.Plugins) - 1
	}
	return nil
}

// discoverPluginEntities walks the closure's Recipe / FeatureRecipe entities,
// reading each spec.pipeline[].plugin URI and each spec.capability binding, and
// returns the deduped set of plugin entity URIs to descend plus, per entity, the
// set of Recipe / FeatureRecipe URIs that reference it. A Plugin entity already on
// the entity axis (a workspace plugin socket ComputeClosure pinned) is added as
// its own descent target with no recipe-ref. The returned entity-URI slice is
// sorted, so the descent is order-independent and each entity is descended once.
//
// A non-empty spec.capability is resolved through providerResolver to its serving
// provider-rendering Plugin identity and recorded as a descent target, keyed by
// the resolved Plugin's version-exact URI. A provider Plugin that is also a
// pipeline plugin dedupes to a single descent target. A nil providerResolver
// skips provider discovery.
func discoverPluginEntities(c *Closure, loader RecipeLoader, providerResolver ProviderPluginResolver) (map[string]map[string]struct{}, []string, error) {
	recipeRefs := map[string]map[string]struct{}{}
	var entityURIs []string
	seen := map[string]struct{}{}
	addEntityURI := func(entityURI string) {
		if entityURI == "" {
			return
		}
		if _, ok := seen[entityURI]; !ok {
			seen[entityURI] = struct{}{}
			entityURIs = append(entityURIs, entityURI)
		}
	}
	recordRecipeRef := func(entityURI, recipeURI string) {
		addEntityURI(entityURI)
		if recipeRefs[entityURI] == nil {
			recipeRefs[entityURI] = map[string]struct{}{}
		}
		recipeRefs[entityURI][recipeURI] = struct{}{}
	}
	// recordProvider resolves a Recipe / FeatureRecipe spec.capability binding to
	// its serving provider-rendering Plugin entity and records it as a descent
	// target referenced from the recipe. A binding no plugin serves is left to
	// build-time engine resolution; a version pin no serving plugin version
	// satisfies propagates the resolver's error.
	recordProvider := func(providerRef, recipeURI string) error {
		if providerResolver == nil || providerRef == "" {
			return nil
		}
		plugin, ok, err := providerResolver(providerRef)
		if err != nil {
			return fmt.Errorf("resolve: provider %q for %q: %w", providerRef, recipeURI, err)
		}
		if !ok {
			return nil
		}
		recordRecipeRef(providerPluginEntityURI(plugin), recipeURI)
		return nil
	}

	for _, ent := range c.Entities {
		entityKind, ok := ent.KindHint, ent.KindHint != ""
		if !ok {
			entityKind, ok = classifyEntityKind(ent.URI)
		}
		if !ok {
			continue
		}
		switch entityKind {
		case "recipe":
			r, err := loader.LoadRecipe(ent.URI)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve: load recipe %q for plugin descent: %w", ent.URI, err)
			}
			for _, step := range r.Spec.Pipeline {
				recordRecipeRef(step.Plugin, ent.URI)
			}
			if err := recordProvider(r.Spec.Capability, ent.URI); err != nil {
				return nil, nil, err
			}
		case "feature_recipe":
			fr, err := loader.LoadFeatureRecipe(ent.URI)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve: load feature recipe %q for plugin descent: %w", ent.URI, err)
			}
			for _, step := range fr.Spec.Pipeline {
				recordRecipeRef(step.Plugin, ent.URI)
			}
			if err := recordProvider(fr.Spec.Capability, ent.URI); err != nil {
				return nil, nil, err
			}
		case pluginKindHint:
			// A Plugin entity pinned on the entity axis directly — a workspace
			// plugin socket. It is its own descent target; ComputeClosure already
			// recorded its socket referenced-from, so no recipe-ref is added.
			addEntityURI(ent.URI)
		}
	}

	sort.Strings(entityURIs)
	return recipeRefs, entityURIs, nil
}
