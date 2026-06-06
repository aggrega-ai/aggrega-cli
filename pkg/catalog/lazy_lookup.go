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

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
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
		providers:        map[string]types.Provider{},
		mappingLibraries: map[string]types.MappingLibrary{},
		aggregateSources: map[string]string{},
		recipeSkips:      map[string]error{},
		entityBytes:      map[string][]byte{},
	}
}

// AsCatalogLookup returns the resolver.CatalogLookup view of the lazy
// lookup. Both Component and Feature fields close over the underlying
// *LazyLookup, so the same hot caches + negative caches + indexes
// serve every consumer.
func (l *LazyLookup) AsCatalogLookup() resolver.CatalogLookup {
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
	}
}

// Reset clears the hot caches, negative caches, and per-kind index
// state, returning the lookup to its post-construction shape.
// Subsequent Component / Feature calls re-walk the cascade.
//
// Reset does NOT touch the workspaceRoot / cacheDir / catalogDirs /
// puller configuration — those came from NewLazyLookup and stay
// stable across the session. Reset is the right operation when the
// workspace's filesystem state changed (a file was added, removed,
// or modified) and the lookup's cached resolutions could be stale.
//
// Thread-safety: the lazy lookup is not concurrency-safe; the
// daemon's sequential dispatch loop is the only intended caller.
//
// Idempotent: calling Reset twice in a row leaves the state empty;
// the second call re-allocates the maps but observes no state to
// clear.
func (l *LazyLookup) Reset() {
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
	l.pins = nil
	l.recipes = map[string]types.Recipe{}
	l.featureRecipes = map[string]types.FeatureRecipe{}
	l.aggregates = map[string]types.Aggregate{}
	l.providers = map[string]types.Provider{}
	l.mappingLibraries = map[string]types.MappingLibrary{}
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
	for uri, comp := range cat.Components {
		l.components[uri] = comp
	}
	for uri, feat := range cat.Features {
		l.features[uri] = feat
	}
	l.componentIndexBuilt = true
	l.featureIndexBuilt = true
}

