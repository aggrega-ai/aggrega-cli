// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
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
// Cycle detection comes free via validator.ResolveInheritance's
// visited-set guard.
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
		workspaceRoot:    workspaceRoot,
		cacheDir:         cacheDir,
		catalogDirs:      catalogDirs,
		puller:           puller,
		components:       map[string]types.Component{},
		features:         map[string]types.Feature{},
		schemaLibraries:  map[string]types.SchemaLibrary{},
		componentMisses:  map[string]struct{}{},
		featureMisses:    map[string]struct{}{},
		componentIndex:   map[string]string{},
		featureIndex:     map[string]string{},
		recipes:          map[string]types.Recipe{},
		featureRecipes:   map[string]types.FeatureRecipe{},
		aggregates:       map[string]types.Aggregate{},
		mappingLibraries: map[string]types.MappingLibrary{},
		plugins:          map[string]types.Plugin{},
		aggregateSources: map[string]string{},
		recipeSkips:      map[string]error{},
		entityBytes:      map[string][]byte{},
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
	// that fails validation is surfaced as a hard reject by the eager recipe-catalog
	// load (serve's resolveRecipeCatalog) and by buildSchemaLibraries' returned
	// error to a direct caller; AsCatalogLookup exposes the resolvable set.
	_ = l.buildSchemaLibraries()
	return resolver.CatalogLookup{
		Component:       l.Component,
		Feature:         l.Feature,
		SchemaLibraries: l.schemaLibraries,
		// EffectiveSchema serves the composed (parent+child) schema for an
		// extends-ing Component OR Feature, keyed by metadata.name — populated by
		// ResolveClosureExtends. Returns (_, false) for a non-extends or
		// not-yet-resolved type, so consumers fall back to the authored schema.
		EffectiveSchema: l.effectiveSchema,
		// Plugin serves the Plugin entity by URI over the lazily-walked
		// set, so the build-load pipeline socket fronts the artifact cascade with
		// the entity → spec.plugin two-step from the lazy backing too.
		Plugin: l.PluginByURI,
		// ProviderPlugin resolves a provider name to the provider-rendering Plugin
		// serving it (via compatibleProviders) over the lazily-walked set, so
		// render dispatch resolves the same provider→Plugin binding from the lazy
		// backing as the eager one.
		ProviderPlugin: l.ProviderPluginByName,
	}
}

// effectiveSchema serves the composed (parent+child) schema for an
// extends-ing Component or Feature, keyed by metadata.name — populated
// by ResolveClosureExtends. Returns (_, false) for a non-extends or
// not-yet-resolved type, so consumers fall back to the authored schema.
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
	l.plugins = map[string]types.Plugin{}
	l.aggregateSources = map[string]string{}
	l.recipeSkips = map[string]error{}
	l.recipeIndexBuilt = false
	l.recipeCat = nil
	l.entityBytes = map[string][]byte{}
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
	// extends-ing closure Component/Feature, keyed by metadata.name — the
	// build-path counterpart to the eager catalog's effectiveSchemas. Populated
	// by ResolveClosureExtends via the same ResolveInheritance merge the eager
	// whole-catalog path uses; served through AsCatalogLookup so value
	// conformance and schema-defaults validate extends entities against the
	// composed schema, not the child-only structural one. Empty for a closure
	// with no extends-ing entity.
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
	recipes          map[string]types.Recipe
	featureRecipes   map[string]types.FeatureRecipe
	aggregates       map[string]types.Aggregate
	mappingLibraries map[string]types.MappingLibrary
	plugins          map[string]types.Plugin // keyed by identity (metadata.name:version), like the eager catalog
	aggregateSources map[string]string
	recipeSkips      map[string]error // skipped recipe/FR/aggregate/maplib/plugin file path -> reason
	recipeIndexBuilt bool

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
	// (Recipes, FeatureRecipes, Aggregates, aggregateSources, workspaceRoot), so
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
	l.plugins = map[string]types.Plugin{}
	l.aggregateSources = map[string]string{}
	l.recipeSkips = map[string]error{}
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
		_, err := tier2ReadVerified(l.workspaceRoot, l.cacheDir, versionedSlotURI(uri, pin.ResolvedVersion), pin.Digest)
		if err != nil && errors.Is(err, oci.ErrFetchIntegrity) {
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
}

