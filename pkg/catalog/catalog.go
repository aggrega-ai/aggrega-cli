// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"go.yaml.in/yaml/v3"
)

// Catalog holds every entity loaded from a directory walk and exposes both a
// direct-map surface and adapter callbacks for the compiler pipeline.
//
// The map fields are public so callers that need to iterate (deprecation
// scans, the inspect command's catalog dump, tests asserting entity counts)
// can read them without going through a callback. The two surfaces are not
// redundant: the callback returners (CatalogLookup, RecipeLookup,
// SubAggregateLoader) conform to interfaces each consumer package defines on
// its own side. Adapting at the catalog boundary keeps consumer interfaces free of
// pkg/catalog import dependencies and lets each consumer evolve its data
// needs without forcing the map layout to change.
//
// Recipes and FeatureRecipes index by spec.implements so the renderer's
// per-component lookup ("which Recipe implements core/api-server?") is a
// single map fetch. Components, Features, and Aggregates index by
// metadata.name because that is the URI the resolver receives.
type Catalog struct {
	Components       map[string]types.Component
	Features         map[string]types.Feature
	Recipes          map[string]types.Recipe         // keyed by identity (provider/name:version)
	FeatureRecipes   map[string]types.FeatureRecipe  // keyed by identity (provider/name:version)
	Aggregates       map[string]types.Aggregate      // keyed by metadata.name
	SchemaLibraries  map[string]types.SchemaLibrary  // keyed by metadata.name (PascalCase library identifier)
	MappingLibraries map[string]types.MappingLibrary // keyed by metadata.name (PascalCase library identifier)
	Plugins          map[string]types.Plugin         // keyed by identity (metadata.name:version)

	// componentSources / featureSources / pluginSources
	// track the file path each entity was loaded from. Populated by Pass 1
	// (indexFile) and consumed by Pass 2 (the schema validation pass at the
	// end of LoadAll) so the validator's error messages name the offending
	// file. Unexported because the bookkeeping is internal to LoadAll;
	// downstream callers identify entities by metadata.name, not by source
	// path.
	componentSources map[string]string
	featureSources   map[string]string
	pluginSources    map[string]string

	// aggregateSources records the absolute filesystem path each Aggregate
	// entity was loaded from. Populated by Pass 1 (indexFile) and exposed
	// by AggregateSourceFile (and the SubAggregateLoader callback) after
	// normalization against workspaceRoot — see RelativeToWorkspace.
	aggregateSources map[string]string

	// workspaceRoot is the workspace directory the caller resolved its
	// inputs from. Set at Load/LoadAll construction time so the public
	// source-file accessors render their returned paths workspace-
	// relative, keeping the IR wire shape byte-identical across
	// developer machines whose workspaces live at different absolute
	// paths. Empty disables normalization — paths flow through
	// verbatim. The catalog's load-time fields (Components, Features,
	// etc.) remain absolute; only the path-rendering accessors consult
	// this field.
	workspaceRoot string

	// lintWarnings accumulates non-fatal advisory diagnostics raised
	// during catalog load — currently the schema-pillar-overlap
	// lint, which warns when a Component or Feature declares the
	// same property name in both values: and infra: pillars at the
	// same nesting depth. Build callers retrieve these via
	// LintWarnings() and print them to stderr alongside
	// DeprecationWarnings.
	lintWarnings []string

	// effectiveSchemas holds the composed (parent+child) schema for each
	// entity that declares extends:, keyed by metadata.name, in compactform
	// map form. Populated by ResolveAllExtends from the inheritance JSON-Schema
	// merge and exposed via resolver.CatalogLookup.EffectiveSchema so the
	// schema-conformance gate, defaults materialisation, and inject-key
	// provides extraction validate an extends-ing entity against its inherited
	// schema rather than its own authored slice. Absent for non-extends
	// entities — those consumers fall back to compiling the authored schema, so
	// the non-extends path is unchanged.
	effectiveSchemas map[string]compactform.Schema

	// entityBytes records the raw YAML bytes each entity was loaded
	// from, keyed by "<kind>:<identifier>" where identifier matches
	// the typed-map key (metadata.name for Component/Feature/
	// Aggregate/SchemaLibrary/MappingLibrary, spec.implements
	// for Recipe/FeatureRecipe). ClosureLookup hashes these bytes to
	// produce the content-digest the lockfile pins for Tier-1 hits.
	// Populated lazily during indexFile; unset for entity kinds the
	// closure walker does not resolve (currently none — every kind
	// the walker traverses is recorded here).
	entityBytes map[string][]byte

	// pathByIdentity maps "<Kind>:<identity-key>" to the file each entity was
	// loaded from, for every indexed kind. indexFile populates it and uses it
	// to detect a WITHIN-DIRECTORY duplicate identity (a second file claiming
	// an identity already seen in the same directory load is the homogenized
	// ErrDuplicateEntityIdentity collision). It also supplies the winner/shadowed
	// file paths when an ordered multi-directory merge records a ShadowedEntity.
	pathByIdentity map[string]string

	// Shadows records, in deterministic listed-directory order, every entity a
	// higher-precedence catalog directory overrode during a multi-directory
	// load (see ShadowedEntity). Empty for a single-directory load. Surfaced on
	// the resolution-detail reporting path; it never alters which entity
	// resolves (first-win is decided at merge), only makes the override visible.
	Shadows []ShadowedEntity

	// resolveEmbeddedProviders marks this catalog as the production build/pull
	// workspace view that should resolve core providers from the binary's embedded
	// entity set at render time (providerResolutionPlugins) — the embedded-wins
	// overlay. It is set by the lazy backing shell (recipeCatalog), the resolution
	// path every real build and pull runs through, so a no-local-catalog build
	// resolves a core provider offline and a dev build renders with its own
	// embedded core over a same-coordinate catalog entry. It is deliberately NOT
	// set on the eager whole-catalog view or on a bare Catalog literal: those back
	// controlled, self-contained catalogs (the build-faithful render harness, the
	// resolver guards, direct-constructed test fixtures) that must render exactly
	// the providers they declare, unperturbed by the binary's embedded set.
	resolveEmbeddedProviders bool
}

// LoadAll walks dirs in argument order, recursively reads every .yaml / .yml
// file under each, and indexes every catalog-resident entity kind into a
// single Catalog. Files are dispatched by their top-level "kind:" field; see
// indexFile for the dispatch table.
//
// workspaceRoot is the workspace the loader is loading on behalf of: the
// catalog's source-file accessors (AggregateSourceFile, SubAggregateLoader)
// render their returned paths relative to this root so the IR wire shape
// stays byte-identical across developer machines whose workspaces live at
// different absolute paths. Pass "" when the caller has no workspace
// concept — paths flow through verbatim, the no-normalization mode used
// by synthetic tests and workspace-agnostic operations.
//
// Layered-catalog merge. Later directories override earlier ones on key
// collisions — last write wins on the entity's index key (spec.implements
// for Recipes / FeatureRecipes, metadata.name for the others). The pattern
// supports layered catalog authoring like `base/` + `recipes/<variant>/`
// where the variant directory's Recipe replaces the base entry under the
// same `implements` URI without further ceremony. Within a single directory
// the merge order is filesystem-defined (filepath.WalkDir order); duplicate
// keys inside one directory are not currently flagged.
//
// Error model. A read or unmarshal error on a recognised entity kind aborts
// the walk and surfaces as a "catalog load %q: ..." wrapping the underlying
// error. YAML files whose top-level structure can't be unmarshalled into
// the entityHeader peek (malformed YAML lacking a parseable "kind:" field)
// are silently skipped — the catalog tree may legitimately contain
// non-entity YAML (catalog-conventions docs, fixture configs, build
// metadata) and the loader does not assume every .yaml file is an entity.
// Unknown kinds (e.g. "Manifest", "Release", or a typo'd kind) parse cleanly
// and are also silently skipped — Manifest and Release are deployment-side
// entities loaded through different code paths and are expected to coexist
// in some directory layouts.
//
// Recipe-side side effects. The Recipe dispatch arm runs validator.ValidateRecipe
// at load time (load-time shape rules from chapter 16: valuesMapping ⇔
// source pairing, workload mutex, at-least-one-input-kind, artifacts
// basename collision, eval-provider input-kind ban) and rewrites a relative
// string spec.source to an absolute path against the Recipe file's
// directory so co-located charts stay resolvable when the consuming code
// is run from a different working directory.
//
// Two-pass load model. Pass 1 walks dirs and hydrates every entity into the
// typed maps via indexFile. Pass 2 runs at the end of LoadAll once Pass 1
// has populated the full SchemaLibraries map, and validates each Component
// and Feature's spec.schema body against the canonical pillar contract via
// validator.ValidateSchemaPillars. Splitting the work lets cross-document
// $ref expansion succeed regardless of filesystem walk order: a Feature
// authored before its SchemaLibrary in walk order still validates because
// the library is in the map by the time Pass 2 runs. Pass 1 errors are
// fail-fast (a YAML parse failure aborts the walk); Pass 2 errors are
// collected across entities and surfaced as a single aggregate CatalogError
// so authors fix all schema failures in one editing pass.
func LoadAll(workspaceRoot string, dirs ...string) (*Catalog, error) {
	return LoadAllWithCacheDir(workspaceRoot, "", dirs...)
}