type LazyLookup struct {
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

	// pins maps a versioned entity URI to its lockfile content-digest pin, set
	// per request via SetPins from the request's loaded lockfile. A Tier-2 cache
	// hit is verified against the pin before use (verify-on-read). nil pins means
	// no lockfile is threaded for this request — the cache is not trusted, so
	// Tier-2 falls through to a Tier-3 re-establish rather than returning
	// unverified bytes.
	pins map[string]string

	// Recipe / FeatureRecipe / Aggregate sets enumerated TOLERANTLY from the
	// catalog directories on first need (buildRecipeIndex). Recipes are keyed by
	// identity (metadata.name:version), the same key the eager catalog uses, so
	// the re-homed matcher resolves identically. A recipe/FR/aggregate whose body
	// won't parse (or whose identity collides) is SKIPPED with a warning and a
	// recorded reason rather than aborting the walk — so a broken UNUSED recipe is
	// invisible to a resolution that doesn't need it (the blast-radius fix). The
	// eager catalog, by contrast, fails the whole walk on the first broken recipe.
	recipes          map[string]types.Recipe
	featureRecipes   map[string]types.FeatureRecipe
	aggregates       map[string]types.Aggregate
	providers        map[string]types.Provider
	mappingLibraries map[string]types.MappingLibrary
	aggregateSources map[string]string
	recipeSkips      map[string]error // skipped recipe/FR/aggregate/provider/maplib file path -> reason
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

// SetPins threads the per-request lockfile pins into the lazy lookup, enabling
// verify-on-read on the Tier-2 cache. It MUST be called before the request's
// Component/Feature reads run (the daemon's sequential dispatch sets it from the
// per-request loaded lockfile, then resolves). Passing nil disables trust in the
// cache for the request: Tier-2 hits are not returned unverified; the cascade
// falls through to Tier-3.
//
// On a pin mismatch the slot is skipped (not used, not re-fetched) so the IR is
// not built from drifted content; the per-request drift DIAGNOSTIC is the
// closure-level lockfile-verify already run on the same request, so the skip
// deliberately does NOT emit a second notice (no double-report).
func (l *LazyLookup) SetPins(pins map[string]string) {
	l.pins = pins
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
	bytes, err := tier2ReadVerified(l.workspaceRoot, l.cacheDir, uri, pin)
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
	merged, vErrs := validator.ResolveComponentExtends(comp, l.Component)
	if len(vErrs) > 0 {
		fmt.Fprintf(os.Stderr, "  extends: Component %s: %s\n", uri, vErrs[0].Error())
	}
	l.components[uri] = merged
	return merged, true
}

// Feature mirrors Component for the Feature surface. Extends-resolution
// errors surface to os.Stderr per the same convention.
func (l *LazyLookup) Feature(uri string) (types.Feature, bool) {
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
	merged, vErrs := validator.ResolveFeatureExtends(feat, l.Feature)
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
		if comp, err := parseComponentEntity(bytes); err == nil {
			l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
			return comp, true
		}
	} else if outcome == tier2Skip {
		// Cache drifted from the pin; diagnostic already emitted. Do not use the
		// drifted bytes and do not self-heal — the entity is unavailable for this
		// request (an honest degraded preview, not a false-green render).
		return types.Component{}, false
	}
	bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, l.puller, uri)
	if err == nil && bytes != nil {
		if comp, perr := parseComponentEntity(bytes); perr == nil {
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
		if feat, err := parseFeatureEntity(bytes); err == nil {
			l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
			return feat, true
		}
	} else if outcome == tier2Skip {
		// Cache drifted from the pin; diagnostic already emitted. Do not use the
		// drifted bytes and do not self-heal.
		return types.Feature{}, false
	}
	bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, l.puller, uri)
	if err == nil && bytes != nil {
		if feat, perr := parseFeatureEntity(bytes); perr == nil {
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
	comp, perr := parseComponentEntity(data)
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
	feat, perr := parseFeatureEntity(data)
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
			lib, perr := parseSchemaLibraryEntity(data)
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
	return firstErr
}

// buildRecipeIndex walks the catalog directories once and TOLERANTLY enumerates
// every Recipe, FeatureRecipe, and Aggregate into the hot maps, keyed exactly as
// the eager catalog keys them (recipes/FRs by metadata.name:version, aggregates
// by metadata.name). A file that won't parse — or whose recipe/FR identity
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
			switch hdr.Kind {
			case "Recipe":
				e, perr := parseRecipeEntity(data, path)
				if perr != nil {
					l.recordRecipeSkip(path, perr)
					return nil
				}
				key := recipeURIKey(e.Metadata.Name, e.Metadata.Version)
				if _, dup := l.recipes[key]; dup {
					l.recordRecipeSkip(path, fmt.Errorf("duplicate recipe identity %q; this file is shadowed", key))
					return nil
				}
				l.recipes[key] = e
				l.entityBytes[entityBytesKey("Recipe", e.Metadata.Name)] = data
			case "FeatureRecipe":
				e, perr := parseFeatureRecipeEntity(data, path)
				if perr != nil {
					l.recordRecipeSkip(path, perr)
					return nil
				}
				key := recipeURIKey(e.Metadata.Name, e.Metadata.Version)
				if _, dup := l.featureRecipes[key]; dup {
					l.recordRecipeSkip(path, fmt.Errorf("duplicate feature recipe identity %q; this file is shadowed", key))
					return nil
				}
				l.featureRecipes[key] = e
				l.entityBytes[entityBytesKey("FeatureRecipe", e.Metadata.Name)] = data
			case "Aggregate":
				e, perr := parseAggregateEntity(data)
				if perr != nil {
					l.recordRecipeSkip(path, perr)
					return nil
				}
				if _, dup := l.aggregates[e.Metadata.Name]; dup {
					l.recordRecipeSkip(path, fmt.Errorf("duplicate aggregate name %q; this file is shadowed", e.Metadata.Name))
					return nil
				}
				l.aggregates[e.Metadata.Name] = e
				l.aggregateSources[e.Metadata.Name] = path
				l.entityBytes[entityBytesKey("Aggregate", e.Metadata.Name)] = data
			case "Provider":
				var e types.Provider
				if uerr := yaml.Unmarshal(data, &e); uerr != nil {
					l.recordRecipeSkip(path, uerr)
					return nil
				}
				if _, dup := l.providers[e.Metadata.Name]; dup {
					l.recordRecipeSkip(path, fmt.Errorf("duplicate provider name %q; this file is shadowed", e.Metadata.Name))
					return nil
				}
				l.providers[e.Metadata.Name] = e
				l.entityBytes[entityBytesKey("Provider", e.Metadata.Name)] = data
			case "MappingLibrary":
				e, perr := parseMappingLibraryEntity(data)
				if perr != nil {
					l.recordRecipeSkip(path, perr)
					return nil
				}
				if _, dup := l.mappingLibraries[e.Metadata.Name]; dup {
					l.recordRecipeSkip(path, fmt.Errorf("duplicate mapping library name %q; this file is shadowed", e.Metadata.Name))
					return nil
				}
				l.mappingLibraries[e.Metadata.Name] = e
				l.entityBytes[entityBytesKey("MappingLibrary", e.Metadata.Name)] = data
			}
			return nil
		})
	}
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
		Providers:        l.providers,
		SchemaLibraries:  l.schemaLibraries,
		MappingLibraries: l.mappingLibraries,
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
	l.buildRecipeIndex()
	return l.mappingLibraries
}

