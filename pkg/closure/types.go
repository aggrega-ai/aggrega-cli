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
	"context"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Closure captures the full result of ComputeClosure. Fields map
// one-to-one to lockfile sections; downstream code consumes the
// Closure field-by-field when materialising the lockfile.
//
// Fields are grouped by what they hold — the entity refs, then the
// library axes, then the source artifacts and the plugins — rather
// than alphabetically. The library axes' order is the declared axis
// order and is load-bearing: it fixes the order the per-axis passes
// emit diagnostics in, so reordering them reorders operator-visible
// output.
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
//
// ReleaseDocuments is populated by neither pass. ComputeClosure is handed the
// Release already composed, with no file behind it, so it cannot say which files
// the composition read; the caller that loaded and composed the Release fills it
// from the same load.
type Closure struct {
	Entities          []ResolvedRef
	MappingLibraries  []ResolvedLibrary
	SchemaLibraries   []ResolvedLibrary
	WorkloadLibraries []ResolvedLibrary
	ArtifactLibraries []ResolvedLibrary
	SourceArtifacts   []SourceArtifact
	Plugins           []ResolvedRef
	ReleaseDocuments  []ReleaseDocument
}

// ReleaseDocument records one Release file a composition of the Release read, by
// content: the Release the resolution was asked for, or one it extends. It mirrors
// lockfile.ReleaseDocument field-for-field.
//
// Path is relative to the directory holding the Release the resolution was asked
// for, written with forward slashes, so two machines with different directory
// layouts record the same value. Digest is the content digest of the file's bytes
// as they stand, with nothing normalised away: a Release is recorded by the same
// rule as every other document a lockfile pins by content.
type ReleaseDocument struct {
	Path   string
	Digest string
}

// SourceArtifact mirrors lockfile.SourceArtifact field-for-field;
// the lockfile writer copies the closure value into the lockfile
// type at orchestrator integration time. Identity is
// {Kind, URL, Chart, ModuleSubdir, PinnedVersion} — URL is the authored
// identity URL, Chart names the chart within a repo root, and ModuleSubdir
// names the directory within a git-sourced repository; multiple Recipes
// pinning the same artifact dedupe to one SourceArtifact with multiple
// ReferencedBy records. ResolvedURL is provenance only (the concrete
// .tgz a repo-root helm source resolved to), never part of identity.
type SourceArtifact struct {
	Kind           string
	URL            string
	Chart          string
	ModuleSubdir   string
	PinnedVersion  string
	Digest         string
	ManifestDigest string
	Mediatype      string
	ResolvedURL    string
	ReferencedBy   []SourceArtifactReference
}

// SourceArtifactReference is the closure-side back-pointer mirror
// of lockfile.SourceArtifactReference. Entity + URI identify the
// catalog entity that resolved to the artifact; Field names the
// source-side path inside the entity's spec (always "spec.source"
// in the current implementation). RequestedRef preserves the Recipe's
// authored spec.source.ref, and is written whenever the Recipe authored one
// (git-repository tag/branch → commit SHA); empty when it authored none.
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
	// IdentityAuthored reports whether the referencing entity's author wrote out
	// in full every field that fixes which artifact its source names. It is the
	// descent's own reading of the source, taken once before anything was
	// acquired, carried here so a later pass acts on that decision rather than
	// reading the source a second time.
	//
	// It is what decides whether the pin belongs beside the entity. An identity
	// written out in full names one artifact whatever uses the entity, so its pin
	// is a fact about the entity. An identity filled in from a template names an
	// artifact only once an instance's own values do the filling, so it is known
	// per Release rather than beside the entity, and the lockfile of each Release
	// is where each identity it resolves to is pinned.
	//
	// It is not recorded in a lockfile: the lockfile pins every identity either
	// way, so it has nothing to decide with it.
	IdentityAuthored bool
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
	// descent derives the .wasm artifact reference from its identity to pin the
	// artifact alongside the entity. Not-found surfaces as a wrapped error so the
	// descent can distinguish a missing-entity failure from other parse errors.
	LoadPlugin(uri string) (*types.Plugin, error)
}

// RecipeLoaderSource is a Lookup that can also hand out the RecipeLoader the
// recipe axis needs. Lookup resolves a URI to its version/digest/tier tuple; the
// recipe passes additionally need the parsed Recipe / FeatureRecipe / Plugin
// bodies behind those URIs. An implementation backed by a catalog can serve both
// and says so by exposing this method, which keeps Lookup itself a resolver for
// every other implementer.
type RecipeLoaderSource interface {
	ClosureRecipeLoader() RecipeLoader
}