// LoadAllWithCacheDir is LoadAll with the content-addressed cache directory
// skipped during the walk. cacheDir is the workspace-relative cache path (the
// resolution-artifact store, e.g. ".aggrega/cache"); a directory whose path
// relative to a walked root equals cacheDir is skipped, so cached
// `library.yaml` files are never ingested as authored Tier-1 catalog. The skip
// anchors on each walked root (not on workspaceRoot), so it fires for the
// `--catalog .` default regardless of whether workspaceRoot is set. cacheDir
// "" disables the skip — the LoadAll / Load behavior, for callers with no cache.
func LoadAllWithCacheDir(workspaceRoot, cacheDir string, dirs ...string) (*Catalog, error) {
	c, err := loadAllPassOne(workspaceRoot, cacheDir, dirs...)
	if err != nil {
		return nil, err
	}
	if err := c.validateSchemaPillarsPass(); err != nil {
		return nil, err
	}
	return c, nil
}

// peekEntityKind returns the `kind` of an entity from its raw bytes, or "" when
// the bytes are not a parseable YAML entity envelope (e.g. a Recipe's tar.gz
// cache slot, whose bytes are gzip, not YAML). Used by the lazy lookup's
// pin-driven Tier-2 enumeration to dispatch a cache-present entity by kind
// without committing to a full parse.
func peekEntityKind(data []byte) string {
	var hdr struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &hdr); err != nil {
		return ""
	}
	return hdr.Kind
}

// entityMetadataVersion reads metadata.version from an entity's raw bytes, or
// "" when the bytes are not a parseable YAML envelope. The registry tier pins
// this (the entity's identity) rather than the OCI tag it was fetched under.
func entityMetadataVersion(data []byte) string {
	var hdr struct {
		Metadata struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(data, &hdr); err != nil {
		return ""
	}
	return hdr.Metadata.Version
}

// sortedMapKeys returns the keys of any string-keyed map in sorted order — the
// generic companion to sortedKeys, used by the first-win merge to visit each
// kind's identities deterministically.
func sortedMapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// LoadPassOne runs only Pass 1 of the catalog load — the filesystem walk +
// per-file indexing, content-cache directory skipped — WITHOUT Pass-2 pillar
// validation. Pass 1 alone is sufficient for the closure walk (which scans raw
// `$ref` values, not expanded pillars); cross-document `$ref` expansion is the
// closure-scoped Pass-2's job, against the resolved closure's library set.
func LoadPassOne(workspaceRoot, cacheDir string, dirs ...string) (*Catalog, error) {
	return loadAllPassOne(workspaceRoot, cacheDir, dirs...)
}

// SkippedEntity records a catalog file the TOLERANT Pass-1 walk
// (LoadPassOneTolerant) could not index — a parse failure, a load-time shape
// violation, a duplicate identity. Kind and Name are best-effort from a header
// re-parse (empty when the YAML is malformed enough to lose even the header);
// Err is the original indexing error. Callers replay these on an in-closure
// resolution failure so a tolerated out-of-closure skip never hides WHY an
// entity the Release reaches went missing.
type SkippedEntity struct {
	Path string
	Kind string
	Name string
	Err  error
}

// ShadowedEntity records a catalog entity that was OVERRIDDEN by a
// higher-precedence catalog directory during a multi-directory load. The
// catalog_dirs list is an ordered, first-win precedence stack: when one
// identity appears in more than one directory, the earlier-listed directory
// wins and the later occurrence is shadowed (not loaded, not an error — an
// intentional, observable override). Each shadow names the kind, the identity,
// the winning directory + file, and the shadowed directory + file, so the
// resolution-detail surface can report exactly what overrode what.
type ShadowedEntity struct {
	Kind         string
	Identity     string
	WinnerDir    string
	WinnerPath   string
	ShadowedDir  string
	ShadowedPath string
}

// loadAllPassOne runs Pass 1 only — the filesystem walk + per-file indexing with
// the content-addressed cache directory skipped — and returns the catalog BEFORE
// Pass-2 pillar validation (validateSchemaPillarsPass). LoadAllWithCacheDir runs
// Pass 2 immediately after. cacheDir "" disables the cache skip.
//
// Fail-fast: a per-file read / parse / index error aborts the whole walk. The
// tolerant counterpart (LoadPassOneTolerant) skips-and-records instead, for the
// closure-scoped build / pull paths where an out-of-closure broken entity must
// not fail an unrelated Release.
func loadAllPassOne(workspaceRoot, cacheDir string, dirs ...string) (*Catalog, error) {
	c, _, err := loadAllPassOneImpl(workspaceRoot, cacheDir, false, dirs...)
	return c, err
}

// LoadPassOneTolerant runs Pass 1 like LoadPassOne but SKIPS a file that fails to
// read, parse, or index — recording it in the returned []SkippedEntity — rather
// than aborting the walk. The catalog returned holds every parseable entity; a
// broken file is simply absent. This is the closure-scoped build / pull load:
// combined with closure-scoped Pass-2, a broken entity OUTSIDE the Release's
// closure never fails the build, while an in-closure break still fails loudly
// (it is either absent — surfaced by the closure walk, with the skip replayed —
// or present-but-schema-invalid, surfaced by closure-scoped Pass-2).
//
// The skip is per-file and post-`indexFile`: `indexFile` mutates the catalog
// only after a successful parse, so a skipped file leaves no partial state.
func LoadPassOneTolerant(workspaceRoot, cacheDir string, dirs ...string) (*Catalog, []SkippedEntity, error) {
	return loadAllPassOneImpl(workspaceRoot, cacheDir, true, dirs...)
}

// loadAllPassOneImpl is the shared Pass-1 walk. When tolerant is false it is
// fail-fast (the historical loadAllPassOne behavior, byte-identical); when true a
// per-file read/parse/index failure is recorded and skipped. The returned skip
// slice is nil in the fail-fast mode.
func loadAllPassOneImpl(workspaceRoot, cacheDir string, tolerant bool, dirs ...string) (*Catalog, []SkippedEntity, error) {
	cacheClean := ""
	if cacheDir != "" {
		cacheClean = filepath.Clean(cacheDir)
	}

	// catalog_dirs is an ordered first-win precedence stack. Each directory is
	// indexed into its OWN catalog first — so a duplicate identity WITHIN one
	// directory is the ErrDuplicateEntityIdentity collision (caught by
	// indexFile via pathByIdentity), distinct from the same identity appearing
	// ACROSS directories, which mergeFrom resolves as a first-win shadow. The
	// per-directory walk preserves the tolerant-skip-vs-strict-halt split: a
	// read/index failure (including a within-directory duplicate) is recorded
	// and skipped in tolerant mode, or aborts the load in strict mode, exactly
	// as before.
	acc := newPassOneCatalog(workspaceRoot)
	winnerDir := make(map[string]string) // "<Kind>:<identity>" -> directory that won it
	var skipped []SkippedEntity
	for _, dir := range dirs {
		perDir := newPassOneCatalog(workspaceRoot)
		dirSkips, err := perDir.walkDirInto(dir, cacheClean, tolerant)
		if err != nil {
			return nil, nil, err
		}
		skipped = append(skipped, dirSkips...)
		acc.mergeFrom(perDir, dir, winnerDir)
	}
	return acc, skipped, nil
}

// newPassOneCatalog allocates a catalog with every Pass-1 index map ready. Both
// the accumulated result and each per-directory sub-index are built through it
// so they share one initialisation.
func newPassOneCatalog(workspaceRoot string) *Catalog {
	return &Catalog{
		workspaceRoot:    workspaceRoot,
		Components:       make(map[string]types.Component),
		Features:         make(map[string]types.Feature),
		Recipes:          make(map[string]types.Recipe),
		FeatureRecipes:   make(map[string]types.FeatureRecipe),
		Aggregates:       make(map[string]types.Aggregate),
		SchemaLibraries:  make(map[string]types.SchemaLibrary),
		MappingLibraries: make(map[string]types.MappingLibrary),
		Plugins:          make(map[string]types.Plugin),
		componentSources: make(map[string]string),
		featureSources:   make(map[string]string),
		pluginSources:    make(map[string]string),
		aggregateSources: make(map[string]string),
		entityBytes:      make(map[string][]byte),
		pathByIdentity:   make(map[string]string),
	}
}

// walkDirInto indexes one catalog directory into c. cacheClean (when non-empty)
// is the workspace-relative cache path skipped during the walk. tolerant selects
// skip-and-record vs fail-fast for a per-file read/index failure — identical to
// the historical single-walk behavior, now scoped to one directory so a
// within-directory duplicate identity is caught here while a cross-directory
// duplicate is left for the first-win merge.
func (c *Catalog) walkDirInto(dir, cacheClean string, tolerant bool) ([]SkippedEntity, error) {
	var skipped []SkippedEntity
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip the content-addressed cache: it is a resolution
			// artifact, not authored Tier-1 catalog. Anchored on the
			// walked root so the `--catalog .` default (cache under the
			// catalog dir) is caught regardless of workspaceRoot.
			if cacheClean != "" {
				if rel, relErr := filepath.Rel(dir, path); relErr == nil && filepath.Clean(rel) == cacheClean {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if tolerant {
				skipped = append(skipped, newSkippedEntity(path, data, err))
				return nil
			}
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "read catalog file")
		}
		if ierr := c.indexFile(data, path); ierr != nil {
			if tolerant {
				skipped = append(skipped, newSkippedEntity(path, data, ierr))
				return nil
			}
			return ierr
		}
		return nil
	}); err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: dir}, "catalog load")
	}
	return skipped, nil
}

