// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ErrQualifiedPluginReference is returned when a Plugin reference carries a
// leading registry-host segment (or an artifact path) instead of the plugin's
// coordinate identity. A plugin entity is addressed by its coordinate
// (<pluginType>/<name>:<version>); the pull binds that identity to its physical
// location by prefixing the configured registry and the plugin path segment
// itself. A reference that already includes a host or path segment would be
// double-prefixed into an unfetchable location, so it is refused loudly here
// rather than surfacing later as a cryptic tag error.
func ErrQualifiedPluginReference() error { return errQualifiedPluginReference }

var errQualifiedPluginReference = cerrors.Register("plugin-reference-must-not-be-registry-qualified", cerrors.Meta{
	ShortCode: "E0102",
	Message:   "wasm plugin reference must be the plugin coordinate identity (<pluginType>/<name>:<version>), not a registry-qualified or artifact path",
	RulePlace: cerrors.RuleNoStageReaches("A pull binds this reference to its physical registry location, and the rule runs at that " +
		"binding: an identity already leading with a registry host would be prefixed a second time. The " +
		"pull fetches an entity before anything reads it, so no declared stage of a compilation holds the " +
		"reference at the moment it is bound."),
})

// ErrUnscopedPluginReference is returned when a Plugin reference carries no
// pluginType scope — either no "/" at all or an empty leading segment
// ("/name"). Plugin identity is the coordinate <pluginType>/<name>, and the OCI
// location encodes the pluginType segment because two sockets may hold a plugin
// of the same name; an empty scope would bind to a location with an empty scope
// segment (a double slash) that cannot resolve. The reference is refused loudly
// here rather than surfacing later as a cryptic fetch failure.
func ErrUnscopedPluginReference() error { return errUnscopedPluginReference }

var errUnscopedPluginReference = cerrors.Register("plugin-reference-must-be-scoped", cerrors.Meta{
	ShortCode: "E0104",
	Message:   "wasm plugin reference must carry a pluginType scope (<pluginType>/<name>); an empty scope cannot bind to an OCI location",
	RulePlace: cerrors.RuleNoStageReaches("A pull binds this reference to its physical registry location, and the rule runs at that " +
		"binding: an identity carrying no pluginType scope forms a location no registry can serve. The " +
		"pull fetches an entity before anything reads it, so no declared stage of a compilation holds the " +
		"reference at the moment it is bound."),
})

// ErrUnscopedRecipeReference is returned when a Recipe / FeatureRecipe reference
// carries no provider scope — either no "/" at all or an empty leading provider
// ("/name"). Recipe identity is provider/name, and the OCI location encodes the
// provider segment because two providers may ship a recipe of the same name; an
// empty provider would bind to a location with an empty provider segment (a
// double slash) that cannot resolve. The reference is refused loudly here rather
// than surfacing later as a cryptic fetch failure.
func ErrUnscopedRecipeReference() error { return errUnscopedRecipeReference }

var errUnscopedRecipeReference = cerrors.Register("recipe-reference-must-be-scoped", cerrors.Meta{
	ShortCode: "E0105",
	Message:   "recipe reference must be provider-scoped (<provider>/<name>); an empty provider cannot bind to an OCI location",
	RulePlace: cerrors.RuleNoStageReaches("A pull binds this reference to its physical registry location, and the rule runs at that " +
		"binding: a recipe identity carrying no provider scope forms a location no registry can serve. " +
		"The pull fetches an entity before anything reads it, so no declared stage of a compilation holds " +
		"the reference at the moment it is bound."),
})

// ErrAllVersionsRetracted is returned when every published version of an entity
// that selection could otherwise have chosen has been withdrawn, so the
// discovery step is left with no eligible candidate. It is a loud, recoverable
// halt.
//
// It is deliberately NOT the not-found sentinel. A repository whose every
// version is withdrawn still exists and still holds those versions; reporting it
// as a missing entity would name the wrong problem, send the reader looking for a
// typo, and read as a quiet fallback rather than the refusal it is.
func ErrAllVersionsRetracted() error { return errAllVersionsRetracted }