// PinsFromLockfile builds the version-aware pin set SetPins consumes from a loaded
// lockfile, across every kind the closure walker resolves through the Tier-2 cache
// (entities, schema libraries, mapping libraries, wasm plugins) — the same axes
// Lockfile.PinsByURI covers, but retaining ResolvedVersion so a bare-URI pin can
// address its versioned cache slot. Source artifacts are excluded (they descend on
// their own read-time digest gate, not the closure walker's Tier-2 reuse).
func PinsFromLockfile(lf *lockfile.Lockfile) map[string]Pin {
	if lf == nil {
		return nil
	}
	pins := make(map[string]Pin, len(lf.Entities)+len(lf.SchemaLibraries)+len(lf.MappingLibraries)+len(lf.Plugins))
	for _, e := range lf.Entities {
		pins[e.URI] = Pin{ResolvedVersion: e.ResolvedVersion, Digest: e.Digest}
	}
	for _, sl := range lf.SchemaLibraries {
		pins[sl.URI] = Pin{ResolvedVersion: sl.ResolvedVersion, Digest: sl.Digest}
	}
	for _, ml := range lf.MappingLibraries {
		pins[ml.URI] = Pin{ResolvedVersion: ml.ResolvedVersion, Digest: ml.Digest}
	}
	for _, wp := range lf.Plugins {
		pins[wp.URI] = Pin{ResolvedVersion: wp.ResolvedVersion, Digest: wp.Digest}
	}
	return pins
}

