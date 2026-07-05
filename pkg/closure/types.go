// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package closure walks a Release plus its associated Manifest,
// Aggregate, workspace-config, and an injected Lookup, producing
// a typed Closure value the lockfile writer consumes.
//
// The walker is purely structural: it enumerates reference sites
// across the catalog-entity axis (Components, Features, Recipes,
// FeatureRecipes, Aggregates), the library closures
// (SchemaLibrary $ref: and MappingLibrary $import: sites with
// minimum version selection over conflicting requirements), and
// the Wasm plugin axis (embedded Recipe / FeatureRecipe pipeline
// plugins plus workspace-level plugin attachments). The Recipe
// source: field axis is not part of this package's scope; a
// dedicated source-resolver package handles structured-source
// descent separately.
//
// The walker never makes network calls. Every URI resolution is a
// method invocation on the injected Lookup interface, satisfying
// the ahead-of-time-purity discipline (no runtime fetch from
// inside the compiler core; resolution happens via callbacks the
// caller composes from filesystem-catalog and OCI-client surfaces).
package closure

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Closure captures the full result of ComputeClosure. Fields map
// one-to-one to lockfile sections; downstream code consumes the
// Closure field-by-field when materialising the lockfile.
//
// Field declaration order is alphabetical for predictability.
//
// SourceArtifacts is populated by DescendRecipeSources, NOT by
// ComputeClosure. The two passes run sequentially at orchestrator
// integration time: ComputeClosure produces the Entities + library
// + plugin axes; DescendRecipeSources reads the Recipe and
// FeatureRecipe URIs from Entities, evaluates each spec.source
// against the Release's resolved infra, and emits the
// SourceArtifacts axis. Keeping the two passes separate keeps
// ComputeClosure's existing surface (and test suite) untouched and
// confines source-descent's dependencies (RecipeLoader,
// SourceResolver) to the new function.
type Closure struct {
	Entities         []ResolvedRef
	MappingLibraries []ResolvedLibrary
	SchemaLibraries  []ResolvedLibrary
	SourceArtifacts  []SourceArtifact
	Plugins          []ResolvedRef
}

// SourceArtifact mirrors lockfile.SourceArtifact field-for-field;
// the lockfile writer copies the closure value into the lockfile
// type at orchestrator integration time. Identity is the triple
// {Kind, URL, PinnedVersion}; multiple Recipes pinning the same
// artifact dedupe to one SourceArtifact with multiple ReferencedBy
// records.
type SourceArtifact struct {
	Kind          string
	URL           string
	PinnedVersion string
	Digest        string
	Mediatype     string
	ReferencedBy  []SourceArtifactReference
}

// SourceArtifactReference is the closure-side back-pointer mirror
// of lockfile.SourceArtifactReference. Entity + URI identify the
// catalog entity that resolved to the artifact; Field names the
// source-side path inside the entity's spec (always "spec.source"
// in the current implementation). RequestedRef preserves the
// user-authored ref string when the resolved PinnedVersion is a
// transformed value (terraform-module-git tag/branch → commit SHA);
// empty otherwise.
type SourceArtifactReference struct {
	Entity       string
	URI          string
	Field        string
	RequestedRef string
	// Component is the instance whose per-instance infra resolved this reference —
	// a component instance for a Recipe, a feature/secret-attachment instance for
	// a FeatureRecipe. An entity used by N instances yields N references (one per
	// instance) that dedupe to a single artifact when the resolved identity
	// coincides; the field distinguishes them. Empty when the reference was not
	// resolved per-instance.
	Component string
}

// ComponentSourceInfra pairs a using instance with the per-instance infra its
// Recipe/FeatureRecipe source resolves against — a component instance (with its
// cascade-resolved boundary infra) for a Recipe, or a feature/secret-attachment
// instance (with its BoundaryFeature.infra) for a FeatureRecipe. Source-descent
// resolves per instance (not against a flattened Release-wide infra bag), so the
// same entity used by two instances with different infra resolves twice — deduped
// by identity, distinct when the infra genuinely differs.
type ComponentSourceInfra struct {
	// Component is the using instance's identifier (component instance name, or
	// feature/secret-attachment instance name).
	Component string
	Infra     map[string]any
	// Values is the using instance's per-instance values — the materialized
	// component values (for a Recipe) or the per-instance attachment values
	// (for a FeatureRecipe/secret). Source templates resolve `.values` against
	// it, the same materialized values the renderer's `.values` pillar carries,
	// so a value-driven source identity pins identically at lock and build.
	Values map[string]any
}