// ErrNoRecipeLoader halts a caller whose catalog lookup cannot load resolved
// Recipe / FeatureRecipe / Plugin bodies, so the recipe axis cannot be computed
// over it.
func ErrNoRecipeLoader() error { return errNoRecipeLoader }

var errNoRecipeLoader = cerrors.Register("catalog-cannot-load-recipe-bodies", cerrors.Meta{
	ShortCode: "E0109",
	Message:   "resolve: catalog lookup cannot load resolved recipe and plugin bodies",
	RulePlace: cerrors.NotARule("A caller supplied a catalog lookup that cannot load resolved recipe and plugin bodies, so " +
		"the recipe axis cannot be computed over it at all. It reports how this program was wired " +
		"rather than a rule over anything an author wrote."),
})

// RequireRecipeLoader adapts a Lookup to the RecipeLoader the recipe axis needs,
// refusing with ErrNoRecipeLoader when the lookup cannot serve those bodies.
//
// The failure it guards is silent by construction: every recipe-axis pass reads a
// nil loader as "no loader was wired" and returns immediately, so a caller that
// discarded a failed assertion would hand one of them nil and get back a closure
// short an entire axis with nothing reported. Obtaining the loader here instead
// leaves that site unable to discard the check.
//
// A lookup that exposes the method but yields no loader is refused on the same
// footing as one that does not expose it at all: both leave a pass with nothing
// to read bodies through, and the pass cannot tell either apart from a caller
// that deliberately wired no registry tier.
//
// It fits a caller whose Lookup is also the loader's source. A caller that draws
// the two from different views on purpose — because they carry different tiers —
// has no single lookup to ask and supplies its loader directly.
func RequireRecipeLoader(lookup Lookup) (RecipeLoader, error) {
	source, ok := lookup.(RecipeLoaderSource)
	if !ok {
		return nil, ErrNoRecipeLoader()
	}
	loader := source.ClosureRecipeLoader()
	if loader == nil {
		return nil, ErrNoRecipeLoader()
	}
	return loader, nil
}

// SourceResolver is the walker's own resolution contract, declared here
// so the closure walker stays decoupled from the per-kind adapter pull
// machinery; tests inject a static-result implementation. Production
// wiring lives at the orchestrator, which composes a SourceResolver
// from the shipped sourceresolver package via a small type-conversion
// shim.
//
// It takes no context, and the resolver it is wired to does. Ending a
// run still ends a fetch in progress: the shim holds the run's context
// and hands it to each resolution, so the descent is interruptible
// without this contract having to carry a context of its own — which it
// could not do without every injected implementation accepting one.
type SourceResolver interface {
	Resolve(spec SourceSpec) (*ResolvedSource, error)
}

// SourceSpec carries what a resolution needs about one source. It
// mirrors sourceresolver.SourceSpec apart from that struct's transport
// client, which the wiring shim supplies from the workspace's egress
// configuration rather than the walker: the walker names the source,
// and how it is reached is not its concern. The shim performs a
// mechanical field-copy across the package boundary.
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
	Chart          string
	ModuleSubdir   string
	PinnedVersion  string
	Digest         string
	ManifestDigest string
	Mediatype      string
	ResolvedURL    string
	CachePath      string
	OriginalSource any
}

// ErrSourceTemplateAtLockTime wraps every lock-time source-template
// evaluation failure: .provides references (forbidden — provides
// evaluation has not run at lock time, so no component has produced a
// value to reference yet), ambiguous kind
// classification (scalar source URL without recognised scheme + no
// explicit kind), missing .infra/.locals keys, template parse
// errors, and any other condition that surfaces during
// DescendRecipeSources's lock-time evaluation pass. The build-time
// counterpart (lockfile-vs-actual drift) wraps ErrFetchIntegrity at
// the orchestrator, not this sentinel.
func ErrSourceTemplateAtLockTime() error { return errSourceTemplateAtLockTime }

var errSourceTemplateAtLockTime = cerrors.Register("source-template-at-lock-time", cerrors.Meta{
	ShortCode: "E0110",
	Message:   "resolve: source-template evaluation failure at lock time",
	RulePlace: cerrors.NotARule("A source template could not be evaluated at lock time, and this is the umbrella every such " +
		"failure is wrapped in. It reports that evaluation became impossible; the failures that are " +
		"rules about what a template says carry their own identities and wrap this one."),
})

// ErrSourceTemplateParse marks a source-template that fails to PARSE (malformed
// template text). A parse error is a structural authoring bug at any phase, so
// lock-time local resolution keeps it loud rather than tolerating it. Errors of
// this class also wrap ErrSourceTemplateAtLockTime.
func ErrSourceTemplateParse() error { return errSourceTemplateParse }