// PinsFromClosure builds the version-aware pin set SetPins consumes from a
// freshly-computed closure — pull's lock-time source, where no lockfile exists
// yet. Covers the same axes as PinsFromLockfile (entities, schema + mapping
// libraries, wasm plugins), each carrying the version the walk resolved so a
// bare-URI pin addresses its versioned cache slot. Source artifacts are excluded.
func PinsFromClosure(cl closure.Closure) map[string]Pin {
	pins := make(map[string]Pin, len(cl.Entities)+len(cl.SchemaLibraries)+len(cl.MappingLibraries)+len(cl.Plugins))
	for _, e := range cl.Entities {
		pins[e.URI] = Pin{ResolvedVersion: e.ResolvedVersion, Digest: e.Digest}
	}
	for _, sl := range cl.SchemaLibraries {
		pins[sl.URI] = Pin{ResolvedVersion: sl.ResolvedVersion, Digest: sl.Digest}
	}
	for _, ml := range cl.MappingLibraries {
		pins[ml.URI] = Pin{ResolvedVersion: ml.ResolvedVersion, Digest: ml.Digest}
	}
	for _, wp := range cl.Plugins {
		pins[wp.URI] = Pin{ResolvedVersion: wp.ResolvedVersion, Digest: wp.Digest}
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
func (l *LazyLookup) tier2Verified(uri string) ([]byte, tier2Outcome) {
	pin, havePin := l.pins[uri]
	if !havePin {
		return nil, tier2Fetch
	}
	// The cache slot is keyed by the versioned URI; combine the lookup URI with
	// the pin's resolved version so a bare-URI pin addresses the right slot.
	bytes, err := tier2ReadVerified(l.workspaceRoot, l.cacheDir, versionedSlotURI(uri, pin.ResolvedVersion), pin.Digest)
	switch {
	case err == nil:
		return bytes, tier2Use
	case errors.Is(err, oci.ErrFetchIntegrity):
		return nil, tier2Skip
	default: // oci.ErrFetchNotFound (slot absent) or an unreadable slot
		return nil, tier2Fetch
	}
}

// fileHeader is the partial unmarshal struct used by the catalog-tree
// walker to index entities by authored metadata.name without parsing
// the full entity body.
type fileHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
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
		fmt.Fprintf(os.Stderr, "  extends: Component %s: %s\n", uri, vErrs[0].Error())
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
		fmt.Fprintf(os.Stderr, "  extends: Feature %s: %s\n", uri, vErrs[0].Error())
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
func (l *LazyLookup) resolveComponent(uri string) (types.Component, bool) {
	if comp, ok := l.tier1ReadComponent(uri); ok {
		return comp, true
	}
	if bytes, outcome := l.tier2Verified(uri); outcome == tier2Use {
		// parseComponentEntity is the shared per-entity resolve; a non-Component
		// kind returns an error and the cascade falls through to Tier-3.
		if comp, err := parseComponentEntity(bytes, uri); err == nil {
			l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
			return comp, true
		}
	} else if outcome == tier2Skip {
		// Cache drifted from the pin; diagnostic already emitted. Do not use the
		// drifted bytes and do not self-heal — the entity is unavailable for this
		// request (an honest degraded preview, not a false-green render).
		return types.Component{}, false
	}
	bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, l.puller, uri, "Component")
	if err == nil && bytes != nil {
		if comp, perr := parseComponentEntity(bytes, uri); perr == nil {
			l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
			return comp, true
		}
	}
	return types.Component{}, false
}

// resolveFeature mirrors resolveComponent for the Feature surface.
// The same kind-check guards Tier-2 + Tier-3 unmarshal paths against
// non-Feature bytes arriving from the cache or registry.
func (l *LazyLookup) resolveFeature(uri string) (types.Feature, bool) {
	if feat, ok := l.tier1ReadFeature(uri); ok {
		return feat, true
	}
	if bytes, outcome := l.tier2Verified(uri); outcome == tier2Use {
		if feat, err := parseFeatureEntity(bytes, uri); err == nil {
			l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
			return feat, true
		}
	} else if outcome == tier2Skip {
		// Cache drifted from the pin; diagnostic already emitted. Do not use the
		// drifted bytes and do not self-heal.
		return types.Feature{}, false
	}
	bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, l.puller, uri, "Feature")
	if err == nil && bytes != nil {
		if feat, perr := parseFeatureEntity(bytes, uri); perr == nil {
			l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
			return feat, true
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
		return types.Component{}, false
	}
	l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = data
	return comp, true
}

// tier1ReadFeature mirrors tier1ReadComponent for the Feature surface.
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
// does not match are skipped without indexing.
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
				return nil
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
			index[hdr.Metadata.Name] = path
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
// Returns the first validation error encountered, in catalog-walk order — the same
// rejection the eager catalog.Load surfaces, so the lazy backing does not silently
// accept a library the build rejects (reject-parity). Valid libraries are still
// cached so a best-effort caller (AsCatalogLookup) sees the resolvable set; a
// caller that must reject (the conformance test; a future serve gate) observes the
// returned error. In the daemon, an invalid library is additionally caught as a
// hard load failure by the eager recipe-catalog resolution before render.
func (l *LazyLookup) buildSchemaLibraries() error {
	if l.schemaLibrariesBuilt {
		return nil
	}
	l.schemaLibrariesBuilt = true
	var firstErr error
	for _, dir := range l.catalogDirs {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
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
			return nil
		})
	}
	// Tier-2: fold lockfile-pinned SchemaLibrary cache slots, after the Tier-1 walk
	// so a locally-authored library wins. Registry-pinned libraries live in the
	// cache, not the catalog dirs, so without this the lazy backing cannot see them.
	l.foldPinnedCacheEntities(map[string]bool{"SchemaLibrary": true})
	return firstErr
}

// buildRecipeIndex walks the catalog directories once and TOLERANTLY enumerates
// every recipe-shell kind (Recipe, FeatureRecipe, Aggregate,
// MappingLibrary, Plugin) into the hot maps, keyed exactly as the eager
// catalog keys them (recipes/FRs by provider/name:version, Plugins by
// name:version, aggregates/libraries by metadata.name). A file that
// won't parse — or whose recipe/FR identity
// collides with one already indexed — is skipped with a one-line stderr warning
// and a recorded reason; it never aborts the walk. This is the robustness crux:
// a broken UNUSED recipe is invisible to a resolution that doesn't need it, where
// the eager catalog.Load fails the whole walk on the first broken recipe.
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
			if err != nil || d.IsDir() {
				return nil
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
			l.foldRecipeShellEntity(hdr.Kind, data, data, path)
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
		"Recipe": true, "FeatureRecipe": true, "Aggregate": true, "MappingLibrary": true, "Plugin": true,
	})
}

