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
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
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
// Components, Features, Aggregates and Plugins index by name:version, so every
// version a catalog holds coexists and a reference selects among them through
// selectEntityVersion; Recipes and FeatureRecipes index by capability-scoped
// identity, and libraries by name.
type Catalog struct {
	Components        map[string]types.Component       // keyed by identity (metadata.name:version)
	Features          map[string]types.Feature         // keyed by identity (metadata.name:version)
	Recipes           map[string]types.Recipe          // keyed by identity (provider/name:version)
	FeatureRecipes    map[string]types.FeatureRecipe   // keyed by identity (provider/name:version)
	Aggregates        map[string]types.Aggregate       // keyed by identity (metadata.name:version)
	SchemaLibraries   map[string]types.SchemaLibrary   // keyed by metadata.name (PascalCase library identifier)
	MappingLibraries  map[string]types.MappingLibrary  // keyed by metadata.name (PascalCase library identifier)
	WorkloadLibraries map[string]types.WorkloadLibrary // keyed by metadata.name (PascalCase library identifier)
	ArtifactLibraries map[string]types.ArtifactLibrary // keyed by metadata.name (PascalCase library identifier)
	Plugins           map[string]types.Plugin          // keyed by identity (pluginType/name:version)

	// componentSources / featureSources / pluginSources
	// track the file path each entity was loaded from. Populated by the
	// indexing walk (indexFile) and consumed by pillar validation (the schema
	// validation that runs at the end of LoadAll) so the validator's error
	// messages name the offending
	// file. Unexported because the bookkeeping is internal to LoadAll;
	// downstream callers identify entities by metadata.name, not by source
	// path.
	componentSources map[string]string
	featureSources   map[string]string
	pluginSources    map[string]string

	// aggregateSources records the absolute filesystem path each Aggregate
	// entity was loaded from. Populated by the indexing walk (indexFile) and
	// exposed by AggregateSourceFile (and the SubAggregateLoader callback) after
	// normalization against workspaceRoot — see RelativeToWorkspace. The lazy
	// view's shell leaves it empty and answers from origins instead.
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

	// pillarLintWarnings holds the schema-pillar-overlap advisories, which
	// warn when a Component or Feature declares the same property name in
	// both values: and infra: pillars at the same nesting depth. Owned
	// exclusively by validateSchemaPillarsPass, which clears it on entry.
	//
	// narrowingWarnings holds the narrowing-on-extend advisories a chain's
	// pillar composition produces. Owned exclusively by ResolveAllExtends,
	// which clears it on entry.
	//
	// They are two fields rather than one accumulator BECAUSE each is owned by
	// a different pass. A pass that may run twice on one catalog has to clear
	// what it contributed, or its second run reports every advisory twice; and
	// a single shared slice cannot be cleared by either pass without erasing
	// the other's contribution. Splitting is what makes "clear on entry"
	// correct rather than a new defect. LintWarnings() serves the two in the
	// order the passes run.
	pillarLintWarnings []string
	narrowingWarnings  []string

	// effectiveSchemas holds the composed (parent+child) schema for each
	// FEATURE that declares extends:, keyed by metadata.name, in compactform
	// map form. Populated by ResolveAllExtends from the inheritance JSON-Schema
	// merge and exposed via resolver.CatalogLookup.EffectiveSchema so
	// Feature-schema consumers (defaults materialisation, annotation expansion,
	// annotation-binding target validation) read a Feature's inherited schema
	// rather than its own authored slice. Absent for non-extends Features —
	// those consumers fall back to compiling the authored schema.
	//
	// Components are deliberately absent. An extending Component authors no
	// pillar content and receives its base's block verbatim on the merge, so
	// its authored schema IS its inherited schema and a second view would be a
	// second, divergent answer to one question.
	effectiveSchemas map[string]compactform.Schema

	// entityBytes records the raw YAML bytes each entity was loaded
	// from, keyed by "<kind>:<index key>" (entityBytesKey), where the index
	// key is the entity's key in its kind's typed map: name:version for a
	// Component, Feature or Aggregate, coordinate:version for a Plugin, the
	// capability-scoped identity for a Recipe or FeatureRecipe, and the bare
	// name for a library. Keying the bytes exactly as the entity is indexed is
	// what keeps two versions of one name apart, so the content digest
	// ClosureLookup takes for a selected version is of that version's own
	// bytes. Populated during indexFile; unset for entity kinds the closure
	// walker does not resolve (currently none — every kind the walker
	// traverses is recorded here).
	entityBytes map[string][]byte

	// pathByIdentity maps "<Kind>:<identity-key>" to the file each entity was
	// loaded from, for every indexed kind. indexFile populates it and uses it
	// to detect a WITHIN-DIRECTORY duplicate identity (a second file claiming
	// an identity already seen in the same directory load is the homogenized
	// ErrDuplicateEntityIdentity collision). It also supplies the winner/shadowed
	// file paths when an ordered multi-directory merge records a ShadowedEntity.
	// The lazy view's shell leaves it empty and answers from origins instead.
	pathByIdentity map[string]string

	// recordedPins holds the content pins in force for each pin-bearing entity
	// this catalog indexed, keyed by the document that entity was read from. The
	// render reads it to compare what a build acquired against what the
	// resolution recorded, which is a question only the load can answer: the
	// entity and the directory it was read from are both in hand here and in no
	// later pass.
	//
	// It is keyed by document rather than by identity for the reason
	// recipepin.Lookup states, and it holds what applyRecordedPins settled — the
	// lock beside the document where one sits, the document's own pins where none
	// does. An entity read from no file on disk records nothing.
	recordedPins recipepin.Lookup

	// origins maps "<Kind>:<identity-key>" to where the entity indexed under that
	// identity was read from — a catalog-directory document, or a lockfile-pinned
	// cache slot with the document its content paths anchor at inside the slot's
	// layout (see entityOrigin). It is the lazy view's one record of that fact,
	// handed to the shell it builds, and every provenance answer that shell gives is
	// derived from it: the document an entity was declared in, the anchor its
	// content paths resolve against, the tier a closure walk reports for it, and the
	// source a flattened Aggregate records.
	//
	// A catalog loaded from directories folds no pinned slot and holds no record.
	// It answers the same questions from its own load-time indexes (pathByIdentity,
	// aggregateSources), and an identity absent from this map is answered that way —
	// which is how every entity of such a catalog, and of a Catalog literal, keeps
	// the answer it has always had.
	origins map[string]entityOrigin

	// unwrittenLocks is the lazy view's record of the locks a reporting run would
	// leave beside the documents it pins (see UnwrittenLocks), handed to the shell
	// it builds so the entity rows the shell takes read them as the view does. A
	// catalog loaded from directories, and a Catalog literal, hold none and read
	// every lock from disk.
	unwrittenLocks UnwrittenLocks

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
// Two-pass load model. The indexing walk traverses dirs and hydrates every
// entity into the typed maps via indexFile. Pillar validation runs at the end
// of LoadAll once the indexing walk has populated the full SchemaLibraries
// map, and validates each Component and Feature's spec.schema body against the
// canonical pillar contract via validator.ValidateSchemaPillars. Splitting the
// work lets cross-document $ref expansion succeed regardless of filesystem
// walk order: a Feature authored before its SchemaLibrary in walk order still
// validates because the library is in the map by the time pillar validation
// runs. Indexing-walk errors are fail-fast (a YAML parse failure aborts the
// walk); pillar-validation errors are collected across entities and surfaced
// as a single aggregate CatalogError so authors fix all schema failures in one
// editing pass.
func LoadAll(workspaceRoot string, dirs ...string) (*Catalog, error) {
	return LoadAllWithCacheDir(workspaceRoot, "", dirs...)
}

// LoadAllWithCacheDir is LoadAll with the artifact cache directory
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

// entityMetadataVersion reads metadata.version from the bytes published at a
// registry coordinate. The registry tier pins this (the entity's identity)
// rather than the OCI tag it was fetched under.
//
// The bytes are taken through [oci.EntityYAML] first, because two of the kinds
// this product publishes go to a coordinate as an archive rather than as a
// document. A decode applied to the archive directly yields an empty version,
// which every caller here would read as "this entity declares none" and answer
// by falling back to the tag — recording the label somebody happened to publish
// under as the resolved identity, with nothing printed and nothing non-zero.
// An archive that cannot be opened is therefore an error rather than an empty
// string: those two outcomes have opposite correct responses, and a single
// return value cannot tell them apart.
//
// A document that parses but declares no version still returns ("", nil). That
// is the case the tag fallback exists for, and it stays a caller's decision.
func entityMetadataVersion(published []byte) (string, error) {
	data, err := oci.EntityYAML(published)
	if err != nil {
		return "", err
	}
	var hdr struct {
		Metadata struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(data, &hdr); err != nil {
		return "", nil
	}
	return hdr.Metadata.Version, nil
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

// LoadPassOne runs only the indexing walk of the catalog load — the filesystem
// walk + per-file indexing, content-cache directory skipped — WITHOUT pillar
// validation. The indexing walk alone is sufficient for the closure walk (which
// scans raw `$ref` values, not expanded pillars); cross-document `$ref`
// expansion is closure-scoped pillar validation's job, against the resolved
// closure's library set.
func LoadPassOne(workspaceRoot, cacheDir string, dirs ...string) (*Catalog, error) {
	return loadAllPassOne(workspaceRoot, cacheDir, dirs...)
}

// SkippedEntity records a catalog file the TOLERANT indexing walk
// (LoadPassOneTolerant) could not index — a parse failure, a load-time shape
// violation, a duplicate identity. Err is the original indexing error.
// Callers replay these on an in-closure resolution failure so a tolerated
// out-of-closure skip never hides WHY an entity the Release reaches went
// missing.
//
// Kind, Name, Version and Capability are the identity the refused document
// declares for itself, recovered best-effort from a header re-parse. Together
// they are what SoleRefusalFor matches a failing reference against, so a
// refusal can be reported ahead of the failure it explains rather than beneath
// it. Every one of them is empty when the YAML is malformed enough to lose even
// the header, and a document whose identity could not be read is never matched
// to a reference — it is reported by path alone, which is still enough to open
// the file.
type SkippedEntity struct {
	Path string
	Kind string
	Name string
	// Version is the authored metadata.version, empty for a version-less
	// document.
	Version string
	// Capability is the authored spec.capability, which is the provider
	// segment of a recipe's identity. Empty for every kind that declares none.
	Capability string
	Err        error
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

// loadAllPassOne runs the indexing walk only — the filesystem walk + per-file
// indexing with the artifact cache directory skipped — and returns the
// catalog BEFORE pillar validation (validateSchemaPillarsPass).
// LoadAllWithCacheDir runs pillar validation immediately after. cacheDir ""
// disables the cache skip.
//
// Fail-fast: a per-file read / parse / index error aborts the whole walk. The
// tolerant counterpart (LoadPassOneTolerant) skips-and-records instead, for the
// closure-scoped build / pull paths where an out-of-closure broken entity must
// not fail an unrelated Release.
func loadAllPassOne(workspaceRoot, cacheDir string, dirs ...string) (*Catalog, error) {
	c, _, err := loadAllPassOneImpl(workspaceRoot, cacheDir, false, dirs...)
	return c, err
}

// LoadPassOneTolerant runs the indexing walk like LoadPassOne but SKIPS a file
// that fails to read, parse, or index — recording it in the returned
// []SkippedEntity — rather than aborting the walk. The catalog returned holds
// every parseable entity; a broken file is simply absent. This is the
// closure-scoped build / pull load: combined with closure-scoped pillar
// validation, a broken entity OUTSIDE the Release's closure never fails the
// build, while an in-closure break still fails loudly (it is either absent —
// surfaced by the closure walk, with the skip replayed — or
// present-but-schema-invalid, surfaced by closure-scoped pillar validation).
//
// The skip is per-file and post-`indexFile`: `indexFile` mutates the catalog
// only after a successful parse, so a skipped file leaves no partial state.
func LoadPassOneTolerant(workspaceRoot, cacheDir string, dirs ...string) (*Catalog, []SkippedEntity, error) {
	return loadAllPassOneImpl(workspaceRoot, cacheDir, true, dirs...)
}

// loadAllPassOneImpl is the shared indexing walk. When tolerant is false it is
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

// newPassOneCatalog allocates a catalog with every indexing-walk index map ready. Both
// the accumulated result and each per-directory sub-index are built through it
// so they share one initialisation.
func newPassOneCatalog(workspaceRoot string) *Catalog {
	return &Catalog{
		workspaceRoot:     workspaceRoot,
		Components:        make(map[string]types.Component),
		Features:          make(map[string]types.Feature),
		Recipes:           make(map[string]types.Recipe),
		FeatureRecipes:    make(map[string]types.FeatureRecipe),
		Aggregates:        make(map[string]types.Aggregate),
		SchemaLibraries:   make(map[string]types.SchemaLibrary),
		MappingLibraries:  make(map[string]types.MappingLibrary),
		WorkloadLibraries: make(map[string]types.WorkloadLibrary),
		ArtifactLibraries: make(map[string]types.ArtifactLibrary),
		Plugins:           make(map[string]types.Plugin),
		componentSources:  make(map[string]string),
		featureSources:    make(map[string]string),
		pluginSources:     make(map[string]string),
		aggregateSources:  make(map[string]string),
		entityBytes:       make(map[string][]byte),
		pathByIdentity:    make(map[string]string),
		recordedPins:      make(recipepin.Lookup),
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
			return SkipDirIfArtifactCache(c.workspaceRoot, path, cacheClean)
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

// SkipDirIfArtifactCache returns the walk directive for a directory reached at
// path: fs.SkipDir when that directory IS the workspace's artifact cache, nil
// otherwise. It is the single decision site for "does a catalog walk step over
// this directory", so every walk that enumerates authored catalog entities
// routes through it and the walks cannot drift on the answer.
//
// The cache stores resolution artifacts — fetched bytes kept under the coordinate
// they were fetched at and vouched for by a lockfile digest. They are not
// authored catalog entities, and a walk that reads one as though an author had
// written it takes bytes nothing has verified, on a path the author cannot edit.
//
// cacheDir is the `artifact_cache_dir` setting, `.aggrega/cache` by default. It
// is interpreted relative to workspaceRoot, which is the anchor the setting
// itself is written against — so the cache resolves to one location no matter
// which directory a given walk happens to start from. Passing "" for cacheDir
// disables the check, for a caller with no cache of its own.
//
// Both sides are resolved to absolute form before they are compared, because a
// catalog directory is not required to sit at the workspace root: a walk may
// start above the cache, at it, or in a sibling subtree, and comparing a
// walk-relative path against a workspace-relative setting would only ever agree
// in the one layout where those two anchors coincide. An absolute cacheDir is
// taken as already resolved rather than re-anchored, so it is never silently
// relocated under the workspace root. A relative workspaceRoot — including the
// empty string, from a caller that tracks no root — anchors to the process
// working directory, the same anchor the path side resolves against.
func SkipDirIfArtifactCache(workspaceRoot, path, cacheDir string) error {
	if cacheDir == "" {
		return nil
	}
	if !filepath.IsAbs(cacheDir) {
		cacheDir = filepath.Join(workspaceRoot, cacheDir)
	}
	cacheAbs, err := filepath.Abs(cacheDir)
	if err != nil {
		return nil
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	if pathAbs == cacheAbs {
		return fs.SkipDir
	}
	return nil
}

// mergeFrom folds one already-indexed directory (perDir) into the accumulated
// catalog with FIRST-WIN precedence: an identity acc already holds (from an
// earlier-listed directory) wins, and perDir's occurrence is recorded as a
// ShadowedEntity rather than overwriting. A not-yet-seen identity is inserted
// with its side-table entries (source path, raw bytes). Iteration is in sorted
// identity order per kind so the resolved set and the Shadows list are a pure,
// deterministic function of directory order — preserving byte-identical output.
func (acc *Catalog) mergeFrom(perDir *Catalog, perDirRoot string, winnerDir map[string]string) {
	// Every kind's retained bytes are keyed by the same index key as its typed
	// map, so the winning identity carries its own bytes and never another
	// version's.
	mergeKind(acc, "Component", acc.Components, perDir.Components, perDir, perDirRoot, winnerDir, func(k string) {
		acc.componentSources[k] = perDir.componentSources[k]
		bk := entityBytesKey("Component", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	mergeKind(acc, "Feature", acc.Features, perDir.Features, perDir, perDirRoot, winnerDir, func(k string) {
		acc.featureSources[k] = perDir.featureSources[k]
		bk := entityBytesKey("Feature", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	mergeKind(acc, "Recipe", acc.Recipes, perDir.Recipes, perDir, perDirRoot, winnerDir, func(k string) {
		bk := entityBytesKey("Recipe", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
		acc.carryRecordedPins(perDir, bk)
	})
	mergeKind(acc, "FeatureRecipe", acc.FeatureRecipes, perDir.FeatureRecipes, perDir, perDirRoot, winnerDir, func(k string) {
		bk := entityBytesKey("FeatureRecipe", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
		acc.carryRecordedPins(perDir, bk)
	})
	mergeKind(acc, "Aggregate", acc.Aggregates, perDir.Aggregates, perDir, perDirRoot, winnerDir, func(k string) {
		acc.aggregateSources[k] = perDir.aggregateSources[k]
		bk := entityBytesKey("Aggregate", k)
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
	mergeKind(acc, "WorkloadLibrary", acc.WorkloadLibraries, perDir.WorkloadLibraries, perDir, perDirRoot, winnerDir, func(k string) {
		bk := entityBytesKey("WorkloadLibrary", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	mergeKind(acc, "ArtifactLibrary", acc.ArtifactLibraries, perDir.ArtifactLibraries, perDir, perDirRoot, winnerDir, func(k string) {
		bk := entityBytesKey("ArtifactLibrary", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
	// The Plugin source side-table feeds the pillar-validation
	// spec.provider.workloadSchema compile gate so its error messages name the
	// offending file.
	mergeKind(acc, "Plugin", acc.Plugins, perDir.Plugins, perDir, perDirRoot, winnerDir, func(k string) {
		acc.pluginSources[k] = perDir.pluginSources[k]
		bk := entityBytesKey("Plugin", k)
		acc.entityBytes[bk] = perDir.entityBytes[bk]
	})
}

// carryRecordedPins folds one winning entity's recorded pins from a
// per-directory index into the accumulated one, under the document that entity
// was read from. identityKey is the kind-namespaced identity the merge is
// resolving, which is how the document is looked up.
//
// It is called only from a merge's insert arm, so a shadowed occurrence's pins
// never reach the accumulated catalog. That matters because these are keyed by
// document and documents do not collide: a plain union would carry both
// occurrences' pins side by side, and the loser's entry would sit there
// answering for a file the build never read.
func (acc *Catalog) carryRecordedPins(perDir *Catalog, identityKey string) {
	document := perDir.pathByIdentity[identityKey]
	acc.recordedPins.Record(document, perDir.recordedPins.For(document))
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

// newSkippedEntity builds a SkippedEntity, recovering the identity the refused
// document declares for itself from a best-effort header re-parse (every field
// empty when the YAML is too malformed to peek). Version and capability are
// recovered alongside kind and name because a recipe's identity is
// provider-scoped and version-scoped: kind and name alone cannot tell two
// refused recipes of one name apart, and matching a reference against a
// half-identity would name the wrong file.
func newSkippedEntity(path string, data []byte, err error) SkippedEntity {
	se := SkippedEntity{Path: path, Err: err}
	var hdr fileHeader
	if yaml.Unmarshal(data, &hdr) == nil {
		se.Kind = hdr.Kind
		se.Name = hdr.Metadata.Name
		se.Version = hdr.Metadata.Version
		se.Capability = hdr.Spec.Capability
	}
	return se
}

// validateSchemaPillarsPass is the pillar-validation pass of LoadAll: walks every Component,
// Feature, and Plugin in sorted order, runs the corresponding
// load-time validator against each entity's schema-bearing fields with
// the now-fully-populated SchemaLibraries map for cross-document $ref
// expansion (Component / Feature only), collects every failure, and
// returns a single aggregated CatalogError when at least one entity
// failed. The aggregate names the pass and counts what it found, and
// carries every finding on its chain, so a caller can read a finding's
// condition and its code rather than only the text of the group.
// Sorted iteration is required for diagnostic stability:
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
// UnusablePillarContractErrors refuses each declared pillar of a Component or
// Feature whose contract compiles and then builds no validator, under the same
// condition the conformance rules raise for a contract that does not compile at
// all.
//
// One condition covers both because they are one fault with two spellings: a
// subject declaring a contract nothing can be measured against. The reading
// itself carries no condition, because it is performed a layer below the one
// that declares it; attaching it here is what makes the refusal matchable, and
// keeping the attachment in a single exported composer is what stops the load
// surface and the entity-validation surface reporting the same fault under
// different identities.
//
// The refusal is located at the pillar's own authored field and source line, and
// attributed to the entity that DECLARED the contract rather than to any
// document configuring it — an Aggregate author handed a component type whose
// contract is unusable has nothing to change in their own file. The validator's
// own text is carried verbatim, since re-deriving what is wrong with the schema
// would be a second answer to keep in step.
func UnusablePillarContractErrors(
	entityKind, entityName, filePath, extends string,
	schemaNode *yaml.Node,
	sockets types.OrderedMap[types.UsesSchemaEntry],
	libraries map[string]types.SchemaLibrary,
) []error {
	unusable := validator.UnusableSchemaContracts(filePath, extends, schemaNode, sockets, libraries)
	if len(unusable) == 0 {
		return nil
	}
	out := make([]error, 0, len(unusable))
	for _, u := range unusable {
		out = append(out, cerrors.Wrap(
			resolver.ErrSchemaContractCompileFailure(),
			cerrors.CatalogError,
			cerrors.SourceLocation{File: filePath, Line: u.Line},
			cerrors.ComposeMessage(resolver.ErrSchemaContractCompileFailure(), nil, fmt.Sprintf(
				"%s %q: %s declares a schema no validator can be built from, so the %s it "+
					"constrains would go unmeasured: %s",
				entityKind, entityName, u.Field, u.Pillar, u.Err.Error(),
			)),
		))
	}
	return out
}

func (c *Catalog) validateSchemaPillarsPass() error {
	// Clear what this pass contributed on the last run. A second run over the
	// same catalog would otherwise report every pillar-overlap advisory twice.
	c.pillarLintWarnings = nil
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
			comp.Spec.Extends,
			&comp.Spec.Schema,
			c.SchemaLibraries,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
		validationErrors = append(validationErrors, UnusablePillarContractErrors(
			"Component",
			name,
			c.componentSources[name],
			comp.Spec.Extends,
			&comp.Spec.Schema,
			comp.Spec.UsesSchema,
			c.SchemaLibraries,
		)...)
		c.pillarLintWarnings = append(c.pillarLintWarnings, validator.ValidateSchemaPillarOverlap(
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
			feat.Spec.Extends,
			&feat.Spec.Schema,
			c.SchemaLibraries,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
		validationErrors = append(validationErrors, UnusablePillarContractErrors(
			"Feature",
			name,
			c.featureSources[name],
			feat.Spec.Extends,
			&feat.Spec.Schema,
			feat.Spec.UsesSchema,
			c.SchemaLibraries,
		)...)
		c.pillarLintWarnings = append(c.pillarLintWarnings, validator.ValidateSchemaPillarOverlap(
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
	// The findings are carried on the chain, and the message names the pass and
	// counts them. Rendering each finding into the message text as well would be
	// a second home for content the chain already holds, and the two would be
	// printed one after the other on the same diagnostic.
	//
	// Attaching them is what the report was missing rather than a refinement of
	// it: joining the findings into a string and constructing with no wrap left
	// every consumer of this envelope unable to read a finding's condition,
	// match one, or reach its code, so a build could print a coded finding while
	// the program holding it knew of no condition at all.
	//
	// This group takes no identity of its own. It names a pass and counts what
	// that pass found, and its findings come from three separate producers whose
	// fixes differ per finding, so no one sentence is true of all of them and
	// tells the reader what to do. Whatever code the head carries is therefore
	// the condition the chain actually names, never one minted for the count.
	return cerrors.Unidentified(
		cerrors.CatalogError,
		cerrors.SourceLocation{},
		fmt.Sprintf("catalog load: %d schema validation error(s)", len(validationErrors)),
		nil,
		errors.Join(validationErrors...),
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
// invariant); errors are collected across entities and carried on the chain of
// one aggregate CatalogError, whose own message names the pass and counts them.
// Returns nil when every supplied entity validates. The advisory
// pillar-overlap lint is intentionally NOT run here — it is a non-fatal authoring
// smell the build collects separately, not part of the reject gate.
//
// remedy, when non-nil, supplies an operator-actionable line appended to the
// aggregate message. It belongs on the group rather than on a finding because
// it is one suggestion about the pass as a whole, which no single finding
// carries. It is a function rather than a string because it is called
// ONLY when there is already an error to attach it to: a caller that has to read
// the filesystem to work out what to suggest must not pay for that on a clean
// validation. It belongs in the message rather than in a wrapping error because
// the aggregate message is what the diagnostic surface renders. Pass nil for a
// caller with nothing to suggest.
func ValidateClosurePillars(
	components map[string]types.Component, componentSources map[string]string,
	features map[string]types.Feature, featureSources map[string]string,
	libs map[string]types.SchemaLibrary,
	remedy func() string,
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
			"Component", name, componentSources[name], comp.Spec.Extends, &comp.Spec.Schema, libs,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
		validationErrors = append(validationErrors, UnusablePillarContractErrors(
			"Component", name, componentSources[name], comp.Spec.Extends,
			&comp.Spec.Schema, comp.Spec.UsesSchema, libs,
		)...)
	}

	featNames := make([]string, 0, len(features))
	for name := range features {
		featNames = append(featNames, name)
	}
	sort.Strings(featNames)
	for _, name := range featNames {
		feat := features[name]
		if err := validator.ValidateSchemaPillars(
			"Feature", name, featureSources[name], feat.Spec.Extends, &feat.Spec.Schema, libs,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
		validationErrors = append(validationErrors, UnusablePillarContractErrors(
			"Feature", name, featureSources[name], feat.Spec.Extends,
			&feat.Spec.Schema, feat.Spec.UsesSchema, libs,
		)...)
	}

	if len(validationErrors) == 0 {
		return nil
	}
	message := fmt.Sprintf("closure validation: %d schema validation error(s)", len(validationErrors))
	// The remedy is consulted only now, on the failing path, so a caller that
	// has to read the filesystem to produce one never pays for it on a clean
	// validation.
	if remedy != nil {
		if r := remedy(); r != "" {
			message += "\n  " + r
		}
	}
	// The findings are carried on the chain, for the reason given at the
	// catalog-load counterpart: attaching them is what lets a consumer read a
	// finding's condition, and rendering them into the message as well would
	// print the same content twice on one diagnostic. This group takes no
	// identity of its own either — it names a pass and counts what it found,
	// over findings whose fixes differ per finding.
	return cerrors.Unidentified(
		cerrors.CatalogError,
		cerrors.SourceLocation{},
		message,
		nil,
		errors.Join(validationErrors...),
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
// SchemaLibrary, MappingLibrary, WorkloadLibrary, ArtifactLibrary, Plugin), leaving out Manifest and
// Release (which are deployment-side and are silently skipped — see
// LoadAll's error model). Adding a new catalog-resident kind requires
// extending the switch here AND adding the matching map field on Catalog.
//
// This kind set coincides exactly with the publishable set that pkg/oci's
// mediatype table declares. The coincidence is real but not definitional, and
// the two must not be collapsed into one home: this switch answers which kinds
// a catalog holds and how each one is parsed into which map, while the
// mediatype table answers which kinds may be published and under which
// mediatype. Every arm below carries irreducible per-kind work — its own parse
// call, its own identity claim, its own destination map and its own source map
// — so there is no membership test here to extract in the first place. A kind
// that is catalog-resident without being publishable is a shape the design
// permits, and the two sides already exclude on unrelated grounds: Manifest and
// Release are absent here because they are deployment-side, which is not a
// reason a publishing rule would ever give.
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
		// The indexing-walk parse + the static per-entity x-aggrega-* discouragement
		// scan run through the shared per-entity resolve (parseComponentEntity) so the
		// eager and lazy backings cannot drift. The pillar-shape + body validation runs
		// at pillar validation via validateSchemaPillarsPass (it needs the complete
		// SchemaLibraries map for cross-document $ref expansion, so it cannot run
		// per-file here).
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
		c.entityBytes[entityBytesKey("Component", ckey)] = data
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
		c.entityBytes[entityBytesKey("Feature", fkey)] = data
	case "Recipe":
		// parseRecipeEntity is the shared per-entity resolve (unmarshal, shape
		// validation, relative source / artifact-$import rewrites) so the eager
		// and lazy backings cannot drift. The dup-identity check is catalog-level
		// state and stays here.
		e, pins, err := parseRecipeEntity(data, path)
		if err != nil {
			return err
		}
		rKey := recipeIdentityKey(e.Spec.Capability, e.Metadata.Name, e.Metadata.Version)
		if err := c.claimIdentity("Recipe:"+rKey, fmt.Sprintf("recipe identity %q", rKey), path); err != nil {
			return err
		}
		c.Recipes[rKey] = e
		c.entityBytes[entityBytesKey("Recipe", rKey)] = data
		c.recordedPins.Record(path, pins)
	case "FeatureRecipe":
		e, pins, err := parseFeatureRecipeEntity(data, path)
		if err != nil {
			return err
		}
		frKey := recipeIdentityKey(e.Spec.Capability, e.Metadata.Name, e.Metadata.Version)
		if err := c.claimIdentity("FeatureRecipe:"+frKey, fmt.Sprintf("feature recipe identity %q", frKey), path); err != nil {
			return err
		}
		c.FeatureRecipes[frKey] = e
		c.entityBytes[entityBytesKey("FeatureRecipe", frKey)] = data
		c.recordedPins.Record(path, pins)
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
		c.entityBytes[entityBytesKey("Aggregate", akey)] = data
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
		// parseMappingLibraryEntity is the shared per-entity resolve so the eager
		// and lazy backings cannot drift. It self-wraps its failures into the final
		// CATALOG_ERROR shape, so the bare return preserves the diagnostics.
		//
		// No template-body content validation analogous to SchemaLibrary's
		// $dynamicAnchor / $dynamicRef scan — MappingLibrary entries are
		// opaque Go template strings; the compiler does not parse them
		// at entity load. Content semantics (including any cross-library
		// $import: references) resolve at Recipe / FeatureRecipe render
		// time via the Go template engine.
		e, err := parseMappingLibraryEntity(data, path)
		if err != nil {
			return err
		}
		if err := c.claimIdentity("MappingLibrary:"+e.Metadata.Name, fmt.Sprintf("MappingLibrary identity %q", e.Metadata.Name), path); err != nil {
			return err
		}
		c.MappingLibraries[e.Metadata.Name] = e
		c.entityBytes[entityBytesKey("MappingLibrary", e.Metadata.Name)] = data
	case "WorkloadLibrary":
		// parseWorkloadLibraryEntity is the shared per-entity resolve so the eager
		// and lazy backings cannot drift; it self-wraps its failures, so the bare
		// return preserves the diagnostics.
		//
		// No template-body content validation: WorkloadLibrary entries are
		// opaque Go template strings; the compiler does not parse them at
		// entity load. Content semantics (including any cross-library
		// $import: references) resolve at Recipe render time via the Go
		// template engine.
		e, err := parseWorkloadLibraryEntity(data, path)
		if err != nil {
			return err
		}
		if err := c.claimIdentity("WorkloadLibrary:"+e.Metadata.Name, fmt.Sprintf("WorkloadLibrary identity %q", e.Metadata.Name), path); err != nil {
			return err
		}
		c.WorkloadLibraries[e.Metadata.Name] = e
		c.entityBytes[entityBytesKey("WorkloadLibrary", e.Metadata.Name)] = data
	case "ArtifactLibrary":
		// parseArtifactLibraryEntity is the shared per-entity resolve so the eager
		// and lazy backings cannot drift; it self-wraps its failures, so the bare
		// return preserves the diagnostics.
		//
		// No template-body content validation: ArtifactLibrary bundle files are
		// opaque Go template strings; the compiler does not parse them at entity
		// load. Content semantics (including any cross-library $import: references)
		// resolve at Recipe / FeatureRecipe render time via the Go template engine.
		e, err := parseArtifactLibraryEntity(data, path)
		if err != nil {
			return err
		}
		if err := c.claimIdentity("ArtifactLibrary:"+e.Metadata.Name, fmt.Sprintf("ArtifactLibrary identity %q", e.Metadata.Name), path); err != nil {
			return err
		}
		c.ArtifactLibraries[e.Metadata.Name] = e
		c.entityBytes[entityBytesKey("ArtifactLibrary", e.Metadata.Name)] = data
	case "Plugin":
		// parsePluginEntity is the shared per-entity resolve so the eager and lazy
		// backings cannot drift. Sharing the scan is what gives the lazy backing
		// reject-parity for plugins; it self-wraps its failures, so the bare return
		// preserves the diagnostics.
		e, err := parsePluginEntity(data, path)
		if err != nil {
			return err
		}
		wkey := entityIndexKey(e.Coordinate(), e.Metadata.Version)
		if err := c.claimIdentity("Plugin:"+wkey, fmt.Sprintf("Plugin identity %q", wkey), path); err != nil {
			return err
		}
		c.Plugins[wkey] = e
		c.pluginSources[wkey] = path
		c.entityBytes[entityBytesKey("Plugin", wkey)] = data
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
		return cerrors.Wrap(ErrDuplicateEntityIdentity(), cerrors.CatalogError, cerrors.SourceLocation{File: path},
			cerrors.ComposeMessage(ErrDuplicateEntityIdentity(), []string{desc}, ""))
	}
	c.pathByIdentity[fullKey] = path
	return nil
}

// ParseComponentEntity is the exported form of the shared per-entity Component
// resolution, for a surface that holds one entity's bytes rather than a catalog
// directory — `aggrega validate` on a single file. Such a surface routes here
// rather than unmarshalling for itself, so it reads the same resolved entity a
// build does; a second parse of its own would read an unresolved schema for the
// one entity the caller actually asked about.
func ParseComponentEntity(data []byte, path string) (types.Component, error) {
	return parseComponentEntity(data, path)
}

// ParseFeatureEntity is the Feature counterpart of [ParseComponentEntity], and
// exists for the same reason.
func ParseFeatureEntity(data []byte, path string) (types.Feature, error) {
	return parseFeatureEntity(data, path)
}

// ParseRecipeEntity is the Recipe counterpart of [ParseComponentEntity], and
// exists for the same reason: the per-entity resolve is where a Recipe's local
// source and every file its imports name are anchored to the directory holding the
// Recipe and held inside it, so a surface that decoded the bytes for itself would
// read those paths against whatever directory it happened to run in, and would
// never meet the refusal a path leaving that directory gets everywhere else.
//
// It resolves the document and reads nothing beside it: the lock the catalog
// backings read alongside is not part of what the document declares, and a
// surface asking about one entity in isolation has no use for the pins it
// records.
func ParseRecipeEntity(data []byte, path string) (types.Recipe, error) {
	return resolveRecipeDocument(data, path)
}

// ParseFeatureRecipeEntity is the FeatureRecipe counterpart of
// [ParseRecipeEntity], and exists for the same reason.
func ParseFeatureRecipeEntity(data []byte, path string) (types.FeatureRecipe, error) {
	return resolveFeatureRecipeDocument(data, path)
}

// parseComponentEntity is the single per-entity Component resolution shared by the
// eager (catalog.Load, per file) and lazy (LazyLookup, per URI on cache miss)
// catalog-access backings: full unmarshal, kind-check, the static per-entity
// load-time check, and the relative-`$import` schema substitution against the
// entity file's own directory. It is the SSOT for "what an authored Component
// resolves to" so the two backings cannot drift in parse/validation. Extends-merge
// is applied separately by each backing through validator.ResolveComponentExtends
// (eager in ResolveAllExtends; lazy in Component()); library-dependent pillar
// validation (validateSchemaPillarsPass) needs the complete SchemaLibraries map and
// is not a per-entity step.
func parseComponentEntity(data []byte, path string) (types.Component, error) {
	return resolveComponentDocument(data, path, nil)
}

// resolveComponentDocument is parseComponentEntity's body, with read told each
// file the schema imports substitute. It is one body with an optional listener
// rather than two parses, so the files a publication carries for a Component are
// the files the same parse a build runs actually read.
func resolveComponentDocument(data []byte, path string, read schemaFileRead) (types.Component, error) {
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
	if err := resolveEntitySchemaImports(path, &e.Spec.Schema, &e.Spec.UsesSchema, read); err != nil {
		return types.Component{}, err
	}
	validator.DiscourageDirectXAggregaInSchema(e.Metadata.Name, &e.Spec.Schema)
	return e, nil
}

// parseFeatureEntity mirrors parseComponentEntity for the Feature surface.
func parseFeatureEntity(data []byte, path string) (types.Feature, error) {
	return resolveFeatureDocument(data, path, nil)
}

// resolveFeatureDocument mirrors resolveComponentDocument for the Feature
// surface.
func resolveFeatureDocument(data []byte, path string, read schemaFileRead) (types.Feature, error) {
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
	if err := resolveEntitySchemaImports(path, &e.Spec.Schema, &e.Spec.UsesSchema, read); err != nil {
		return types.Feature{}, err
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
// The source is anchored through anchorEntitySource, which both source-bearing
// kinds share and which documents what that resolution decides and why.
//
// It also reads the content pins recorded for the entity, from the lock file
// beside it, and returns them alongside. That belongs here for the same reason
// the path rewrites do: this is where the document and the directory it was read
// from are both in hand, and every later reader has the entity alone. The pins
// are returned rather than carried on the entity because an entity document
// declares no pins — what the machine derived lives beside it.
//
// Unlike parseComponentEntity, this self-wraps every failure into its final
// CATALOG_ERROR shape (unmarshal, source rewrite, artifact rewrite each carry a
// distinct message; ValidateRecipe already returns a shaped error) so a single
// `return err` at the caller preserves byte-identical diagnostics.
func parseRecipeEntity(data []byte, path string) (types.Recipe, recipepin.Pins, error) {
	e, err := resolveRecipeDocument(data, path)
	if err != nil {
		return types.Recipe{}, recipepin.Pins{}, err
	}
	pins, err := recordedPins(path)
	if err != nil {
		return types.Recipe{}, recipepin.Pins{}, err
	}
	return e, pins, nil
}

// resolveRecipeDocument is the half of parseRecipeEntity that reads the document
// alone: every step that decides what the authored Recipe resolves to, and none
// that reads what lies beside it. It is a separate body so that a surface holding
// one entity's bytes reaches exactly these steps — see [ParseRecipeEntity] — while
// the catalog backings reach them through parseRecipeEntity, so there is still one
// statement of what an authored Recipe resolves to.
func resolveRecipeDocument(data []byte, path string) (types.Recipe, error) {
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
	anchoredSource, err := anchorEntitySource(path, "Recipe", e.Spec.Source)
	if err != nil {
		return types.Recipe{}, err
	}
	e.Spec.Source = anchoredSource

	mappingTarget, err := resolveImportPath(path, e.Spec.ValuesMapping.Import, e.Spec.ValuesMapping)
	if err != nil {
		return types.Recipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve Recipe valuesMapping $import target %q", e.Spec.ValuesMapping.Import))
	}
	e.Spec.ValuesMapping.Import = mappingTarget

	workloadTarget, err := resolveImportPath(path, e.Spec.Workload.Import, e.Spec.Workload)
	if err != nil {
		return types.Recipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve Recipe workload $import target %q", e.Spec.Workload.Import))
	}
	e.Spec.Workload.Import = workloadTarget

	if err := resolveArtifactImports(path, "Recipe", &e.Spec.Artifacts); err != nil {
		return types.Recipe{}, err
	}
	return e, nil
}

// recordedPins reads the content pins recorded for the entity at path, from the
// lock file beside it. It is the one body both pin-bearing kinds route through,
// and the one answer to "which pins are in force for this entity" — a second
// spelling of it would be free to settle something the render never sees.
//
// The lock is the only home for these pins. An entity document is never
// machine-written, so nothing an author typed is a measurement of what was
// acquired; the file beside it is where the resolution records what it fetched
// and what it read, and that is what the build compares against.
//
// Absence of a lock is an entity with nothing recorded for it, which is the zero
// value rather than an error: a recipe with no remote source, no imported
// artifact and no local source has nothing to pin, and one whose remote source is
// templated has an identity knowable only per-Release.
//
// A lock that will not read is a failure rather than an absence, and the reason
// is the one that makes the file worth having: a pin nobody could read is a
// comparison that silently does not happen, which reads exactly like a build
// whose inputs had not moved.
func recordedPins(path string) (recipepin.Pins, error) {
	lock, present, err := recipepin.ReadLock(path)
	if err != nil {
		return recipepin.Pins{}, err
	}
	if !present {
		return recipepin.Pins{}, nil
	}
	return lock.Pins(), nil
}

// anchorEntitySource resolves an entity's authored source against the directory
// of the document that declared it, and is the one body both source-bearing kinds
// route through.
//
// The resolution itself lives in sourceref, which is where the locality reading
// lives too: a reference carrying a transport scheme names something fetched
// rather than a path, and joining one to a directory destroys the reference and
// leaves a value every later pass reads as local — the pass that pins a Release's
// dependencies pins nothing, and the render then stats a directory nobody
// authored. That single reading also answers the empty case, which is why no
// separate emptiness test sits beside it.
//
// It asks that resolution for the source VALUE rather than for a reference, so
// both shapes a source is authored in are covered by one rule: a scalar source is
// its own reference, and a structured source names its reference at the url entry
// and carries every other key — including the keys the provider reads — through
// untouched. Asking for a reference instead is what left the structured shape
// unanchored: a relative url an author wrote beside their own document stayed
// relative to whatever directory the build ran from.
//
// A reference written as a TEMPLATE is deliberately left as the author wrote it.
// It names nothing until it has been evaluated, so the directory it is relative to
// belongs to the value it produces, and the render anchors it there once that
// value exists. Anchoring the template's text instead reads as harmless whenever
// the value happens to be relative — a directory joined to a relative path is the
// same directory either way — while silently mangling every value that is not,
// because an absolute path, a transport reference and an empty result are each
// decided by text the join has already prefixed by the time they exist. The test
// is over the REFERENCE, not over the source's identity: a structured source whose
// version is a template still has a url its author wrote out in full.
//
// A local reference that leaves the entity's directory, or names an absolute
// location, is refused by that same resolution with
// [sourceref.ErrContentOutsideEntityDirectory], the refusal every content path an
// entity declares meets; a templated one meets it at render, once it has a value.
//
// kind names the entity in the failure, as it does for the artifacts rewrite, so
// each caller's diagnostic reads as its own.
func anchorEntitySource(entityPath, kind string, source any) (any, error) {
	anchored, err := sourceref.AnchorSource(entityPath, source)
	if err != nil {
		ref, _ := sourceref.Reference(source)
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: entityPath}, fmt.Sprintf("resolve %s source %q", kind, ref))
	}
	return anchored, nil
}

// importRef is the shape the three import-bearing Recipe and FeatureRecipe
// fields — valuesMapping, workload, and each artifacts entry — have in common:
// each answers on its own type whether the authored value is an identity
// reference rather than a filesystem path. The question cannot be answered
// generically because the fragment naming a library differs per field, so each
// type keeps its own answer and this is how the shared resolution asks for it.
type importRef interface {
	IsLibraryImport() bool
	HasOCIPrefix() bool
}

// resolveImportPath returns an authored `$import:` value resolved against the
// directory of the entity file that declared it, so a path an author wrote
// beside their own file reads the same whatever directory the build runs from.
//
// Three values come back untouched. An empty one carries no import at all. A
// bare-name library reference and a registry-host reference are identities
// rather than paths, and the consumption site reads their exact leading text to
// resolve the first and to reject the second — so rewriting either would turn a
// named refusal into a missing-file report. Everything else is a local path, and
// it is anchored to the entity's directory and refused where it leaves it.
//
// The registry-host arm is not reachable from a document: every `$import:`
// position on either kind refuses a registry host as the document is read, so
// such a value is never carried this far. It is kept because it is what makes
// the resolution total over the values these types can hold, and it is proved
// against that specification directly rather than through a caller. A reference
// read out of a library entry's body is not gated that way, because such a body
// is declared as a plain string; it is refused where it is consumed, which is a
// different resolution.
//
// This is the one home of that resolution for every import-bearing field, so
// the surfaces cannot come to disagree on what a relative path means.
func resolveImportPath(entityPath, authored string, ref importRef) (string, error) {
	if !importNamesFile(authored, ref) {
		return authored, nil
	}
	return resolveAgainstEntityDir(entityPath, authored)
}

// importNamesFile reports whether an authored `$import:` value names a file
// beside the entity, rather than nothing, a library entry or a registry location.
// It is the one reading of that question: the resolution above anchors exactly the
// values it answers yes for, and the files a publication carries for an import are
// exactly the ones it answers yes for, so the two cannot come to disagree about
// which imports are files.
func importNamesFile(authored string, ref importRef) bool {
	return authored != "" && !ref.IsLibraryImport() && !ref.HasOCIPrefix()
}

// resolveAgainstEntityDir anchors an authored import path to the directory
// holding the entity file that declared it, and refuses one that does not stay
// inside that directory — an absolute path included — with
// [sourceref.ErrContentOutsideEntityDirectory].
//
// Both halves are sourceref.AnchorContent's, reached rather than restated: it is
// the one statement of what an entity's content path is relative to and bounded
// by, shared with the source resolution and the render, so an import and a source
// cannot come to disagree about which paths an entity may name. The bound exists
// because every file a publication carries for the entity is taken from its
// directory: an import naming a file outside it names content that would not
// travel with the entity.
//
// An entity parsed from bytes carrying no file of their own resolves against
// the directory the process runs in, which is what an empty path's directory
// component yields; such an entity has no directory to be relative to, so
// there is no better anchor available at this point. It is held to the same
// bound, which does not depend on which directory that is.
//
// It shares an idiom with sourceref.AnchorToDocument and answers a DIFFERENT
// question, which is why the two are deliberately separate rather than one body
// with a flag. An `$import:` names a file and only a file — a transport
// reference in that position is refused where it is consumed — so this
// resolution anchors whatever it is given. A source reference may legitimately
// name something fetched, or still be a template, so that resolution has to read
// the value before deciding whether to anchor it at all. Folding them together
// would put a gate in front of the import path that its callers have already
// answered differently.
func resolveAgainstEntityDir(entityPath, authored string) (string, error) {
	return sourceref.AnchorContent(entityPath, authored)
}

// resolveArtifactImports rewrites each artifacts entry's `$import:` target
// against the directory of the entity file that declared it, in place and in
// author order. An entry whose target the resolution hands back unchanged is
// left alone rather than rewritten to itself.
//
// Recipe and FeatureRecipe expose the same artifacts surface and resolve it
// identically, so the rewrite has one home and cannot come to differ between
// the two kinds. The kind argument names the entity in the failure, so each
// caller's diagnostic reads as its own.
func resolveArtifactImports(entityPath, kind string, artifacts *types.OrderedMap[types.ArtifactEntry]) error {
	for _, name := range artifacts.Keys() {
		entry, _ := artifacts.Get(name)
		target, err := resolveImportPath(entityPath, entry.Import, entry)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: entityPath}, fmt.Sprintf("resolve %s artifact %q $import target %q", kind, name, entry.Import))
		}
		if target == entry.Import {
			continue
		}
		entry.Import = target
		artifacts.Set(name, entry)
	}
	return nil
}

// parseFeatureRecipeEntity is the FeatureRecipe analogue of parseRecipeEntity:
// envelope gate, unmarshal, kind-check, the load-time shape rules
// (validator.ValidateFeatureRecipe), and the same relative-source and
// relative-`$import` rewrites against the entity file's own directory, and the
// same reading of the lock file beside it. Both kinds route their artifacts
// rewrite through resolveArtifactImports, their source through
// anchorEntitySource and their pins through recordedPins, so the two cannot
// drift; those functions document what each resolution decides and why.
// Self-wraps its failures like parseRecipeEntity.
func parseFeatureRecipeEntity(data []byte, path string) (types.FeatureRecipe, recipepin.Pins, error) {
	e, err := resolveFeatureRecipeDocument(data, path)
	if err != nil {
		return types.FeatureRecipe{}, recipepin.Pins{}, err
	}
	pins, err := recordedPins(path)
	if err != nil {
		return types.FeatureRecipe{}, recipepin.Pins{}, err
	}
	return e, pins, nil
}

// resolveFeatureRecipeDocument is the FeatureRecipe counterpart of
// resolveRecipeDocument: the steps that read the document alone, split out for the
// same reason.
func resolveFeatureRecipeDocument(data []byte, path string) (types.FeatureRecipe, error) {
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
	anchoredSource, err := anchorEntitySource(path, "FeatureRecipe", e.Spec.Source)
	if err != nil {
		return types.FeatureRecipe{}, err
	}
	e.Spec.Source = anchoredSource

	mappingTarget, err := resolveImportPath(path, e.Spec.ValuesMapping.Import, e.Spec.ValuesMapping)
	if err != nil {
		return types.FeatureRecipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve FeatureRecipe valuesMapping $import target %q", e.Spec.ValuesMapping.Import))
	}
	e.Spec.ValuesMapping.Import = mappingTarget

	workloadTarget, err := resolveImportPath(path, e.Spec.Workload.Import, e.Spec.Workload)
	if err != nil {
		return types.FeatureRecipe{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve FeatureRecipe workload $import target %q", e.Spec.Workload.Import))
	}
	e.Spec.Workload.Import = workloadTarget

	if err := resolveArtifactImports(path, "FeatureRecipe", &e.Spec.Artifacts); err != nil {
		return types.FeatureRecipe{}, err
	}
	return e, nil
}

// parseAggregateEntity is the single per-entity Aggregate resolution: unmarshal
// plus kind-check. The decode and kind-check failures return the raw error, as
// parseComponentEntity does, and the indexing caller wraps the unmarshal failure
// with "unmarshal Aggregate". The two failures that read the document — the
// envelope gate and the parse below it — instead report as classified catalog
// errors carrying the file they were reading, because a reader who cannot open a
// catalog entry needs to know which entry it was before anything else.
//
// It reads the document through the same tolerant parse the envelope gate
// applies, for the reason LoadAggregateFromBytes does: a catalog-resident
// Aggregate may leave a `{{ .parameters.x }}` reference unquoted, and the index
// is a structural read — identity, versions, and the type references the
// closure walk follows. The authoritative parse of such an Aggregate happens
// after its parameters are substituted into the retained raw bytes, so the
// index never decides a value's type.
//
// # It is a DELIBERATE near-duplicate of the resolver's structural read
//
// LoadAggregateFromBytes performs the same three steps over the same bytes and
// folds through the same normalization core. Three things keep the two apart,
// and they are written out once, on that function; what is worth having here is
// which side of each this one sits on. A failure met here is charged to the
// catalog rather than to the Aggregate's author, because the reader who can act
// is whoever owns the catalog entry. The kind is asserted again after the
// decode, because this read is indexing whatever a directory walk handed it and
// the decode is what says which index the entity belongs in. And the fold is
// not reimplemented: types.Normalize below is the same call, so what is
// duplicated is the wrapper and never the fold.
//
// The two do NOT differ in error policy — see that function for why the
// sentinel match it performs is not the narrower of two policies.
func parseAggregateEntity(data []byte, path string) (types.Aggregate, error) {
	if _, err := envelope.ValidateBytes(envelope.KindAggregate, data, path); err != nil {
		return types.Aggregate{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	node, perr := envelope.ParseDecodable(data, path)
	if perr != nil {
		// Structurally unreachable, and kept marked rather than removed. The gate
		// above refuses every error ValidateBytes returns, and ValidateBytes
		// reaches a successful return only by obtaining a parse of these same
		// bytes — its own generic decode, or this very call where that decode
		// failed. These bytes have therefore parsed once already, and this parse
		// is a deterministic function of them.
		//
		// The identical arm stands at the resolver's structural read, for the
		// identical reason. Whether the two should stay two gates or collapse into
		// one is open and is not settled here: ValidateBytes already returns what
		// it parsed, so handing back a decodable node instead would remove the
		// second parse at both sites together — a change to its signature and to
		// every one of its callers, which is why it is named rather than taken.
		// Both sites carry this note meanwhile, so neither reads as a live failure
		// path the other lacks.
		return types.Aggregate{}, cerrors.Wrap(perr, cerrors.CatalogError, cerrors.SourceLocation{File: path}, perr.Error())
	}
	var e types.Aggregate
	if err := node.Decode(&e); err != nil {
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
// by both backings: full unmarshal, kind-check, the forbidden-keyword
// (ValidateSchemaEntry) / template-body (ValidateTemplateBody) scans the eager load
// applies, the `$instantiate:` substitution that resolves a template reference
// into the entry's body, and the refusal of a format name outside the supported
// set in any resolved entry (refuseUnsupportedLibraryFormats). Sharing it is what gives the lazy backing reject-parity
// once it resolves libraries directly (no longer Prime-copied from an
// already-validated eager Catalog): an invalid library the eager build rejects is
// rejected by the lazy resolution too.
//
// Substitution lands here, at the one place a library is turned into a value,
// so every consumer downstream reads a resolved entry and none of them has to
// know the entry was authored as a reference.
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
	// Template bodies are scanned before any entry is resolved, so a body
	// carrying a forbidden keyword literally is named as the template it is,
	// rather than as whichever entry happened to instantiate it first.
	for name, tmpl := range e.Spec.Templates.All() {
		if err := types.ValidateTemplateBody(&tmpl); err != nil {
			return types.SchemaLibrary{}, fmt.Errorf("SchemaLibrary %q template %q: %w", e.Metadata.Name, name, err)
		}
	}
	for name, entry := range e.Spec.Schemas.All() {
		inst, isRef, err := types.ParseInstantiation(&entry)
		if err != nil {
			return types.SchemaLibrary{}, fmt.Errorf("SchemaLibrary %q schema %q: %w", e.Metadata.Name, name, err)
		}
		if isRef {
			body, err := types.InstantiateTemplate(&e, inst.Template, inst.Args)
			if err != nil {
				return types.SchemaLibrary{}, fmt.Errorf("SchemaLibrary %q schema %q: %w", e.Metadata.Name, name, err)
			}
			// Writing back at a key that already exists leaves both the value's
			// position and the key sequence this loop is walking untouched.
			e.Spec.Schemas.Set(name, types.SchemaEntry{Content: body})
			continue
		}
		// InstantiateTemplate runs the forbidden-keyword scan itself, on the
		// resolved body, so the scan here covers an entry authored as content.
		if err := types.ValidateSchemaEntry(&entry); err != nil {
			return types.SchemaLibrary{}, fmt.Errorf("SchemaLibrary %q schema %q: %w", e.Metadata.Name, name, err)
		}
	}
	if err := refuseUnsupportedLibraryFormats(e, path); err != nil {
		return types.SchemaLibrary{}, err
	}
	return e, nil
}

// refuseUnsupportedLibraryFormats refuses a SchemaLibrary whose entries name a
// format outside the supported set, when the library loads rather than when a
// consumer first references the entry — an entry nothing references yet would
// otherwise carry a constraint that reads as one and checks nothing.
//
// Each entry is compiled against its own library, which is what resolves a
// reference from one of its entries to another, and the names judged are those
// the entry authors itself: a position another entry contributed is judged at
// that entry. An entry that does not compile against its own library alone — one
// referencing another library, or one whose body is malformed — is left to the
// consumer that compiles it, where the same judgement is made of the whole
// consuming schema; this pass refuses nothing it did not already hold a compiled
// entry for.
//
// The refusal is the condition a declared contract no validator can be built
// from raises, located at the entry's own line, so a library entry and a
// Component pillar carrying the same fault are refused as one fault.
func refuseUnsupportedLibraryFormats(e types.SchemaLibrary, path string) error {
	own := map[string]types.SchemaLibrary{e.Metadata.Name: e}
	for name, entry := range e.Spec.Schemas.All() {
		content := entry.Content
		if content.Kind == 0 {
			continue
		}
		compiled, err := compactform.Compile(&content,
			compactform.WithSchemaLibraries(own), compactform.WithConsumerFile(path))
		if err != nil {
			continue
		}
		if ferr := jsonvalidator.RefuseUnsupportedAuthoredFormats(compiled); ferr != nil {
			return cerrors.Wrap(
				resolver.ErrSchemaContractCompileFailure(),
				cerrors.CatalogError,
				cerrors.SourceLocation{File: path, Line: entry.Content.Line},
				cerrors.ComposeMessage(resolver.ErrSchemaContractCompileFailure(), nil, fmt.Sprintf(
					"SchemaLibrary %q: schema %q names a format no validator can check, so the values "+
						"it constrains would go unmeasured: %s",
					e.Metadata.Name, name, ferr.Error(),
				)),
			)
		}
	}
	return nil
}

// parseMappingLibraryEntity is the single per-entity MappingLibrary resolution,
// mirroring parseSchemaLibraryEntity on the mapping axis: full unmarshal +
// kind-check. There is no forbidden-keyword / template-body scan — MappingLibrary
// entries are opaque Go template strings the compiler does not parse at entity
// load (their content, including any cross-library `$import:`, resolves at
// Recipe / FeatureRecipe render time). Used by the build-side bridge so a
// lockfile-pinned remote MappingLibrary is parsed identically to a local one.
//
// Like parseRecipeEntity, this self-wraps every failure into its final
// CATALOG_ERROR shape (the envelope rejection and the unmarshal failure each
// carry a distinct message) so a single `return err` at the indexing caller
// preserves byte-identical diagnostics.
func parseMappingLibraryEntity(data []byte, path string) (types.MappingLibrary, error) {
	if _, err := envelope.ValidateBytes(envelope.KindMappingLibrary, data, path); err != nil {
		return types.MappingLibrary{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	var e types.MappingLibrary
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.MappingLibrary{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal MappingLibrary")
	}
	if e.Kind != "MappingLibrary" {
		return types.MappingLibrary{}, fmt.Errorf("expected kind MappingLibrary, got %q", e.Kind)
	}
	return e, nil
}

// parseWorkloadLibraryEntity is the single per-entity WorkloadLibrary resolution,
// mirroring parseMappingLibraryEntity: full unmarshal + kind-check, self-wrapping
// every failure into its final CATALOG_ERROR shape. There is no
// forbidden-keyword / template-body scan — WorkloadLibrary entries are opaque Go
// template strings the compiler does not parse at entity load (their content,
// including any cross-library `$import:`, resolves at Recipe render time).
func parseWorkloadLibraryEntity(data []byte, path string) (types.WorkloadLibrary, error) {
	if _, err := envelope.ValidateBytes(envelope.KindWorkloadLibrary, data, path); err != nil {
		return types.WorkloadLibrary{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	var e types.WorkloadLibrary
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.WorkloadLibrary{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal WorkloadLibrary")
	}
	if e.Kind != "WorkloadLibrary" {
		return types.WorkloadLibrary{}, fmt.Errorf("expected kind WorkloadLibrary, got %q", e.Kind)
	}
	return e, nil
}

// parseArtifactLibraryEntity is the single per-entity ArtifactLibrary resolution,
// mirroring parseWorkloadLibraryEntity: full unmarshal + kind-check, self-wrapping
// every failure into its final CATALOG_ERROR shape. There is no
// forbidden-keyword / template-body scan — ArtifactLibrary bundle files are opaque
// Go template strings the compiler does not parse at entity load (their content,
// including any cross-library `$import:`, resolves at Recipe / FeatureRecipe render
// time). Used by the build-side bridge so a lockfile-pinned remote ArtifactLibrary
// is parsed identically to a local one.
func parseArtifactLibraryEntity(data []byte, path string) (types.ArtifactLibrary, error) {
	if _, err := envelope.ValidateBytes(envelope.KindArtifactLibrary, data, path); err != nil {
		return types.ArtifactLibrary{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	var e types.ArtifactLibrary
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.ArtifactLibrary{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal ArtifactLibrary")
	}
	if e.Kind != "ArtifactLibrary" {
		return types.ArtifactLibrary{}, fmt.Errorf("expected kind ArtifactLibrary, got %q", e.Kind)
	}
	return e, nil
}

// parsePluginEntity validates a Plugin entity's YAML against the plugin envelope
// and unmarshals it. It is the single Plugin-parse home the eager index arm, the
// recipe-shell index fold and the cache-by-URI verdict-socket resolution all
// call, so a plugin entity is validated and decoded identically wherever it is
// read from.
//
// The static per-entity load-time check is the well-known-annotation scope
// contradiction: a provider declaring a key at an input-kind scope its own
// contract does not render is refused here, so the rejection reaches every
// surface that reads a plugin entity — including a catalog walk with no plugin
// binary present, where the entity-against-guest cross-check cannot run.
//
// Like parseRecipeEntity, this self-wraps every failure into its final
// CATALOG_ERROR shape so a single `return err` at the indexing caller preserves
// byte-identical diagnostics. The wrapping preserves the error chain, so a
// caller matching on the envelope sentinel still matches.
func parsePluginEntity(data []byte, path string) (types.Plugin, error) {
	if _, err := envelope.ValidateBytes(envelope.KindPlugin, data, path); err != nil {
		return types.Plugin{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	var e types.Plugin
	if err := yaml.Unmarshal(data, &e); err != nil {
		return types.Plugin{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Plugin")
	}
	if err := types.ValidateWellKnownAnnotationScopes(e); err != nil {
		return types.Plugin{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, err.Error())
	}
	return e, nil
}

// LintWarnings returns non-fatal advisory diagnostics accumulated
// during catalog load. Today's lint surface is schema-pillar-overlap:
// a warning fires when a Component or Feature declares the same
// property name in both values: and infra: pillars at the same
// nesting depth. The compiler accepts overlaps (pillars are separate
// namespaces by design); the lint surfaces a frequent authoring smell
// where reads and template authors face "which pillar holds the foo
// I'm reading?" friction.
//
// The narrowing-on-extend advisories a chain's pillar composition produces are
// served here too, after the pillar-overlap lines, which is the order the two
// producing passes run in.
//
// Returns nil when no lint warnings fired.
//
// Deliberately NOT de-duplicating: two identical lines from two different
// entities are two findings, and collapsing them here would hide the second
// while also hiding the accumulator bug the per-pass clears exist to prevent.
func (c *Catalog) LintWarnings() []string {
	if c == nil {
		return nil
	}
	if len(c.narrowingWarnings) == 0 {
		return c.pillarLintWarnings
	}
	out := make([]string, 0, len(c.pillarLintWarnings)+len(c.narrowingWarnings))
	out = append(out, c.pillarLintWarnings...)
	out = append(out, c.narrowingWarnings...)
	return out
}

// DeprecationWarnings returns non-fatal notices about deprecated catalog shapes,
// formatted as a string slice the caller is free to print, log, or escalate to an
// error as fits its own output.
//
// It returns nil: no deprecation rule is active, so there is no catalog shape this
// accessor reports on today.
//
// Contract. It never returns an error and never mutates the catalog, and an empty
// result means no deprecated shape was detected rather than that detection was
// skipped. A warning never blocks Catalog use. The order is deterministic, so a
// caller may compare or golden the result.
//
// This accessor is where a catalog-shape deprecation rule attaches: a rule reads
// its own catalog state, formats one message per affected entity, and appends to
// the returned slice. Attaching one elsewhere puts a second answer to "what counts
// as a deprecated catalog shape" in the tree.
func (c *Catalog) DeprecationWarnings() []string {
	return nil
}

// ResolveAllExtends walks every Component and Feature in the catalog and
// resolves its extends chain in-place via the validator package's
// ResolveComponentExtends / ResolveFeatureExtends. Both kinds merge labels and
// annotations per key, the extending entity winning a shared key, and leave
// every other metadata field to the extending entity; the two kinds part
// company on the pillar block. An extending
// Component authors no pillar content and receives its base's block verbatim,
// so its chain composes nothing and stores no second view. An extending
// Feature's chain still composes its pillars under JSON Schema composition,
// and that composition DETECTS a child widening a parent's constraint and
// reports it as a non-halting side-band signal (merger.NarrowingViolation);
// it does not enforce a narrow-only rule. Whether a child may narrow but not
// relax a parent constraint is opt-in policy enforced by the
// publishing-discipline plugin at publish time, not a compiler check. Cycles
// are detected by the validator for both kinds and surface as errors rather
// than infinite loops.
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
	// Clear what this pass contributed on the last run. Nothing stops a caller
	// resolving one catalog's chains twice, and without this every narrowing
	// advisory would be reported once per run.
	c.narrowingWarnings = nil

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

	// CatalogEntry chain lookup that compiles each chain entry's authored
	// schema into the JSON-Schema-merge input, so the inheritance merge phase
	// composes the chain's pillars into one effective schema. Features only:
	// an extending Component inherits its base's pillar block verbatim, so its
	// chain has nothing to compose.
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
		c.narrowingWarnings = append(c.narrowingWarnings, composition.Narrowings...)
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
		if _, ok, verr := c.resolveComponentKey(comp.Spec.Extends); !ok && errors.Is(verr, ErrUnsatisfiableEntityVersion()) {
			errs = append(errs, validator.NewValidationError(envelope.KindComponent, name, validator.ValidationDetail{
				FieldPath:  ".extends",
				Constraint: fmt.Sprintf("no catalog version satisfies extends parent %q", comp.Spec.Extends),
				Err:        ErrUnsatisfiableEntityVersion(),
			}))
			continue
		}
		resolved, vErrs := validator.ResolveComponentExtends(comp, compLookup)
		if len(vErrs) > 0 {
			errs = append(errs, vErrs...)
			continue
		}
		// The resolved Component's Spec.Schema is the chain root's node, copied
		// down verbatim by the merge — an extending Component authors no pillar
		// content, so there is nothing to compose and no second view to store.
		// Every Component-schema consumer reads the authored node.
		c.Components[name] = resolved
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
		if _, ok, verr := c.resolveFeatureKey(feat.Spec.Extends); !ok && errors.Is(verr, ErrUnsatisfiableEntityVersion()) {
			errs = append(errs, validator.NewValidationError(envelope.KindFeature, name, validator.ValidationDetail{
				FieldPath:  ".extends",
				Constraint: fmt.Sprintf("no catalog version satisfies extends parent %q", feat.Spec.Extends),
				Err:        ErrUnsatisfiableEntityVersion(),
			}))
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
		inh := validator.ResolveFeatureSchemaChain(rootEntry, featEntryLookup)
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
		// The recipe lookups and the joint instance resolution are served here so
		// a rule asked at a stage of the compilation reads the same recipe index
		// the render resolves through. They were absent while nothing but the
		// mapping-consistency gate read them, and that gate builds its own lookup
		// because it also needs an ancestry resolver this adapter cannot supply.
		Recipe:               c.RecipeByURI,
		FeatureRecipe:        c.FeatureRecipeByURI,
		NodeRecipeResolution: c.nodeRecipeResolution,
		// The local source a build compares with its recorded pins, read through the
		// declared set a publication packs and the pin step pins.
		LocalSourcePins: LocalSourcePinsAt,
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
	path, ok := c.aggregateSource(key)
	if !ok {
		return "", false
	}
	return RelativeToWorkspace(c.workspaceRoot, path), true
}

// aggregateSource returns the source the Aggregate indexed under key was read from,
// as the index recorded it: the document path for one authored in a catalog
// directory, and the label naming its lockfile pin for one read from a cache slot.
// The lazy view's shell answers from the one record of where each entity came from;
// a catalog loaded from directories, which holds no record, answers from the path
// its walk recorded.
func (c *Catalog) aggregateSource(key string) (string, bool) {
	if origin, ok := c.origins[entityBytesKey("Aggregate", key)]; ok {
		return origin.source, true
	}
	path, ok := c.aggregateSources[key]
	return path, ok
}

// EntityBytes returns the raw authored bytes the catalog retained for the entity
// of the given kind (e.g. "Aggregate") that reference resolves to — the exact
// bytes a lockfile digest pins, not a re-read. Returns (nil, false) when nothing
// indexed matches.
//
// For a Component, Feature, Aggregate or Plugin the reference is resolved the way
// every other reference to those kinds is — exact, caret, or unversioned, to the
// highest satisfying version — so a bare name answers with the highest version's
// bytes and a version-pinned one with that version's. For every other kind the
// reference is the identity the entity is indexed under: a Recipe's or
// FeatureRecipe's capability-scoped identity, or a library's name.
func (c *Catalog) EntityBytes(kind, reference string) ([]byte, bool) {
	key, ok := c.indexKeyFor(kind, reference)
	if !ok {
		return nil, false
	}
	b, ok := c.entityBytes[entityBytesKey(kind, key)]
	return b, ok
}

// indexKeyFor returns the key the entity reference resolves to in its kind's
// index: the version select's answer for the kinds indexed per version, and the
// reference itself for the kinds indexed by an identity the reference already
// spells out.
func (c *Catalog) indexKeyFor(kind, reference string) (string, bool) {
	var key string
	var ok bool
	switch kind {
	case "Component":
		key, ok, _ = c.resolveComponentKey(reference)
	case "Feature":
		key, ok, _ = c.resolveFeatureKey(reference)
	case "Aggregate":
		key, ok, _ = c.resolveAggregateKey(reference)
	case "Plugin":
		key, ok, _ = c.resolvePluginKey(reference)
	default:
		return reference, true
	}
	return key, ok
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
		data := c.entityBytes[entityBytesKey("Aggregate", key)]
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
			return nil, "", nil, cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{}, fmt.Sprintf("sub-aggregate %q not found in catalog (check the aggregates-block reference names a known Aggregate type, or run `aggrega pull --release <release>` if it must be fetched)", typeName), nil, nil)
		}
		agg := c.Aggregates[key]
		// The return path (TopologyNode provenance) always comes from the
		// recorded source — decoupled from raw-byte acquisition, so a
		// cache-bridged sub-aggregate (bytes, no path) or a failed re-read does
		// not lose source provenance.
		absPath, _ := c.aggregateSource(key)
		returnPath := RelativeToWorkspace(c.workspaceRoot, absPath)
		raw := c.subAggregateRawBytes(key, absPath)
		return resolveSubAggregateParameters(typeName, raw, absPath, returnPath, agg, parentParams)
	}
}

// subAggregateRawBytes returns the raw YAML bytes of the sub-aggregate indexed
// under key (needed for byte-level parameter interpolation). A cache-bridged /
// registry sub-aggregate retains its verified bytes; a local-filesystem one is
// re-read from absPath. Returns nil when neither is available (the loader then
// falls back to the already-parsed, un-interpolated Aggregate).
func (c *Catalog) subAggregateRawBytes(key, absPath string) []byte {
	if b, ok := c.entityBytes[entityBytesKey("Aggregate", key)]; ok && len(b) > 0 {
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
		// Every finding here is one unfulfilled slot of one contract, so one
		// sentence is true of all of them and names the fix: the parameter
		// contract of a sub-module the Aggregate composes was not fulfilled.
		// That makes the group a condition rather than a roll-up, and a
		// condition declares its identity so the header opens with that
		// condition's registered sentence.
		return nil, "", nil, &resolver.MultiResolverError{
			Entity:     fmt.Sprintf("sub-aggregate %s", typeName),
			Summary:    fmt.Sprintf("%d parameter contract violation(s)", len(verrs)),
			Sentinel:   resolver.ErrParameterContractViolation(),
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