// RecipeLoader loads parsed Recipe / FeatureRecipe bodies by URI.
// The walker's existing Lookup interface returns only
// version/digest/path tuples; DescendRecipeSources needs the
// parsed spec.source / spec.locals fields, which live on the
// body. Production wiring implements LoadRecipe + LoadFeatureRecipe
// over the catalog filesystem + OCI cache surfaces; tests inject
// in-memory implementations seeded with fixture bodies.
type RecipeLoader interface {
	LoadRecipe(uri string) (*types.Recipe, error)
	LoadFeatureRecipe(uri string) (*types.FeatureRecipe, error)
	// LoadPlugin loads a Plugin entity body by URI. The pipeline-plugin
	// descent reads spec.plugin (the .wasm artifact reference) from it to pin the
	// artifact alongside the entity. Not-found surfaces as a wrapped error so the
	// descent can distinguish a missing-entity failure from other parse errors.
	LoadPlugin(uri string) (*types.Plugin, error)
}

// SourceResolver matches the contract of sourceresolver.Resolve
// without importing the package. The closure walker stays decoupled
// from the per-kind adapter pull machinery; tests inject a
// static-result implementation. Production wiring lives at the
// orchestrator at build time which composes a SourceResolver from the
// shipped sourceresolver package via a small type-conversion shim.
type SourceResolver interface {
	Resolve(spec SourceSpec) (*ResolvedSource, error)
}

// SourceSpec mirrors sourceresolver.SourceSpec field-for-field.
// The two structs are deliberately identical in shape; the
// orchestrator's wiring shim performs a mechanical field-copy
// across the package boundary.
type SourceSpec struct {
	Kind           string
	URL            string
	Version        string
	Chart          string
	ModuleSubdir   string
	Ref            string
	WorkspaceRoot  string
	CacheDir       string
	OriginalSource any
}

// ResolvedSource mirrors sourceresolver.ResolvedSource field-for-field.
// The Mediatype field carries through to the lockfile's
// SourceArtifact.Mediatype slot at the orchestrator's lockfile-
// writer call.
type ResolvedSource struct {
	Kind           string
	URL            string
	PinnedVersion  string
	Digest         string
	Mediatype      string
	CachePath      string
	OriginalSource any
}

// ErrSourceTemplateAtLockTime wraps every lock-time source-template
// evaluation failure: .provides references (forbidden — Phase 1 of
// Step 5 Render has not run at lock time), ambiguous kind
// classification (scalar source URL without recognised scheme + no
// explicit kind), missing .infra/.locals keys, template parse
// errors, and any other condition that surfaces during
// DescendRecipeSources's lock-time evaluation pass. The build-time
// counterpart (lockfile-vs-actual drift) wraps ErrFetchIntegrity at
// the orchestrator, not this sentinel.
var ErrSourceTemplateAtLockTime = errors.New("resolve: source-template evaluation failure at lock time")

// ErrSourceTemplateParse marks a source-template that fails to PARSE (malformed
// template text). A parse error is a structural authoring bug at any phase, so
// lock-time local resolution keeps it loud rather than tolerating it. Errors of
// this class also wrap ErrSourceTemplateAtLockTime.
var ErrSourceTemplateParse = errors.New("resolve: source-template parse error")

// ErrSourceTemplateProvidesRef marks a source-template that references
// `.provides`. Locals feed `.provides`, never the reverse — a template (or a
// local) reading `.provides` is an invalid reverse reference at any phase, not a
// lock-time-only absence, so lock-time local resolution keeps it loud. Errors of
// this class also wrap ErrSourceTemplateAtLockTime.
var ErrSourceTemplateProvidesRef = errors.New("resolve: source-template references .provides (invalid reverse reference)")

// ResolvedRef captures a single catalog-entity or Wasm-plugin
// resolution result. Mirrors the lockfile package's Entity /
// Plugin wire shapes so the lockfile writer can copy
// field-for-field.
type ResolvedRef struct {
	URI             string
	ResolvedVersion string
	Digest          string
	// ManifestDigest is the OCI manifest digest the artifact's signature
	// commits to — the verify subject at pull time. It differs from Digest,
	// the portable content digest used for the cache pin and integrity check.
	// Set only for a registry-resolved (Tier-3) ref; empty for a Tier-1 /
	// Tier-2 / external ref, none of which carry a registry signature.
	ManifestDigest  string
	ResolutionPath  string
	ReferencedFrom  []string
	// KindHint is the catalog kind known from where the URI was collected —
	// "recipe" or "feature_recipe" (a Manifest mapping value's position) or
	// "aggregate" (a sub-aggregate type reference). Empty when the collection
	// site carried no kind. Source-descent prefers this hint over re-deriving
	// the kind lexically from the URI, so a short-form Recipe reference (one
	// without a "/recipe/" path segment) is still recognised for fetching, and
	// DescendSubAggregates uses the "aggregate" hint to select the entities to
	// load and re-walk. Not a lockfile field; the lockfile writer copies only
	// the wire-shape fields above.
	KindHint string
}

// ResolvedLibrary captures a SchemaLibrary or MappingLibrary
// resolution result with its consumer chain. Mirrors the lockfile
// package's SchemaLibrary / MappingLibrary wire shapes so the
// lockfile writer can copy field-for-field.
type ResolvedLibrary struct {
	URI             string
	ResolvedVersion string
	Digest          string
	ResolutionPath  string
	Consumers       []LibraryConsumer
}