var errAllVersionsRetracted = cerrors.Register("all-versions-retracted", cerrors.Meta{
	ShortCode: "E0089",
	Message:   "every published version this reference could select has been withdrawn",
	RulePlace: cerrors.RuleNoStageReaches("A pull's registry-side version discovery evaluates the rule, over the published tag list " +
		"minus whatever the workspace's withdrawal screen removed from it. Discovery reaches a registry " +
		"and settles which version is fetched before anything reads the entity, so no declared stage of a " +
		"compilation holds the tag list it selects over."),
})

// VersionExclusion is one published version a RetractionScreen removed from
// selection, paired with the reason recorded against it.
type VersionExclusion struct {
	// Version is the excluded published version.
	Version string
	// Reason is the withdrawal reason recorded against that version. It may be
	// empty: a record is not required to carry prose.
	Reason string
}

// RetractionScreen removes withdrawn published versions from the registry-side
// discovery step and reports what it removed.
//
// It is injected rather than implemented in this package, for two reasons.
// Deciding that a version is withdrawn means reading a signed marker and judging
// its signer against the workspace's trust policy, and neither the marker format
// nor the trust policy belongs here. And injection is what keeps
// selectPullVersion and oci.SelectLatestVersion pure over a tag list: a screen
// that reached a registry from inside selection would make every one of
// selection's call sites inherit a network dependency.
//
// An implementation answers offline from a recorded, signature-verified fact and
// never fetches. That is what lets the screen run at no extra round trips, and it
// fixes the screen's one honest limit: the fact is a snapshot, so it says what the
// registry reported when the snapshot was taken and never what it reports now.
//
// A nil screen is the unscreened default — selection considers every published
// version, which is what a workspace with no local snapshot must do.
type RetractionScreen interface {
	// ScreenVersions partitions an artifact's published tags into the versions
	// eligible for selection and the versions withdrawn from it. A tag the screen
	// holds no record for is eligible.
	ScreenVersions(reference string, tags []string) (eligible []string, excluded []VersionExclusion)

	// ReportSelection announces the outcome of a screened selection: the version
	// taken, and every version withdrawn from consideration with its reason. The
	// caller invokes it only when at least one version was excluded, so an
	// ordinary selection stays silent.
	ReportSelection(reference, selected string, excluded []VersionExclusion)

	// ReportPinned announces that a reference named an exact version the screen
	// holds a withdrawal record for. Naming a version IS the override, so such a
	// reference still resolves; this is what stops it resolving quietly. It is
	// silent for a version with no record.
	ReportPinned(reference, version string)
}

// NewDefaultOCIPuller returns the production OCIPuller backed by
// pkg/oci.NewRemoteRepository per Pull-call. Returns nil when the
// workspace is configured offline so closure-walker and lazy-lookup
// Tier-3 paths surface as NotFound without attempting any network
// access.
//
// The exported function lives next to the OCIPuller interface
// definition so both CLI verbs (pull, build) and long-running
// consumers (the compiler-as-service daemon) construct pullers via
// the same canonical helper. Local copies would drift; one
// implementation prevents that.
//
// The returned puller carries no retraction screen, so its discovery step
// considers every published version. A caller that can supply a verified
// withdrawal record builds its puller through NewOCIPullerWithRetractionScreen
// instead.
func NewDefaultOCIPuller(wsCfg workspaceconfig.Config) OCIPuller {
	return NewOCIPullerWithRetractionScreen(wsCfg, nil)
}

