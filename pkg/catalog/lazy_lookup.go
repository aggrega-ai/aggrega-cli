// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// NewLazyLookup returns a *LazyLookup whose AsCatalogLookup method
// produces a resolver.CatalogLookup. The returned *LazyLookup carries
// a Prime method that primes the hot caches from an already-loaded
// *Catalog, eliminating the redundant first-Tier-1 walk on sessions
// where the eager catalog has already been built for other purposes
// (e.g., for Recipe + FeatureRecipe + SubAggregateLoader).
//
// The lazy lookup resolves Component and Feature URIs through the
// Tier-1 → Tier-2 → Tier-3
// cascade. Tier-1 walks the configured catalog directories once per
// session (header-only YAML parse) and builds an in-memory index
// keyed by metadata.name; subsequent Tier-1 calls hit the index in
// O(1). Tier-2 reads cached entity bytes from the workspace cache
// directory. Tier-3 pulls from the registry via the supplied
// OCIPuller and populates the cache for subsequent lookups.
//
// Extends chains are resolved recursively on demand. When a Component
// (or Feature) has a non-empty spec.extends, the lookup invokes
// validator.ResolveComponentExtends (or ResolveFeatureExtends) with
// itself as the closure, walking the chain through the same cascade.
// Cycle detection comes free via validator.ResolveExtendsChain's
// visited-set guard, which is the walk both of those collect through.
//
// The lookup is layout-agnostic: entities are indexed by authored
// metadata.name regardless of where the YAML lives under
// catalogDirs. Workspaces using flat layouts, generated catalog
// trees, or any other organisation work identically to the
// canonical kind-directory layout.
//
// The returned lookup is safe to reuse across many resolver +
// pipeline invocations within a session. The in-memory caches grow
// unbounded; the negative cache short-circuits repeated lookups of
// known-missing URIs.
//
// For the eager / one-shot build flow, callers should continue to
// use catalog.Load + (*Catalog).CatalogLookup, which loads every
// entity upfront. The lazy primitive is for long-running consumers
// where most catalog entities are never referenced.
func NewLazyLookup(workspaceRoot, cacheDir string, catalogDirs []string, puller OCIPuller) *LazyLookup {
	return &LazyLookup{
		workspaceRoot:     workspaceRoot,
		cacheDir:          cacheDir,
		catalogDirs:       catalogDirs,
		puller:            puller,
		components:        map[string]types.Component{},
		features:          map[string]types.Feature{},
		schemaLibraries:   map[string]types.SchemaLibrary{},
		componentMisses:   map[string]struct{}{},
		featureMisses:     map[string]struct{}{},
		componentIndex:    map[string]string{},
		featureIndex:      map[string]string{},
		recipes:           map[string]types.Recipe{},
		featureRecipes:    map[string]types.FeatureRecipe{},
		aggregates:        map[string]types.Aggregate{},
		mappingLibraries:  map[string]types.MappingLibrary{},
		workloadLibraries: map[string]types.WorkloadLibrary{},
		artifactLibraries: map[string]types.ArtifactLibrary{},
		plugins:           map[string]types.Plugin{},
		recipeSkips:       map[string]error{},
		entityBytes:       map[string][]byte{},
		origins:           map[string]entityOrigin{},
		recordedPins:      recipepin.Lookup{},
	}
}

// ForRequest returns a request-scoped *LazyLookup carrying its OWN pin set and
// its OWN pin-DEPENDENT caches, seeded with pins, so a concurrent request
// resolving with different pins never perturbs this one. Pins and every cache
// whose contents depend on them (the hot Component/Feature caches, the
// enumerated Recipe/FeatureRecipe/Aggregate/Plugin/MappingLibrary/
// SchemaLibrary shell, the effective-schema and narrowing accumulators, the
// recipe-catalog shell, the retained entity bytes) are private to the returned
// view. The pin-INDEPENDENT, session-stable state — workspaceRoot, cacheDir,
// catalogDirs, puller — is carried over, and the already-built Tier-1
// filesystem indexes (a pure function of catalog-dir contents) are SNAPSHOTTED
// under the parent's lock into the view, so the view never aliases a map the
// parent's Reset or a concurrent index build could rewrite under it. The view
// owns its own mutex; being single-request scoped, it sees no cross-request
// contention.
//
// The session catalog the view forks from is unchanged — ForRequest installs no
// pins on it. A request resolves entirely against its view; the session catalog
// holds only the shared session-stable state and is reset on workspace
// invalidation. This is what removes shared mutable pin state: two requests
// hold two views, so one request's pins can never interleave into another's
// multi-step resolution.
func (l *LazyLookup) ForRequest(pins map[string]Pin) *LazyLookup {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Build the Tier-1 indexes once on the session catalog (idempotent), then
	// snapshot them into the view. Copying decouples the view from the parent's
	// map identity: a later Reset on the parent swaps in fresh maps and a
	// concurrent parent index build writes its own maps — neither touches the
	// view's snapshot.
	l.buildComponentIndex()
	l.buildFeatureIndex()
	componentIndex := make(map[string]string, len(l.componentIndex))
	for k, v := range l.componentIndex {
		componentIndex[k] = v
	}
	featureIndex := make(map[string]string, len(l.featureIndex))
	for k, v := range l.featureIndex {
		featureIndex[k] = v
	}
	view := NewLazyLookup(l.workspaceRoot, l.cacheDir, l.catalogDirs, l.puller)
	view.componentIndex = componentIndex
	view.componentIndexBuilt = true
	view.featureIndex = featureIndex
	view.featureIndexBuilt = true
	view.SetPins(pins)
	return view
}

// AsCatalogLookup returns the resolver.CatalogLookup view of the lazy
// lookup. Both Component and Feature fields close over the underlying
// *LazyLookup, so the same hot caches + negative caches + indexes
// serve every consumer.
func (l *LazyLookup) AsCatalogLookup() resolver.CatalogLookup {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Resolve the SchemaLibrary set (once, cached) so cross-document $ref expansion
	// sees the same libraries the eager backing does. Best-effort here: a library
	// that fails validation is surfaced by buildSchemaLibraries' returned error to a
	// caller that observes it; AsCatalogLookup discards that error and exposes the
	// resolvable set.
	_ = l.buildSchemaLibraries()
	return resolver.CatalogLookup{
		Component:       l.Component,
		Feature:         l.Feature,
		SchemaLibraries: l.schemaLibraries,
		// EffectiveSchema serves the composed (parent+child) schema for an
		// extends-ing FEATURE, keyed by metadata.name — populated by
		// ResolveClosureExtends. Returns (_, false) for a non-extends Feature, a
		// Component of any kind, or a not-yet-resolved type, so consumers fall
		// back to the authored schema. Components are absent by construction:
		// extending copies the base's pillar block down verbatim, so a Component's
		// authored schema already is its inherited one.
		EffectiveSchema: l.effectiveSchema,
		// Plugin serves the Plugin entity by URI over the lazily-walked
		// set, so the build-load pipeline socket fronts the artifact cascade with
		// the entity → artifact-reference two-step from the lazy backing too.
		Plugin: l.PluginByURI,
		// PluginFromCache resolves a Plugin entity from the pull-populated cache by
		// URI with no lockfile pin — the cache-not-pin read a verdict socket
		// (policy / linter) falls back to when the pinned / Tier-1 path does not
		// surface the gate plugin.
		PluginFromCache: l.PluginFromCacheByURI,
		// ProviderPlugin resolves a provider name to the provider-rendering Plugin
		// serving it (via targets) over the lazily-walked set, so
		// render dispatch resolves the same provider→Plugin binding from the lazy
		// backing as the eager one.
		ProviderPlugin: l.ProviderPluginByName,
		// ParserPlugin resolves a source format to the parser Plugin that
		// advertises parse:<format> over the lazily-walked set, so a socket that
		// must parse a foreign artifact (render-policy handing a policy plugin a
		// non-native artifact's tree) resolves the same format→parser binding from
		// the lazy backing as the eager one.
		ParserPlugin: l.ParserPluginForFormat,
		// PluginColocatedDirs maps each Tier-1-enumerated Plugin entity's artifact
		// reference to its catalog directory, so the build-load cascade's co-located
		// tier resolves a core guest from beside its catalog entity when the binary
		// embeds none. Only catalog-directory (Tier-1) entities carry a directory;
		// a registry-resolved (Tier-2) entity lives in a cache slot and never
		// appears here.
		PluginColocatedDirs: l.pluginColocatedDirs(),
		// The recipe lookups and the joint instance resolution over the
		// lazily-enumerated recipe set, so a rule asked at a stage of the
		// compilation reads the same complete enumeration the build's own
		// resolution reads. Each takes l.mu when it is called rather than now, so
		// handing the methods over from under the lock is not a re-entry.
		Recipe:               l.RecipeByURI,
		FeatureRecipe:        l.FeatureRecipeByURI,
		NodeRecipeResolution: l.NodeRecipeResolution,
		// The local source a build compares with its recorded pins, read through the
		// declared set a publication packs and the pin step pins. It reads the
		// document at the path it is handed and holds no state of the lookup's, so
		// it needs no lock.
		LocalSourcePins: LocalSourcePinsAt,
	}
}

// NodeRecipeResolution joins the Manifest's mapping table and the
// lazily-enumerated recipe set into the answer about one component instance, so
// a rule asked over the composition reads the same complete enumeration the
// build's own resolution reads rather than a partially-walked one.
func (l *LazyLookup) NodeRecipeResolution(
	manifest *types.Manifest,
	instanceName string,
	componentType string,
) resolver.NodeRecipeResolution {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().nodeRecipeResolution(manifest, instanceName, componentType)
}

// PluginColocatedDirs is the co-located directory map AsCatalogLookup hands the
// build's plugin cascade, read on its own: a caller that needs only the directory
// each locally authored Plugin entity's .wasm sits in beside it — a pull asking
// the build's cascade whether such an entity's artifact can be read — reads the
// same map without assembling the rest of the lookup.
func (l *LazyLookup) PluginColocatedDirs() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pluginColocatedDirs()
}

// pluginColocatedDirs maps each Tier-1-enumerated Plugin entity's bare-identity
// artifact reference to the catalog directory its plugin.yaml was authored in —
// the directory a co-located .wasm sits in beside the entity. It mirrors the
// eager catalog's PluginColocatedDirs over the lazily-walked local set, reading
// each entity's authored document from the one record of where it came from; an
// entity read from a pinned cache slot has no authored directory and is skipped.
// The caller (AsCatalogLookup, PluginColocatedDirs) holds l.mu. Returns nil when no local Plugin
// entity was enumerated. On two versions of one identity, the lexicographically
// last source path wins, so the map is deterministic regardless of map order.
func (l *LazyLookup) pluginColocatedDirs() map[string]string {
	l.buildRecipeIndex()
	if len(l.plugins) == 0 {
		return nil
	}
	keys := make([]string, 0, len(l.plugins))
	for key := range l.plugins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	dirs := make(map[string]string, len(l.plugins))
	for _, key := range keys {
		src, ok := l.origins[entityBytesKey("Plugin", key)].authoredDocument()
		if !ok {
			continue
		}
		p := l.plugins[key]
		dirs[p.ArtifactReference()] = filepath.Dir(src)
	}
	if len(dirs) == 0 {
		return nil
	}
	return dirs
}

// effectiveSchema serves the composed (parent+child) schema for an
// extends-ing Feature, keyed by metadata.name — populated by
// ResolveClosureExtends. Returns (_, false) for a non-extends Feature, a
// Component of any kind, or a not-yet-resolved type, so consumers fall
// back to the authored schema.
// It is handed out through AsCatalogLookup and invoked by the pipeline
// outside any LazyLookup method, so it takes the lock itself to read
// the effectiveSchemas map race-free against a concurrent
// ResolveClosureExtends / Reset / SetPins.
func (l *LazyLookup) effectiveSchema(typeURI string) (compactform.Schema, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.effectiveSchemas == nil {
		return compactform.Schema{}, false
	}
	s, ok := l.effectiveSchemas[lookupNameFromURI(typeURI)]
	return s, ok
}

// Reset clears the hot caches, negative caches, and per-kind index
// state, returning the lookup to its post-construction shape.
// Subsequent Component / Feature calls re-walk the cascade.
//
// Reset is the session catalog's workspace-invalidation operation: when
// the workspace's filesystem state changed (a file was added, removed,
// or modified) the session-stable Tier-1 indexes and the cached
// resolutions built from them are stale, so Reset drops them and the
// next access re-walks. It rebuilds fresh maps rather than clearing in
// place, so a request view that snapshotted the indexes before the
// reset keeps its own copy unaffected.
//
// Reset does NOT touch the workspaceRoot / cacheDir / catalogDirs /
// puller configuration — those came from NewLazyLookup and stay stable
// across the session.
//
// Thread-safety: concurrency-safe via the lookup's internal mutex,
// like every exported method; callers need no external lock.
//
// Idempotent: calling Reset twice in a row leaves the state empty;
// the second call re-allocates the maps but observes no state to
// clear.
func (l *LazyLookup) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.components = map[string]types.Component{}
	l.features = map[string]types.Feature{}
	l.componentMisses = map[string]struct{}{}
	l.featureMisses = map[string]struct{}{}
	l.componentIndex = map[string]string{}
	l.componentIndexBuilt = false
	l.featureIndex = map[string]string{}
	l.featureIndexBuilt = false
	l.schemaLibraries = map[string]types.SchemaLibrary{}
	l.schemaLibrariesBuilt = false
	l.effectiveSchemas = nil
	l.closureNarrowings = nil
	l.pins = nil
	l.recipes = map[string]types.Recipe{}
	l.featureRecipes = map[string]types.FeatureRecipe{}
	l.aggregates = map[string]types.Aggregate{}
	l.mappingLibraries = map[string]types.MappingLibrary{}
	l.workloadLibraries = map[string]types.WorkloadLibrary{}
	l.artifactLibraries = map[string]types.ArtifactLibrary{}
	l.plugins = map[string]types.Plugin{}
	l.recipeSkips = map[string]error{}
	l.recipeIndexBuilt = false
	l.recipeCat = nil
	l.entityBytes = map[string][]byte{}
	l.origins = map[string]entityOrigin{}
	l.recordedPins = recipepin.Lookup{}
}