// foldRecipeShellEntity parses one enumerated recipe-shell entity (Recipe,
// FeatureRecipe, Aggregate, MappingLibrary, or Plugin) and folds it into the hot
// maps, keyed exactly as the eager catalog keys it, first-wins on a name/identity
// collision (a later duplicate is skipped and recorded). parseData is the YAML to
// parse; rawBytes is retained as entityBytes for digest fidelity — identical to
// parseData for a Tier-1 catalog file, the whole tar.gz bundle for a Tier-2 recipe
// cache slot; src labels the source for skip diagnostics. The single fold path
// feeds both the Tier-1 walk and the pinned Tier-2 pass, so the two cannot drift.
func (l *LazyLookup) foldRecipeShellEntity(kind string, parseData, rawBytes []byte, src string) {
	switch kind {
	case "Recipe":
		e, perr := parseRecipeEntity(parseData, src)
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		key := recipeIdentityKey(e.Spec.Provider, e.Metadata.Name, e.Metadata.Version)
		if _, dup := l.recipes[key]; dup {
			l.recordRecipeSkip(src, fmt.Errorf("duplicate recipe identity %q; this file is shadowed", key))
			return
		}
		l.recipes[key] = e
		l.entityBytes[entityBytesKey("Recipe", key)] = rawBytes
	case "FeatureRecipe":
		e, perr := parseFeatureRecipeEntity(parseData, src)
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		key := recipeIdentityKey(e.Spec.Provider, e.Metadata.Name, e.Metadata.Version)
		if _, dup := l.featureRecipes[key]; dup {
			l.recordRecipeSkip(src, fmt.Errorf("duplicate feature recipe identity %q; this file is shadowed", key))
			return
		}
		l.featureRecipes[key] = e
		l.entityBytes[entityBytesKey("FeatureRecipe", key)] = rawBytes
	case "Aggregate":
		e, perr := parseAggregateEntity(parseData, src)
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		if _, dup := l.aggregates[e.Metadata.Name]; dup {
			l.recordRecipeSkip(src, fmt.Errorf("duplicate aggregate name %q; this file is shadowed", e.Metadata.Name))
			return
		}
		l.aggregates[e.Metadata.Name] = e
		l.aggregateSources[e.Metadata.Name] = src
		l.entityBytes[entityBytesKey("Aggregate", e.Metadata.Name)] = rawBytes
	case "MappingLibrary":
		e, perr := parseMappingLibraryEntity(parseData, src)
		if perr != nil {
			l.recordRecipeSkip(src, perr)
			return
		}
		if _, dup := l.mappingLibraries[e.Metadata.Name]; dup {
			l.recordRecipeSkip(src, fmt.Errorf("duplicate mapping library name %q; this file is shadowed", e.Metadata.Name))
			return
		}
		l.mappingLibraries[e.Metadata.Name] = e
		l.entityBytes[entityBytesKey("MappingLibrary", e.Metadata.Name)] = rawBytes
	case "Plugin":
		if _, eerr := envelope.ValidateBytes(envelope.KindPlugin, parseData, src); eerr != nil {
			l.recordRecipeSkip(src, eerr)
			return
		}
		var e types.Plugin
		if uerr := yaml.Unmarshal(parseData, &e); uerr != nil {
			l.recordRecipeSkip(src, uerr)
			return
		}
		key := entityIndexKey(e.Metadata.Name, e.Metadata.Version)
		if _, dup := l.plugins[key]; dup {
			l.recordRecipeSkip(src, fmt.Errorf("duplicate wasm plugin identity %q; this file is shadowed", key))
			return
		}
		l.plugins[key] = e
		l.entityBytes[entityBytesKey("Plugin", e.Metadata.Name)] = rawBytes
	}
}