// NewOCIPullerWithRetractionScreen returns the production OCIPuller with a
// retraction screen attached, so the discovery step skips versions the screen
// reports withdrawn and announces every skip. It shares NewDefaultOCIPuller's
// construction exactly, including returning nil when the workspace is offline —
// an offline workspace reaches no registry, so it never selects a version at all.
//
// A nil screen yields the unscreened puller, so this is the single construction
// path and the screened and unscreened forms cannot drift.
func NewOCIPullerWithRetractionScreen(wsCfg workspaceconfig.Config, screen RetractionScreen) OCIPuller {
	httpClient, err := egress.HTTPClient(wsCfg)
	if err != nil {
		// The network-egress gate refused (the workspace is offline): return no
		// puller so closure-walker and lazy-lookup Tier-3 paths surface NotFound
		// without attempting any network access. The offline decision lives at the
		// gate; this call site only reacts to it.
		return nil
	}
	return DefaultOCIPuller{registry: wsCfg.DefaultRegistryLocation().Prefix(), httpClient: httpClient, retraction: screen}
}

// DefaultOCIPuller is the production OCIPuller implementation. Pull constructs a
// fresh oci.RemoteRepository per call so per-call authentication + retry behavior
// matches pkg/oci's documented surface exactly. registry is the configured
// default registry's location prefix — its host, followed by the repository
// path it carries, if any — read from the configuration's one reading of it, and
// every host-less identity this puller binds is bound under it, as publication
// publishes under it.
//
// retraction is the optional withdrawal screen consulted by the discovery step of
// every pull entrypoint on this puller. It is nil on an unscreened puller — see
// selectScreenedPullVersion for the scope statement and what it leaves uncovered.
type DefaultOCIPuller struct {
	registry   string
	httpClient *http.Client
	retraction RetractionScreen
}

func (p DefaultOCIPuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	repo, err := oci.NewRemoteRepository(reference, p.httpClient)
	if err != nil {
		return nil, err
	}
	return oci.MemoizedPull(ctx, reference, repo, tag)
}

// PullLibrary pulls a library by its host-less identity (bare name + kind),
// binding it to a physical location through the configured default registry — the
// pull-side mirror of push's name→location (`<registry>/<kind-segment>/<name>`).
// The version is discovered from the registry's tags (discoverLibraryVersion):
// with an empty floorVersion (a pure bare-name reference at first pull) the
// highest semver an attached withdrawal screen leaves eligible; with a floor, the
// highest such release in the floor's major at or above it, the selection a local
// catalog makes of the same floor (selectLibraryVersion). The selected version is
// returned so the lockfile pins it. The "latest" resolution happens HERE, at the
// network step, and is pinned — the build never lists tags or fetches.
func (p DefaultOCIPuller) PullLibrary(ctx context.Context, name, kind, floorVersion string) (*oci.PullResult, string, error) {
	reference := libraryReference(p.registry, kind, name)
	repo, err := oci.NewRemoteRepository(reference, p.httpClient)
	if err != nil {
		return nil, "", err
	}
	version, err := p.discoverLibraryVersion(reference, floorVersion, func() ([]string, error) {
		return oci.MemoizedListTags(ctx, reference, repo)
	})
	if err != nil {
		return nil, "", err
	}
	result, perr := oci.MemoizedPull(ctx, reference, repo, version)
	if perr != nil {
		return nil, "", perr
	}
	return result, version, nil
}