// RecipeLookupWithManifest exposes the catalog matcher's Manifest-aware recipe
// resolution over the lazily-enumerated recipe set. The Manifest mapping tiers
// resolve by URI (point-lookup); the implements fallback searches the enumerated
// set, halting loudly on ambiguity exactly as the eager path does.
func (l *LazyLookup) RecipeLookupWithManifest(manifest *types.Manifest) renderer.RecipeLookup {
	return l.recipeCatalog().RecipeLookupWithManifest(manifest)
}

// SubAggregateLoader exposes the catalog's sub-aggregate loader over the lazily-
// enumerated Aggregate set. A referenced sub-aggregate that is absent (or was
// skipped as broken) is a fatal graph-construction failure, exactly as eager.
func (l *LazyLookup) SubAggregateLoader() topology.SubAggregateLoader {
	return l.recipeCatalog().SubAggregateLoader()
}

// Skips returns the tolerant-enumeration skip reasons keyed by file path — every
// catalog file the lazy enumeration could not index (a recipe/FR/aggregate/
// provider/mapping-library that failed to parse, a duplicate identity, or a
// schema library that failed its forbidden-keyword / template-body checks). The
// daemon surfaces these as a closure-level diagnostic so an in-closure entity
// that went missing because it was skipped is explained, not silently absent.
func (l *LazyLookup) Skips() map[string]error {
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
	_ = l.buildSchemaLibraries()
	l.buildComponentIndex()
	l.buildFeatureIndex()
	comps := map[string]types.Component{}
	compSrc := map[string]string{}
	feats := map[string]types.Feature{}
	featSrc := map[string]string{}
	for _, e := range cl.Entities {
		name := lookupNameFromURI(e.URI)
		if comp, ok := l.Component(e.URI); ok {
			comps[comp.Metadata.Name] = comp
			compSrc[comp.Metadata.Name] = l.componentIndex[name]
			continue
		}
		if feat, ok := l.Feature(e.URI); ok {
			feats[feat.Metadata.Name] = feat
			featSrc[feat.Metadata.Name] = l.featureIndex[name]
		}
	}
	return ValidateClosurePillars(comps, compSrc, feats, featSrc, l.schemaLibraries)
}