// foldPinnedCacheEntities folds every lockfile-pinned Tier-2 cache slot whose kind
// is in allowed into the enumerated maps, under the SAME verify-on-read / pin
// discipline resolveComponent uses (tier2ReadVerified). The pin set (SetPins) is
// the lockfile's URI→digest map; the cache is keyed by URI, not walkable like the
// Tier-1 dirs, so enumeration is pin-driven. A pinned slot that is absent, drifted,
// or unreadable is skipped — a clean miss; an entity the build actually needs that
// is missing surfaces as a loud halt at the guard layer, not here. Recipe /
// FeatureRecipe slots are tar.gz bundles: the bundle is unwrapped to parse the
// entity YAML, but the WHOLE bundle blob is retained as entityBytes so the pinned
// digest covers the bundle (parity with the cache→catalog bridge). Deterministic:
// pins are visited in sorted URI order, so first-wins shadowing among pinned
// duplicates is stable across runs.
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
		blob, err := tier2ReadVerified(l.workspaceRoot, l.cacheDir, versionedSlotURI(uri, pin.ResolvedVersion), pin.Digest)
		if err != nil {
			continue
		}
		parseData := blob
		if oci.IsTarGzBundle(blob) {
			y, uerr := oci.EntityYAMLFromBundle(blob)
			if uerr != nil {
				continue
			}
			parseData = y
		}
		kind := peekEntityKind(parseData)
		if !allowed[kind] {
			continue
		}
		if kind == "SchemaLibrary" {
			l.foldPinnedSchemaLibrary(parseData, blob, "lockfile-pinned:"+uri)
			continue
		}
		l.foldRecipeShellEntity(kind, parseData, blob, "lockfile-pinned:"+uri)
	}
}

// foldPinnedSchemaLibrary folds one lockfile-pinned SchemaLibrary cache slot into
// l.schemaLibraries, first-wins (a Tier-1 library of the same name already present
// is kept). Mirrors the SchemaLibrary arm of buildSchemaLibraries' Tier-1 walk; the
// raw bytes are retained for digest fidelity.
func (l *LazyLookup) foldPinnedSchemaLibrary(parseData, rawBytes []byte, src string) {
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
}

// recordRecipeSkip emits the one-line tolerance warning and records the reason,
// keyed by file path (a parse failure has no reliable identity to key on).
func (l *LazyLookup) recordRecipeSkip(path string, err error) {
	fmt.Fprintf(os.Stderr, "  recipe: %s skipped: %s\n", path, err.Error())
	l.recipeSkips[path] = err
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
		Recipes:          l.recipes,
		FeatureRecipes:   l.featureRecipes,
		Aggregates:       l.aggregates,
		SchemaLibraries:  l.schemaLibraries,
		MappingLibraries: l.mappingLibraries,
		Plugins:          l.plugins,
		aggregateSources: l.aggregateSources,
		entityBytes:      l.entityBytes,
		workspaceRoot:    l.workspaceRoot,
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

// AssertManifestImplementsConsistency runs the catalog's manifest-implements
// consistency gate (each mapped type resolves to a recipe whose spec.implements
// matches) over the lazily-enumerated recipe set — the build's earliest loud
// guardrail, now reading the closure rather than the bridged eager catalog.
func (l *LazyLookup) AssertManifestImplementsConsistency(manifest *types.Manifest, manifestPath string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().AssertManifestImplementsConsistency(manifest, manifestPath)
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

// ProviderPluginByName resolves a provider name to the provider-rendering Plugin
// entity serving it (via its compatibleProviders) over the lazily-enumerated
// (Tier-1 + pinned Tier-2) Plugin set. It delegates to the same *Catalog matcher
// the eager backing uses (via the recipeCatalog() shell), so the two backings
// resolve identically — no per-backing matcher.
func (l *LazyLookup) ProviderPluginByName(name string) (types.Plugin, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().ProviderPluginByName(name)
}

// ClosureRecipeLoader exposes the source-descent recipe loader over the lazily-
// enumerated (Tier-1 + pinned Tier-2) recipe set, so pull's lock-time source
// descent loads a registry-resolved Recipe / FeatureRecipe from the pinned cache.
func (l *LazyLookup) ClosureRecipeLoader() closure.RecipeLoader {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recipeCatalog().ClosureRecipeLoader()
}

// SchemaLibrary / MappingLibrary resolve a library by its bare-name URI over the
// lazily-walked (Tier-1 + pinned Tier-2) library sets, for the closure-library
// discovery's transitive body access. Returns ok=false when the library is absent.
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

// Skips returns the tolerant-enumeration skip reasons keyed by file path — every
// catalog file the lazy enumeration could not index (a recipe/FR/aggregate/
// provider/mapping-library that failed to parse, a duplicate identity, or a
// schema library that failed its forbidden-keyword / template-body checks). The
// daemon surfaces these as a closure-level diagnostic so an in-closure entity
// that went missing because it was skipped is explained, not silently absent.
func (l *LazyLookup) Skips() map[string]error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildRecipeIndex()
	_ = l.buildSchemaLibraries()
	return l.recipeSkips
}

// ValidateClosureScopedPillars runs closure-scoped Pass-2 over the LAZY backing:
// it resolves each closure Component/Feature through the cascade and validates its
// schema pillars against the lazily-walked SchemaLibrary set — the daemon's
// early-warning gate, replacing the whole-catalog Pass-2 the eager
// resolveRecipeCatalog load used to provide. A broken out-of-closure entity is
// never resolved here, so it is never validated; an in-closure pillar error (a
// schema-invalid Component/Feature, or a $ref to a skipped/missing library) is
// surfaced. Sources come from the per-kind index (file paths) for error
// provenance. The resolved entities are extends-merged (what the daemon renders);
// build remains the authoritative pre-extends validator.
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
	return ValidateClosurePillars(comps, compSrc, feats, featSrc, l.schemaLibraries)
}