// Prime copies the supplied catalog's already-merged Components and
// Features into the lazy lookup's hot caches. After Prime, calls to
// Component(uri) or Feature(uri) for URIs in the supplied catalog
// return cache hits without walking Tier-1.
//
// SchemaLibraries are NOT primed. The lazy backing resolves them itself
// (buildSchemaLibraries) through the same shared per-entity resolve the eager
// path uses, so the lazy view never depends on being fed eager-loaded libraries
// to reach parity. Prime is purely a Component/Feature hot-cache optimization.
//
// The catalog MUST have completed extends resolution (via
// (*Catalog).ResolveAllExtends) before Prime is called — the entries
// in cat.Components and cat.Features are copied verbatim into the
// hot caches under the assumption they are already merged.
//
// Prime also flips componentIndexBuilt + featureIndexBuilt to true so
// subsequent Tier-1 walks are skipped. URIs not present in the
// supplied catalog fall through to Tier-2 (workspace cache) and
// Tier-3 (OCI puller) per the documented cascade — the eager
// catalog.Load is presumed to be authoritative over the catalog
// directories, so a URI not found there is presumed to live in cache
// or registry.
//
// Order matters for the optimisation. Prime eliminates the redundant
// Tier-1 walk only when it runs BEFORE any Component / Feature
// lookup triggers the cascade. If a caller invokes the lookup first
// and Prime second, the cascade already walked Tier-1 to build its
// index; Prime then overlays already-merged entries on top of
// cascade-resolved entries (semantically equivalent — both produce
// the same shape — but the walk-elimination payoff is lost).
// Consumers wanting the optimisation should call Prime before
// exposing the lookup to operations that may resolve URIs.
//
// Idempotent: calling Prime twice with the same catalog has no
// additional effect.
func (l *LazyLookup) Prime(cat *Catalog) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// The lazy hot cache is keyed by the lookup URI callers request (the bare
	// metadata.name), so prime under metadata.name rather than the eager
	// catalog's name:version index key — otherwise a bare-name request would
	// miss the primed entry and fall through to the Tier-1 walk.
	for _, comp := range cat.Components {
		l.components[comp.Metadata.Name] = comp
	}
	for _, feat := range cat.Features {
		l.features[feat.Metadata.Name] = feat
	}
	l.componentIndexBuilt = true
	l.featureIndexBuilt = true
}

type LazyLookup struct {
	// mu guards every operation on the lazy lookup. Each exported
	// method takes it on entry; the many unexported helpers
	// (resolveComponent, the Tier-1/2/3 readers, the build* index
	// builders, foldRecipeShellEntity, recipeCatalog, the component /
	// feature cores) run lock-free because they are only ever reached
	// from an already-locked exported method. The mutex is not
	// re-entrant, so no locked exported method calls another — the
	// extends-merge recursion is wired through the lock-free cores, and
	// the closures the accessors hand out (AsCatalogLookup's Component /
	// Feature, the recipe-shell lookups) re-enter only the exported
	// methods, which the pipeline invokes after the accessor returned
	// and released the lock. A single coarse mutex is the conservative
	// correct choice: SetPins and Reset rewrite whole maps, so a reader
	// holding a finer lock could observe a half-reset state.
	//
	// The mutex makes each individual method atomic; it does not make a
	// SetPins-then-reads SEQUENCE atomic against another goroutine's
	// SetPins. A consumer that needs a consistent pin set across a
	// multi-call resolution must therefore hold its OWN lookup for that
	// span rather than share one whose pins another goroutine can swap
	// mid-resolution — the daemon does this by forking a per-request
	// view (ForRequest), so cross-request pin interleaving cannot occur.
	mu sync.Mutex

	workspaceRoot string
	cacheDir      string
	catalogDirs   []string
	puller        OCIPuller

	// Hot cache populated by successful resolutions. Holds merged
	// entities (extends chain already applied via validator
	// primitives).
	components map[string]types.Component
	features   map[string]types.Feature

	// SchemaLibraries resolved by the lazy backing itself (buildSchemaLibraries),
	// not copied from an eager *Catalog. Cross-library schema `$ref` resolution
	// needs the whole library set at compile time, so the set is walked + parsed +
	// validated as a unit (not per-URI on demand) on first need and cached here.
	// schemaLibrariesBuilt guards the one-time walk; Reset clears both so a changed
	// workspace re-walks.
	schemaLibraries      map[string]types.SchemaLibrary
	schemaLibrariesBuilt bool

	// effectiveSchemas holds the composed (parent+child) schema for each
	// extends-ing closure FEATURE, keyed by metadata.name — the build-path
	// counterpart to the eager catalog's effectiveSchemas. Populated by
	// ResolveClosureExtends via the same ResolveFeatureSchemaChain merge the
	// eager whole-catalog path uses; served through AsCatalogLookup so Feature-schema
	// consumers read an extending Feature's composed schema, not the child-only
	// structural one. Empty for a closure with no extends-ing Feature.
	//
	// Components are deliberately absent: extending copies the base's pillar
	// block down verbatim onto the child, so a Component's authored schema is
	// its inherited schema and a second view would be a second answer.
	effectiveSchemas map[string]compactform.Schema

	// closureNarrowings holds the narrowing-on-extend advisory notices (a child
	// widening an inherited constraint) accumulated while composing the closure's
	// extends chains — a non-halting side-band signal the lint closure mode
	// surfaces informationally. Order follows the sorted-name closure walk that
	// produces it. Never consulted by the build (build-time narrowing stays
	// deferred); cleared with effectiveSchemas on Reset/SetPins.
	closureNarrowings []string

	// Negative cache short-circuits repeated lookups of known-missing
	// URIs within the session.
	componentMisses map[string]struct{}
	featureMisses   map[string]struct{}

	// Per-kind Tier-1 indexes built on first access. Maps
	// metadata.name → filesystem path. Built once per session via
	// the catalog-tree walker; reused thereafter. No in-session
	// invalidation.
	componentIndex      map[string]string
	componentIndexBuilt bool
	featureIndex        map[string]string
	featureIndexBuilt   bool

	// pins maps an entity URI (as the lockfile records it — bare or versioned) to
	// its resolved version + content-digest pin, set per request via SetPins from
	// the request's loaded lockfile. The version is load-bearing: the Tier-2 cache
	// slot is keyed by the VERSIONED URI, so a bare-URI pin (e.g. a sub-aggregate
	// referenced by bare `type`, pinned with a separate ResolvedVersion) must form
	// its slot key via versionedSlotURI before the verify-on-read. A Tier-2 hit is
	// verified against the pin's digest before use. nil pins means no lockfile is
	// threaded — the cache is not trusted, so Tier-2 falls through to a Tier-3
	// re-establish rather than returning unverified bytes.
	pins map[string]Pin

	// Recipe / FeatureRecipe / Aggregate sets enumerated TOLERANTLY from the
	// catalog directories on first need (buildRecipeIndex). Recipes are keyed by
	// identity (provider/name:version), the same key the eager catalog uses, so
	// the re-homed matcher resolves identically. A recipe/FR/aggregate whose body
	// won't parse (or whose identity collides) is SKIPPED with a warning and a
	// recorded reason rather than aborting the walk — so a broken UNUSED recipe is
	// invisible to a resolution that doesn't need it (the blast-radius fix). The
	// eager catalog, by contrast, fails the whole walk on the first broken recipe.
	recipes           map[string]types.Recipe
	featureRecipes    map[string]types.FeatureRecipe
	aggregates        map[string]types.Aggregate
	mappingLibraries  map[string]types.MappingLibrary
	workloadLibraries map[string]types.WorkloadLibrary
	artifactLibraries map[string]types.ArtifactLibrary
	plugins           map[string]types.Plugin // keyed by identity (metadata.name:version), like the eager catalog
	// recipeSkips records every catalog file this backing could not index, keyed
	// by file path, with the reason. It spans more kinds than its name suggests:
	// recipe, FeatureRecipe, Aggregate, mapping / workload / artifacts / schema
	// library, plugin, and — recorded on a per-URI cascade read rather than an
	// enumeration walk — Component and Feature.
	recipeSkips      map[string]error
	recipeIndexBuilt bool

	// origins is the one record of where each enumerated entity was read from,
	// keyed by entityBytesKey(kind, identityKey): the catalog-directory document
	// that authored it, or the lockfile-pinned cache slot it was read from together
	// with the document in that slot's layout its content paths anchor at (see
	// entityOrigin). It is written once per identity, by the fold that places the
	// winning entry in its kind's index — foldRecipeShellEntity, and for a schema
	// library the Tier-1 walk in buildSchemaLibraries and foldPinnedSchemaLibrary —
	// and it covers every kind those folds admit: Recipe and FeatureRecipe keyed by
	// capability-scoped identity, Plugin by its versioned coordinate, Aggregate by
	// name, and the schema, mapping, workload and artifacts libraries by bare name.
	//
	// Every provenance answer this backing gives is derived from it, and none has an
	// index of its own: whether an author owns a document for the entity (the pin
	// step writes only beside one, and never into a cache slot), where its content
	// paths anchor, which tier a closure walk reports for it, the directory a
	// co-located artifact sits in, and the source a flattened Aggregate records.
	//
	// Component and Feature are not here — their paths are componentIndex and
	// featureIndex, which are the maps the per-URI cascade reads to find the file in
	// the first place, and those kinds are resolved on demand rather than folded. A
	// record depends on the pins, so SetPins and Reset drop it with the rest of the
	// enumerated state.
	origins map[string]entityOrigin

	// recordedPins holds the content pins in force for each pin-bearing entity
	// this backing enumerated, keyed by the document it was read from. It is the
	// lazy backing's half of what the eager load records under the same name, and
	// the recipe-catalog shell hands it on to the render.
	//
	// Only an entity with an authored document records pins here: a pinned cache
	// slot is not a directory a lock could sit beside, so an entity read from one
	// records nothing here and is answered as recording nothing.
	recordedPins recipepin.Lookup

	// entityBytes retains the RAW authored bytes of every enumerated /
	// resolved entity, keyed by entityBytesKey(kind, metadata.name) — the same
	// key the eager catalog uses. The closure lookup (ClosureLookup) hashes
	// these to produce the content digest the lockfile pins, so a serve drift
	// verify compares the SAME digest a build/pull computed (digest fidelity:
	// the bytes hashed are the bytes resolved, no second source of truth, no
	// re-read TOCTOU). Populated by buildRecipeIndex / buildSchemaLibraries
	// (enumerated kinds) and by resolveComponent / resolveFeature (on-demand
	// kinds). Cleared by Reset so an invalidated workspace retains no stale
	// bytes — they must not survive an mtime-eviction / invalidation.
	entityBytes map[string][]byte

	// recipeCat is the cached *Catalog shell over the enumerated recipe/FR/
	// aggregate maps. The shell carries ONLY the maps the matcher methods read
	// (Recipes, FeatureRecipes, Aggregates, origins, workspaceRoot), so
	// RecipeLookupWithManifest / VerifyRecipeResolution / SubAggregateLoader are
	// reused verbatim against a lazily-enumerated set — no matcher reimplemented.
	// nil after construction / Reset; rebuilt on first recipeCatalog() call so a
	// changed workspace (serve's incremental edits) re-walks.
	recipeCat *Catalog
}

// SetPins installs the lockfile pins on the lookup, enabling verify-on-read on
// the Tier-2 cache. It MUST be called before the reads that consume them run,
// on a lookup a single owner holds for the duration: a whole-invocation CLI
// lookup (build/pull/lint set their pins then read), or a per-request daemon
// view obtained from ForRequest (which seeds its pins through this same path, so
// pin installation has one implementation). Passing nil disables trust in the
// cache: Tier-2 hits are not returned unverified; the cascade falls through to
// Tier-3.
//
// SetPins is not a cross-request handle on a shared catalog — the daemon never
// calls it on its session catalog. A lookup whose pins another goroutine may
// reset mid-resolution would resolve against the wrong pin set; ForRequest
// exists precisely so each daemon request owns its own pinned view.
//
// On a pin mismatch the slot is skipped (not used, not re-fetched) so the IR is
// not built from drifted content; the per-request drift DIAGNOSTIC is the
// closure-level lockfile-verify already run on the same request, so the skip
// deliberately does NOT emit a second notice (no double-report).
func (l *LazyLookup) SetPins(pins map[string]Pin) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pins = pins
	// The enumerated kinds (Recipe / FeatureRecipe / Aggregate / Plugin /
	// MappingLibrary / SchemaLibrary) fold a PIN-DEPENDENT Tier-2 set, and the
	// enumeration is built-once (idempotent). When the pin set changes — pull's
	// second SetPins after library discovery, or serve's per-request pins — drop the
	// enumerated state so the next access re-folds against THESE pins; otherwise a
	// newly-pinned library/recipe/aggregate (discovered after the first SetPins)
	// would never be folded. The Tier-1 catalog-dir walk re-runs too (cheap; the
	// maps rebuild identically from disk). The Component/Feature on-demand cascade
	// reads l.pins directly, so it needs no invalidation here.
	l.schemaLibrariesBuilt = false
	l.recipeIndexBuilt = false
	l.recipeCat = nil
	l.schemaLibraries = map[string]types.SchemaLibrary{}
	l.effectiveSchemas = nil
	l.closureNarrowings = nil
	l.recipes = map[string]types.Recipe{}
	l.featureRecipes = map[string]types.FeatureRecipe{}
	l.aggregates = map[string]types.Aggregate{}
	l.mappingLibraries = map[string]types.MappingLibrary{}
	l.workloadLibraries = map[string]types.WorkloadLibrary{}
	l.artifactLibraries = map[string]types.ArtifactLibrary{}
	l.plugins = map[string]types.Plugin{}
	l.recipeSkips = map[string]error{}
	// The record of where each entity came from is rebuilt with the enumeration
	// that writes it: an entry placed from a slot depends on the pins, and the
	// catalog-directory entries are rewritten identically by the walks that re-run.
	l.origins = map[string]entityOrigin{}
}

// VerifyPinnedIntegrity is the BUILD's loud integrity gate: it reads every pinned
// Tier-2 cache slot verify-on-read and returns the first ErrFetchIntegrity — a
// slot whose content drifted from its lockfile pin (the tier2Skip condition). An
// absent slot (ErrFetchNotFound) is NOT an error: a pin with no cache slot is a
// catalog-resident entity resolved at Tier-1, exactly as the cache→catalog bridge
// treated it. The build calls this after SetPins so a drifted pin halts loudly
// with the integrity diagnostic ("re-pull") rather than collapsing into a
// misleading downstream "not resolvable". Serve does NOT call it — its
// resilient-inspection posture keeps the tier2Skip (the same build-halt /
// serve-skip split the guard layer uses). Pins are visited in sorted URI order so
// the surfaced drift is deterministic.
func (l *LazyLookup) VerifyPinnedIntegrity() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	uris := make([]string, 0, len(l.pins))
	for uri := range l.pins {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	for _, uri := range uris {
		pin := l.pins[uri]
		_, err := tier2ReadVerified(l.workspaceRoot, l.cacheDir, versionedSlotURI(uri, pin.ResolvedVersion), pin.Digest, pin.ManifestDigest)
		if err != nil && (errors.Is(err, oci.ErrFetchIntegrity()) || errors.Is(err, oci.ErrCachedManifestIntegrity())) {
			return fmt.Errorf("pinned entity %s: %w", uri, err)
		}
	}
	return nil
}