// LibraryConsumer records a single catalog-entity → library
// reference edge. Mirrors the lockfile package's LibraryConsumer
// wire shape so the lockfile writer can copy field-for-field.
type LibraryConsumer struct {
	ConsumerURI string
	Fragments   []string
}

// Lookup is the abstract surface the walker uses to resolve URIs
// to their (version, digest, tier) tuple. The walker never makes
// network calls itself; every URI resolution is a method
// invocation on a concrete Lookup implementation.
//
// Implementations live outside this package: tests use in-memory
// mocks; production wiring composes a Lookup from a filesystem
// catalog plus an OCI client.
//
// Design note: this interface uses methods rather than the
// callback-struct or bare-function patterns established elsewhere
// in aggrega-cli (pkg/resolver/CatalogLookup is a struct of two
// callback fields; pkg/validator/ResolveInheritance takes a bare
// function-value parameter). The four-method surface here would be
// awkward as a callback struct: each test mock would need a
// 4-field literal with at least 3 non-nil function values, and a
// partial construction would panic at call site rather than fail
// at construction. Method-set interface satisfaction gives
// compile-time guarantees that every implementer provides all
// five resolution surfaces.
type Lookup interface {
	// ResolveCatalogEntity resolves a core catalog entity (Component / Feature /
	// Recipe / FeatureRecipe). kindHint is the KindHint recorded at
	// the collection site (empty when none was known); a registry-backed Tier-3
	// pull uses it to bind the bare identity to its OCI location, since a bare
	// identity carries no kind segment.
	ResolveCatalogEntity(uri, kindHint string) (Resolution, error)
	// ResolveAggregate resolves a sub-aggregate reference (a KindHint=="aggregate"
	// entity). It is distinct from ResolveCatalogEntity because a bare-name
	// Aggregate resolves at a dedicated registry tier (the AggregatePuller),
	// mirroring ResolveSchemaLibrary — the generic catalog-entity tier-3 needs a
	// tag and cannot fetch a bare name.
	ResolveAggregate(uri string) (Resolution, error)
	ResolveSchemaLibrary(uri string) (LibraryResolution, error)
	ResolveMappingLibrary(uri string) (LibraryResolution, error)
	ResolvePlugin(uri string) (Resolution, error)
}

// Resolution is the common output shape for catalog-entity and
// Wasm-plugin lookups: the resolved version + content digest +
// resolution-path diagnostic tag. NotFound signals that the URI
// did not resolve through any tier; the walker treats NotFound as
// a fatal closure gap unless the URI was optional.
//
// No Body field is exposed: the walker never needs the resolved
// bytes at this package's scope (only the version/digest/tier
// tuple). Future consumers that need body content add a separate
// accessor on Lookup rather than growing this struct.
type Resolution struct {
	ResolvedVersion string
	Digest          string
	// ManifestDigest is the OCI manifest digest the artifact's signature
	// commits to — the verify subject at pull time. It differs from Digest,
	// the portable content digest used for the cache pin and integrity check.
	// A registry (Tier-3) resolution captures it from the pull's manifest
	// descriptor; lower tiers leave it empty (they carry no registry
	// signature to verify).
	ManifestDigest  string
	ResolutionPath  string
	NotFound        bool
}

// LibraryResolution extends Resolution with the library's entry
// index so the walker can identify which <entry-name> fragments a
// consumer references without re-parsing the body. The Entries
// set is populated by the Lookup implementation at resolution time.
type LibraryResolution struct {
	Resolution
	Entries map[string]struct{}
}

// ErrMVSConflict surfaces when two library consumers require
// incompatible major versions of the same library URI. The
// wrapped error message names the library URI, the conflicting
// versions, and the consumer chain.
var ErrMVSConflict = errors.New("resolve: minimum version selection conflict — incompatible library version requirements")

// ErrUnresolvableURI wraps a Lookup NotFound return that the
// walker cannot proceed past (a missing catalog entity inside the
// Release's closure halts the walk). The wrapped error names the
// offending URI and the referenced_from path that led to it.
var ErrUnresolvableURI = errors.New("resolve: URI not resolvable via Lookup")

// ErrInvalidLibraryRef surfaces when a $ref: or $import: value
// does not match the canonical library-reference URI shape per
// the file-import vocabulary spec. The wrapped error names the
// offending input string.
var ErrInvalidLibraryRef = errors.New("resolve: invalid library reference URI")

// inputs bundles the four loaded structures ComputeClosure walks.
// Keeping them in one struct keeps the public signature small and
// makes future additions (e.g., a Catalog-side index of every
// entity reachable across the workspace) additive.
type inputs struct {
	release      *types.Release
	manifest     *types.Manifest
	aggregate    *types.Aggregate
	workspaceCfg workspaceconfig.Config
	lookup       Lookup
}