// discoverLibraryVersion is the discovery step of a library pull. Without a floor
// it is discoverVersion. With one it lists reference's tags through listTags and
// selects the highest version the withdrawal screen leaves eligible that the
// floor admits — the same version selectLibraryVersion picks among the same
// versions held locally, so a floor records one version whichever tier serves it.
// Only where no eligible version admits the floor is a listed tag the floor names
// outright selected though the screen withholds it: the floor then names the one
// version that can satisfy it, and the screen announces it. Where an eligible
// version admits the floor, a withheld tag the floor names is passed over like any
// other. A listing the registry reports as not found, and a floor no listed
// version admits, report ErrFetchNotFound; any other listing failure is returned
// as it is.
func (p DefaultOCIPuller) discoverLibraryVersion(reference, floorVersion string, listTags func() ([]string, error)) (string, error) {
	if floorVersion == "" {
		return p.discoverVersion(reference, floorVersion, listTags)
	}
	tags, err := listTags()
	if err != nil {
		return "", err
	}
	eligible := tags
	if p.retraction != nil {
		eligible, _ = p.retraction.ScreenVersions(reference, tags)
	}
	const name = "library"
	floorRef := name + ":" + floorVersion
	meta := func(tag string) (string, string) { return name, tag }
	index := make(map[string]string, len(eligible))
	for _, tag := range eligible {
		index[tag] = tag
	}
	switch key, ok, err := selectLibraryVersion(index, floorRef, meta); {
	case ok:
		return key, nil
	case err != nil && !errors.Is(err, ErrUnsatisfiableEntityVersion()):
		return "", err
	}
	if slices.Contains(tags, floorVersion) {
		p.reportPinnedVersion(reference, floorVersion)
		return floorVersion, nil
	}
	return "", fmt.Errorf("%w: %s: no published version admitted by the floor %s", oci.ErrFetchNotFound(), reference, floorVersion)
}

// PullAggregate pulls a published Aggregate by its bare identity, binding it to
// a physical location through the configured default registry — the pull-side
// mirror of push's name→location (`<registry>/aggregate/<name>`). Discovery
// mirrors PullLibrary: an empty floorVersion lists tags and selects the highest
// semver an attached withdrawal screen leaves eligible; a non-empty floor is the
// tag, announced when the screen holds a withdrawal record for it. It returns the
// OCI tag it fetched under (a location/discovery coordinate); the IDENTITY pinned
// in the lockfile is the fetched body's metadata.version, which
// PullAndCacheAggregate reads and pins — so the tag is discarded and the pin is
// identity-stable even when a publisher tags `latest`/`stable` over the versioned
// identity.
func (p DefaultOCIPuller) PullAggregate(ctx context.Context, name, floorVersion string) (*oci.PullResult, string, error) {
	reference := RegistryReference(p.registry, "Aggregate", "", name)
	repo, err := oci.NewRemoteRepository(reference, p.httpClient)
	if err != nil {
		return nil, "", err
	}
	tag, err := p.discoverVersion(reference, floorVersion, func() ([]string, error) {
		return oci.MemoizedListTags(ctx, reference, repo)
	})
	if err != nil {
		return nil, "", err
	}
	result, perr := oci.MemoizedPull(ctx, reference, repo, tag)
	if perr != nil {
		return nil, "", perr
	}
	return result, tag, nil
}

// PullEntity pulls a core catalog entity (Component / Feature / Recipe /
// FeatureRecipe) by its bare identity, binding it to a physical
// location through the configured default registry — the pull-side mirror of
// push's name→location. The bare identity carries no registry or kind segment,
// so a provider-scoped recipe identity (provider/name) would otherwise be
// misparsed with its provider taken as the registry host; entityReference binds
// it to `<registry>/<OCILocation(kind, …)>` instead. When floorVersion is empty
// (a bare type reference, e.g. an Aggregate component `type` with no version) it
// lists the registry's tags and selects the highest semver, mirroring
// PullLibrary; a non-empty floor is used directly as the tag. The selected
// version is returned so the lockfile pins it.
func (p DefaultOCIPuller) PullEntity(ctx context.Context, identity, kind, floorVersion string) (*oci.PullResult, string, error) {
	reference, berr := entityReference(p.registry, kind, identity)
	if berr != nil {
		return nil, "", berr
	}
	repo, err := oci.NewRemoteRepository(reference, p.httpClient)
	if err != nil {
		return nil, "", err
	}
	version, err := p.discoverVersion(reference, floorVersion, func() ([]string, error) {
		return oci.MemoizedListTags(ctx, reference, repo)
	})
	if err != nil {
		return nil, "", err
	}
	// A Plugin publishes as ONE OCI manifest whose CONFIG blob is the entity YAML
	// and whose single layer is the inline .wasm. A generic content-layer pull
	// would return the .wasm as the entity bytes; read the config blob instead so
	// the entity content the cache stores and the closure pins is the entity YAML.
	// result.Digest stays the manifest digest either way (the ManifestDigest pin);
	// only the content descriptor read differs. Every other kind reads the content
	// layer through Pull unchanged.
	if kind == "Plugin" {
		result, perr := oci.MemoizedFetchPluginEntity(ctx, reference, repo, version)
		if perr != nil {
			return nil, "", perr
		}
		return result, version, nil
	}
	result, perr := oci.MemoizedPull(ctx, reference, repo, version)
	if perr != nil {
		return nil, "", perr
	}
	return result, version, nil
}