// ResolveClosureExtends resolves the extends chain for each Component/Feature the
// closure reaches and returns the collected resolution errors (a dangling parent,
// a cycle, a malformed chain) — the daemon's early-warning extends gate, the lazy
// counterpart to the CLI build's closure-scoped extends. An out-of-closure
// entity's extends is never resolved, so a broken out-of-closure parent never
// produces a diagnostic. Deterministic: closure entity names are deduped + sorted.
func (l *LazyLookup) ResolveClosureExtends(cl closure.Closure) []error {
	compLookup := func(uri string) (types.Component, bool) { return l.resolveComponent(uri) }
	featLookup := func(uri string) (types.Feature, bool) { return l.resolveFeature(uri) }

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
			if _, vErrs := validator.ResolveComponentExtends(comp, compLookup); len(vErrs) > 0 {
				for _, ve := range vErrs {
					errs = append(errs, ve)
				}
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
			}
		}
	}
	return errs
}

// ClosureLookup returns a closure.Lookup that walks a Release's closure over the
// LAZY backing — the daemon's replacement for the eager whole-catalog
// `*Catalog.ClosureLookupWithExternals`. No catalog is loaded up front:
// Component/Feature resolve per-URI through the Tier-1 (filesystem index) →
// Tier-2 (verified cache) → Tier-3 (the SUPPLIED puller) cascade, and
// Recipe/FeatureRecipe/Aggregate/Provider/SchemaLibrary/MappingLibrary resolve
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
// which serves Recipe/FR/Aggregate/Provider at Tier-1 and all kinds at Tier-2/3.
type lazyClosureLookup struct {
	l      *LazyLookup
	puller OCIPuller
	base   closure.Lookup
}

func (x *lazyClosureLookup) ResolveCatalogEntity(uri string) (closure.Resolution, error) {
	if res, ok := x.l.resolveComponentForClosure(uri, x.puller); ok {
		return res, nil
	}
	if res, ok := x.l.resolveFeatureForClosure(uri, x.puller); ok {
		return res, nil
	}
	return x.base.ResolveCatalogEntity(uri)
}

func (x *lazyClosureLookup) ResolveAggregate(uri string) (closure.Resolution, error) {
	return x.base.ResolveAggregate(uri)
}

func (x *lazyClosureLookup) ResolveSchemaLibrary(uri string) (closure.LibraryResolution, error) {
	return x.base.ResolveSchemaLibrary(uri)
}

func (x *lazyClosureLookup) ResolveMappingLibrary(uri string) (closure.LibraryResolution, error) {
	return x.base.ResolveMappingLibrary(uri)
}

func (x *lazyClosureLookup) ResolveWasmPlugin(uri string) (closure.Resolution, error) {
	return x.base.ResolveWasmPlugin(uri)
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
		if comp, err := parseComponentEntity(bytes); err == nil {
			l.entityBytes[entityBytesKey("Component", comp.Metadata.Name)] = bytes
			return l.closureResolution("Component", comp.Metadata.Name, comp.Metadata.Version, uri, "tier-2-cache"), true
		}
	} else if outcome == tier2Skip {
		return closure.Resolution{}, false
	}
	if bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, puller, uri); err == nil && bytes != nil {
		if comp, perr := parseComponentEntity(bytes); perr == nil {
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
		if feat, err := parseFeatureEntity(bytes); err == nil {
			l.entityBytes[entityBytesKey("Feature", feat.Metadata.Name)] = bytes
			return l.closureResolution("Feature", feat.Metadata.Name, feat.Metadata.Version, uri, "tier-2-cache"), true
		}
	} else if outcome == tier2Skip {
		return closure.Resolution{}, false
	}
	if bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, puller, uri); err == nil && bytes != nil {
		if feat, perr := parseFeatureEntity(bytes); perr == nil {
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