// Pin is a lockfile entry's resolved version + content digest, keyed in the lazy
// lookup by the entity URI as the lockfile records it. The version is retained
// (not just the digest) because the Tier-2 cache slot is keyed by the VERSIONED
// URI: a bare-URI pin must combine its URI with ResolvedVersion (via
// versionedSlotURI) to address the slot, exactly as the cache→catalog bridge does.
type Pin struct {
	ResolvedVersion string
	Digest          string
	// ManifestDigest is the OCI manifest digest the independent record (the
	// lockfile / prior-lockfile entry) committed to — the root of trust the
	// offline manifest self-verify checks the persisted manifest blob against.
	// Empty for a pin with no registry manifest (a locally-resolved entity);
	// the manifest self-verify is then skipped, but the persisted manifest is
	// still the authority the content is checked against.
	ManifestDigest string
}

// PinsFromLockfile builds the version-aware pin set SetPins consumes from a loaded
// lockfile, across every kind the closure walker resolves through the Tier-2 cache
// — the same axes Lockfile.PinsByURI covers, over the identical URI set, but
// retaining ResolvedVersion so a bare-URI pin can address its versioned cache
// slot. The axes are not enumerated here: the loops below are the statement, and a
// list beside them is a second copy that goes stale the moment an axis is added.
// Source artifacts are excluded (they descend on their own read-time digest gate,
// not the closure walker's Tier-2 reuse).
func PinsFromLockfile(lf *lockfile.Lockfile) map[string]Pin {
	if lf == nil {
		return nil
	}
	pins := make(map[string]Pin, len(lf.Entities)+len(lf.SchemaLibraries)+len(lf.MappingLibraries)+len(lf.WorkloadLibraries)+len(lf.ArtifactLibraries)+len(lf.Plugins))
	for _, e := range lf.Entities {
		pins[e.URI] = Pin{ResolvedVersion: e.ResolvedVersion, Digest: e.Digest, ManifestDigest: e.ManifestDigest}
	}
	for _, sl := range lf.SchemaLibraries {
		pins[sl.URI] = Pin{ResolvedVersion: sl.ResolvedVersion, Digest: sl.Digest}
	}
	for _, ml := range lf.MappingLibraries {
		pins[ml.URI] = Pin{ResolvedVersion: ml.ResolvedVersion, Digest: ml.Digest}
	}
	for _, wl := range lf.WorkloadLibraries {
		pins[wl.URI] = Pin{ResolvedVersion: wl.ResolvedVersion, Digest: wl.Digest}
	}
	for _, al := range lf.ArtifactLibraries {
		pins[al.URI] = Pin{ResolvedVersion: al.ResolvedVersion, Digest: al.Digest}
	}
	for _, wp := range lf.Plugins {
		pins[wp.URI] = Pin{ResolvedVersion: wp.ResolvedVersion, Digest: wp.Digest, ManifestDigest: wp.ManifestDigest}
	}
	return pins
}

// PinsFromClosure builds the version-aware pin set SetPins consumes from a
// freshly-computed closure — the lock-time source, used where no lockfile exists
// yet and again wherever a closure is re-derived and briefly pinned from itself.
// It projects the same axes as PinsFromLockfile, over the same URI set, each entry
// carrying the version the walk resolved so a bare-URI pin addresses its versioned
// cache slot. That agreement is what lets a caller shift a view between the two
// sources without narrowing what verify-on-read covers, and it is asserted rather
// than assumed: TestPinBuildersProjectTheSameAxes compares the two key sets.
// Source artifacts are excluded, exactly as PinsFromLockfile excludes them.
//
// The two builders are kept as separate projections rather than routed through one
// another: they read different source types, so a shared implementation would need
// a third abstraction over both, and neither caller has a use for one.
func PinsFromClosure(cl closure.Closure) map[string]Pin {
	pins := make(map[string]Pin, len(cl.Entities)+len(cl.SchemaLibraries)+len(cl.MappingLibraries)+len(cl.WorkloadLibraries)+len(cl.ArtifactLibraries)+len(cl.Plugins))
	for _, e := range cl.Entities {
		pins[e.URI] = Pin{ResolvedVersion: e.ResolvedVersion, Digest: e.Digest, ManifestDigest: e.ManifestDigest}
	}
	for _, sl := range cl.SchemaLibraries {
		pins[sl.URI] = Pin{ResolvedVersion: sl.ResolvedVersion, Digest: sl.Digest}
	}
	for _, ml := range cl.MappingLibraries {
		pins[ml.URI] = Pin{ResolvedVersion: ml.ResolvedVersion, Digest: ml.Digest}
	}
	for _, wl := range cl.WorkloadLibraries {
		pins[wl.URI] = Pin{ResolvedVersion: wl.ResolvedVersion, Digest: wl.Digest}
	}
	for _, al := range cl.ArtifactLibraries {
		pins[al.URI] = Pin{ResolvedVersion: al.ResolvedVersion, Digest: al.Digest}
	}
	for _, wp := range cl.Plugins {
		pins[wp.URI] = Pin{ResolvedVersion: wp.ResolvedVersion, Digest: wp.Digest, ManifestDigest: wp.ManifestDigest}
	}
	return pins
}

// tier2Outcome is the verify-on-read decision for a Tier-2 cache slot.
type tier2Outcome int

const (
	tier2Use   tier2Outcome = iota // bytes verified against the pin — use them
	tier2Fetch                     // slot absent, or no pin threaded — fall through to Tier-3
	tier2Skip                      // slot drifted from the pin — diagnostic emitted; do NOT use, do NOT fetch
)

// tier2Verified applies the serve verify-on-read posture to the Tier-2 cache
// slot for uri. It routes through tier2ReadVerified so a daemon never previews
// unverified cache content:
//   - pin match -> (bytes, tier2Use).
//   - pin mismatch (drift) -> (nil, tier2Skip): the slot is NOT used and NOT
//     re-fetched. Self-healing a drift would show content a real build would
//     halt on (a false-green preview). The user-facing drift notice is the
//     closure-level lockfile verify already run on the same request, so the skip
//     does not emit a second diagnostic (avoids double-reporting one drift).
//   - slot absent, or no pin threaded for uri -> (nil, tier2Fetch): the cascade
//     re-establishes at Tier-3 (an on-demand fetch verifies against the registry
//     descriptor), rather than returning unverified cache bytes.
//
// On tier2Use it also returns the persisted manifest the bytes were verified
// against, which a caller describing the hit reports the digest of
// (cacheHitResolution), and the slot the bytes were read from, which is where a
// caller that parses them lays them out (unpackEntitySlot).
func (l *LazyLookup) tier2Verified(uri string) ([]byte, []byte, string, tier2Outcome) {
	pin, havePin := l.pins[uri]
	if !havePin {
		return nil, nil, "", tier2Fetch
	}
	// The cache slot is keyed by the versioned URI; combine the lookup URI with
	// the pin's resolved version so a bare-URI pin addresses the right slot.
	slotURI := versionedSlotURI(uri, pin.ResolvedVersion)
	bytes, manifest, err := tier2ReadVerifiedWithManifest(l.workspaceRoot, l.cacheDir, slotURI, pin.Digest, pin.ManifestDigest)
	switch {
	case err == nil:
		slot, _, _ := tier2Slot(l.workspaceRoot, l.cacheDir, slotURI)
		return bytes, manifest, slot, tier2Use
	case errors.Is(err, oci.ErrFetchIntegrity()), errors.Is(err, oci.ErrCachedManifestIntegrity()):
		return nil, nil, "", tier2Skip
	default: // oci.ErrFetchNotFound (slot absent / incomplete) or an unreadable slot
		return nil, nil, "", tier2Fetch
	}
}

// fileHeader is the partial unmarshal struct used by the catalog-tree
// walker to index entities by authored identity without parsing the full
// entity body.
//
// spec.capability is part of that identity rather than part of the body: a
// recipe's identity is provider-scoped, and the provider segment IS the
// authored capability, so a recipe's identity cannot be recovered from
// metadata alone.
type fileHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
	Spec struct {
		Capability string `yaml:"capability"`
	} `yaml:"spec"`
}

// Component resolves the URI through the Tier-1 → Tier-2 → Tier-3
// cascade and applies extends resolution via the validator primitive.
// Returns the merged Component on hit, (zero-value, false) on
// cascade exhaustion.
//
// Extends-resolution errors (cycle, missing parent, malformed chain)
// are emitted as one-line warnings to os.Stderr per the existing
// mid-pipeline warning convention. The lookup does NOT halt on
// extends errors — it caches and returns the unmerged entity so
// downstream consumers can proceed; the operator sees the warning
// in the daemon's stderr stream. This mirrors how dispatchBuild
// surfaces catalog deprecation + binding-lint warnings.
func (l *LazyLookup) Component(uri string) (types.Component, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.component(uri)
}

// component is the lock-free core of Component. The extends-merge
// recursion is wired to component itself (not the exported Component),
// so a multi-level chain resolves without re-acquiring the lock — the
// caller already holds it. Every internal helper it reaches
// (resolveComponent, the Tier-1/2/3 readers) is likewise lock-free.
func (l *LazyLookup) component(uri string) (types.Component, bool) {
	if c, ok := l.components[uri]; ok {
		return c, true
	}
	if _, ok := l.componentMisses[uri]; ok {
		return types.Component{}, false
	}
	comp, ok := l.resolveComponent(uri)
	if !ok {
		l.componentMisses[uri] = struct{}{}
		return types.Component{}, false
	}
	merged, vErrs := validator.ResolveComponentExtends(comp, l.component)
	if len(vErrs) > 0 {
		// The failure names its own entity, kind included, so the line adds no
		// prefix of its own — one identity per diagnostic, and the same line
		// shape the whole-catalog resolution pass prints.
		fmt.Fprintf(os.Stderr, "  extends: %s\n", vErrs[0].Error())
	}
	l.components[uri] = merged
	return merged, true
}

// Feature mirrors Component for the Feature surface. Extends-resolution
// errors surface to os.Stderr per the same convention.
func (l *LazyLookup) Feature(uri string) (types.Feature, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.feature(uri)
}