// ListEntityVersions lists the versions the default registry publishes a core
// catalog entity at, satisfying EntityVersionLister: the tags of the location
// PullEntity binds identity and kind to, less every version the attached
// withdrawal screen withholds from a selection, so a version a reference naming no
// version would never be resolved to is never a candidate.
func (p DefaultOCIPuller) ListEntityVersions(ctx context.Context, identity, kind string) ([]string, error) {
	reference, err := entityReference(p.registry, kind, identity)
	if err != nil {
		return nil, err
	}
	repo, err := oci.NewRemoteRepository(reference, p.httpClient)
	if err != nil {
		return nil, err
	}
	tags, err := oci.MemoizedListTags(ctx, reference, repo)
	if err != nil {
		return nil, err
	}
	if p.retraction == nil {
		return tags, nil
	}
	eligible, _ := p.retraction.ScreenVersions(reference, tags)
	return eligible, nil
}

// discoverVersion is the discovery step the three pull entrypoints share: an empty
// floorVersion lists reference's tags through listTags and selects the highest
// version the attached withdrawal screen leaves eligible; a non-empty one is the
// version, and the screen announces it when it holds a withdrawal record for it.
func (p DefaultOCIPuller) discoverVersion(reference, floorVersion string, listTags func() ([]string, error)) (string, error) {
	if floorVersion != "" {
		p.reportPinnedVersion(reference, floorVersion)
		return floorVersion, nil
	}
	tags, err := listTags()
	if err != nil {
		return "", err
	}
	return p.selectScreenedPullVersion(reference, tags)
}

// RegistryPrefix reports the location prefix this puller binds host-less
// identities under: the configured default registry's host, followed by the
// repository path it carries, if any. A Tier-3 consumer binds a plugin
// artifact's location under it, as the entity's own is bound, and reads the host
// out of it — registryhost.Of — to point an authorization-failure remediation at
// the registry a login is stored for, rather than at a bare identity's leading
// path segment or at the path.
func (p DefaultOCIPuller) RegistryPrefix() string {
	return p.registry
}

// BindEntityLocation reports the physical OCI repository reference a bare
// catalog identity resolves from — `<registry>/<OCILocation(kind, provider,
// name)>`, registry being the default registry's location prefix — for a Tier-3
// registry pull. It is the pull-side location binding
// exposed for consumers that hold only the closure's bare identity plus its
// collection-site kind hint and must recover the host+path the entity was
// fetched from (the referrer-discovery subject at signature-verify time).
//
// It is the same binding EntityPuller.PullEntity performs, routed through the
// same entityReference primitive, so verify-location == pull-location ==
// push-location holds by construction rather than by a parallel re-derivation.
// The uri carries the closure identity (optionally version-pinned); the version
// suffix is stripped before binding since the location is version-independent.
//
// kindHint is the closure collection-site kind ("component" / "feature" /
// "recipe" / "feature_recipe" / "plugin" / "aggregate", or the Release's own
// closure.ReleaseAggregateKindHint); it is mapped to the catalog kind the
// location rule expects. Returns ok=false when registry is
// empty, when the hint does not map to a catalog kind, or when the binding is
// refused (a registry-qualified identity) — the same conditions under which the
// puller itself cannot bind a bare identity and falls to the legacy
// located-reference path, so the caller skips rather than fabricating a location.
func BindEntityLocation(registry, kindHint, uri string) (location string, ok bool) {
	if registry == "" {
		return "", false
	}
	kind := EntityKindForHint(kindHint)
	if kind == "" {
		return "", false
	}
	identity, _, _ := resolver.SplitVersionedURI(uri)
	ref, err := entityReference(registry, kind, identity)
	if err != nil {
		return "", false
	}
	return ref, true
}