// ClosureLintWarnings returns the schema-pillar-overlap advisories for every
// Component and Feature the closure reaches — the non-fatal, warning-level
// sibling of ValidateClosureScopedPillars's pillar validation, over the same
// closure-entity set. The whole-catalog Pass-2 ran the overlap lint beside the
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
	// The closure's library set is the cross-document $ref context the schema
	// merge composes against — the same set the eager whole-catalog path feeds
	// CompileSchemaForMerge.
	_ = l.buildSchemaLibraries()

	compLookup := func(uri string) (types.Component, bool) { return l.resolveComponent(uri) }
	featLookup := func(uri string) (types.Feature, bool) { return l.resolveFeature(uri) }

	// CatalogEntry chain lookups that compile each chain entry's authored schema
	// into the JSON-Schema-merge input — identical to the eager ResolveAllExtends,
	// so both surfaces compose through the same ResolveInheritance merge phase (no
	// second merge implementation).
	compEntryLookup := func(uri string) (validator.CatalogEntry, bool) {
		comp, ok := compLookup(uri)
		if !ok {
			return validator.CatalogEntry{}, false
		}
		return validator.CatalogEntry{
			URI:        comp.Metadata.Name,
			Extends:    comp.Spec.Extends,
			JSONSchema: validator.CompileSchemaForMerge(comp.Spec.Schema, l.schemaLibraries),
		}, true
	}
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
			// Structural merge (metadata, secrets, uses, inject) — leaves
			// Spec.Schema as-authored on the child.
			if _, vErrs := validator.ResolveComponentExtends(comp, compLookup); len(vErrs) > 0 {
				for _, ve := range vErrs {
					errs = append(errs, ve)
				}
				continue
			}
			// Schema composition (values+infra+provides pillars the structural
			// merge drops) — the composed EffectiveSchema value conformance and
			// schema-defaults validate against.
			rootEntry := validator.CatalogEntry{
				URI:        comp.Metadata.Name,
				Extends:    comp.Spec.Extends,
				JSONSchema: validator.CompileSchemaForMerge(comp.Spec.Schema, l.schemaLibraries),
			}
			inh := validator.ResolveInheritance(rootEntry, compEntryLookup)
			if len(inh.Errors) > 0 {
				for _, ie := range inh.Errors {
					errs = append(errs, ie)
				}
				continue
			}
			storeEffective(comp.Metadata.Name, validator.CompositionFromResult(comp.Metadata.Name, inh))
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
			inh := validator.ResolveInheritance(rootEntry, featEntryLookup)
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

func (x *lazyClosureLookup) ResolveCatalogEntity(uri, kindHint string) (closure.Resolution, error) {
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
		if res, ok := x.l.resolveComponentForClosure(uri, x.puller); ok {
			return res, nil
		}
	}
	if kindHint != "component" {
		if res, ok := x.l.resolveFeatureForClosure(uri, x.puller); ok {
			return res, nil
		}
	}
	return x.base.ResolveCatalogEntity(uri, kindHint)
}