// mergeFrom folds one already-indexed directory (perDir) into the accumulated
// catalog with FIRST-WIN precedence: an identity acc already holds (from an
// earlier-listed directory) wins, and perDir's occurrence is recorded as a
// ShadowedEntity rather than overwriting. A not-yet-seen identity is inserted
// with its side-table entries (source path, raw bytes). Iteration is in sorted
// identity order per kind so the resolved set and the Shadows list are a pure,
// deterministic function of directory order — preserving byte-identical output.
func (acc *Catalog) mergeFrom(perDir *Catalog, perDirRoot string, winnerDir map[string]string) {
	mergeKind(acc, "Component", acc.Components, perDir.Components, perDir, perDirRoot, winnerDir, func(k string) {
		acc.componentSources[k] = perDir.componentSources[k]
		bk := entityBytesKey("Component", perDir.Components[k].Metadata.Name)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	mergeKind(acc, "Feature", acc.Features, perDir.Features, perDir, perDirRoot, winnerDir, func(k string) {
		acc.featureSources[k] = perDir.featureSources[k]
		bk := entityBytesKey("Feature", perDir.Features[k].Metadata.Name)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	mergeKind(acc, "Recipe", acc.Recipes, perDir.Recipes, perDir, perDirRoot, winnerDir, func(k string) {
		bk := entityBytesKey("Recipe", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	mergeKind(acc, "FeatureRecipe", acc.FeatureRecipes, perDir.FeatureRecipes, perDir, perDirRoot, winnerDir, func(k string) {
		bk := entityBytesKey("FeatureRecipe", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	mergeKind(acc, "Aggregate", acc.Aggregates, perDir.Aggregates, perDir, perDirRoot, winnerDir, func(k string) {
		acc.aggregateSources[k] = perDir.aggregateSources[k]
		bk := entityBytesKey("Aggregate", perDir.Aggregates[k].Metadata.Name)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	mergeKind(acc, "SchemaLibrary", acc.SchemaLibraries, perDir.SchemaLibraries, perDir, perDirRoot, winnerDir, func(k string) {
		bk := entityBytesKey("SchemaLibrary", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	mergeKind(acc, "MappingLibrary", acc.MappingLibraries, perDir.MappingLibraries, perDir, perDirRoot, winnerDir, func(k string) {
		bk := entityBytesKey("MappingLibrary", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	// Plugin's bytes side-table keys on the plugin coordinate (the closure walker
	// hashes them for the lockfile pin) while the typed map and the source
	// side-table key on the coordinate identity (coordinate:version) — the same
	// split the Recipe / Library kinds use for their typed maps. The source
	// side-table feeds the Pass-2 spec.provider.workloadSchema compile gate so its
	// error messages name the offending file.
	mergeKind(acc, "Plugin", acc.Plugins, perDir.Plugins, perDir, perDirRoot, winnerDir, func(k string) {
		acc.pluginSources[k] = perDir.pluginSources[k]
		bk := entityBytesKey("Plugin", perDir.Plugins[k].Coordinate())
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
}

// mergeKind merges one entity kind from src into dst with first-win precedence,
// appending a ShadowedEntity to acc for each identity dst already holds and
// running copyAux for each newly-inserted identity (its source / bytes side
// tables). Keys are visited in sorted order so the Shadows list is deterministic.
func mergeKind[T any](
	acc *Catalog,
	kind string,
	dst, src map[string]T,
	perDir *Catalog,
	perDirRoot string,
	winnerDir map[string]string,
	copyAux func(key string),
) {
	for _, key := range sortedMapKeys(src) {
		fk := kind + ":" + key
		foldFirstWin(dst, key, src[key],
			func() {
				acc.pathByIdentity[fk] = perDir.pathByIdentity[fk]
				winnerDir[fk] = perDirRoot
				copyAux(key)
			},
			func() {
				acc.Shadows = append(acc.Shadows, ShadowedEntity{
					Kind:         kind,
					Identity:     key,
					WinnerDir:    winnerDir[fk],
					WinnerPath:   acc.pathByIdentity[fk],
					ShadowedDir:  perDirRoot,
					ShadowedPath: perDir.pathByIdentity[fk],
				})
			})
	}
}

// foldFirstWin is the single source of the "which occurrence of a duplicated
// identity resolves" decision across every catalog loading strategy. The FIRST
// occurrence of an identity claims it; a later occurrence is a shadow. The eager
// whole-catalog merge (mergeKind), the lazy recipe-shell enumeration
// (foldRecipeShellEntity), and the lazy Component/Feature index walk
// (walkAndIndex) all fold through this primitive, so the three strategies cannot
// drift on which duplicate wins. Strategies vary only in the value type V and the
// onWin / onShadow callbacks, never in the decision.
//
// claimed is the identity→value map being populated. When identity is already
// present, onShadow (if non-nil) runs and the function returns false without
// mutating claimed. When identity is new, value is stored, onWin (if non-nil)
// runs, and the function returns true. It decides cross-occurrence precedence
// only; a strategy's within-directory duplicate rule is a distinct concern the
// caller keeps.
func foldFirstWin[V any](claimed map[string]V, identity string, value V, onWin, onShadow func()) bool {
	if _, exists := claimed[identity]; exists {
		if onShadow != nil {
			onShadow()
		}
		return false
	}
	claimed[identity] = value
	if onWin != nil {
		onWin()
	}
	return true
}

// newSkippedEntity builds a SkippedEntity, recovering kind + name from a
// best-effort header re-parse (empty when the YAML is too malformed to peek).
func newSkippedEntity(path string, data []byte, err error) SkippedEntity {
	se := SkippedEntity{Path: path, Err: err}
	var hdr fileHeader
	if yaml.Unmarshal(data, &hdr) == nil {
		se.Kind = hdr.Kind
		se.Name = hdr.Metadata.Name
	}
	return se
}

// validateSchemaPillarsPass is Pass 2 of LoadAll: walks every Component,
// Feature, and Plugin in sorted order, runs the corresponding
// load-time validator against each entity's schema-bearing fields with
// the now-fully-populated SchemaLibraries map for cross-document $ref
// expansion (Component / Feature only), collects every failure, and
// returns a single aggregated CatalogError when at least one entity
// failed. Sorted iteration is required for diagnostic stability:
// map-iteration order would flap across runs and make CI logs unstable
// against the deterministic-output invariant.
//
// A Plugin's provider block validates the flat workloadSchema JSON Schema body
// (different contract from the pillar-wrapped Component / Feature schemas —
// no SchemaLibrary $ref expansion is expected at the Plugin provider field
// surface today). Wired so today's catalogs reach the same load-time guardrail
// the Component / Feature surface enjoys. A capability served by more than one
// provider-rendering Plugin is NOT a load error — a federated catalog is legal;
// the ambiguity halts at the point of use (pull resolution / build render) when no
// plugins.providers entry selects one.
func (c *Catalog) validateSchemaPillarsPass() error {
	var validationErrors []error

	componentNames := make([]string, 0, len(c.Components))
	for name := range c.Components {
		componentNames = append(componentNames, name)
	}
	sort.Strings(componentNames)
	for _, name := range componentNames {
		comp := c.Components[name]
		if err := validator.ValidateSchemaPillars(
			"Component",
			name,
			c.componentSources[name],
			&comp.Spec.Schema,
			c.SchemaLibraries,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
		c.lintWarnings = append(c.lintWarnings, validator.ValidateSchemaPillarOverlap(
			"Component",
			name,
			c.componentSources[name],
			comp.Spec.Schema,
		)...)
	}

	featureNames := make([]string, 0, len(c.Features))
	for name := range c.Features {
		featureNames = append(featureNames, name)
	}
	sort.Strings(featureNames)
	for _, name := range featureNames {
		feat := c.Features[name]
		if err := validator.ValidateSchemaPillars(
			"Feature",
			name,
			c.featureSources[name],
			&feat.Spec.Schema,
			c.SchemaLibraries,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
		c.lintWarnings = append(c.lintWarnings, validator.ValidateSchemaPillarOverlap(
			"Feature",
			name,
			c.featureSources[name],
			feat.Spec.Schema,
		)...)
	}

	pluginKeys := make([]string, 0, len(c.Plugins))
	for key := range c.Plugins {
		pluginKeys = append(pluginKeys, key)
	}
	sort.Strings(pluginKeys)
	for _, key := range pluginKeys {
		p := c.Plugins[key]
		for _, err := range validator.ValidatePluginProviderSchemas(
			key,
			c.pluginSources[key],
			&p.Spec,
		) {
			validationErrors = append(validationErrors, err)
		}
	}

	// A capability served by more than one provider-rendering Plugin is NOT a
	// catalog-load error: a federated catalog where two publishers each ship a
	// plugin for the same target is legal. Ambiguity is a point-of-use concern the
	// plugins.providers surface resolves at pull, and it halts loudly there (and at
	// build render) via ErrAmbiguousProviderPlugin when a reached target is served
	// by more than one plugin and no binding selects one — so an ambiguity outside a
	// Release's reach never blocks an unrelated build.

	if len(validationErrors) == 0 {
		return nil
	}
	parts := make([]string, 0, len(validationErrors))
	for _, e := range validationErrors {
		parts = append(parts, e.Error())
	}
	return cerrors.New(
		cerrors.CatalogError,
		cerrors.SourceLocation{},
		fmt.Sprintf("catalog load: %d schema validation error(s):\n  - %s",
			len(validationErrors), strings.Join(parts, "\n  - ")),
		nil,
		nil,
	)
}

// ValidateClosurePillars is the closure-scoped counterpart to
// validateSchemaPillarsPass: it validates ONLY the supplied Components and
// Features — the Release's closure — against the supplied SchemaLibrary map,
// rather than every entity in the catalog. The library map is the closure's
// walked library set (not the whole catalog's), so cross-document $ref expansion
// resolves exactly the libraries the closure reaches. A closure entity whose
// $ref names a library absent from libs fails here (the hard in-closure
// library-reject) via validator.ValidateSchemaPillars surfacing the
// unresolvable-$ref; the callers (build's tolerant-load SkippedEntity context,
// the daemon's Skips() diagnostic) explain WHY a referenced library went missing
// when it was skipped as broken.
//
// Iteration is sorted by name for diagnostic stability (the deterministic-output
// invariant); errors are collected across entities and joined into one aggregate
// CatalogError. Returns nil when every supplied entity validates. The advisory
// pillar-overlap lint is intentionally NOT run here — it is a non-fatal authoring
// smell the build collects separately, not part of the reject gate.
func ValidateClosurePillars(
	components map[string]types.Component, componentSources map[string]string,
	features map[string]types.Feature, featureSources map[string]string,
	libs map[string]types.SchemaLibrary,
) error {
	var validationErrors []error

	compNames := make([]string, 0, len(components))
	for name := range components {
		compNames = append(compNames, name)
	}
	sort.Strings(compNames)
	for _, name := range compNames {
		comp := components[name]
		if err := validator.ValidateSchemaPillars(
			"Component", name, componentSources[name], &comp.Spec.Schema, libs,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	featNames := make([]string, 0, len(features))
	for name := range features {
		featNames = append(featNames, name)
	}
	sort.Strings(featNames)
	for _, name := range featNames {
		feat := features[name]
		if err := validator.ValidateSchemaPillars(
			"Feature", name, featureSources[name], &feat.Spec.Schema, libs,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	if len(validationErrors) == 0 {
		return nil
	}
	parts := make([]string, 0, len(validationErrors))
	for _, e := range validationErrors {
		parts = append(parts, e.Error())
	}
	return cerrors.New(
		cerrors.CatalogError,
		cerrors.SourceLocation{},
		fmt.Sprintf("closure validation: %d schema validation error(s):\n  - %s",
			len(validationErrors), strings.Join(parts, "\n  - ")),
		nil,
		nil,
	)
}

// entityHeader peeks at the top-level "kind:" field before deciding which
// pkg/types struct to unmarshal into. The peek is cheap (a partial YAML
// decode that only touches one field) and lets indexFile dispatch through
// a single switch on Kind without paying the cost of attempting six
// speculative full-struct unmarshals per file.
type entityHeader struct {
	Kind string `yaml:"kind"`
}

// indexFile is the single point of dispatch over the catalog-resident
// entity kinds — the SSOT for "what counts as a catalog entity" in this
// package. The switch arms enumerate every catalog entity kind
// (Component, Feature, Recipe, FeatureRecipe, Aggregate,
// SchemaLibrary, MappingLibrary, Plugin), leaving out Manifest and
// Release (which are deployment-side and are silently skipped — see
// LoadAll's error model). Adding a new catalog-resident kind requires
// extending the switch here AND adding the matching map field on Catalog.
func (c *Catalog) indexFile(data []byte, path string) error {
	var header entityHeader
	if err := yaml.Unmarshal(data, &header); err != nil {
		return nil // skip non-entity YAML files silently
	}
	if c.entityBytes == nil {
		c.entityBytes = make(map[string][]byte)
	}

	switch header.Kind {
	case "Component":
		// Pass 1 parse + the static per-entity x-aggrega-* discouragement scan run
		// through the shared per-entity resolve (parseComponentEntity) so the eager
		// and lazy backings cannot drift. The pillar-shape + body validation runs at
		// Pass 2 via validateSchemaPillarsPass (it needs the complete SchemaLibraries
		// map for cross-document $ref expansion, so it cannot run per-file here).
		e, err := parseComponentEntity(data, path)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Component")
		}
		ckey := entityIndexKey(e.Metadata.Name, e.Metadata.Version)
		if err := c.claimIdentity("Component:"+ckey, fmt.Sprintf("Component identity %q", ckey), path); err != nil {
			return err
		}
		c.Components[ckey] = e
		c.componentSources[ckey] = path
		c.entityBytes[entityBytesKey("Component", e.Metadata.Name)] = data
	case "Feature":
		e, err := parseFeatureEntity(data, path)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Feature")
		}
		fkey := entityIndexKey(e.Metadata.Name, e.Metadata.Version)
		if err := c.claimIdentity("Feature:"+fkey, fmt.Sprintf("Feature identity %q", fkey), path); err != nil {
			return err
		}
		c.Features[fkey] = e
		c.featureSources[fkey] = path
		c.entityBytes[entityBytesKey("Feature", e.Metadata.Name)] = data
	case "Recipe":
		// parseRecipeEntity is the shared per-entity resolve (unmarshal, shape
		// validation, relative source / artifact-$import rewrites) so the eager
		// and lazy backings cannot drift. The dup-identity check is catalog-level
		// state and stays here.
		e, err := parseRecipeEntity(data, path)
		if err != nil {
			return err
		}
		rKey := recipeIdentityKey(e.Spec.Capability, e.Metadata.Name, e.Metadata.Version)
		if err := c.claimIdentity("Recipe:"+rKey, fmt.Sprintf("recipe identity %q", rKey), path); err != nil {
			return err
		}
		c.Recipes[rKey] = e
		c.entityBytes[entityBytesKey("Recipe", rKey)] = data
	case "FeatureRecipe":
		e, err := parseFeatureRecipeEntity(data, path)
		if err != nil {
			return err
		}
		frKey := recipeIdentityKey(e.Spec.Capability, e.Metadata.Name, e.Metadata.Version)
		if err := c.claimIdentity("FeatureRecipe:"+frKey, fmt.Sprintf("feature recipe identity %q", frKey), path); err != nil {
			return err
		}
		c.FeatureRecipes[frKey] = e
		c.entityBytes[entityBytesKey("FeatureRecipe", frKey)] = data
	case "Aggregate":
		e, err := parseAggregateEntity(data, path)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Aggregate")
		}
		akey := entityIndexKey(e.Metadata.Name, e.Metadata.Version)
		if err := c.claimIdentity("Aggregate:"+akey, fmt.Sprintf("Aggregate identity %q", akey), path); err != nil {
			return err
		}
		c.Aggregates[akey] = e
		c.aggregateSources[akey] = path
		c.entityBytes[entityBytesKey("Aggregate", e.Metadata.Name)] = data
	case "SchemaLibrary":
		// Parse + the forbidden-keyword ($dynamicAnchor / $dynamicRef) and
		// template-body scans run through the shared per-entity resolve
		// (parseSchemaLibraryEntity). Authors who use these keywords get a clear
		// error citing the file path; the publishing-linter peer layers a redundant
		// guard at publish time, and the InstantiateTemplate post-substitution scan
		// closes the parametric-introduction route at instantiation time. Sharing
		// the scan is what gives the lazy backing reject-parity for libraries.
		e, err := parseSchemaLibraryEntity(data, path)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "load SchemaLibrary")
		}
		if err := c.claimIdentity("SchemaLibrary:"+e.Metadata.Name, fmt.Sprintf("SchemaLibrary identity %q", e.Metadata.Name), path); err != nil {
			return err
		}
		c.SchemaLibraries[e.Metadata.Name] = e
		c.entityBytes[entityBytesKey("SchemaLibrary", e.Metadata.Name)] = data
	case "MappingLibrary":
		if _, eerr := envelope.ValidateBytes(envelope.KindMappingLibrary, data, path); eerr != nil {
			return cerrors.Wrap(eerr, cerrors.CatalogError, cerrors.SourceLocation{File: path}, eerr.Error())
		}
		var e types.MappingLibrary
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal MappingLibrary")
		}
		// No template-body content validation analogous to SchemaLibrary's
		// $dynamicAnchor / $dynamicRef scan — MappingLibrary entries are
		// opaque Go template strings; the compiler does not parse them
		// at entity load. Content semantics (including any cross-library
		// $import: references) resolve at Recipe / FeatureRecipe render
		// time via the Go template engine.
		if err := c.claimIdentity("MappingLibrary:"+e.Metadata.Name, fmt.Sprintf("MappingLibrary identity %q", e.Metadata.Name), path); err != nil {
			return err
		}
		c.MappingLibraries[e.Metadata.Name] = e
		c.entityBytes[entityBytesKey("MappingLibrary", e.Metadata.Name)] = data
	case "Plugin":
		if _, eerr := envelope.ValidateBytes(envelope.KindPlugin, data, path); eerr != nil {
			return cerrors.Wrap(eerr, cerrors.CatalogError, cerrors.SourceLocation{File: path}, eerr.Error())
		}
		var e types.Plugin
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Plugin")
		}
		wkey := entityIndexKey(e.Coordinate(), e.Metadata.Version)
		if err := c.claimIdentity("Plugin:"+wkey, fmt.Sprintf("Plugin identity %q", wkey), path); err != nil {
			return err
		}
		c.Plugins[wkey] = e
		c.pluginSources[wkey] = path
		c.entityBytes[entityBytesKey("Plugin", e.Coordinate())] = data
	}
	// Unknown kinds silently skipped (Manifest, Release handled separately)
	return nil
}

// claimIdentity records that fullKey ("<Kind>:<identity>") was indexed at path
// within the current directory load, or returns ErrDuplicateEntityIdentity when
// that identity was already claimed in the SAME directory — the homogenized
// within-directory collision, loud for every kind. A duplicate identity ACROSS
// directories never reaches here: each directory indexes into its own catalog,
// and the cross-directory case is resolved as a first-win shadow by mergeFrom.
// desc is the human label woven into the collision message.
func (c *Catalog) claimIdentity(fullKey, desc, path string) error {
	if c.pathByIdentity == nil {
		c.pathByIdentity = make(map[string]string)
	}
	if _, dup := c.pathByIdentity[fullKey]; dup {
		return cerrors.Wrap(ErrDuplicateEntityIdentity, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("%s already declared in this catalog directory", desc))
	}
	c.pathByIdentity[fullKey] = path
	return nil
}

// parseComponentEntity is the single per-entity Component resolution shared by the
// eager (catalog.Load, per file) and lazy (LazyLookup, per URI on cache miss)
// catalog-access backings: full unmarshal, kind-check, and the static per-entity
// load-time check. It is the SSOT for "what an authored Component resolves to" so
// the two backings cannot drift in parse/validation. Extends-merge is applied
// separately by each backing through validator.ResolveComponentExtends (eager in
// ResolveAllExtends; lazy in Component()); library-dependent pillar validation
// (validateSchemaPillarsPass) needs the complete SchemaLibraries map and is not a
// per-entity step.
func parseComponentEntity(data []byte, path string) (types.Component, error) {
	if _, err := envelope.ValidateBytes(envelope.KindComponent, data, path); err != nil {
		return types.Component{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	var e types.Component
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.Component{}, err
	}
	if e.Kind != "Component" {
		return types.Component{}, fmt.Errorf("expected kind Component, got %q", e.Kind)
	}
	validator.DiscourageDirectXAggregaInSchema(e.Metadata.Name, &e.Spec.Schema)
	return e, nil
}

// parseFeatureEntity mirrors parseComponentEntity for the Feature surface.
func parseFeatureEntity(data []byte, path string) (types.Feature, error) {
	if _, err := envelope.ValidateBytes(envelope.KindFeature, data, path); err != nil {
		return types.Feature{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	var e types.Feature
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.Feature{}, err
	}
	if e.Kind != "Feature" {
		return types.Feature{}, fmt.Errorf("expected kind Feature, got %q", e.Kind)
	}
	validator.DiscourageDirectXAggregaInSchema(e.Metadata.Name, &e.Spec.Schema)
	return e, nil
}

// parseRecipeEntity is the single per-entity Recipe resolution shared by the
// eager (catalog.Load, per file via indexFile) and lazy (LazyLookup recipe
// enumeration) backings: full unmarshal, kind-check, the load-time shape rules
// (validator.ValidateRecipe — valuesMapping ⇔ source pairing, at-least-one-
// input-kind, eval-provider input-kind ban), and the relative-source /
// relative-artifact-$import path rewrites against the Recipe file's directory.
// It is the SSOT for "what an authored Recipe resolves to" so the two backings
// cannot drift. The cross-entity dup-identity check is a catalog-level concern
// and stays with the indexing caller, not here.
//
// Unlike parseComponentEntity, this self-wraps every failure into its final
// CATALOG_ERROR shape (unmarshal, source rewrite, artifact rewrite each carry a
// distinct message; ValidateRecipe already returns a shaped error) so a single
// `return err` at the caller preserves byte-identical diagnostics.
func parseRecipeEntity(data []byte, path string) (types.Recipe, error) {
	if _, eerr := envelope.ValidateBytes(envelope.KindRecipe, data, path); eerr != nil {
		return types.Recipe{}, cerrors.Wrap(eerr, cerrors.CatalogError, cerrors.SourceLocation{File: path}, eerr.Error())
	}
	var e types.Recipe
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.Recipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Recipe")
	}
	if e.Kind != "Recipe" {
		return types.Recipe{}, fmt.Errorf("expected kind Recipe, got %q", e.Kind)
	}
	if err := validator.ValidateRecipe(&e, path); err != nil {
		return types.Recipe{}, err
	}
	if s, ok := e.Spec.Source.(string); ok && s != "" && !filepath.IsAbs(s) {
		abs, err := filepath.Abs(filepath.Join(filepath.Dir(path), s))
		if err != nil {
			return types.Recipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve Recipe source %q", s))
		}
		e.Spec.Source = abs
	}
	for _, name := range e.Spec.Artifacts.Keys() {
		entry, _ := e.Spec.Artifacts.Get(name)
		if entry.Import == "" || filepath.IsAbs(entry.Import) {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(filepath.Dir(path), entry.Import))
		if err != nil {
			return types.Recipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve Recipe artifact %q $import target %q", name, entry.Import))
		}
		entry.Import = abs
		e.Spec.Artifacts.Set(name, entry)
	}
	return e, nil
}

// parseFeatureRecipeEntity is the FeatureRecipe analogue of parseRecipeEntity:
// unmarshal, kind-check, and the relative-source rewrite. FeatureRecipes carry
// no ValidateRecipe shape pass and no artifacts surface, so the body is the
// Recipe parse minus those two steps. Self-wraps its failures like
// parseRecipeEntity.
func parseFeatureRecipeEntity(data []byte, path string) (types.FeatureRecipe, error) {
	if _, eerr := envelope.ValidateBytes(envelope.KindFeatureRecipe, data, path); eerr != nil {
		return types.FeatureRecipe{}, cerrors.Wrap(eerr, cerrors.CatalogError, cerrors.SourceLocation{File: path}, eerr.Error())
	}
	var e types.FeatureRecipe
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.FeatureRecipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal FeatureRecipe")
	}
	if e.Kind != "FeatureRecipe" {
		return types.FeatureRecipe{}, fmt.Errorf("expected kind FeatureRecipe, got %q", e.Kind)
	}
	if err := validator.ValidateFeatureRecipe(&e, path); err != nil {
		return types.FeatureRecipe{}, err
	}
	if s, ok := e.Spec.Source.(string); ok && s != "" && !filepath.IsAbs(s) {
		abs, err := filepath.Abs(filepath.Join(filepath.Dir(path), s))
		if err != nil {
			return types.FeatureRecipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve FeatureRecipe source %q", s))
		}
		e.Spec.Source = abs
	}
	for _, name := range e.Spec.Artifacts.Keys() {
		entry, _ := e.Spec.Artifacts.Get(name)
		if entry.Import == "" || filepath.IsAbs(entry.Import) {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(filepath.Dir(path), entry.Import))
		if err != nil {
			return types.FeatureRecipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve FeatureRecipe artifact %q $import target %q", name, entry.Import))
		}
		entry.Import = abs
		e.Spec.Artifacts.Set(name, entry)
	}
	return e, nil
}

// parseAggregateEntity is the single per-entity Aggregate resolution: unmarshal
// plus kind-check. Mirrors parseComponentEntity (returns the raw error; the
// indexing caller wraps the unmarshal failure with "unmarshal Aggregate").
func parseAggregateEntity(data []byte, path string) (types.Aggregate, error) {
	if _, err := envelope.ValidateBytes(envelope.KindAggregate, data, path); err != nil {
		return types.Aggregate{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	var e types.Aggregate
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.Aggregate{}, err
	}
	if e.Kind != "Aggregate" {
		return types.Aggregate{}, fmt.Errorf("expected kind Aggregate, got %q", e.Kind)
	}
	// Single normalization core: fold a top-level components surface into the
	// synthesized `default` subsystem at birth (the same step
	// LoadAggregateFromBytes applies), so every downstream consumer sees the
	// uniform subsystems-only shape. Normalize is non-mutating; return the
	// normalized value it produced.
	return *types.Normalize(&e).Aggregate(), nil
}

// parseSchemaLibraryEntity is the single per-entity SchemaLibrary resolution shared
// by both backings: full unmarshal, kind-check, and the forbidden-keyword
// (ValidateSchemaEntry) / template-body (ValidateTemplateBody) scans the eager load
// applies. Sharing it is what gives the lazy backing reject-parity once it resolves
// libraries directly (no longer Prime-copied from an already-validated eager
// Catalog): an invalid library the eager build rejects is rejected by the lazy
// resolution too.
func parseSchemaLibraryEntity(data []byte, path string) (types.SchemaLibrary, error) {
	if _, err := envelope.ValidateBytes(envelope.KindSchemaLibrary, data, path); err != nil {
		return types.SchemaLibrary{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	var e types.SchemaLibrary
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.SchemaLibrary{}, err
	}
	if e.Kind != "SchemaLibrary" {
		return types.SchemaLibrary{}, fmt.Errorf("expected kind SchemaLibrary, got %q", e.Kind)
	}
	for name, entry := range e.Spec.Schemas.All() {
		if err := types.ValidateSchemaEntry(&entry); err != nil {
			return types.SchemaLibrary{}, fmt.Errorf("SchemaLibrary %q schema %q: %w", e.Metadata.Name, name, err)
		}
	}
	for name, tmpl := range e.Spec.Templates.All() {
		if err := types.ValidateTemplateBody(&tmpl); err != nil {
			return types.SchemaLibrary{}, fmt.Errorf("SchemaLibrary %q template %q: %w", e.Metadata.Name, name, err)
		}
	}
	return e, nil
}

// parseMappingLibraryEntity is the single per-entity MappingLibrary resolution,
// mirroring parseSchemaLibraryEntity on the mapping axis: full unmarshal +
// kind-check. There is no forbidden-keyword / template-body scan — MappingLibrary
// entries are opaque Go template strings the compiler does not parse at entity
// load (their content, including any cross-library `$import:`, resolves at
// Recipe / FeatureRecipe render time). Used by the build-side bridge so a
// lockfile-pinned remote MappingLibrary is parsed identically to a local one.
func parseMappingLibraryEntity(data []byte, path string) (types.MappingLibrary, error) {
	if _, err := envelope.ValidateBytes(envelope.KindMappingLibrary, data, path); err != nil {
		return types.MappingLibrary{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	var e types.MappingLibrary
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.MappingLibrary{}, err
	}
	if e.Kind != "MappingLibrary" {
		return types.MappingLibrary{}, fmt.Errorf("expected kind MappingLibrary, got %q", e.Kind)
	}
	return e, nil
}

// parsePluginEntity validates a Plugin entity's YAML against the plugin envelope
// and unmarshals it. It is the single Plugin-parse home the recipe-shell index
// fold and the cache-by-URI verdict-socket resolution both call, so a plugin
// entity is validated and decoded identically wherever it is read from the cache.
func parsePluginEntity(data []byte, path string) (types.Plugin, error) {
	if _, err := envelope.ValidateBytes(envelope.KindPlugin, data, path); err != nil {
		return types.Plugin{}, err
	}
	var e types.Plugin
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.Plugin{}, err
	}
	return e, nil
}

// DeprecationWarnings returns non-fatal notices about deprecated catalog
// shapes — formatted as a string slice the caller is free to print, log,
// or escalate to errors as fits its UX. The build command currently prints
// each entry to stderr after catalog load; tooling that wants stricter
// behaviour can match on the slice and exit non-zero.
//
// Contract. The function never returns an error and never mutates the
// catalog. An empty slice means "no deprecated shapes detected"; the
// presence of a warning never blocks Catalog use. Iteration is sorted by
// entity name within each kind so the message stream is deterministic.
//
// Current scope. No deprecation rules are active. New deprecation rules
// attach here: each rule reads its own catalog state, formats one
// message per affected entity, and appends to the returned slice. The
// function is the single point where "what counts as a deprecated
// catalog shape" lives; rules living elsewhere risk drifting out of the
// build command's warning stream.
// LintWarnings returns non-fatal advisory diagnostics accumulated
// during catalog load. Today's lint surface is schema-pillar-overlap:
// a warning fires when a Component or Feature declares the same
// property name in both values: and infra: pillars at the same
// nesting depth. The compiler accepts overlaps (pillars are separate
// namespaces by design); the lint surfaces a frequent authoring smell
// where reads and template authors face "which pillar holds the foo
// I'm reading?" friction.
//
// Returns nil when no lint warnings fired. Build callers range the
// returned slice and print each line to stderr alongside the existing
// DeprecationWarnings output.
func (c *Catalog) LintWarnings() []string {
	if c == nil {
		return nil
	}
	return c.lintWarnings
}

func (c *Catalog) DeprecationWarnings() []string {
	return nil
}

// ResolveAllExtends walks every Component and Feature in the catalog and
// resolves its extends chain in-place via the validator package's
// ResolveComponentExtends / ResolveFeatureExtends. The merge follows the
// two-tiered inheritance pattern: deep-merge the YAML envelope, compose
// the Schema with parent under JSON Schema composition. The merge DETECTS
// a child that widens a parent's constraint and reports it as a non-halting
// side-band signal (merger.NarrowingViolation); it does not enforce a
// narrow-only rule. Whether a child may narrow but not relax a parent
// constraint is opt-in policy enforced by the publishing-discipline plugin
// at publish time, not a compiler check. Cycles are detected by the
// validator and surface as errors rather than infinite loops.
//
// Precondition. Must be called after Load / LoadAll and before any
// downstream consumer reads from the catalog (CatalogLookup, RecipeLookup,
// the inspect command's render, the resolver's guard checks). Catalog
// entries with non-empty Spec.Extends are stored unresolved by the loader;
// downstream code that reads those entries directly will see the child's
// own metadata + Schema only. There is no compiler-side guard that
// catches "ResolveAllExtends was skipped" — caller discipline at the
// command-line entry points (cmd/aggrega/build.go and inspect.go each
// call this immediately after Load) carries the contract today.
//
// Determinism. Both Component and Feature passes iterate sorted-name keys
// before mutating the underlying maps. The mutation order is sorted; the
// final map state is order-independent (resolved entities replace the
// child key, not the parent key, so the order one sibling-or-cousin
// resolves before another does not affect what a third resolves to). The
// sorted iteration is the catalog-side contribution to the byte-for-byte
// deterministic-output contract — error messages and any logged trace of
// resolution order both come out byte-identical across runs.
//
// Error model. Errors are collected into the returned slice rather than
// short-circuiting on the first failure, so a malformed parent in one
// chain does not mask broken extends elsewhere in the catalog. Each error
// carries its own entity context via validator.ValidationError. Callers
// (cmd/aggrega/build.go, cmd/aggrega/inspect.go) print every error and
// exit non-zero when the slice is non-empty.
func (c *Catalog) ResolveAllExtends() []*validator.ValidationError {
	var errs []*validator.ValidationError

	compLookup := func(uri string) (types.Component, bool) {
		key, ok, _ := c.resolveComponentKey(uri)
		if !ok {
			return types.Component{}, false
		}
		return c.Components[key], true
	}
	featLookup := func(uri string) (types.Feature, bool) {
		key, ok, _ := c.resolveFeatureKey(uri)
		if !ok {
			return types.Feature{}, false
		}
		return c.Features[key], true
	}

	// CatalogEntry chain lookups that compile each chain entry's authored
	// schema into the JSON-Schema-merge input, so the inheritance merge phase
	// composes the chain's pillars into one effective schema.
	compEntryLookup := func(uri string) (validator.CatalogEntry, bool) {
		comp, ok := compLookup(uri)
		if !ok {
			return validator.CatalogEntry{}, false
		}
		return validator.CatalogEntry{
			URI:        comp.Metadata.Name,
			Extends:    comp.Spec.Extends,
			JSONSchema: validator.CompileSchemaForMerge(comp.Spec.Schema, c.SchemaLibraries),
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
			JSONSchema: validator.CompileSchemaForMerge(feat.Spec.Schema, c.SchemaLibraries),
		}, true
	}
	storeEffective := func(uri string, composition *validator.ExtendsComposition) {
		if composition == nil {
			return
		}
		if composition.EffectiveSchema != nil {
			if c.effectiveSchemas == nil {
				c.effectiveSchemas = map[string]compactform.Schema{}
			}
			c.effectiveSchemas[uri] = composition.EffectiveSchema
		}
		c.lintWarnings = append(c.lintWarnings, composition.Narrowings...)
	}

	compKeys := make([]string, 0, len(c.Components))
	for k := range c.Components {
		compKeys = append(compKeys, k)
	}
	sort.Strings(compKeys)
	for _, name := range compKeys {
		comp := c.Components[name]
		if comp.Spec.Extends == "" {
			continue
		}
		if _, ok, verr := c.resolveComponentKey(comp.Spec.Extends); !ok && errors.Is(verr, ErrUnsatisfiableEntityVersion) {
			errs = append(errs, &validator.ValidationError{
				Entity:     "Component " + name,
				FieldPath:  ".extends",
				Constraint: fmt.Sprintf("no catalog version satisfies extends parent %q", comp.Spec.Extends),
				Err:        ErrUnsatisfiableEntityVersion,
			})
			continue
		}
		resolved, vErrs := validator.ResolveComponentExtends(comp, compLookup)
		if len(vErrs) > 0 {
			errs = append(errs, vErrs...)
			continue
		}
		c.Components[name] = resolved

		// Compose the chain's JSON Schemas into the effective schema. The
		// metadata resolution above already validated cycle/depth/missing-parent,
		// so the only new error here is a structural JSON-Schema merge failure
		// (ErrInheritanceMerge); narrowing is signal-only.
		rootEntry := validator.CatalogEntry{
			URI:        comp.Metadata.Name,
			Extends:    comp.Spec.Extends,
			JSONSchema: validator.CompileSchemaForMerge(comp.Spec.Schema, c.SchemaLibraries),
		}
		inh := validator.ResolveInheritance(rootEntry, compEntryLookup)
		if len(inh.Errors) > 0 {
			errs = append(errs, inh.Errors...)
			continue
		}
		storeEffective(name, validator.CompositionFromResult(comp.Metadata.Name, inh))
	}

	featKeys := make([]string, 0, len(c.Features))
	for k := range c.Features {
		featKeys = append(featKeys, k)
	}
	sort.Strings(featKeys)
	for _, name := range featKeys {
		feat := c.Features[name]
		if feat.Spec.Extends == "" {
			continue
		}
		if _, ok, verr := c.resolveFeatureKey(feat.Spec.Extends); !ok && errors.Is(verr, ErrUnsatisfiableEntityVersion) {
			errs = append(errs, &validator.ValidationError{
				Entity:     "Feature " + name,
				FieldPath:  ".extends",
				Constraint: fmt.Sprintf("no catalog version satisfies extends parent %q", feat.Spec.Extends),
				Err:        ErrUnsatisfiableEntityVersion,
			})
			continue
		}
		resolved, vErrs := validator.ResolveFeatureExtends(feat, featLookup)
		if len(vErrs) > 0 {
			errs = append(errs, vErrs...)
			continue
		}
		c.Features[name] = resolved

		rootEntry := validator.CatalogEntry{
			URI:        feat.Metadata.Name,
			Extends:    feat.Spec.Extends,
			JSONSchema: validator.CompileSchemaForMerge(feat.Spec.Schema, c.SchemaLibraries),
		}
		inh := validator.ResolveInheritance(rootEntry, featEntryLookup)
		if len(inh.Errors) > 0 {
			errs = append(errs, inh.Errors...)
			continue
		}
		storeEffective(name, validator.CompositionFromResult(feat.Metadata.Name, inh))
	}

	return errs
}

// CatalogLookup returns a resolver.CatalogLookup whose Component and Feature
// closures read from this catalog's Components / Features maps.
//
// The returned shape is defined on the resolver side — pkg/resolver owns
// the resolver.CatalogLookup struct and the function signatures it expects.
// Returning that struct from here, populated with closures over c, is the
// adapter pattern the package uses to keep the resolver free of any
// pkg/catalog import dependency: the resolver's guard checks reach back
// into the catalog through these closures rather than through a typed
// reference to *Catalog. Same pattern repeats in RecipeLookup
// (renderer.RecipeLookup) and SubAggregateLoader (topology.SubAggregateLoader).
// resolveComponentKey returns the index key of the version of typeURI that best
// satisfies the reference under the three-form rule (exact / caret / unversioned
// -> highest-satisfying), plus a matched flag and the recoverable
// ErrUnsatisfiableEntityVersion when the base name exists but no version satisfies
// a version-pinned reference. A bare typeURI resolves to the highest available
// version. The eager catalog indexes by name:version, so this is the single point
// that picks among coexisting versions; resolveFeatureKey /
// resolveAggregateKey mirror it for the other by-name kinds.
func (c *Catalog) resolveComponentKey(typeURI string) (string, bool, error) {
	return selectEntityVersion(c.Components, typeURI, func(v types.Component) (string, string) {
		return v.Metadata.Name, v.Metadata.Version
	})
}

func (c *Catalog) resolveFeatureKey(typeURI string) (string, bool, error) {
	return selectEntityVersion(c.Features, typeURI, func(v types.Feature) (string, string) {
		return v.Metadata.Name, v.Metadata.Version
	})
}

func (c *Catalog) resolveAggregateKey(name string) (string, bool, error) {
	return selectEntityVersion(c.Aggregates, name, func(v types.Aggregate) (string, string) {
		return v.Metadata.Name, v.Metadata.Version
	})
}

func (c *Catalog) resolvePluginKey(uri string) (string, bool, error) {
	return selectEntityVersion(c.Plugins, uri, func(v types.Plugin) (string, string) {
		return v.Coordinate(), v.Metadata.Version
	})
}

func (c *Catalog) CatalogLookup() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			key, ok, _ := c.resolveComponentKey(typeURI)
			if !ok {
				return types.Component{}, false
			}
			return c.Components[key], true
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			key, ok, _ := c.resolveFeatureKey(typeURI)
			if !ok {
				return types.Feature{}, false
			}
			return c.Features[key], true
		},
		SchemaLibraries: c.SchemaLibraries,
		EffectiveSchema: func(typeURI string) (compactform.Schema, bool) {
			// Resolve as a Component then a Feature — both kinds compose extends
			// schemas (stored by ResolveAllExtends), so an extends-ing Feature's
			// composed schema is served, not just a Component's.
			if key, ok, _ := c.resolveComponentKey(typeURI); ok {
				if s, ok := c.effectiveSchemas[key]; ok {
					return s, true
				}
			}
			if key, ok, _ := c.resolveFeatureKey(typeURI); ok {
				if s, ok := c.effectiveSchemas[key]; ok {
					return s, true
				}
			}
			return compactform.Schema{}, false
		},
		Plugin:              c.PluginByURI,
		ProviderPlugin:      c.ProviderPluginByName,
		ParserPlugin:        c.ParserPluginForFormat,
		PluginColocatedDirs: c.PluginColocatedDirs(),
	}
}

// PluginColocatedDirs maps each indexed Plugin entity's bare-identity artifact
// reference to the directory its plugin.yaml was loaded from — the directory a
// co-located .wasm sits in beside the entity. The build-time plugin cascade's
// co-located tier consults it, so a binary with no embedded core guests resolves
// them from beside their catalog entities. Returns nil when no Plugin entity was
// indexed. Keys are built through the single Plugin.ArtifactReference derivation,
// so the tier keys by the same reference the cascade resolves by. When two
// versions of one identity are indexed (same artifact reference, distinct
// directories), the lexicographically last source path wins, so the map is
// deterministic regardless of index-map iteration order.
func (c *Catalog) PluginColocatedDirs() map[string]string {
	if len(c.pluginSources) == 0 {
		return nil
	}
	keys := make([]string, 0, len(c.pluginSources))
	for wkey := range c.pluginSources {
		keys = append(keys, wkey)
	}
	sort.Strings(keys)
	dirs := make(map[string]string, len(c.pluginSources))
	for _, wkey := range keys {
		p, ok := c.Plugins[wkey]
		if !ok {
			continue
		}
		dirs[p.ArtifactReference()] = filepath.Dir(c.pluginSources[wkey])
	}
	if len(dirs) == 0 {
		return nil
	}
	return dirs
}

// AggregateSourceFile returns the path the Aggregate entity named
// name was loaded from, rendered against the workspace root the
// catalog was constructed with (the first argument to Load/LoadAll)
// or the absolute path when that root was empty. Returns ("", false)
// when the catalog carries no Aggregate by that name. The topology
// builder's SubAggregateLoader callback uses this to attach
// source-file provenance to flattened nodes so the analyzer's
// per-file diagnostic filtering operates on consistent path forms.
func (c *Catalog) AggregateSourceFile(name string) (string, bool) {
	key, ok, _ := c.resolveAggregateKey(name)
	if !ok {
		return "", false
	}
	path, ok := c.aggregateSources[key]
	if !ok {
		return "", false
	}
	return RelativeToWorkspace(c.workspaceRoot, path), true
}

// EntityBytes returns the raw authored bytes the catalog retained for the entity
// of the given kind (e.g. "Aggregate") and metadata.name — the exact bytes a
// lockfile digest pins, not a re-read. Returns ("", false) when no such entity
// was indexed. Used by the Release-triad resolver's catalog tier to surface the
// resolved Aggregate's bytes alongside the entity, so a catalog-resolved
// aggregate carries the same (entity, path, bytes) contract as a
// same-directory-resolved one.
func (c *Catalog) EntityBytes(kind, name string) ([]byte, bool) {
	b, ok := c.entityBytes[entityBytesKey(kind, name)]
	return b, ok
}

// AggregateLookup returns a resolver.AggregateLookup backed by this catalog's
// Aggregate index, source-path, and retained bytes — the catalog tier of the
// Release-triad resolver. A name miss returns ok=false (the resolver's identity
// cascade then continues to the registry tier or stops); a hit carries the
// entity together with its workspace-relative source path and its
// digest-verified bytes, so a catalog-resolved Aggregate presents the same
// (entity, path, bytes) contract as a same-directory-resolved one.
func (c *Catalog) AggregateLookup() resolver.AggregateLookup {
	return func(name string) (resolver.ResolvedAggregate, bool) {
		key, ok, _ := c.resolveAggregateKey(name)
		if !ok {
			return resolver.ResolvedAggregate{}, false
		}
		agg := c.Aggregates[key]
		path, _ := c.AggregateSourceFile(key)
		data, _ := c.EntityBytes("Aggregate", agg.Metadata.Name)
		return resolver.ResolvedAggregate{Aggregate: &agg, Path: path, Bytes: data}, true
	}
}

// SubAggregateLoader returns a topology.SubAggregateLoader closure that the
// topology builder calls when it encounters a sub-aggregate reference
// during graph construction. The closure looks up the named Aggregate in
// this catalog's Aggregates map and returns the entity together with its
// source path, rendered in the same coordinate system as AggregateSourceFile.
//
// The signature returns `(*types.Aggregate, string, error)` rather than the
// `(value, bool)` shape used by CatalogLookup / RecipeLookup
// because the topology package treats a sub-aggregate miss as a fatal
// graph-construction failure (the parent Aggregate references a
// sub-aggregate that does not exist; the build cannot continue). The
// error carries the missing type name so callers can surface it without
// needing to recompose the diagnostic. The path return threads source-file
// provenance through flatten so per-node IR records can carry it.
func (c *Catalog) SubAggregateLoader() topology.SubAggregateLoader {
	return func(typeName string, parentParams map[string]any) (*types.Aggregate, string, map[string]any, error) {
		key, ok, _ := c.resolveAggregateKey(typeName)
		if !ok {
			return nil, "", nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{}, fmt.Sprintf("sub-aggregate %q not found in catalog (check the aggregates-block reference names a known Aggregate type, or run `aggrega pull` if it must be fetched)", typeName), nil, nil)
		}
		agg := c.Aggregates[key]
		// The return path (TopologyNode provenance) always comes from
		// aggregateSources, exactly as before — decoupled from raw-byte
		// acquisition, so a cache-bridged sub-aggregate (bytes, no path) or a
		// failed re-read does not lose source provenance.
		absPath := c.aggregateSources[key]
		returnPath := RelativeToWorkspace(c.workspaceRoot, absPath)
		raw := c.subAggregateRawBytes(agg.Metadata.Name, absPath)
		return resolveSubAggregateParameters(typeName, raw, absPath, returnPath, agg, parentParams)
	}
}

// subAggregateRawBytes returns the raw YAML bytes of a loaded sub-aggregate
// (needed for byte-level parameter interpolation). A cache-bridged / registry
// sub-aggregate retains its verified bytes; a local-filesystem one is re-read
// from absPath. Returns nil when neither is available (the loader then falls
// back to the already-parsed, un-interpolated Aggregate).
func (c *Catalog) subAggregateRawBytes(typeName, absPath string) []byte {
	if b, ok := c.entityBytes[entityBytesKey("Aggregate", typeName)]; ok && len(b) > 0 {
		return b
	}
	if absPath != "" {
		if data, err := os.ReadFile(absPath); err == nil {
			return data
		}
	}
	return nil
}

// resolveSubAggregateParameters runs the parameter lifecycle for one
// sub-aggregate import: it fulfils the sub-aggregate's own `spec.parameters`
// contract with the parent-supplied params (defaults applied, contract
// validated), interpolates the sub-aggregate's `{{ .parameters.X }}` templates
// against the resolved map, and re-parses the interpolated bytes into the
// authoritative Aggregate. It mirrors the root parameter-resolution lifecycle
// (merge, validate, interpolate, single-pass), one level in. Returns the
// interpolated Aggregate, the
// workspace-relative source path, and the resolved parameter map (carried onto
// the sub-aggregate's nodes for predicate-aware conformance). Shared by the
// Catalog and LazyLookup loaders.
//
// When no raw bytes are available (neither path nor cache bytes — not expected
// for a genuinely loaded aggregate), it falls back to the already-parsed,
// un-interpolated Aggregate so the build does not break; the resolved map is
// then empty.
func resolveSubAggregateParameters(typeName string, raw []byte, absPath, returnPath string, fallback types.Aggregate, parentParams map[string]any) (*types.Aggregate, string, map[string]any, error) {
	if len(raw) == 0 {
		return &fallback, returnPath, nil, nil
	}
	contract, err := resolver.LoadAggregateParameterContractFromBytes(raw, absPath)
	if err != nil {
		return nil, "", nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: absPath}, fmt.Sprintf("sub-aggregate %q parameter contract", typeName))
	}
	resolved := resolver.ApplyParameterDefaults(contract, parentParams)
	if verrs := resolver.ValidateParametersAgainstContract(contract, resolved, absPath); len(verrs) > 0 {
		return nil, "", nil, &resolver.MultiResolverError{
			Entity:     fmt.Sprintf("sub-aggregate %s", typeName),
			Summary:    fmt.Sprintf("%d parameter contract violation(s)", len(verrs)),
			Violations: verrs,
		}
	}
	interpolated, err := resolver.InterpolateParameters(raw, resolved)
	if err != nil {
		return nil, "", nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: absPath}, fmt.Sprintf("sub-aggregate %q parameter interpolation", typeName))
	}
	agg, err := resolver.LoadAggregateFromBytes(interpolated, absPath)
	if err != nil {
		return nil, "", nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: absPath}, fmt.Sprintf("sub-aggregate %q parse (post-interpolation)", typeName))
	}
	return agg, returnPath, resolved, nil
}

// RelativeToWorkspace returns absPath rendered relative to
// workspaceRoot, falling back to absPath unchanged when workspaceRoot
// is empty (no workspace concept), absPath is empty (no provenance to
// normalize), or filepath.Rel fails (absPath lives outside the
// workspace tree on a different volume). The single source of truth
// for the IR's SourceFile normalization — Catalog uses it on the
// emit side (AggregateSourceFile, SubAggregateLoader), the daemon's
// per-file filter uses it on the consume side (converting the LSP-
// provided validated file path to the same coordinate system).
//
// Catalogs whose paths share a common parent with the workspace root
// produce a traversal-prefixed relative path (e.g. "../shared/...").
// The result is still stable across developer machines that share the
// same workspace-relative layout, which is the byte-identity property
// the IR wire shape requires.
func RelativeToWorkspace(workspaceRoot, absPath string) string {
	if workspaceRoot == "" || absPath == "" {
		return absPath
	}
	rel, err := filepath.Rel(workspaceRoot, absPath)
	if err != nil {
		return absPath
	}
	return rel
}