// EntityKindForHint maps a closure KindHint to the catalog kind the OCI location
// rule expects for a registry-resolved entity, covering both the core-catalog
// tier (whose fetch binds by it, registryFetchKind) and the Aggregate tier, whose
// bare-name pull binds through the same location rule under the "Aggregate" kind.
// It is the location-binding counterpart of the pull-tier dispatch, so an entity
// that resolves from a registry maps to a location for verify exactly as it did
// for the pull. It is also the published kind a reference's cache slot is kept
// under.
//
// The hint is read as the kind it records (closure.EntityKindOf), so both
// Aggregate hints — a sub-aggregate's and the Release's own — name "Aggregate". One
// fetch serves both (PullAndCacheAggregate), so the Aggregate a Release names by
// identity is bound to the location it was fetched from, as a sub-aggregate is.
//
// It is the single home for the KindHint-to-catalog-kind normalization: the
// pull-time verify pass derives an entity's canonical kind through it (to
// consult the org signing policy), so the kind vocabulary is not re-spelled at
// the call site. It reads the one translation of an entity kind into the kind its
// document is written as (closure.DocumentKind), held to the kinds a cache slot
// can hold (closure.KindReadFromCacheSlot): a Manifest, read from beside its
// Release and never published, names none, and neither does a hint naming no
// declared kind.
func EntityKindForHint(hint string) string {
	kind := closure.EntityKindOf(hint)
	if kind == "" || !closure.KindReadFromCacheSlot(kind) {
		return ""
	}
	return closure.DocumentKind(kind)
}

// registryFetchKind returns the catalog kind the entity tier's registry fetch
// binds a reference's location by, for a reference whose cache slot is kept under
// slotKind (EntityKindForHint): the slot kind itself, so the fetch, the admission
// of the fetched body and the slot it is written to name one kind. An Aggregate is
// the exception, and the fetch names no kind for it: a bare-name Aggregate is
// fetched by the Aggregate tier (PullAndCacheAggregate), so one reaching the entity
// tier is fetched as the already-located reference it is. A slot kind that is
// empty names no kind to fetch by either.
func registryFetchKind(slotKind string) string {
	if slotKind == aggregateKind {
		return ""
	}
	return slotKind
}

// EntityKindForCatalogKind is the inverse of EntityKindForHint: the entity kind
// (closure.DeclaredEntityKinds) a document of the catalog kind catalogKind —
// "Recipe", "FeatureRecipe" — is recorded as, and "" for a catalog kind no entity
// kind names. It reads the pairing EntityKindForHint states rather than stating it
// again, so the two directions cannot come to disagree.
func EntityKindForCatalogKind(catalogKind string) string {
	if catalogKind == "" {
		return ""
	}
	for _, kind := range closure.DeclaredEntityKinds() {
		if EntityKindForHint(kind) == catalogKind {
			return kind
		}
	}
	return ""
}