// FeatureNames reports the metadata.name of every Feature the configured
// catalog directories declare, in sorted order.
//
// It enumerates the Tier-1 directory index and only that, so it reads the
// filesystem and never the network. A Feature reachable only from the
// artifact cache or from a registry has no entry in that index and
// is not reported — the honest bound, because enumerating those would mean
// fetching, and a caller asking what is already here must not be the thing
// that reaches out. Callers that need the whole surface resolve a URI they
// already hold through Feature instead.
//
// It reports names rather than entities on purpose. The name is the lookup
// coordinate, so a caller resolves each through Feature and the tier
// cascade, the extends merge and the miss bookkeeping all stay in the one
// place that implements them. Returning resolved entities here would be a
// second resolution path free to disagree with the first.
//
// Sorted, so the order is a function of the catalog's content rather than of
// directory-walk order, which varies between filesystems.
func (l *LazyLookup) FeatureNames() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildFeatureIndex()
	names := make([]string, 0, len(l.featureIndex))
	for name := range l.featureIndex {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// feature is the lock-free core of Feature, mirroring component.
func (l *LazyLookup) feature(uri string) (types.Feature, bool) {
	if f, ok := l.features[uri]; ok {
		return f, true
	}
	if _, ok := l.featureMisses[uri]; ok {
		return types.Feature{}, false
	}
	feat, ok := l.resolveFeature(uri)
	if !ok {
		l.featureMisses[uri] = struct{}{}
		return types.Feature{}, false
	}
	merged, vErrs := validator.ResolveFeatureExtends(feat, l.feature)
	if len(vErrs) > 0 {
		fmt.Fprintf(os.Stderr, "  extends: %s\n", vErrs[0].Error())
	}
	l.features[uri] = merged
	return merged, true
}

// resolveComponent walks the Tier-1/2/3 cascade and unmarshals the
// resolved bytes into a Component. Returns the unmerged entity;
// extends resolution is the caller's responsibility.
//
// Tier-1's walker already filters on kind=="Component" at index
// build time (only Component YAMLs land in componentIndex). Tier-2
// and Tier-3 do NOT pre-filter — the cache slot or registry artifact
// could in principle carry bytes of a different kind. Both unmarshal
// paths therefore include a kind check: a parsed entity whose
// Kind != "Component" is rejected, the cascade falls through to the
// next tier, and the lookup ultimately reports miss if no tier
// produces a Component-kind result. This matches catalog.Load's
// kind-routing semantic (indexFile dispatches by hdr.Kind).
// The contextless form is what the plain-function accessor calls, since that
// accessor's own signature carries none. **A Tier-3 fetch reached that way still
// runs to completion**: there is no caller context to hand on, and that bound is
// unchanged. resolveComponentContext below is where the cascade lives, so a
// caller that does hold a context runs the same walk under it and can end the
// fetch; the closure walk reaches the same primitive through the resolution
// surface, which does carry one.
func (l *LazyLookup) resolveComponent(uri string) (types.Component, bool) {
	return l.resolveComponentContext(context.Background(), uri)
}

// resolveComponentContext is resolveComponent's cascade, under a caller's
// context. Tier 3 is a registry pull, so ending the caller's run ends the fetch
// rather than leaving it to finish for an answer nobody is waiting for.
func (l *LazyLookup) resolveComponentContext(ctx context.Context, uri string) (types.Component, bool) {
	if comp, ok := l.tier1ReadComponent(uri); ok {
		return comp, true
	}
	if bytes, _, slot, outcome := l.tier2Verified(uri); outcome == tier2Use {
		// parseComponentEntity is the shared per-entity resolve; a non-Component
		// kind returns an error and the cascade falls through to Tier-3.
		if path, doc, ok := registryDocument(slot, bytes, uri); ok {
			if comp, err := parseComponentEntity(doc, path); err == nil {
				l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
				return comp, true
			}
		}
	} else if outcome == tier2Skip {
		// Cache drifted from the pin; diagnostic already emitted. Do not use the
		// drifted bytes and do not self-heal — the entity is unavailable for this
		// request (an honest degraded preview, not a false-green render).
		return types.Component{}, false
	}
	bytes, res, err := tier3PullCatalogEntity(ctx, l.workspaceRoot, l.cacheDir, l.puller, uri, "Component")
	if err == nil && bytes != nil {
		if path, doc, ok := registryDocument(tier3Slot(l.workspaceRoot, l.cacheDir, uri, res), bytes, uri); ok {
			if comp, perr := parseComponentEntity(doc, path); perr == nil {
				l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
				return comp, true
			}
		}
	}
	return types.Component{}, false
}

// registryDocument returns the entity document inside bytes a registry tier
// handed back, together with the path the document's parse is anchored at, and
// false when the artifact cannot be opened or laid out.
//
// The artifact is laid out in the slot it was read from or written to
// (unpackEntitySlot), and the path is the document's own place in that layout, so
// a path the entity declares relative to its document resolves inside what was
// published. With no slot — no cache directory is configured, so a fetch was
// written nowhere — there is nothing to lay the artifact out in, and the document
// is parsed against label, which names the entity rather than a directory.
//
// It speaks the shape these cascades speak: they signal by boolean and fall
// through to the next tier, so an error here has to become a miss rather than a
// halt. That is not a swallow — a tier that cannot yield the entity leaves the
// cascade to exhaust, and an entity the build actually needs then surfaces as a
// loud refusal at the guard layer, keyed to what the build asked for rather than
// to how one tier failed.
//
// The caller keeps the bytes it was given. The retained entityBytes entry is the
// whole artifact, because that is what the pinned digest covers; only the parse
// takes the document.
func registryDocument(slot string, published []byte, label string) (string, []byte, bool) {
	if slot == "" {
		document, err := oci.EntityYAML(published)
		return label, document, err == nil
	}
	path, document, err := unpackEntitySlot(slot, published)
	return path, document, err == nil
}

// resolveFeature mirrors resolveComponent for the Feature surface.
// The same kind-check guards Tier-2 + Tier-3 unmarshal paths against
// non-Feature bytes arriving from the cache or registry.
// It carries the same bound resolveComponent states: a Tier-3 fetch reached
// through the plain-function accessor runs to completion, because that accessor
// carries no caller context to hand on.
func (l *LazyLookup) resolveFeature(uri string) (types.Feature, bool) {
	return l.resolveFeatureContext(context.Background(), uri)
}

// resolveFeatureContext mirrors resolveComponentContext for the Feature surface.
func (l *LazyLookup) resolveFeatureContext(ctx context.Context, uri string) (types.Feature, bool) {
	if feat, ok := l.tier1ReadFeature(uri); ok {
		return feat, true
	}
	if bytes, _, slot, outcome := l.tier2Verified(uri); outcome == tier2Use {
		if path, doc, ok := registryDocument(slot, bytes, uri); ok {
			if feat, err := parseFeatureEntity(doc, path); err == nil {
				l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
				return feat, true
			}
		}
	} else if outcome == tier2Skip {
		// Cache drifted from the pin; diagnostic already emitted. Do not use the
		// drifted bytes and do not self-heal.
		return types.Feature{}, false
	}
	bytes, res, err := tier3PullCatalogEntity(ctx, l.workspaceRoot, l.cacheDir, l.puller, uri, "Feature")
	if err == nil && bytes != nil {
		if path, doc, ok := registryDocument(tier3Slot(l.workspaceRoot, l.cacheDir, uri, res), bytes, uri); ok {
			if feat, perr := parseFeatureEntity(doc, path); perr == nil {
				l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
				return feat, true
			}
		}
	}
	return types.Feature{}, false
}

// tier1ReadComponent looks up the URI in the (lazily-built) component
// index and reads + full-unmarshals the indexed file. Returns the
// unmerged Component on hit.
func (l *LazyLookup) tier1ReadComponent(uri string) (types.Component, bool) {
	l.buildComponentIndex()
	lookupName := lookupNameFromURI(uri)
	path, ok := l.componentIndex[lookupName]
	if !ok {
		return types.Component{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return types.Component{}, false
	}
	comp, perr := parseComponentEntity(data, path)
	if perr != nil {
		// The rejection is RECORDED, not discarded. A rejected file makes the
		// cascade report a miss, and a miss surfaces downstream as an
		// unresolvable entity — a symptom that names nothing an author can fix.
		// Keeping the reason means the surfaces that report the miss can say
		// which file was refused and why.
		l.recipeSkips[path] = perr
		return types.Component{}, false
	}
	l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = data
	return comp, true
}

// tier1ReadFeature mirrors tier1ReadComponent for the Feature surface,
// recording a rejection for the same reason.
func (l *LazyLookup) tier1ReadFeature(uri string) (types.Feature, bool) {
	l.buildFeatureIndex()
	lookupName := lookupNameFromURI(uri)
	path, ok := l.featureIndex[lookupName]
	if !ok {
		return types.Feature{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return types.Feature{}, false
	}
	feat, perr := parseFeatureEntity(data, path)
	if perr != nil {
		l.recipeSkips[path] = perr
		return types.Feature{}, false
	}
	l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = data
	return feat, true
}

// buildComponentIndex walks the catalog directories once (header-only
// YAML parse) and populates l.componentIndex with metadata.name →
// filesystem path mappings for every Component-kind YAML found.
// Idempotent: a second call is a no-op once componentIndexBuilt is
// true.
func (l *LazyLookup) buildComponentIndex() {
	if l.componentIndexBuilt {
		return
	}
	l.componentIndexBuilt = true
	l.walkAndIndex("Component", l.componentIndex)
}

// buildFeatureIndex mirrors buildComponentIndex for the Feature surface.
func (l *LazyLookup) buildFeatureIndex() {
	if l.featureIndexBuilt {
		return
	}
	l.featureIndexBuilt = true
	l.walkAndIndex("Feature", l.featureIndex)
}

// walkAndIndex walks every catalog directory recursively, header-
// unmarshals each *.yaml / *.yml file, and records files whose kind
// matches the target kind into the supplied index. Files whose kind
// does not match are skipped without indexing. The artifact cache is
// stepped over (SkipDirIfArtifactCache): a cache slot holds resolution
// artifacts, which are never authored catalog entities.
//
// Precedence across catalog directories is FIRST-WIN: catalogDirs is
// caller-ordered, so the earliest directory declaring a bare
// metadata.name claims it and a later directory re-declaring the same
// name is a shadow, reported and skipped. This is the same
// cross-occurrence decision the eager whole-catalog merge and the lazy
// recipe-shell enumeration make, folded through the shared foldFirstWin
// primitive so the strategies cannot drift on which duplicate resolves.
//
// Walk errors and per-file parse failures are silently ignored —
// the lazy lookup's contract is "find what you can"; broken files
// surface at full-unmarshal time when a specific URI is requested,
// not during the index walk.
func (l *LazyLookup) walkAndIndex(targetKind string, index map[string]string) {
	for _, dir := range l.catalogDirs {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return SkipDirIfArtifactCache(l.workspaceRoot, path, l.cacheDir)
			}
			ext := filepath.Ext(path)
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			var hdr fileHeader
			if uerr := yaml.Unmarshal(data, &hdr); uerr != nil {
				return nil
			}
			if hdr.Kind != targetKind {
				return nil
			}
			if hdr.Metadata.Name == "" {
				return nil
			}
			foldFirstWin(index, hdr.Metadata.Name, path, nil, func() {
				l.recordShadowedComponent(targetKind, hdr.Metadata.Name, path)
			})
			return nil
		})
	}
}

// buildSchemaLibraries walks the catalog directories once, parses + validates every
// SchemaLibrary through the shared per-entity resolve (parseSchemaLibraryEntity),
// and caches them in l.schemaLibraries. SchemaLibraries are consumed as a complete
// set for cross-document $ref expansion, so the whole set is resolved as a unit
// here (not per-URI on demand). Idempotent via schemaLibrariesBuilt.
//
// The artifact cache is stepped over (SkipDirIfArtifactCache). A cached library
// enters through the pinned Tier-2 fold below, where its digest and manifest are
// checked; reading it here instead would take it on trust, and — because this
// walk keys on the bare name and the fold below is first-win — would let an
// unpinned cache slot claim the name before the verified one is ever consulted.
//
// Returns the first validation error encountered, in catalog-walk order — the same
// rejection the eager catalog.Load surfaces, so the lazy backing does not silently
// accept a library the build rejects (reject-parity). Valid libraries are still
// cached so a best-effort caller (AsCatalogLookup) sees the resolvable set; a
// caller that must reject (the conformance test; a future serve gate) observes the
// returned error.
func (l *LazyLookup) buildSchemaLibraries() error {
	if l.schemaLibrariesBuilt {
		return nil
	}
	l.schemaLibrariesBuilt = true
	var firstErr error
	for _, dir := range l.catalogDirs {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return SkipDirIfArtifactCache(l.workspaceRoot, path, l.cacheDir)
			}
			ext := filepath.Ext(path)
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			var hdr fileHeader
			if uerr := yaml.Unmarshal(data, &hdr); uerr != nil || hdr.Kind != "SchemaLibrary" {
				return nil
			}
			lib, perr := parseSchemaLibraryEntity(data, path)
			if perr != nil {
				if firstErr == nil {
					firstErr = perr
				}
				// Tolerant: a broken schema library is skipped (recorded), not
				// fatal — a broken UNUSED library never fails an unrelated build.
				// The skip is recorded in the shared skip map (keyed by path, like
				// the recipe enumeration) so the daemon surfaces it as a
				// closure-level diagnostic when the closure references it.
				l.recipeSkips[path] = perr
				return nil
			}
			l.schemaLibraries[lib.Metadata.Name] = lib
			l.entityBytes[entityBytesKey("SchemaLibrary", lib.Metadata.Name)] = data
			l.origins[entityBytesKey("SchemaLibrary", lib.Metadata.Name)] = authoredOrigin(path)
			return nil
		})
	}
	// Tier-2: fold lockfile-pinned SchemaLibrary cache slots, after the Tier-1 walk
	// so a locally-authored library wins. Registry-pinned libraries live in the
	// cache, not the catalog dirs, so without this the lazy backing cannot see them.
	l.foldPinnedCacheEntities(map[string]bool{"SchemaLibrary": true})
	return firstErr
}

// LocalRecipePath returns the writable filesystem path a Tier-1 (catalog-
// directory) Recipe was authored in, keyed by its identity {target, name,
// version}, and whether one is recorded. A registry-resolved Recipe living in a
// cache slot returns ("", false), so the entity author's pin
// step writes only beside author-owned files. Triggers the Tier-1 index walk on
// first call.
//
// It hands back the path as the walk recorded it, because its caller writes to
// it. That is what separates it from LocalEntityPath, which renders the same
// value against the workspace root for a caller that reports it.
func (l *LazyLookup) LocalRecipePath(target, name, version string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.localPathFor("Recipe", target, name, version)
}

// LocalFeatureRecipePath is the FeatureRecipe analogue of LocalRecipePath.
func (l *LazyLookup) LocalFeatureRecipePath(target, name, version string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.localPathFor("FeatureRecipe", target, name, version)
}

// LocalEntityPath returns the document a resolved entity of kind was authored
// in, rendered against the workspace root, and whether one is recorded.
//
// scope, name and version are the entity's own identity as a by-name resolution
// reads it off the matched entity — scope being a Recipe's or FeatureRecipe's
// capability and a Plugin's plugin type, and empty for every kind that carries
// none. A caller hands back what it already holds, and the per-kind key formula
// stays here beside the walk that wrote it: spelled at a call site it would be a
// second statement of that formula, free to disagree the moment one kind's
// identity shape moves.
//
// Only a Tier-1 (catalog-directory) entity has one. An entity resolved from a
// registry lives in a cache slot, which is not a document anybody authored, so
// it is reported as no path rather than as the slot's location.
//
// The Aggregate is deliberately not answered here. Its own accessor already
// carries the document it was loaded from, and answering it here as well would
// give one fact two accessors, free to drift apart. A kind this does not recognise
// reports no path, which is what an unenumerated kind honestly has.
//
// It triggers whichever walk holds the kind on first call. Every accessor that
// resolves an entity of that kind has already run the same walk, so a caller
// asking about a match it has just resolved pays nothing for the answer.
func (l *LazyLookup) LocalEntityPath(kind, scope, name, version string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.localPathFor(kind, scope, name, version)
	if !ok || p == "" {
		return "", false
	}
	return RelativeToWorkspace(l.workspaceRoot, p), true
}

// localPathFor is the one per-kind lookup both the writable-path accessors and
// LocalEntityPath read: which index holds the kind, and the key it is held
// under. It hands back the path as the walk recorded it; rendering it for a
// reader is the reporting accessor's own step.
//
// For a folded kind the answer is the authored document the record of where the
// entity came from holds, so an entity read from a pinned cache slot answers no
// path from the same value that makes a closure walk report it as cache-served.
//
// The caller holds l.mu. Each arm triggers the walk that populates its own index
// rather than the caller choosing one, so a kind added to an index cannot be
// looked up before that index exists.
func (l *LazyLookup) localPathFor(kind, scope, name, version string) (string, bool) {
	switch kind {
	case "Component":
		l.buildComponentIndex()
		p, ok := l.componentIndex[name]
		return p, ok
	case "Feature":
		l.buildFeatureIndex()
		p, ok := l.featureIndex[name]
		return p, ok
	case "Recipe", "FeatureRecipe":
		l.buildRecipeIndex()
		return l.origins[entityBytesKey(kind, recipeIdentityKey(scope, name, version))].authoredDocument()
	case "Plugin":
		l.buildRecipeIndex()
		return l.origins[entityBytesKey(kind, entityIndexKey(types.PluginCoordinate(scope, name), version))].authoredDocument()
	case "MappingLibrary", "WorkloadLibrary", "ArtifactLibrary":
		l.buildRecipeIndex()
		return l.origins[entityBytesKey(kind, name)].authoredDocument()
	case "SchemaLibrary":
		_ = l.buildSchemaLibraries()
		return l.origins[entityBytesKey(kind, name)].authoredDocument()
	}
	return "", false
}

