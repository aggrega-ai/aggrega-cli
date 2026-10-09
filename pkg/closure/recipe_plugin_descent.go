// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

// LocalPluginArtifact resolves the .wasm artifact of a Plugin entity authored in a
// local catalog directory from the sources already on the machine, and returns the
// refusal when none of them holds it. entityURI is the reference the descent
// resolved; artifactRef is the entity's artifact reference, derived through
// Plugin.ArtifactReference. A registry is never asked for such an artifact: this is
// the only question a descent asks about one.
//
// A pull asks it at both descents, for two different reasons. The entity descent asks
// it about every such entity, so an artifact no local source holds is refused at the
// step that can name the fix instead of leaving a build to fail at load. The artifact
// descent asks it about the one such entity that reaches it — a transform-role
// governance attachment, whose entity stays on the entity axis wherever it resolved
// from — and pins the artifact from the resolution returned, because that
// attachment's bytes shape the rendered output and are held still by a pin like every
// output-shaping plugin's. A plugin attached at a verdict role is never pinned, and a
// pull resolving one authored in such a directory asks the same question through
// ResolvePluginArtifact, so its artifact is not fetched either.
//
// The resolution carries the content digest of the bytes read, as both its digest
// and its version, which is how every pinned plugin artifact records its content; the
// source the bytes came from; and no manifest digest, because bytes read from the
// machine came through no registry manifest. It is supplied by the caller, which holds
// the sources the build's own plugin resolution reads, so this package asks the
// question without answering it a second way. A build reconstructing the same axes
// supplies the same question to the artifact descent, so the pin a pull records and
// the pin the build's drift check expects are computed alike.
//
// Handed to the entity descent, it is also what marks that descent as the one a pull
// runs before it writes: with it, the descent refuses there what the build would
// refuse at load — this question's refusal, and a Plugin entity no source holds (see
// DescendRecipePluginEntities). A nil value asks nothing: the entity descent refuses
// nothing, and the artifact descent leaves such an entity's artifact out rather than
// asking a registry for it.
type LocalPluginArtifact func(entityURI, artifactRef string) (Resolution, error)

// resolvedInLocalCatalog reports whether a resolution came from a local catalog
// directory (or a workspace-relative path) rather than from a registry pull. It is
// the universal leg of the registry-only rule on the entity axis: no plugin entity is
// pinned when it resolves this way, because the build resolves it again through the
// same local catalog walk, and a pin would strand a lockfile entry the hermetic build
// cannot reconstruct. entityNeedsNoPin composes it, and ResolvePluginArtifact reads it
// to send such an entity's artifact to the machine's own sources, so the rule has one
// home.
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
// the pull never fetched and tie the closure to whichever binary wrote it. That
// exception is a property of where the artifact resolved from, so it is written
// against the resolution path and leaves the type's own classification alone.
//
// Every other output-shaping type IS pinned, even when the binary carries it,
// and which types those are is read from the canonical plugin-type registry
// rather than restated here — see shapesRenderedOutput. The lockfile pin is what
// holds an output-shaping plugin's bytes still until the next pull: without it,
// such a plugin is adopted the moment it changes rather than at a pull.
//
// It is the one pin rule for an artifact. Every site that pins one — the artifact
// descent a pull and a build both run, and the workspace-socket reconstruction a
// build performs from the lockfile — applies it to the resolution path it read, so
// what a pull declines a build declines too, and the build's drift check holds the
// lockfile to the same artifact rows the pull wrote.
//
// An artifact read from beside its entity in a catalog directory is pinned by the
// same rule. It reaches this predicate only for the entity that stays on the entity
// axis wherever it resolved from — a transform-role attachment — and the build's
// reconstruction reads it from the same local sources, so the registry-only rule's
// reason, a pin the hermetic build cannot reconstruct, does not hold for it.
func artifactNeedsNoPin(resolutionPath string, declaredType plugintype.Type) bool {
	if resolutionPath == ResolutionPathEmbedded && declaredType == plugintype.Provider {
		return true
	}
	return !shapesRenderedOutput(declaredType)
}