// entityReference binds a bare catalog identity to its physical OCI reference,
// `<registry>/<OCILocation(kind, provider, name)>` — the pull-side mirror of
// push's BuildPushReference, joined through the same RegistryReference so
// push-location == pull-location holds by construction. Recipe / FeatureRecipe identity is
// provider/name; it is split on the first "/" to feed the provider-scoped
// location branch. Other kinds pass the whole identity as the name.
//
// A Plugin identity is the coordinate <pluginType>/<name>: the binder prefixes
// the registry and the plugin path segment itself, so an identity that already
// leads with a registry-host-shaped segment (the part before the first "/"
// carrying a "." or ":") would be double-prefixed into an unfetchable location.
// Such a reference is refused with ErrQualifiedPluginReference. An identity with
// no pluginType scope (no "/", or an empty leading segment) would bind to a
// location with an empty scope segment (a double slash) that cannot resolve, so
// it is refused with ErrUnscopedPluginReference. The Component / Feature binders
// are unchanged.
//
// A Recipe / FeatureRecipe identity must be provider-scoped (provider/name): an
// unscoped identity (no "/") or one with an empty leading provider ("/name")
// would bind to a location with an empty provider segment (a double slash) that
// cannot resolve, so it is refused with ErrUnscopedRecipeReference before the
// location is formed.
func entityReference(registry, kind, identity string) (string, error) {
	if kind == "Plugin" && hasRegistryHostSegment(identity) {
		return "", fmt.Errorf("%w: %q", ErrQualifiedPluginReference(), identity)
	}
	provider, name := "", identity
	switch kind {
	case "Recipe", "FeatureRecipe":
		i := strings.IndexByte(identity, '/')
		if i <= 0 {
			// i < 0: no "/" (unscoped). i == 0: empty leading provider ("/name").
			return "", fmt.Errorf("%w: %q", ErrUnscopedRecipeReference(), identity)
		}
		provider, name = identity[:i], identity[i+1:]
	case "Plugin", "Wasm":
		i := strings.IndexByte(identity, '/')
		if i <= 0 {
			// i < 0: no "/" (unscoped). i == 0: empty leading pluginType ("/name").
			return "", fmt.Errorf("%w: %q", ErrUnscopedPluginReference(), identity)
		}
		provider, name = identity[:i], identity[i+1:]
	}
	return RegistryReference(registry, kind, provider, name), nil
}

// hasRegistryHostSegment reports whether identity leads with a
// registry-host-shaped segment — the substring before the first "/" containing a
// "." (a DNS name) or a ":" (a host:port). A bare plugin identity's leading
// segment is a socket name, which carries neither, so this distinguishes a
// qualified reference from a bare one without false-positiving on the "/plugin/"
// path segment a bound location contains.
func hasRegistryHostSegment(identity string) bool {
	head := identity
	if i := strings.IndexByte(identity, '/'); i >= 0 {
		head = identity[:i]
	}
	return strings.ContainsAny(head, ".:")
}

// libraryReference binds a host-less library identity to a physical OCI
// reference, `<registry>/<OCILocation(kind, "", name)>` — the pull-side mirror of
// push's BuildPushReference, through the same RegistryReference, so a library is
// fetched from exactly where push published it. Libraries are not
// provider-scoped: the location is the lowercased kind segment plus the
// lowercased name. The identity stays cased in the lockfile and cache key; only
// the fetch location lowercases.
func libraryReference(registry, kind, name string) string {
	return RegistryReference(registry, kind, "", name)
}

// selectPullVersion picks the highest conformant published version from a
// registry's tag list — the empty-floorVersion discovery step the three pull
// entrypoints share. Selection is strict SemVer (oci.SelectLatestVersion);
// when no tag conforms it returns a NotFound-wrapped error rather than an
// empty version, so a registry holding only non-conformant tags (a "v" prefix,
// a four-part "1.2.3.4") surfaces as "no version" instead of a bad pull.
func selectPullVersion(reference string, tags []string) (string, error) {
	version := oci.SelectLatestVersion(tags, false /* includePreRelease */)
	if version == "" {
		return "", fmt.Errorf("oci: no semver-tagged version found for %s: %w", reference, oci.ErrFetchNotFound())
	}
	return version, nil
}