// buildRecipeIndex walks the catalog directories once and TOLERANTLY enumerates
// every recipe-shell kind — the set foldRecipeShellEntity dispatches on — into
// the hot maps, keyed exactly as the eager catalog keys them (recipes/FRs by
// provider/name:version, Plugins by name:version, aggregates/libraries by
// metadata.name). A file that
// won't parse — or whose recipe/FR identity
// collides with one already indexed — is skipped with a one-line stderr warning
// and a recorded reason; it never aborts the walk. This is the robustness crux:
// a broken UNUSED recipe is invisible to a resolution that doesn't need it, where
// the eager catalog.Load fails the whole walk on the first broken recipe.
//
// The artifact cache is stepped over (SkipDirIfArtifactCache). A cached entity
// enters through the pinned Tier-2 fold below, under digest and manifest
// verification. Reading it here instead would both bypass that check and — since
// a walk visits "." before any letter or digit — let the cached copy claim a
// name ahead of the author's own file, which would then be reported as the
// shadowed duplicate.
//
// Determinism: filepath.WalkDir visits lexically and catalogDirs is caller-
// ordered, so identity-collision shadowing (first-wins) is stable across runs;
// the matcher's own sort.Strings over candidate identities does the rest. Parsing
// every recipe body (rather than headers only) forfeits the local lazy-CPU saving
// but keeps a single recipe-resolution path feeding both the point-lookup
// (RecipeByURI) and the implements search — no second parse that could drift.
// Idempotent via recipeIndexBuilt; Reset clears it.
func (l *LazyLookup) buildRecipeIndex() {
	if l.recipeIndexBuilt {
		return
	}
	l.recipeIndexBuilt = true
	for _, dir := range l.catalogDirs {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return SkipDirIfArtifactCache(l.workspaceRoot, path, l.cacheDir)
			}
			ext := filepath.Ext(path)
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			var hdr fileHeader
			if uerr := yaml.Unmarshal(data, &hdr); uerr != nil {
				return nil
			}
			l.foldRecipeShellEntity(hdr.Kind, data, data, path, "")
			return nil
		})
	}
	// Tier-2: fold every lockfile-pinned cache slot of the recipe-shell kinds,
	// under the SAME verify-on-read / pin discipline resolveComponent uses. A
	// registry build's recipes / FeatureRecipes / aggregates / mapping
	// libraries live in the pinned cache, not the catalog dirs — this is what makes
	// the lazy backing see them. Runs AFTER the Tier-1 walk so a locally-authored
	// entity wins on a name/identity collision.
	l.foldPinnedCacheEntities(map[string]bool{
		"Recipe": true, "FeatureRecipe": true, "Aggregate": true, "MappingLibrary": true, "WorkloadLibrary": true, "ArtifactLibrary": true, "Plugin": true,
	})
}

// foldRecipeShellEntity parses one enumerated recipe-shell entity (Recipe,
// FeatureRecipe, Aggregate, MappingLibrary, WorkloadLibrary, ArtifactLibrary, or Plugin) and folds it into the hot
// maps, keyed exactly as the eager catalog keys it, first-wins on a name/identity
// collision (a later duplicate is skipped and recorded). parseData is the YAML to
// parse; rawBytes is retained as entityBytes for digest fidelity — identical to
// parseData for a Tier-1 catalog file, the whole tar.gz bundle for a Tier-2 recipe
// cache slot; src labels the source for skip diagnostics. The single fold path
// feeds both the Tier-1 walk and the pinned Tier-2 pass, so the two cannot drift.
//
// slot is the cache slot a pinned entity was read from, and empty for a Tier-1
// file; it says which of those two the entity arrived through, and this is the one
// step that knows. For the winning entry of every kind it admits, the fold records
// that fact once, in origins: the document a Tier-1 file was authored in, or the
// slot a pinned entity was read from together with its label and, for a Recipe or
// FeatureRecipe, the document its parse was anchored at in the slot's layout. A
// Tier-1 file's src is a document on this machine, while a pinned slot's src is a
// label naming the pin rather than a path anybody could open, so the bytes are
// retained either way — the digest covers both — and every answer that needs a
// document (the pin step's writable home, a report of where the entity was
// declared, a co-located artifact's directory) is derived from the record rather
// than from an index kept beside it. A closure walk over the index reports the
// tier from the same record, so an entry read from a slot is reported as resolved
// from the cache and not as authored in a catalog, whatever its kind.
//
// It also decides what a Recipe's or FeatureRecipe's parse is anchored at, those
// being the kinds whose document names content by a path relative to itself. A
// Tier-1 file is parsed at its own path. A pinned slot's artifact is laid out in
// the slot first (unpackEntitySlot) and parsed at its document's place there, so
// the source and imports it names resolve inside what was published; src stays
// the label every skip is recorded under. A pinned slot's document is parsed alone:
// the lock its publication carries is laid out beside it and not read, because
// pins are recorded only for a catalog-directory entity and a pulled one's sources
// are held to the Release's lockfile. Every other kind is parsed at src: none of
// them names content by a path, and an Aggregate's src is its source provenance,
// which a layout path would move.
func (l *LazyLookup) foldRecipeShellEntity(kind string, parseData, rawBytes []byte, src, slot string) {
	local := slot == ""
	switch kind {
	case "Recipe":
		docPath, lerr := contentAnchor(src, slot, rawBytes)
		if lerr != nil {
			l.recordRecipeSkip(src, lerr)
			return
		}
		var e types.Recipe
		var pins recipepin.Pins
		var perr error
		if local {
			e, pins, perr = parseRecipeEntity(parseData, docPath)
		} else {
			e, perr = resolveRecipeDocument(parseData, docPath)
		}
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		key := recipeIdentityKey(e.Spec.Capability, e.Metadata.Name, e.Metadata.Version)
		foldFirstWin(l.recipes, key, e,
			func() {
				l.entityBytes[entityBytesKey("Recipe", key)] = rawBytes
				l.origins[entityBytesKey("Recipe", key)] = foldedOrigin(src, slot, docPath)
				if local {
					l.recordedPins.Record(src, pins)
				}
			},
			func() {
				l.recordRecipeSkip(src, fmt.Errorf("duplicate recipe identity %q; this file is shadowed", key))
			})
	case "FeatureRecipe":
		docPath, lerr := contentAnchor(src, slot, rawBytes)
		if lerr != nil {
			l.recordRecipeSkip(src, lerr)
			return
		}
		var e types.FeatureRecipe
		var pins recipepin.Pins
		var perr error
		if local {
			e, pins, perr = parseFeatureRecipeEntity(parseData, docPath)
		} else {
			e, perr = resolveFeatureRecipeDocument(parseData, docPath)
		}
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		key := recipeIdentityKey(e.Spec.Capability, e.Metadata.Name, e.Metadata.Version)
		foldFirstWin(l.featureRecipes, key, e,
			func() {
				l.entityBytes[entityBytesKey("FeatureRecipe", key)] = rawBytes
				l.origins[entityBytesKey("FeatureRecipe", key)] = foldedOrigin(src, slot, docPath)
				if local {
					l.recordedPins.Record(src, pins)
				}
			},
			func() {
				l.recordRecipeSkip(src, fmt.Errorf("duplicate feature recipe identity %q; this file is shadowed", key))
			})
	case "Aggregate":
		e, perr := parseAggregateEntity(parseData, src)
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		foldFirstWin(l.aggregates, e.Metadata.Name, e,
			func() {
				l.origins[entityBytesKey("Aggregate", e.Metadata.Name)] = foldedOrigin(src, slot, "")
				l.entityBytes[entityBytesKey("Aggregate", e.Metadata.Name)] = rawBytes
			},
			func() {
				l.recordRecipeSkip(src, fmt.Errorf("duplicate aggregate name %q; this file is shadowed", e.Metadata.Name))
			})
	case "MappingLibrary":
		e, perr := parseMappingLibraryEntity(parseData, src)
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		foldFirstWin(l.mappingLibraries, e.Metadata.Name, e,
			func() {
				l.entityBytes[entityBytesKey("MappingLibrary", e.Metadata.Name)] = rawBytes
				l.origins[entityBytesKey("MappingLibrary", e.Metadata.Name)] = foldedOrigin(src, slot, "")
			},
			func() {
				l.recordRecipeSkip(src, fmt.Errorf("duplicate mapping library name %q; this file is shadowed", e.Metadata.Name))
			})
	case "WorkloadLibrary":
		e, perr := parseWorkloadLibraryEntity(parseData, src)
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		foldFirstWin(l.workloadLibraries, e.Metadata.Name, e,
			func() {
				l.entityBytes[entityBytesKey("WorkloadLibrary", e.Metadata.Name)] = rawBytes
				l.origins[entityBytesKey("WorkloadLibrary", e.Metadata.Name)] = foldedOrigin(src, slot, "")
			},
			func() {
				l.recordRecipeSkip(src, fmt.Errorf("duplicate workload library name %q; this file is shadowed", e.Metadata.Name))
			})
	case "ArtifactLibrary":
		e, perr := parseArtifactLibraryEntity(parseData, src)
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		foldFirstWin(l.artifactLibraries, e.Metadata.Name, e,
			func() {
				l.entityBytes[entityBytesKey("ArtifactLibrary", e.Metadata.Name)] = rawBytes
				l.origins[entityBytesKey("ArtifactLibrary", e.Metadata.Name)] = foldedOrigin(src, slot, "")
			},
			func() {
				l.recordRecipeSkip(src, fmt.Errorf("duplicate artifact library name %q; this file is shadowed", e.Metadata.Name))
			})
	case "Plugin":
		e, perr := parsePluginEntity(parseData, src)
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		key := entityIndexKey(e.Coordinate(), e.Metadata.Version)
		foldFirstWin(l.plugins, key, e,
			func() {
				l.entityBytes[entityBytesKey("Plugin", e.Coordinate())] = rawBytes
				l.origins[entityBytesKey("Plugin", key)] = foldedOrigin(src, slot, "")
			},
			func() {
				l.recordRecipeSkip(src, fmt.Errorf("duplicate wasm plugin identity %q; this file is shadowed", key))
			})
	}
}