// shapesRenderedOutput reports whether a plugin of this declared type puts its
// bytes into the rendered output, reading the classification from the canonical
// plugin-type registry so the pin decision and the socket-facing classification
// cannot disagree about a type.
//
// A type the registry does not carry is treated as output-shaping. The pin is the
// safe direction: an artifact pinned when it need not be costs a lockfile row and
// keeps output reproducible, while an unpinned output-shaping artifact silently
// moves the bytes a build emits.
func shapesRenderedOutput(declaredType plugintype.Type) bool {
	entry, known := plugintype.Lookup(declaredType)
	return !known || entry.Tier == plugintype.TierMutating
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
// not yet visible to the entity-front the artifact-load phase reads: the lazy
// index is re-folded from the now-augmented closure between the two phases — the
// re-fold DescendPinnedAxes runs — so the artifact descent's LoadPlugin resolves
// the just-pinned entity. A single-pass descent loads the body against a pin set
// that predates the entity's own pin and misses it.
//
// Both phases run after ComputeClosure (and the sub-aggregate / library discovery
// passes), through DescendPinnedAxes, for a pull and for a build alike — never
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
// cascade and is pinned when a registry serves it.
//
// A reference the lookup resolves through no tier (NotFound) — no catalog directory,
// no cache slot, no registry — is asked of loader, which reads entities where the
// build does. An entity the binary carries is the one it holds there: the lookup
// sends every plugin type but a provider to the registry tiers, while the build reads
// the binary's copy, so such an entity is left unpinned and resolves at build from the
// binary. An entity the loader cannot load either is one no tier holds, and the
// build's load of the step would refuse it; with a localArtifact supplied — a pull,
// before it writes — the descent refuses it here with the loader's refusal, naming
// the Recipes and FeatureRecipes that name it, so the pull writes no lockfile its
// build refuses. A build supplies none, asks nothing here, and raises the same
// condition where it loads the step.
//
// An entity authored in a local catalog directory is left out of the closure, and
// localArtifact is asked about it first: nothing fetches such an entity's artifact
// and a build fetches nothing, so an artifact no local source holds is refused here,
// and the refusal halts the descent before the lockfile is written. Only the refusal
// is read; what the artifact resolves to is the artifact descent's to pin. A nil
// localArtifact asks nothing.
//
// Newly-discovered entities are appended to c.Entities (deduped by URI against
// what is already present, accumulating referenced-from sites). A nil loader is
// a no-op (the caller wired no recipe-body access).
func DescendRecipePluginEntities(ctx context.Context, c *Closure, loader RecipeLoader, lookup Lookup, providerResolver ProviderPluginResolver, localArtifact LocalPluginArtifact) error {
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
		// No catalog directory, cache slot or registry answered. The loader reads
		// entities where the build does, the binary included, so what it holds stays
		// unpinned and resolves at build; what it cannot load, a pull refuses before
		// it writes, with the loader's refusal — the one the build's load would raise.
		if entRes.NotFound {
			if localArtifact != nil {
				if _, lerr := loader.LoadPlugin(entityURI); lerr != nil {
					return unheldPluginEntity(entityURI, recipeFromSet, lerr)
				}
			}
			continue
		}
		// An entity authored in a local catalog directory is read from there at every
		// build, artifact included, so its artifact has to be readable now.
		if localArtifact != nil && resolvedInLocalCatalog(entRes.ResolutionPath) {
			wp, err := loader.LoadPlugin(entityURI)
			if err != nil {
				return fmt.Errorf("resolve: load wasm plugin entity %q: %w", entityURI, err)
			}
			if _, err := localArtifact(entityURI, wp.ArtifactReference()); err != nil {
				return err
			}
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

// unheldPluginEntity carries a refusal for a Plugin entity no source holds,
// naming the Recipes and FeatureRecipes that name it, in sorted order, so a reader
// is told where the reference was written. The refusal itself is carried beneath
// unchanged, and with it the condition it names.
func unheldPluginEntity(entityURI string, namedBy map[string]struct{}, refusal error) error {
	if len(namedBy) == 0 {
		return fmt.Errorf("resolve: plugin %q: %w", entityURI, refusal)
	}
	refs := make([]string, 0, len(namedBy))
	for r := range namedBy {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return fmt.Errorf("resolve: plugin named by %s: %w", strings.Join(refs, ", "), refusal)
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
//
// One entity on the entity axis may have been authored in a local catalog directory:
// a transform-role governance attachment, which ComputeClosure keeps there wherever it
// resolved from. Its artifact is resolved through localArtifact from the machine's own
// sources — beside the entity, from the binary, from the content cache — and pinned
// from those bytes, and lookup is never asked for it, so no registry serving the same
// identity is reached. A nil localArtifact leaves such an artifact out.
func DescendRecipePluginArtifacts(ctx context.Context, c *Closure, loader RecipeLoader, lookup Lookup, localArtifact LocalPluginArtifact) error {
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
	entityPaths := map[string]string{}
	for _, ent := range c.Entities {
		if ent.KindHint == pluginKindHint {
			entityURIs = append(entityURIs, ent.URI)
			entityPaths[ent.URI] = ent.ResolutionPath
		}
	}
	sort.Strings(entityURIs)

	for _, entityURI := range entityURIs {
		if err := pinPluginArtifact(ctx, c, loader, lookup, entityURI, entityPaths[entityURI], pluginIndex, localArtifact); err != nil {
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
// truly-missing artifact). An artifact artifactNeedsNoPin reports is left out, at
// every site that pins one — the artifact descent a pull and a build both run, and
// the workspace-socket reconstruction — so the one pin rule decides each artifact
// from the resolution path it actually has, and a build reconstructs exactly the
// artifact rows the pull wrote.
//
// entityPath is where the entity itself resolved from, and it decides where the
// artifact is resolved from (see ResolvePluginArtifact). A refusal from localArtifact
// halts, because an artifact no local source holds is one no later step can supply. A
// nil localArtifact leaves such an artifact out.
func pinPluginArtifact(ctx context.Context, c *Closure, loader RecipeLoader, lookup Lookup, entityURI, entityPath string, pluginIndex map[string]int, localArtifact LocalPluginArtifact) error {
	wp, err := loader.LoadPlugin(entityURI)
	if err != nil {
		return fmt.Errorf("resolve: load wasm plugin entity %q to read its .wasm: %w", entityURI, err)
	}
	artifactURI := wp.ArtifactReference()
	if artifactURI == "" {
		return nil
	}
	artRes, err := ResolvePluginArtifact(ctx, lookup, localArtifact, entityURI, entityPath, artifactURI, wp.Metadata.Version)
	if err != nil {
		return fmt.Errorf("resolve: resolve pipeline plugin artifact %q: %w", artifactURI, err)
	}
	if artRes.NotFound {
		return nil
	}
	if artifactNeedsNoPin(artRes.ResolutionPath, plugintype.Type(wp.Metadata.PluginType)) {
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

// ResolvePluginArtifact resolves the .wasm artifact a Plugin entity names from where
// the entity itself resolved, entityPath. It is the one place a lock-time pass decides
// whether a plugin artifact may be asked of the lock-time lookup, so every pass that
// resolves one — the artifact descent, the workspace-socket reconstruction, and the
// pull's caching of the plugins attached at a verdict role — decides it the same way.
//
// An entity authored in a local catalog directory has its artifact answered by
// localArtifact, from the sources already on the machine, and lookup is never asked:
// lookup resolves a plugin artifact by version and fetches it from a registry when no
// recorded pin names a cached copy, so asking it would reach a registry serving the
// same identity. A nil localArtifact reports such an artifact NotFound, still without
// asking lookup. Every other entity's artifact is resolved through lookup.ResolvePlugin
// by the entity's version, because the .wasm rides inline in the entity's single
// manifest under that version's tag.
func ResolvePluginArtifact(ctx context.Context, lookup Lookup, localArtifact LocalPluginArtifact, entityURI, entityPath, artifactRef, version string) (Resolution, error) {
	if resolvedInLocalCatalog(entityPath) {
		if localArtifact == nil {
			return Resolution{NotFound: true}, nil
		}
		return localArtifact(entityURI, artifactRef)
	}
	return lookup.ResolvePlugin(ctx, artifactRef, version)
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
				return nil, nil, fmt.Errorf("resolve: load recipe %q to read the plugins it names: %w", ent.URI, err)
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
				return nil, nil, fmt.Errorf("resolve: load feature recipe %q to read the plugins it names: %w", ent.URI, err)
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