func (x *lazyClosureLookup) ResolveAggregate(uri string) (closure.Resolution, error) {
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	return x.base.ResolveAggregate(uri)
}

func (x *lazyClosureLookup) ResolveSchemaLibrary(uri string) (closure.LibraryResolution, error) {
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	return x.base.ResolveSchemaLibrary(uri)
}

func (x *lazyClosureLookup) ResolveMappingLibrary(uri string) (closure.LibraryResolution, error) {
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	return x.base.ResolveMappingLibrary(uri)
}

func (x *lazyClosureLookup) ResolvePlugin(uri string) (closure.Resolution, error) {
	x.l.mu.Lock()
	defer x.l.mu.Unlock()
	return x.base.ResolvePlugin(uri)
}

// resolveComponentForClosure resolves a Component URI for the closure walk —
// Tier-1 (filesystem index) → Tier-2 (verified cache) → Tier-3 (supplied puller),
// retaining the raw bytes for the digest. No extends-merge (the walk is
// structural). A non-Component cache slot (kind mismatch) falls through so the
// caller routes the URI to the recipe shell. Returns ok=false on cascade
// exhaustion. A tier2Skip (drifted cache) returns false — the entity is
// unavailable for this request, surfaced upstream rather than self-healed.
func (l *LazyLookup) resolveComponentForClosure(uri string, puller OCIPuller) (closure.Resolution, bool) {
	if comp, ok := l.tier1ReadComponent(uri); ok {
		return l.closureResolution("Component", comp.Metadata.Name, comp.Metadata.Version, uri, "tier-1-catalog"), true
	}
	if bytes, outcome := l.tier2Verified(uri); outcome == tier2Use {
		if comp, err := parseComponentEntity(bytes, uri); err == nil {
			l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
			return l.closureResolution("Component", comp.Metadata.Name, comp.Metadata.Version, uri, "tier-2-cache"), true
		}
	} else if outcome == tier2Skip {
		return closure.Resolution{}, false
	}
	if bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, puller, uri, "Component"); err == nil && bytes != nil {
		if comp, perr := parseComponentEntity(bytes, uri); perr == nil {
			l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
			return l.closureResolution("Component", comp.Metadata.Name, comp.Metadata.Version, uri, "tier-3-pull"), true
		}
	}
	return closure.Resolution{}, false
}

// resolveFeatureForClosure mirrors resolveComponentForClosure for the Feature
// surface.
func (l *LazyLookup) resolveFeatureForClosure(uri string, puller OCIPuller) (closure.Resolution, bool) {
	if feat, ok := l.tier1ReadFeature(uri); ok {
		return l.closureResolution("Feature", feat.Metadata.Name, feat.Metadata.Version, uri, "tier-1-catalog"), true
	}
	if bytes, outcome := l.tier2Verified(uri); outcome == tier2Use {
		if feat, err := parseFeatureEntity(bytes, uri); err == nil {
			l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
			return l.closureResolution("Feature", feat.Metadata.Name, feat.Metadata.Version, uri, "tier-2-cache"), true
		}
	} else if outcome == tier2Skip {
		return closure.Resolution{}, false
	}
	if bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, puller, uri, "Feature"); err == nil && bytes != nil {
		if feat, perr := parseFeatureEntity(bytes, uri); perr == nil {
			l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
			return l.closureResolution("Feature", feat.Metadata.Name, feat.Metadata.Version, uri, "tier-3-pull"), true
		}
	}
	return closure.Resolution{}, false
}

// closureResolution builds the closure.Resolution for a lazily-resolved entity,
// mirroring catalogLookup.tier1Resolution: digest of the retained raw bytes;
// resolved version = the URI's pin when present, else metadata.version; the tier
// tag matches the eager path's vocabulary so a drift compare never differs on it.
func (l *LazyLookup) closureResolution(kind, name, metaVersion, uri, path string) closure.Resolution {
	resolved := metaVersion
	if _, v, hasV := splitVersionedURI(uri); hasV && v != "" {
		resolved = v
	}
	return closure.Resolution{
		ResolvedVersion: resolved,
		Digest:          contentDigest(l.entityBytes[entityBytesKey(kind, name)]),
		ResolutionPath:  path,
	}
}