// foldPinnedCacheEntities folds every lockfile-pinned Tier-2 cache slot whose kind
// is in allowed into the enumerated maps, under the SAME verify-on-read / pin
// discipline resolveComponent uses (tier2ReadVerified). The pin set (SetPins) is
// the lockfile's URI→digest map; the cache is keyed by URI, not walkable like the
// Tier-1 dirs, so enumeration is pin-driven. Bundled slots are tar.gz archives:
// the bundle is unwrapped to parse the entity YAML, but the WHOLE bundle blob is
// retained as entityBytes so the pinned digest covers the bundle (parity with the
// cache→catalog bridge). A Recipe's or FeatureRecipe's slot is also laid out from
// that verified blob and parsed at its document's place in the layout, so the
// files it carries are read from what was published (foldRecipeShellEntity).
// Deterministic: pins are visited in sorted URI order, so first-wins shadowing
// among pinned duplicates is stable across runs.
//
// # Two skips, and only one of them is silent
//
// A pinned slot that is absent, drifted, or unreadable is skipped without a
// diagnostic, and each of those three has its own reason to stay quiet: an absent
// slot is the ordinary state of a pin resolved at Tier-1, a drift is already
// reported by the closure-level lockfile verify on the same request, and an
// unreadable slot leaves nothing to report about. An entity the build actually
// needs that goes missing this way surfaces as a loud halt at the guard layer.
//
// A slot that is present, verified against its pin, and then cannot be unwrapped
// is a different outcome and is RECORDED. The artifact is there and is the bytes
// the lockfile committed to; what failed is reading an entity out of it. Left
// silent it is indistinguishable from the entity never having been pinned, which
// is a claim about the lockfile rather than about the artifact — so it is skipped
// through the same channel every other unindexable document uses, keyed by the
// pinned reference, and reaches a caller through SkippedEntities.
func (l *LazyLookup) foldPinnedCacheEntities(allowed map[string]bool) {
	uris := make([]string, 0, len(l.pins))
	for uri := range l.pins {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	for _, uri := range uris {
		pin := l.pins[uri]
		// A bare-URI pin (a sub-aggregate / library referenced by bare name, pinned
		// with a separate ResolvedVersion) addresses its versioned cache slot via
		// versionedSlotURI — the same key the cache→catalog bridge reads.
		slotURI := versionedSlotURI(uri, pin.ResolvedVersion)
		blob, err := tier2ReadVerified(l.workspaceRoot, l.cacheDir, slotURI, pin.Digest, pin.ManifestDigest)
		if err != nil {
			continue
		}
		// The verified read above succeeded through this same derivation, so the slot
		// is there; an empty one would fold a pinned entity as a catalog-directory
		// file, which is why the case is skipped rather than passed on.
		slot, _, ok := tier2Slot(l.workspaceRoot, l.cacheDir, slotURI)
		if !ok {
			continue
		}
		parseData, uerr := oci.EntityYAML(blob)
		if uerr != nil {
			l.recordRecipeSkip("lockfile-pinned:"+uri, uerr)
			continue
		}
		kind := peekEntityKind(parseData)
		if !allowed[kind] {
			continue
		}
		if kind == "SchemaLibrary" {
			l.foldPinnedSchemaLibrary(parseData, blob, "lockfile-pinned:"+uri, slot)
			continue
		}
		l.foldRecipeShellEntity(kind, parseData, blob, "lockfile-pinned:"+uri, slot)
	}
}

// contentAnchor returns the path a Recipe's or FeatureRecipe's parse anchors the
// entity's content paths at: src itself for a catalog-directory file, which slot
// is empty for, and otherwise the entity's document in the layout of the slot's
// verified artifact, rebuilt from rawBytes by unpackEntitySlot.
func contentAnchor(src, slot string, rawBytes []byte) (string, error) {
	if slot == "" {
		return src, nil
	}
	path, _, err := unpackEntitySlot(slot, rawBytes)
	return path, err
}

// foldPinnedSchemaLibrary folds one lockfile-pinned SchemaLibrary cache slot into
// l.schemaLibraries, first-wins (a Tier-1 library of the same name already present
// is kept). Mirrors the SchemaLibrary arm of buildSchemaLibraries' Tier-1 walk; the
// raw bytes are retained for digest fidelity, and the library placed is recorded as
// read from slot under the label src, so it is answered as cache-served rather than
// as authored in a catalog directory.
func (l *LazyLookup) foldPinnedSchemaLibrary(parseData, rawBytes []byte, src, slot string) {
	lib, perr := parseSchemaLibraryEntity(parseData, src)
	if perr != nil {
		l.recipeSkips[src] = perr
		return
	}
	if _, dup := l.schemaLibraries[lib.Metadata.Name]; dup {
		return
	}
	l.schemaLibraries[lib.Metadata.Name] = lib
	l.entityBytes[entityBytesKey("SchemaLibrary", lib.Metadata.Name)] = rawBytes
	l.origins[entityBytesKey("SchemaLibrary", lib.Metadata.Name)] = pinnedOrigin(src, slot, "")
}

// recordRecipeSkip emits the one-line tolerance warning and records the reason.
//
// The key is where the refused document was read from, because a document that
// could not be parsed has no reliable identity to key on: a file path for a
// catalog-directory document, and the pinned reference for a lockfile-pinned
// cache slot, which is not a file anyone authored. SkippedEntities recovers what
// identity it can from each and treats an unreadable key as the answer rather
// than as a gap.
//
// One source is warned about once. The pinned-slot fold runs from two builders
// over two different kind sets, and a slot refused before its kind can be read is
// refused by both — so a reader would meet one artifact's failure twice while the
// record held the single entry it always held. The record is the thing that
// matters and is written unconditionally; the warning follows it.
func (l *LazyLookup) recordRecipeSkip(source string, err error) {
	if _, already := l.recipeSkips[source]; !already {
		fmt.Fprintf(os.Stderr, "  recipe: %s skipped: %s\n", source, err.Error())
	}
	l.recipeSkips[source] = err
}

// recordShadowedComponent emits the one-line first-win notice when a later
// catalog directory re-declares a Component/Feature identity an earlier
// directory already claimed. First-win precedence keeps the earlier occurrence;
// the shadowed path is reported so the override is never silent. Stderr-only,
// mirroring the recipe-shell skip diagnostic's form.
func (l *LazyLookup) recordShadowedComponent(kind, name, path string) {
	fmt.Fprintf(os.Stderr, "  catalog: %s %q at %s is shadowed (first-win precedence)\n", kind, name, path)
}

// recipeCatalog returns the cached *Catalog shell over the tolerantly-enumerated
// recipe / FeatureRecipe / Aggregate maps. The shell holds only the fields the
// matcher methods read, so RecipeLookupWithManifest, VerifyRecipeResolution, and
// SubAggregateLoader run verbatim against the lazy set. Built on first call;
// nil-and-rebuilt after Reset so an edited workspace re-walks.
func (l *LazyLookup) recipeCatalog() *Catalog {
	if l.recipeCat != nil {
		return l.recipeCat
	}
	l.buildRecipeIndex()
	_ = l.buildSchemaLibraries()
	l.recipeCat = &Catalog{
		Recipes:           l.recipes,
		FeatureRecipes:    l.featureRecipes,
		Aggregates:        l.aggregates,
		SchemaLibraries:   l.schemaLibraries,
		MappingLibraries:  l.mappingLibraries,
		WorkloadLibraries: l.workloadLibraries,
		ArtifactLibraries: l.artifactLibraries,
		Plugins:           l.plugins,
		entityBytes:       l.entityBytes,
		// The one record of where each enumerated entity came from, under the same
		// kind-namespaced identity the eager load claims each entity under — so the
		// shell answers "which file declared this entity", "what are its content
		// paths anchored at", "which tier served it" and "what source does a
		// flattened Aggregate record" from the fold that placed the entity, rather
		// than from records kept beside it and free to disagree. The shell carries no
		// pathByIdentity or aggregateSources of its own: every entity it indexes has
		// a record here.
		origins: l.origins,
		// The pins the same walk recorded, keyed by each entity's authored document,
		// so the shell answers "what was recorded for this entity" from the reading
		// that resolved it rather than from a second one free to disagree. An entity
		// read from a pinned slot is absent here: its slot is a label naming a pin,
		// and a lock sits beside a document.
		recordedPins:  l.recordedPins,
		workspaceRoot: l.workspaceRoot,
		// The lazy backing is the production build/pull resolution path, so it
		// resolves core providers from the binary's embedded set (embedded-wins).
		resolveEmbeddedProviders: true,
	}
	return l.recipeCat
}

// MappingLibraries returns the tolerantly-enumerated MappingLibrary set, for the
// daemon pipeline's MappingLibraries input (the closure/bridged mapping libraries
// the renderer's $import resolution consumes). Triggers the recipe-shell
// enumeration on first call.
func (l *LazyLookup) MappingLibraries() map[string]types.MappingLibrary {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildRecipeIndex()
	return l.mappingLibraries
}

// WorkloadLibraries returns the WorkloadLibrary set, keyed by bare name, for the
// pipeline's WorkloadLibraries input (the set the renderer's workload
// `$import: <Library>#/workloads/<entry>` resolution consumes). The set is the
// local catalog directories plus every lockfile-pinned remote WorkloadLibrary
// bridged in from the Tier-2 cache, so a remote workload import resolves through
// the same map as a local one. Triggers the recipe-shell enumeration on first call.
func (l *LazyLookup) WorkloadLibraries() map[string]types.WorkloadLibrary {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildRecipeIndex()
	return l.workloadLibraries
}

// ArtifactLibraries returns the ArtifactLibrary set, keyed by bare name, for the
// pipeline's ArtifactLibraries input (the set the renderer's artifacts
// `artifacts.<name>: { $import: <Library>#/artifacts/<entry> }` resolution consumes).
// The set is the local catalog directories plus every lockfile-pinned remote
// ArtifactLibrary bridged in from the Tier-2 cache, so a remote artifacts import
// resolves through the same map as a local one. Triggers the recipe-shell
// enumeration on first call.
func (l *LazyLookup) ArtifactLibraries() map[string]types.ArtifactLibrary {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildRecipeIndex()
	return l.artifactLibraries
}

// RecordedPins returns the content pins in force for every pin-bearing entity
// the lazy enumeration reached, keyed by the document each was read from — the
// render's input for comparing what a build acquired against what the resolution
// recorded. Triggers the recipe-shell enumeration on first call.
//
// It answers through the recipe-catalog shell rather than from the backing's own
// map, so the lazy and eager views expose one reading of the same fact.
func (l *LazyLookup) RecordedPins() recipepin.Lookup {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().RecordedPins()
}

// RecipeLookupWithManifest exposes the catalog matcher's Manifest-aware recipe
// resolution over the lazily-enumerated recipe set. Resolution is mapping-only:
// the Manifest mapping tiers resolve by URI (point-lookup); an unmapped instance
// is a miss — the catalog never chooses a recipe by spec.implements.
func (l *LazyLookup) RecipeLookupWithManifest(manifest *types.Manifest) renderer.RecipeLookup {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().RecipeLookupWithManifest(manifest)
}

// ResolutionFindings runs the shared resolution-completeness producer over the
// tolerantly-enumerated (complete) recipe set, returning one error per instance
// that does not resolve through an explicit Manifest mapping. The serve daemon
// maps these to resilient diagnostics; the slice is identical to the CLI build's
// halt input (VerifyRecipeResolution joins it), so both surfaces enumerate pin
// candidates by the same logic over the same complete recipe set.
func (l *LazyLookup) ResolutionFindings(manifest *types.Manifest, graph *types.IRGraph) []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().ResolutionFindings(manifest, graph)
}

// SubAggregateLoader exposes the catalog's sub-aggregate loader over the lazily-
// enumerated Aggregate set. A referenced sub-aggregate that is absent (or was
// skipped as broken) is a fatal graph-construction failure, exactly as eager.
func (l *LazyLookup) SubAggregateLoader() topology.SubAggregateLoader {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().SubAggregateLoader()
}

// AggregateLookup exposes the catalog's Release-triad aggregate resolver over the
// lazily-enumerated (Tier-1 + pinned Tier-2) Aggregate set, so a build's top-level
// aggregate resolves from the closure directly — no cache→catalog bridge. A
// registry-published top-level resolves through the pinned Tier-2 enumeration the
// same way a local one resolves through Tier-1.
func (l *LazyLookup) AggregateLookup() resolver.AggregateLookup {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().AggregateLookup()
}

// VerifyManifestImplementsConsistency runs the catalog's manifest-implements
// consistency gate (each mapped type resolves to a recipe whose spec.implements
// matches, directly or through a base the mapped type extends) over the
// lazily-enumerated recipe set, reading the closure rather than a bridged eager
// catalog, and returns one finding per inconsistent mapping.
//
// It returns findings rather than a joined error because the verdict is a
// caller's to reach: a surface that produces a deployable artifact halts on a
// finding, while a surface that reports on a document enumerates it alongside its
// other findings. A gate that could only fail would force the reporting surfaces
// to choose between staying silent and replacing their whole report with one
// line.
//
// The ancestry resolver is this view's own, NOT the recipe shell's. The shell
// carries recipes and libraries but no Component index, so the shell's own
// resolver would find no ancestry and the gate would refuse every mapping onto a
// base's Recipe — silently, and only on this path.
func (l *LazyLookup) VerifyManifestImplementsConsistency(manifest *types.Manifest, manifestPath string) []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().VerifyManifestImplementsConsistency(manifest, manifestPath, l.componentAncestry)
}

// AssertManifestImplementsConsistency joins the findings
// VerifyManifestImplementsConsistency produces into a single error, nil when
// every mapping is consistent. It is the halt form for a caller that reaches its
// verdict from the gate alone; a caller that carries the findings onward reads
// them from the verify form instead. The two share one producer, so a halting and
// a reporting surface can never come to disagree about which mappings are
// inconsistent.
func (l *LazyLookup) AssertManifestImplementsConsistency(manifest *types.Manifest, manifestPath string) error {
	return errors.Join(l.VerifyManifestImplementsConsistency(manifest, manifestPath)...)
}

// componentAncestry is this view's ComponentAncestryResolver: it walks the
// extends chain over the Tier-1/2/3 entity cascade, which is where this
// backing's Components live.
//
// The walk reads the RAW cascade rather than the extends-merged accessor, for two
// reasons. Ancestry is a property of the authored extends links, and the merge
// preserves those unchanged, so the raw entity answers the question exactly as
// the merged one would. And the merged accessor resolves each ancestor by running
// the merge on it, so a circular chain would recurse back through the merge
// instead of terminating on the chain walk's own visited set — this surface runs
// before graph construction, early enough to be the first reader of a malformed
// chain, so it reports the cycle rather than exhausting the stack on it.
//
// Lock-free by contract, like the other internal cascade readers: the exported
// verify form above already holds l.mu when it hands this method over, and the
// assert form reaches the cascade only through it, so both entries arrive here
// under the lock.
func (l *LazyLookup) componentAncestry(componentType string) ([]string, error) {
	return componentAncestryOver(componentType, l.resolveComponent)
}

// VerifyRecipeResolution exposes the build-side resolution-completeness halt (the
// joined form of ResolutionFindings) over the lazily-enumerated recipe set, so the
// build's pre-render gate reads the closure's complete recipe enumeration.
func (l *LazyLookup) VerifyRecipeResolution(manifest *types.Manifest, graph *types.IRGraph) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().VerifyRecipeResolution(manifest, graph)
}

// RecipeByURI / FeatureRecipeByURI resolve a recipe / feature-recipe by its
// catalog URI over the lazily-enumerated (Tier-1 + pinned Tier-2) recipe set, for
// the closure-library discovery's mapping-consumer scan.
func (l *LazyLookup) RecipeByURI(uri string) (types.Recipe, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().RecipeByURI(uri)
}

func (l *LazyLookup) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().FeatureRecipeByURI(uri)
}

// PluginByURI resolves a Plugin entity by its catalog URI over the
// lazily-enumerated (Tier-1 + pinned Tier-2) set. It delegates to the same
// *Catalog matcher the eager backing uses (via the recipeCatalog() shell), so the
// two backings resolve identically — no per-backing matcher.
func (l *LazyLookup) PluginByURI(uri string) (types.Plugin, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().PluginByURI(uri)
}

// PluginFromCacheByURI resolves a Plugin entity from the Tier-2 content cache by
// its entity URI WITHOUT a lockfile pin — the cache-not-pin read a verdict socket
// (policy / linter) uses. A verdict-socket plugin is cached at pull but never
// pinned or drift-gated, so it is absent from the lockfile pin set and the
// pin-driven Tier-2 fold does not surface it; this reads its cache slot directly.
//
// The chain is: entity slot (uri, version) → cached OCI manifest (the root of
// trust) → verify the config blob against the manifest → parse the entity → the
// .wasm layer's content digest from the manifest (the key the artifact cache slot
// is addressed by). It returns the parsed entity and that .wasm content digest.
// ok is false on a clean cache miss (an unset cache dir, an unversioned URI, or an
// absent slot / manifest); a non-nil error is a manifest-verification or parse
// failure that must halt rather than fall through.
func (l *LazyLookup) PluginFromCacheByURI(uri string) (types.Plugin, string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cacheDir == "" {
		return types.Plugin{}, "", false, nil
	}
	_, version, hasV := resolver.SplitVersionedURI(uri)
	if !hasV || version == "" {
		return types.Plugin{}, "", false, nil
	}
	entityBytes, err := oci.ReadCached(l.workspaceRoot, l.cacheDir, uri, version)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound()) {
			return types.Plugin{}, "", false, nil
		}
		return types.Plugin{}, "", false, fmt.Errorf("catalog: read cached plugin entity %s: %w", uri, err)
	}
	manifestBytes, merr := oci.ReadCachedManifest(l.workspaceRoot, l.cacheDir, uri, version)
	if merr != nil {
		// A content blob with no persisted manifest is an incomplete slot; treat it
		// as a miss so the caller surfaces the pull-first remediation rather than
		// trusting a half-populated slot.
		if errors.Is(merr, oci.ErrFetchNotFound()) {
			return types.Plugin{}, "", false, nil
		}
		return types.Plugin{}, "", false, fmt.Errorf("catalog: read cached plugin manifest %s: %w", uri, merr)
	}
	// Manifest root of trust: the cached entity (the manifest's CONFIG blob) is
	// trusted only against the descriptor the manifest commits to.
	if err := oci.VerifyConfigAgainstManifest(manifestBytes, entityBytes); err != nil {
		return types.Plugin{}, "", false, fmt.Errorf("catalog: cached plugin entity %s: %w", uri, err)
	}
	entity, perr := parsePluginEntity(entityBytes, "cache:"+uri)
	if perr != nil {
		return types.Plugin{}, "", false, fmt.Errorf("catalog: parse cached plugin entity %s: %w", uri, perr)
	}
	wasmDigest, werr := oci.PluginWasmLayerDigest(manifestBytes)
	if werr != nil {
		return types.Plugin{}, "", false, fmt.Errorf("catalog: cached plugin manifest %s: %w", uri, werr)
	}
	return entity, wasmDigest, true, nil
}