// SelectScreenedVersion picks the highest conformant published version from a tag
// list, minus any version the supplied screen reports withdrawn. reference is the
// artifact the tags belong to: it keys the screen's records and names the artifact
// in the announcement and in any halt.
//
// It is the one home of screened selection, reached two ways. The pull entrypoints
// reach it through the screen attached to the puller they run on. A caller that
// holds a screen of its own and no puller — the plugin attach command, resolving an
// unpinned reference against the local registry index — passes that screen here
// directly. One home and an injected screen is what keeps a second copy of the
// exclusion rule from existing, and a copy that agreed today is what would hide the
// next divergence.
//
// With a nil screen it is exactly selectPullVersion, which stays the one home of
// the selection rule — the screen filters the candidate list and never re-implements
// the choice.
//
// With a screen, a withdrawn version is removed before selection and the outcome
// is announced, naming what was skipped, why, and what was taken instead. The
// announcement is the point: skipping a version silently would substitute a
// release the author never named on evidence absent from their own files, while
// skipping it out loud is the guardrail the author can act on.
//
// The two failure modes are kept apart, because collapsing them would report the
// wrong problem. When the screen withdrew at least one selectable version and
// nothing eligible is left, the withdrawal IS the reason and the halt says so.
// When it withdrew nothing, a failure to select has nothing to do with withdrawal
// and keeps the not-found reading it has always had.
//
// This function reaches no registry. The screen is injected rather than built here
// precisely so that stays true at every call site — see RetractionScreen.
func SelectScreenedVersion(screen RetractionScreen, reference string, tags []string) (string, error) {
	if screen == nil {
		return selectPullVersion(reference, tags)
	}
	eligible, excluded := screen.ScreenVersions(reference, tags)
	version, err := selectPullVersion(reference, eligible)
	if err != nil {
		if len(excluded) > 0 {
			return "", fmt.Errorf("%w: %s: withdrawn: %s", ErrAllVersionsRetracted(), reference, formatVersionExclusions(excluded))
		}
		return "", err
	}
	if len(excluded) > 0 {
		screen.ReportSelection(reference, version, excluded)
	}
	return version, nil
}

// selectScreenedPullVersion is the discovery step every pull entrypoint on this
// puller shares: SelectScreenedVersion over the screen this puller was constructed
// with, which is nil on an unscreened one.
//
// Scope, stated rather than implied: it covers the catalog entity pull
// (PullEntity), the library discovery step (PullLibrary) and the sub-aggregate one
// (PullAggregate), so the version a reference resolves to does not depend on which
// of the three fetched it. A puller constructed without a screen leaves all three
// unscreened, which is the posture the long-running daemon's lazy descent holds.
func (p DefaultOCIPuller) selectScreenedPullVersion(reference string, tags []string) (string, error) {
	return SelectScreenedVersion(p.retraction, reference, tags)
}

// reportPinnedVersion announces a version a reference named outright, when the
// attached screen holds a withdrawal record for it. An exact version bypasses the
// discovery step entirely, which is what makes a withdrawn version's explicit pin
// keep resolving: naming the version IS the override. This is what stops it
// resolving quietly.
//
// It is the one home for that announcement, so the three pull entrypoints cannot
// drift into announcing a pin on some references and not others. An unscreened
// puller, and a version the screen holds no record for, announce nothing.
func (p DefaultOCIPuller) reportPinnedVersion(reference, version string) {
	if p.retraction == nil {
		return
	}
	p.retraction.ReportPinned(reference, version)
}

// formatVersionExclusions renders withdrawn versions and their reasons for a halt
// message. The order is the screen's, which is ascending published version, so the
// message is byte-stable for the same registry state.
func formatVersionExclusions(excluded []VersionExclusion) string {
	parts := make([]string, 0, len(excluded))
	for _, x := range excluded {
		if x.Reason == "" {
			parts = append(parts, x.Version)
			continue
		}
		parts = append(parts, x.Version+" ("+x.Reason+")")
	}
	return strings.Join(parts, ", ")
}