var errSourceTemplateParse = cerrors.Register("source-template-not-parsable", cerrors.Meta{
	ShortCode: "E0111",
	Message:   "resolve: source-template parse error",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrSourceTemplateProvidesRef marks a source-template that references
// `.provides`. Locals feed `.provides`, never the reverse — a template (or a
// local) reading `.provides` is an invalid reverse reference at any phase, not a
// lock-time-only absence, so the local resolution a source coordinate reads keeps
// it loud both where a lockfile is acquired and where a build resolves a structured
// remote source against one. Errors of this class also wrap
// ErrSourceTemplateAtLockTime.
func ErrSourceTemplateProvidesRef() error { return errSourceTemplateProvidesRef }

var errSourceTemplateProvidesRef = cerrors.Register("source-template-provides-ref", cerrors.Meta{
	ShortCode: "E0112",
	Message:   "resolve: source-template references .provides (invalid reverse reference)",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

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
	ManifestDigest string
	ResolutionPath string
	ReferencedFrom []string
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

// ResolvedLibrary captures a SchemaLibrary, MappingLibrary, WorkloadLibrary, or
// ArtifactLibrary resolution result with its consumer chain. Mirrors the lockfile
// package's SchemaLibrary / MappingLibrary / WorkloadLibrary / ArtifactLibrary
// wire shapes so the lockfile writer can copy field-for-field.
type ResolvedLibrary struct {
	URI             string
	ResolvedVersion string
	Digest          string
	// ManifestDigest is the OCI manifest digest the library artifact's signature
	// commits to — the verify subject at pull time. It differs from Digest, the
	// portable content digest used for the cache pin and integrity check. Set only
	// for a registry-resolved (Tier-3) library; empty for a Tier-1 / Tier-2 / local
	// library, none of which carry a registry signature.
	ManifestDigest string
	ResolutionPath string
	Consumers      []LibraryConsumer
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
// callback fields; pkg/validator/ResolveFeatureSchemaChain takes a
// bare function-value parameter). The four-method surface here would be
// awkward as a callback struct: each test mock would need a
// 4-field literal with at least 3 non-nil function values, and a
// partial construction would panic at call site rather than fail
// at construction. Method-set interface satisfaction gives
// compile-time guarantees that every implementer provides all
// four resolution surfaces.
//
// Every method takes the caller's context as its first argument, and the reason is
// that the local tiers are not the whole story: a miss at the in-memory catalog and
// at the content cache falls through to a registry fetch, which is a step that leaves
// the process. A caller that gives up part-way through a walk expects those fetches
// to stop rather than run to completion against a reply nobody will read, and an
// implementation that accepted no context could not offer that however the caller
// asked. The context reaches the puller call itself, so a cancellation lands on the
// transport rather than on the next loop iteration.
type Lookup interface {
	// ResolveCatalogEntity resolves a core catalog entity (Component / Feature /
	// Recipe / FeatureRecipe). kindHint is the KindHint recorded at
	// the collection site (empty when none was known); a registry-backed Tier-3
	// pull uses it to bind the bare identity to its OCI location, since a bare
	// identity carries no kind segment.
	ResolveCatalogEntity(ctx context.Context, uri, kindHint string) (Resolution, error)
	// ResolveAggregate resolves a sub-aggregate reference (a KindHint=="aggregate"
	// entity). It is distinct from ResolveCatalogEntity because a bare-name
	// Aggregate resolves at a dedicated registry tier (the AggregatePuller),
	// mirroring ResolveSchemaLibrary — the generic catalog-entity tier-3 needs a
	// tag and cannot fetch a bare name.
	ResolveAggregate(ctx context.Context, uri string) (Resolution, error)
	// ResolveLibrary resolves a cross-document library reference on the given axis.
	// One method covers every library axis rather than one method per axis: the
	// axis is data, so an implementation dispatches on it and an axis added to the
	// declared set needs no new method here and no new method on any implementer.
	ResolveLibrary(ctx context.Context, axis LibraryAxis, uri string) (LibraryResolution, error)
	// ResolvePlugin resolves a Plugin entity's .wasm artifact reference — the
	// entity's bare identity (<socket>/<name>). The .wasm rides inline as a layer
	// of the single plugin manifest published under the entity's version tag, so
	// version selects the manifest to read the .wasm from. A core plugin embedded
	// in the binary resolves offline by its bare identity through the embed tier.
	ResolvePlugin(ctx context.Context, uri, version string) (Resolution, error)
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
	ManifestDigest string
	ResolutionPath string
	NotFound       bool
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
func ErrMVSConflict() error { return errMVSConflict }

var errMVSConflict = cerrors.Register("version-selection-conflict", cerrors.Meta{
	ShortCode: "E0107",
	Message:   "resolve: minimal version selection conflict — incompatible library version requirements",
	RulePlace: cerrors.RuleNoStageReaches("The closure walk evaluates the rule, when two library consumers it has reached require " +
		"incompatible major versions of one library. The conflict is a fact about the requirements the " +
		"walk has accumulated rather than about any one document, and the walk pins a Release's reachable " +
		"entities rather than compiling them, so no declared stage holds it."),
})

// ErrUnresolvableURI reports a URI in the Release's closure that names
// no entity the catalog holds, which the walker cannot proceed past. The
// wrapped error names the offending URI and the referenced_from path that
// led to it.
//
// The claim is now true in every case it makes: a Manifest mapping value
// whose shape a recipe reference cannot express is judged before the walk
// runs, so a mis-shaped reference no longer arrives here to be reported as
// an entity the catalog does not hold. A reference whose version pin is
// malformed is reported as that malformation instead, for the same reason.
func ErrUnresolvableURI() error { return errUnresolvableURI }

var errUnresolvableURI = cerrors.Register("unresolvable-uri", cerrors.Meta{
	ShortCode: "E0114",
	Message:   "resolve: no entity in the catalog matches this URI",
	RulePlace: cerrors.RuleNoStageReaches("The closure walk evaluates the rule, when a reference it reached names no entity the catalog " +
		"holds. The walk pins what a Release reaches rather than compiling it, so no declared stage of a " +
		"compilation holds either the reference or the catalog it is resolved against."),
})

// UnresolvedEntityError is a resolution failure the walk raises for ONE
// catalog-entity reference, carrying that reference where a caller can read it
// rather than only inside the rendered message.
//
// The reference is the fact a reporter needs to explain the failure. A catalog
// file a tolerant load refused never enters the catalog at all, so what fails is
// the attempt to resolve a reference to it — and a reporter can only put the
// refusal ahead of that failure if it can tell which refused file the reference
// names. A reference formatted into a message is unrecoverable without reading
// the prose back, so it is carried in a field instead.
//
// It DECORATES the failure rather than replacing it. Error renders exactly the
// message the walk always rendered, and Unwrap returns the failure itself, so
// matching against the condition the failure raised — the unresolvable-reference
// condition, or whatever a lookup returned — is unaffected by the reference
// being carried.
type UnresolvedEntityError struct {
	// URI is the reference the walk could not resolve, exactly as the closure
	// recorded it.
	URI string
	// KindHint is the collection-site kind hint the reference was recorded
	// with, empty when the site recorded none.
	KindHint string

	err error
}

// Error renders the underlying failure's message unchanged.
func (e *UnresolvedEntityError) Error() string { return e.err.Error() }

// Unwrap returns the failure this carries the reference for, so errors.Is and
// errors.As reach the condition it raised.
func (e *UnresolvedEntityError) Unwrap() error { return e.err }

// unresolvedEntity attaches a reference and its kind hint to a resolution
// failure. Returns err unchanged when it is nil, so a call site can wrap
// unconditionally.
func unresolvedEntity(uri, kindHint string, err error) error {
	if err == nil {
		return nil
	}
	return &UnresolvedEntityError{URI: uri, KindHint: kindHint, err: err}
}

// ErrInvalidLibraryRef surfaces when a $ref: or $import: value
// does not match the canonical library-reference URI shape per
// the file-import vocabulary spec. The wrapped error names the
// offending input string.
func ErrInvalidLibraryRef() error { return errInvalidLibraryRef }

var errInvalidLibraryRef = cerrors.Register("invalid-library-ref", cerrors.Meta{
	ShortCode: "E0106",
	Message:   "resolve: invalid library reference URI",
	RulePlace: cerrors.RuleNoStageReaches("The closure walk evaluates the rule, over a $ref or $import value met while pinning a " +
		"Release's reachable entities. The walk reads the reference to decide what to fetch rather than " +
		"to compile it, so no declared stage of a compilation holds the value the shape is judged on."),
})

// inputs bundles the four loaded structures ComputeClosure walks.
// Keeping them in one struct keeps the public signature small and
// makes future additions (e.g., a Catalog-side index of every
// entity reachable across the workspace) additive.
type inputs struct {
	release      *types.Release
	manifest     *types.Manifest
	aggregate    types.NormalizedAggregate
	workspaceCfg workspaceconfig.Config
	lookup       Lookup
}