// ProviderPluginByName resolves a provider binding (bare name or version-pinned
// form) to the provider-rendering Plugin entity serving it (via its
// targets and version constraint) over the lazily-enumerated
// (Tier-1 + pinned Tier-2) Plugin set. It delegates to the same *Catalog matcher
// the eager backing uses (via the recipeCatalog() shell), so the two backings
// resolve identically — no per-backing matcher.
func (l *LazyLookup) ProviderPluginByName(ref string) (types.Plugin, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().ProviderPluginByName(ref)
}

// ParserPluginForFormat resolves a source format to the parser Plugin
// advertising parse:<format> over the lazily-enumerated (Tier-1 + pinned Tier-2)
// Plugin set, so a socket that must parse a foreign artifact resolves the same
// format→parser binding from the lazy backing as the eager catalog does. It
// halts loudly on an unadvertised or ambiguous format.
func (l *LazyLookup) ParserPluginForFormat(format string) (types.Plugin, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().ParserPluginForFormat(format)
}

// ProviderPluginCandidates returns every provider-rendering Plugin serving the
// bare capability over the lazily-enumerated (Tier-1 + pinned Tier-2) Plugin
// set, sorted deterministically, for the ambiguity diagnostic and the interactive
// pull binding-helper. Delegates to the same *Catalog matcher the eager backing
// uses.
func (l *LazyLookup) ProviderPluginCandidates(target string) []types.Plugin {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().ProviderPluginCandidates(target)
}

// ProviderPluginForTarget resolves a capability to its provider-rendering
// Plugin entity honoring an explicit plugins.providers selection, over the
// lazily-enumerated (Tier-1 + pinned Tier-2) Plugin set. It delegates to the same
// *Catalog matcher the eager backing uses (via the recipeCatalog() shell), so the
// two backings resolve identically — no per-backing matcher. Pull passes the
// workspace-config bindings; build passes the lockfile-derived bindings.
func (l *LazyLookup) ProviderPluginForTarget(ref string, bindings map[string]string) (types.Plugin, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().ProviderPluginForTarget(ref, bindings)
}

// ClosureRecipeLoader exposes the source-descent recipe loader over the lazily-
// enumerated (Tier-1 + pinned Tier-2) recipe set, so pull's lock-time source
// descent loads a registry-resolved Recipe / FeatureRecipe from the pinned cache.
func (l *LazyLookup) ClosureRecipeLoader() closure.RecipeLoader {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().ClosureRecipeLoader()
}

// SchemaLibrary / MappingLibrary / WorkloadLibrary resolve a library by its
// bare-name URI over the lazily-walked (Tier-1 + pinned Tier-2) library sets, for
// the closure-library discovery's transitive body access. Returns ok=false when
// the library is absent.
func (l *LazyLookup) SchemaLibrary(uri string) (types.SchemaLibrary, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.buildSchemaLibraries()
	lib, ok := l.schemaLibraries[uri]
	return lib, ok
}

func (l *LazyLookup) MappingLibrary(uri string) (types.MappingLibrary, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildRecipeIndex()
	lib, ok := l.mappingLibraries[uri]
	return lib, ok
}

func (l *LazyLookup) WorkloadLibrary(uri string) (types.WorkloadLibrary, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildRecipeIndex()
	lib, ok := l.workloadLibraries[uri]
	return lib, ok
}

func (l *LazyLookup) ArtifactLibrary(uri string) (types.ArtifactLibrary, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildRecipeIndex()
	lib, ok := l.artifactLibraries[uri]
	return lib, ok
}

// LibraryName returns the declared name of an in-closure library on the given
// axis, so a caller that treats the axes alike can resolve one without naming
// which axis it is holding.
//
// This is the single place the axis set is bound to the per-kind accessors above.
// A caller that instead reached for those accessors directly would have to name
// each axis, and naming them is exactly how a caller comes to cover some and not
// others.
func (l *LazyLookup) LibraryName(axis closure.LibraryAxis, uri string) (string, bool) {
	switch axis.Kind {
	case "SchemaLibrary":
		lib, ok := l.SchemaLibrary(uri)
		return lib.Metadata.Name, ok
	case "MappingLibrary":
		lib, ok := l.MappingLibrary(uri)
		return lib.Metadata.Name, ok
	case "WorkloadLibrary":
		lib, ok := l.WorkloadLibrary(uri)
		return lib.Metadata.Name, ok
	case "ArtifactLibrary":
		lib, ok := l.ArtifactLibrary(uri)
		return lib.Metadata.Name, ok
	}
	return "", false
}

// Skips returns the tolerant-enumeration skip reasons keyed by file path — every
// catalog file the lazy enumeration could not index (a recipe/FR/aggregate/
// provider/mapping-library that failed to parse, a duplicate identity, or a
// schema library that failed its forbidden-keyword / template-body checks), plus
// every Component or Feature file a cascade read has refused so far. A refused
// Component or Feature is recorded when its URI is read rather than during an
// enumeration walk, so it appears once the resolution that needed it has been
// attempted — which is exactly when a caller reports the resulting miss. The
// daemon surfaces these as a closure-level diagnostic so an in-closure entity
// that went missing because it was skipped is explained, not silently absent.
func (l *LazyLookup) Skips() map[string]error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildRecipeIndex()
	_ = l.buildSchemaLibraries()
	return l.recipeSkips
}

// SkippedEntities returns the same record Skips reports, in the shape the
// eager tolerant load reports it — so one annotator can render the skips from
// either catalog backing rather than each backing growing its own. Ordered by
// path, so a rendered note is deterministic.
//
// The identity each refused document declares for itself is recovered by
// re-reading its header, through the same helper the eager tolerant load
// recovers it with. Re-reading a file this backing has already refused to trust
// is deliberate, and it is the whole reason the record is worth producing: a
// consumer matches a failing reference against the identity a refused document
// declares, so a record carrying only a path matches nothing on any input, and a
// refusal nothing can match can never be reported ahead of the failure it
// explains. The cost is one header parse per already-refused file — a path no
// successful load reaches.
//
// A skip keyed by something that is not a readable file leaves the identity
// empty, and that is the answer rather than a gap. A lockfile-pinned cache slot
// is recorded under its pinned reference rather than under a path, and a
// document too malformed for its header to be read declares no identity to
// recover. Such a refusal stays reported by whatever it was keyed under, which
// is still enough to find it.
func (l *LazyLookup) SkippedEntities() []SkippedEntity {
	skips := l.Skips()
	if len(skips) == 0 {
		return nil
	}
	paths := make([]string, 0, len(skips))
	for p := range skips {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]SkippedEntity, 0, len(paths))
	for _, p := range paths {
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			out = append(out, SkippedEntity{Path: p, Err: skips[p]})
			continue
		}
		out = append(out, newSkippedEntity(p, data, skips[p]))
	}
	return out
}

// ValidateClosureScopedPillars runs closure-scoped pillar validation over the
// LAZY backing: it resolves each closure Component/Feature through the cascade
// and validates its schema pillars against the lazily-walked SchemaLibrary set —
// the daemon's early-warning gate, and narrower than a whole-catalog pillar
// validation. A broken out-of-closure entity is never resolved here, so it is
// never validated; an in-closure pillar error (a schema-invalid
// Component/Feature, or a $ref to a skipped/missing library) is surfaced.
// Sources come from the per-kind index (file paths) for error provenance. The
// resolved entities are extends-merged (what the daemon renders); build remains
// the authoritative pre-extends validator.
func (l *LazyLookup) ValidateClosureScopedPillars(cl closure.Closure) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.buildSchemaLibraries()
	l.buildComponentIndex()
	l.buildFeatureIndex()
	comps := map[string]types.Component{}
	compSrc := map[string]string{}
	feats := map[string]types.Feature{}
	featSrc := map[string]string{}
	for _, e := range cl.Entities {
		name := lookupNameFromURI(e.URI)
		if comp, ok := l.component(e.URI); ok {
			comps[comp.Metadata.Name] = comp
			compSrc[comp.Metadata.Name] = l.componentIndex[name]
			continue
		}
		if feat, ok := l.feature(e.URI); ok {
			feats[feat.Metadata.Name] = feat
			featSrc[feat.Metadata.Name] = l.featureIndex[name]
		}
	}
	// A schema reference that resolves to nothing has one cause the author cannot
	// see from the file it is reported against: the library sits in the artifact
	// cache with nothing vouching for it, so it was never read. Name it, and name
	// the command that fixes it.
	return ValidateClosurePillars(comps, compSrc, feats, featSrc, l.schemaLibraries, func() string {
		return cachedLibraryRemedy(l.unverifiedCachedSchemaLibraries())
	})
}

// unverifiedCachedSchemaLibraries names the SchemaLibraries the artifact cache
// holds that did not reach the resolved set. Cached content is read only where a
// lockfile entry vouches for its digest, so a library sitting in the cache and
// absent from the resolved set is one nothing has verified — the shape a stale
// or hand-placed cache entry takes.
//
// SchemaLibrary is the only kind reported, because this is the set the schema
// reference surface consumes; naming a kind whose index this call has not built
// would report it as unverified purely because nothing asked for it yet.
//
// The cache is read only once a resolution has already failed, so a build that
// succeeds never pays for the enumeration. Names come back sorted for a stable
// diagnostic.
func (l *LazyLookup) unverifiedCachedSchemaLibraries() []string {
	if l.cacheDir == "" {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	_ = filepath.WalkDir(oci.EntitiesRoot(l.workspaceRoot, l.cacheDir), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "library.yaml" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var hdr fileHeader
		if uerr := yaml.Unmarshal(data, &hdr); uerr != nil {
			return nil
		}
		if hdr.Kind != "SchemaLibrary" || hdr.Metadata.Name == "" {
			return nil
		}
		if _, resolved := l.schemaLibraries[hdr.Metadata.Name]; resolved {
			return nil
		}
		if !seen[hdr.Metadata.Name] {
			seen[hdr.Metadata.Name] = true
			names = append(names, fmt.Sprintf("SchemaLibrary %q", hdr.Metadata.Name))
		}
		return nil
	})
	sort.Strings(names)
	return names
}

// cachedLibraryRemedy is the operator-actionable line appended to a schema
// resolution failure when the artifact cache holds library content that never
// reached the resolved set. It states the rule in the terms the author works in
// — the cache holds what a fetch put there, and it counts as catalog content
// only where a lockfile entry vouches for its digest — then names the command
// that records such an entry, and the alternative of authoring the library
// directly. Returns "" when there is nothing to say.
func cachedLibraryRemedy(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return "remediation: the artifact cache holds library content that no lockfile entry pins (" +
		strings.Join(names, ", ") +
		"); cached content counts as catalog content only where a lockfile entry vouches for its digest, so it was not used. " +
		"Run `aggrega pull --release <release>` to resolve and record it, or author the library under a catalog directory."
}

// ClosureLintWarnings returns the schema-pillar-overlap advisories for every
// Component and Feature the closure reaches — the non-fatal, warning-level
// sibling of ValidateClosureScopedPillars's pillar validation, over the same
// closure-entity set. Whole-catalog pillar validation ran the overlap lint beside the
// pillar validation in one loop; when build resolution moved to a closure-scoped
// view the validation came across but the overlap lint did not, so the build
// surfaced no overlap advisories. This restores them, closure-scoped: a
// registry-pinned closure Component now gets the same overlap advisory a local
// one does.
//
// Entities are walked in sorted-URI order (deterministic; the warnings never
// enter the lockfile or rendered output, but their emission order must be
// stable). The URI is the per-entity locator — a registry-pinned closure entity
// has no local file path. Narrowing-on-extend advisories are a separate
// composition surface and are not included here.
func (l *LazyLookup) ClosureLintWarnings(cl closure.Closure) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Self-sufficient: build the Tier-1 indexes the cascade reads, so the
	// accessor does not depend on a prior ValidateClosureScopedPillars call.
	l.buildComponentIndex()
	l.buildFeatureIndex()

	uris := make([]string, 0, len(cl.Entities))
	seen := map[string]bool{}
	for _, e := range cl.Entities {
		if seen[e.URI] {
			continue
		}
		seen[e.URI] = true
		uris = append(uris, e.URI)
	}
	sort.Strings(uris)

	var out []string
	for _, uri := range uris {
		if comp, ok := l.component(uri); ok {
			out = append(out, validator.ValidateSchemaPillarOverlap(
				"Component", comp.Metadata.Name, uri, comp.Spec.Schema)...)
			continue
		}
		if feat, ok := l.feature(uri); ok {
			out = append(out, validator.ValidateSchemaPillarOverlap(
				"Feature", feat.Metadata.Name, uri, feat.Spec.Schema)...)
		}
	}
	return out
}

// ClosureNarrowingWarnings returns the narrowing-on-extend advisory notices
// accumulated while composing the closure's extends chains (a child widening an
// inherited constraint). Populated by ResolveClosureExtends from the same
// composition that produces the effective schema — never recomputed. Returns nil
// for a closure with no extends-narrowing. Signal-only: callers surface these
// informationally; the compiler never halts on narrowing.
func (l *LazyLookup) ClosureNarrowingWarnings() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closureNarrowings
}

// ResolveClosureExtends resolves the extends chain for each Component/Feature the
// closure reaches and returns the collected resolution errors (a dangling parent,
// a cycle, a malformed chain) — the daemon's early-warning extends gate, the lazy
// counterpart to the CLI build's closure-scoped extends. An out-of-closure
// entity's extends is never resolved, so a broken out-of-closure parent never
// produces a diagnostic. Deterministic: closure entity names are deduped + sorted.
func (l *LazyLookup) ResolveClosureExtends(cl closure.Closure) []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Clear what this pass contributed on the last run. The daemon is long-lived
	// and resolves a closure's chains on each inspection, so without this every
	// narrowing advisory would be reported once per inspection since the last
	// reset. This pass is the field's only producer, so clearing the whole slice
	// erases nothing another pass owns.
	l.closureNarrowings = nil
	// The closure's library set is the cross-document $ref context the schema
	// merge composes against — the same set the eager whole-catalog path feeds
	// CompileSchemaForMerge.
	_ = l.buildSchemaLibraries()

	compLookup := func(uri string) (types.Component, bool) { return l.resolveComponent(uri) }
	featLookup := func(uri string) (types.Feature, bool) { return l.resolveFeature(uri) }

	// CatalogEntry chain lookup that compiles each chain entry's authored schema
	// into the JSON-Schema-merge input — identical to the eager ResolveAllExtends,
	// so both surfaces compose through the same ResolveFeatureSchemaChain merge
	// phase (no second merge implementation). Features only: an extending Component inherits
	// its base's pillar block verbatim, so its chain has nothing to compose.
	featEntryLookup := func(uri string) (validator.CatalogEntry, bool) {
		feat, ok := featLookup(uri)
		if !ok {
			return validator.CatalogEntry{}, false
		}
		return validator.CatalogEntry{
			URI:        feat.Metadata.Name,
			Extends:    feat.Spec.Extends,
			JSONSchema: validator.CompileSchemaForMerge(feat.Spec.Schema, l.schemaLibraries),
		}, true
	}
	storeEffective := func(name string, composition *validator.ExtendsComposition) {
		if composition == nil {
			return
		}
		if composition.EffectiveSchema != nil {
			if l.effectiveSchemas == nil {
				l.effectiveSchemas = map[string]compactform.Schema{}
			}
			l.effectiveSchemas[name] = composition.EffectiveSchema
		}
		// Accumulate the narrowing notices the composition already computed (a child
		// widening an inherited constraint) — surfaced as advisories by the lint
		// closure mode, never enforcement. Appended over the sorted-name closure
		// walk, so the accumulated order is deterministic. Narrowings exist only
		// when a schema merged (EffectiveSchema is then set), so this never strands
		// a notice the nil-EffectiveSchema skip above would drop.
		l.closureNarrowings = append(l.closureNarrowings, composition.Narrowings...)
	}

	byName := map[string]string{}
	for _, e := range cl.Entities {
		n := lookupNameFromURI(e.URI)
		if _, ok := byName[n]; !ok {
			byName[n] = e.URI
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)

	var errs []error
	for _, n := range names {
		uri := byName[n]
		if comp, ok := l.resolveComponent(uri); ok {
			if comp.Spec.Extends == "" {
				continue
			}
			// Structural merge (metadata, secrets, uses, inject), plus the base's
			// pillar block copied down verbatim onto Spec.Schema. An extending
			// Component authors no pillar content, so there is nothing to compose
			// afterwards and no second view to store — every Component-schema
			// consumer reads the authored node.
			if _, vErrs := validator.ResolveComponentExtends(comp, compLookup); len(vErrs) > 0 {
				for _, ve := range vErrs {
					errs = append(errs, ve)
				}
				continue
			}
			continue
		}
		if feat, ok := l.resolveFeature(uri); ok {
			if feat.Spec.Extends == "" {
				continue
			}
			if _, vErrs := validator.ResolveFeatureExtends(feat, featLookup); len(vErrs) > 0 {
				for _, ve := range vErrs {
					errs = append(errs, ve)
				}
				continue
			}
			rootEntry := validator.CatalogEntry{
				URI:        feat.Metadata.Name,
				Extends:    feat.Spec.Extends,
				JSONSchema: validator.CompileSchemaForMerge(feat.Spec.Schema, l.schemaLibraries),
			}
			inh := validator.ResolveFeatureSchemaChain(rootEntry, featEntryLookup)
			if len(inh.Errors) > 0 {
				for _, ie := range inh.Errors {
					errs = append(errs, ie)
				}
				continue
			}
			storeEffective(feat.Metadata.Name, validator.CompositionFromResult(feat.Metadata.Name, inh))
		}
	}
	return errs
}

// ClosureLookup returns a closure.Lookup that walks a Release's closure over the
// LAZY backing — the daemon's replacement for the eager whole-catalog
// `*Catalog.ClosureLookupWithExternals`. No catalog is loaded up front:
// Component/Feature resolve per-URI through the Tier-1 (filesystem index) →
// Tier-2 (verified cache) → Tier-3 (the SUPPLIED puller) cascade, and
// Recipe/FeatureRecipe/Aggregate/Plugin/SchemaLibrary/MappingLibrary resolve
// through the tolerantly-enumerated recipe shell (also with Tier-2/3). A broken
// out-of-closure entity is never enumerated-fatal; the digests returned hash the
// SAME authored bytes a build/pull hashed (retained in entityBytes), so a serve
// drift verify compares like-for-like. Hermeticity follows the supplied puller:
// the daemon passes nil (Tier-3 disabled), matching the eager path it replaces.
//
// The closure walker resolves entity URIs structurally (Aggregate / Manifest /
// Release references); it does not introspect schema bodies or Component extends,
// so the per-URI resolution here deliberately skips extends-merge (the digest is
// of the raw authored entity, exactly as the eager closure lookup hashed).
func (l *LazyLookup) ClosureLookup(puller OCIPuller, workspaceRoot, cacheDir string, externals map[string][]byte) closure.Lookup {
	l.mu.Lock()
	defer l.mu.Unlock()
	return &lazyClosureLookup{
		l:      l,
		puller: puller,
		base:   l.recipeCatalog().ClosureLookupWithExternals(puller, workspaceRoot, cacheDir, externals),
	}
}

// ClosureLookupWithPins returns a closure.Lookup like ClosureLookup whose
// plugin-artifact resolution HONORS the supplied pins: a .wasm artifact is read
// from the content-addressed cache slot its pin names and self-verified before it
// is returned, so a hermetic (nil-puller) consumer resolves a pinned artifact
// offline. The default ClosureLookup leaves the artifact-resolution pins nil (the
// build and serve paths reuse cache slots unconditionally against the closure-level
// drift gate), which cannot address a plugin .wasm slot — that slot is keyed by the
// content digest a pin supplies. The workspace-socket reconstruction uses this so
// the socket .wasm the lockfile pins resolves from the cache without a fetch.
func (l *LazyLookup) ClosureLookupWithPins(puller OCIPuller, workspaceRoot, cacheDir string, externals map[string][]byte, pins map[string]Pin) closure.Lookup {
	l.mu.Lock()
	defer l.mu.Unlock()
	return &lazyClosureLookup{
		l:      l,
		puller: puller,
		base:   l.recipeCatalog().ClosureLookupWithReuse(puller, workspaceRoot, cacheDir, externals, nil, &ReuseVerification{PriorPins: pins}),
	}
}

// lazyClosureLookup composes Component/Feature resolution over the lazy cascade
// with the recipe-shell-backed catalogLookup (base) for every other kind. The
// shell's Components/Features maps are empty (those kinds stay on-demand), so
// Component/Feature are tried here FIRST; everything else falls through to base,
// which serves Recipe/FR/Aggregate/Plugin at Tier-1 and all kinds at Tier-2/3.
type lazyClosureLookup struct {
	l      *LazyLookup
	puller OCIPuller
	base   closure.Lookup
}

// The recipe axis is reached by assertion rather than through the Lookup method
// set, so satisfaction is asserted here. Without it a signature drift would turn
// closure.RequireRecipeLoader into a refusal at every site that passes this
// value, instead of a compile error at the value itself.
var _ closure.RecipeLoaderSource = (*lazyClosureLookup)(nil)

func (x *lazyClosureLookup) ResolveCatalogEntity(ctx context.Context, uri, kindHint string) (closure.Resolution, error) {
	// The closure walker invokes this from pipeline code outside any
	// LazyLookup method, and it re-enters the lookup's lock-free
	// internals (resolveComponentForClosure / the recipe-shell base, which
	// alias the lookup's maps), so it takes the lookup lock here.
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	// Route by the collection-site kind when known, so a Feature is not first
	// attempted at a Component location (a wasted registry miss); fall back to the
	// cascade when the hint is absent or names a kind this lazy path does not
	// resolve directly (recipe/provider → base).
	if kindHint != "feature" {
		if res, ok := x.l.resolveComponentForClosure(ctx, uri, x.puller); ok {
			return res, nil
		}
	}
	if kindHint != "component" {
		if res, ok := x.l.resolveFeatureForClosure(ctx, uri, x.puller); ok {
			return res, nil
		}
	}
	return x.base.ResolveCatalogEntity(ctx, uri, kindHint)
}

func (x *lazyClosureLookup) ResolveAggregate(ctx context.Context, uri string) (closure.Resolution, error) {
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	return x.base.ResolveAggregate(ctx, uri)
}

// LoadAggregate loads a resolved sub-aggregate's parsed body over the lazy
// backing, satisfying closure.AggregateLoader so the transitive sub-aggregate
// axis can be computed against this view rather than only against an eagerly
// loaded catalog.
//
// The read delegates to base — the same recipe-shell-backed lookup that already
// serves the Aggregate kind here — so the body comes from the authored catalog
// bytes or the pinned cache slot the eager path reads, at the same point in the
// walk, through the same code. Nothing is loaded up front: the lazy cascade is
// untouched, and this method reads only what the descent asks for.
//
// A base that cannot serve resolved bodies refuses with
// closure.ErrNoAggregateLoader rather than yielding nothing, because the descent
// treats an absent loader as "none was wired" and skips in silence.
func (x *lazyClosureLookup) LoadAggregate(uri, version, digest string) (*types.Aggregate, error) {
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	loader, err := closure.RequireAggregateLoader(x.base)
	if err != nil {
		return nil, err
	}
	return loader.LoadAggregate(uri, version, digest)
}

// ClosureRecipeLoader hands out the recipe loader over the lazily-enumerated
// (Tier-1 + pinned Tier-2) recipe set, satisfying closure.RecipeLoaderSource so
// the recipe axis can be computed against this view rather than only against an
// eagerly loaded catalog. It is the same loader the underlying view hands out
// directly, reached through the closure surface the walk already carries.
//
// The loader is re-derived from the view on every call rather than taken from
// base: the enumerated recipe set is pin-dependent and a pin change drops it, so
// a loader captured when this lookup was built would keep reading the recipe set
// as it stood before that change.
func (x *lazyClosureLookup) ClosureRecipeLoader() closure.RecipeLoader {
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	return x.l.recipeCatalog().ClosureRecipeLoader()
}

func (x *lazyClosureLookup) ResolveLibrary(ctx context.Context, axis closure.LibraryAxis, uri string) (closure.LibraryResolution, error) {
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	return x.base.ResolveLibrary(ctx, axis, uri)
}

func (x *lazyClosureLookup) ResolvePlugin(ctx context.Context, uri, version string) (closure.Resolution, error) {
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	return x.base.ResolvePlugin(ctx, uri, version)
}

// resolveComponentForClosure resolves a Component URI for the closure walk —
// Tier-1 (filesystem index) → Tier-2 (verified cache) → Tier-3 (supplied puller),
// retaining the raw bytes for the digest. No extends-merge (the walk is
// structural). A non-Component cache slot (kind mismatch) falls through so the
// caller routes the URI to the recipe shell. Returns ok=false on cascade
// exhaustion. A tier2Skip (drifted cache) returns false — the entity is
// unavailable for this request, surfaced upstream rather than self-healed.
func (l *LazyLookup) resolveComponentForClosure(ctx context.Context, uri string, puller OCIPuller) (closure.Resolution, bool) {
	if comp, ok := l.tier1ReadComponent(uri); ok {
		return l.closureResolution("Component", comp.Metadata.Name, comp.Metadata.Version, uri, closure.ResolutionPathCatalog), true
	}
	if bytes, manifest, slot, outcome := l.tier2Verified(uri); outcome == tier2Use {
		if path, doc, ok := registryDocument(slot, bytes, uri); ok {
			if comp, err := parseComponentEntity(doc, path); err == nil {
				l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
				return cacheHitResolution(closureVersion(uri, comp.Metadata.Version), bytes, manifest), true
			}
		}
	} else if outcome == tier2Skip {
		return closure.Resolution{}, false
	}
	if bytes, res, err := tier3PullCatalogEntity(ctx, l.workspaceRoot, l.cacheDir, puller, uri, "Component"); err == nil && bytes != nil {
		if path, doc, ok := registryDocument(tier3Slot(l.workspaceRoot, l.cacheDir, uri, res), bytes, uri); ok {
			if comp, perr := parseComponentEntity(doc, path); perr == nil {
				l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
				return l.closureResolution("Component", comp.Metadata.Name, comp.Metadata.Version, uri, closure.ResolutionPathRegistry), true
			}
		}
	}
	return closure.Resolution{}, false
}

// resolveFeatureForClosure mirrors resolveComponentForClosure for the Feature
// surface.
func (l *LazyLookup) resolveFeatureForClosure(ctx context.Context, uri string, puller OCIPuller) (closure.Resolution, bool) {
	if feat, ok := l.tier1ReadFeature(uri); ok {
		return l.closureResolution("Feature", feat.Metadata.Name, feat.Metadata.Version, uri, closure.ResolutionPathCatalog), true
	}
	if bytes, manifest, slot, outcome := l.tier2Verified(uri); outcome == tier2Use {
		if path, doc, ok := registryDocument(slot, bytes, uri); ok {
			if feat, err := parseFeatureEntity(doc, path); err == nil {
				l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
				return cacheHitResolution(closureVersion(uri, feat.Metadata.Version), bytes, manifest), true
			}
		}
	} else if outcome == tier2Skip {
		return closure.Resolution{}, false
	}
	if bytes, res, err := tier3PullCatalogEntity(ctx, l.workspaceRoot, l.cacheDir, puller, uri, "Feature"); err == nil && bytes != nil {
		if path, doc, ok := registryDocument(tier3Slot(l.workspaceRoot, l.cacheDir, uri, res), bytes, uri); ok {
			if feat, perr := parseFeatureEntity(doc, path); perr == nil {
				l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
				return l.closureResolution("Feature", feat.Metadata.Name, feat.Metadata.Version, uri, closure.ResolutionPathRegistry), true
			}
		}
	}
	return closure.Resolution{}, false
}

// closureResolution builds the closure.Resolution for a lazily-resolved entity,
// mirroring catalogLookup.tier1Resolution: digest of the retained raw bytes;
// resolved version = the URI's pin when present, else metadata.version; the tier
// tag matches the eager path's vocabulary so a drift compare never differs on it.
// A cache hit is not described here but through cacheHitResolution, over the same
// version, so it reports the manifest it holds as every cache-tier read does.
func (l *LazyLookup) closureResolution(kind, name, metaVersion, uri, path string) closure.Resolution {
	return closure.Resolution{
		ResolvedVersion: closureVersion(uri, metaVersion),
		Digest:          contentDigest(l.entityBytes[entityBytesKey(kind, name)]),
		ResolutionPath:  path,
	}
}

// closureVersion is the version a lazily-resolved entity is reported at: the
// version the URI pins when it pins one, else the entity's own metadata.version.
func closureVersion(uri, metaVersion string) string {
	if _, v, hasV := resolver.SplitVersionedURI(uri); hasV && v != "" {
		return v
	}
	return metaVersion
}
