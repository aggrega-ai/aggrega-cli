// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ErrQualifiedPluginReference is returned when a Plugin reference carries a
// leading registry-host segment (or an artifact path) instead of the bare plugin
// entity identity. A plugin entity is addressed by its bare identity
// (<socket>/<name>:<version>); the pull binds that identity to its physical
// location by prefixing the configured registry and the plugin path segment
// itself. A reference that already includes a host or path segment would be
// double-prefixed into an unfetchable location, so it is refused loudly here
// rather than surfacing later as a cryptic tag error.
var ErrQualifiedPluginReference = errors.New("wasm plugin reference must be the bare plugin entity identity (<socket>/<name>:<version>), not a registry-qualified or artifact path")

// ErrUnscopedRecipeReference is returned when a Recipe / FeatureRecipe reference
// carries no provider scope — either no "/" at all or an empty leading provider
// ("/name"). Recipe identity is provider/name, and the OCI location encodes the
// provider segment because two providers may ship a recipe of the same name; an
// empty provider would bind to a location with an empty provider segment (a
// double slash) that cannot resolve. The reference is refused loudly here rather
// than surfacing later as a cryptic fetch failure.
var ErrUnscopedRecipeReference = errors.New("recipe reference must be provider-scoped (<provider>/<name>); an empty provider cannot bind to an OCI location")

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
func NewDefaultOCIPuller(wsCfg workspaceconfig.Config) OCIPuller {
	if wsCfg.Offline {
		return nil
	}
	return DefaultOCIPuller{registry: wsCfg.DefaultRegistry}
}

// DefaultOCIPuller is the production OCIPuller implementation. Pull constructs a
// fresh oci.RemoteRepository per call so per-call authentication + retry behavior
// matches pkg/oci's documented surface exactly. registry is the configured
// default registry, used by PullLibrary to bind a host-less library identity to a
// physical location.
type DefaultOCIPuller struct {
	registry string
}

func (DefaultOCIPuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	repo, err := oci.NewRemoteRepository(reference)
	if err != nil {
		return nil, err
	}
	return oci.Pull(ctx, repo, tag)
}

// PullLibrary pulls a library by its host-less identity (bare name + kind),
// binding it to a physical location through the configured default registry —
// the pull-side mirror of push's name→location (`<registry>/<kind-segment>/<name>`).
// When floorVersion is empty (a pure bare-name reference at first pull) it lists
// the registry's tags and selects the highest semver; otherwise it uses the
// asserted floor as the tag. The selected version is returned so the
// lockfile pins it. The "latest" resolution happens HERE, at the network step, and
// is pinned — the build never lists tags or fetches.
func (p DefaultOCIPuller) PullLibrary(ctx context.Context, name, kind, floorVersion string) (*oci.PullResult, string, error) {
	reference := libraryReference(p.registry, kind, name)
	repo, err := oci.NewRemoteRepository(reference)
	if err != nil {
		return nil, "", err
	}
	version := floorVersion
	if version == "" {
		tags, terr := oci.ListTags(ctx, repo)
		if terr != nil {
			return nil, "", terr
		}
		version, terr = selectPullVersion(reference, tags)
		if terr != nil {
			return nil, "", terr
		}
	}
	result, perr := oci.Pull(ctx, repo, version)
	if perr != nil {
		return nil, "", perr
	}
	return result, version, nil
}

// PullAggregate pulls a published Aggregate by its bare identity, binding it to
// a physical location through the configured default registry — the pull-side
// mirror of push's name→location (`<registry>/aggregate/<name>`). Discovery
// mirrors PullLibrary: an empty floorVersion lists tags and selects the highest
// semver; a non-empty floor is the tag. It returns the OCI tag it fetched under
// (a location/discovery coordinate); the IDENTITY pinned in the lockfile is the
// fetched body's metadata.version, which PullAndCacheAggregate reads and pins —
// so the tag is discarded and the pin is identity-stable even when a publisher
// tags `latest`/`stable` over the versioned identity.
func (p DefaultOCIPuller) PullAggregate(ctx context.Context, name, floorVersion string) (*oci.PullResult, string, error) {
	reference := p.registry + "/" + OCILocation("Aggregate", "", name)
	repo, err := oci.NewRemoteRepository(reference)
	if err != nil {
		return nil, "", err
	}
	tag := floorVersion
	if tag == "" {
		tags, terr := oci.ListTags(ctx, repo)
		if terr != nil {
			return nil, "", terr
		}
		tag, terr = selectPullVersion(reference, tags)
		if terr != nil {
			return nil, "", terr
		}
	}
	result, perr := oci.Pull(ctx, repo, tag)
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
	repo, err := oci.NewRemoteRepository(reference)
	if err != nil {
		return nil, "", err
	}
	version := floorVersion
	if version == "" {
		tags, terr := oci.ListTags(ctx, repo)
		if terr != nil {
			return nil, "", terr
		}
		version, terr = selectPullVersion(reference, tags)
		if terr != nil {
			return nil, "", terr
		}
	}
	// A Plugin publishes as ONE OCI manifest whose CONFIG blob is the entity YAML
	// and whose single layer is the inline .wasm. A generic content-layer pull
	// would return the .wasm as the entity bytes; read the config blob instead so
	// the entity content the cache stores and the closure pins is the entity YAML.
	// result.Digest stays the manifest digest either way (the ManifestDigest pin);
	// only the content descriptor read differs. Every other kind reads the content
	// layer through Pull unchanged.
	if kind == "Plugin" {
		result, perr := oci.FetchPluginEntity(ctx, repo, version)
		if perr != nil {
			return nil, "", perr
		}
		return result, version, nil
	}
	result, perr := oci.Pull(ctx, repo, version)
	if perr != nil {
		return nil, "", perr
	}
	return result, version, nil
}

// RegistryHost reports the configured default registry, so a Tier-3 consumer can
// point an authorization-failure remediation at the real host rather than at a
// bare identity's leading path segment.
func (p DefaultOCIPuller) RegistryHost() string {
	return p.registry
}

// BindEntityLocation reports the physical OCI repository reference a bare
// catalog identity resolves from — `<registry>/<OCILocation(kind, provider,
// name)>` — for a Tier-3 registry pull. It is the pull-side location binding
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
// "recipe" / "feature_recipe" / "plugin" / "aggregate"); it is mapped to the
// catalog kind the location rule expects. Returns ok=false when registry is
// empty, when the hint does not map to a catalog kind, or when the binding is
// refused (a registry-qualified identity) — the same conditions under which the
// puller itself cannot bind a bare identity and falls to the legacy
// located-reference path, so the caller skips rather than fabricating a location.
func BindEntityLocation(registry, kindHint, uri string) (location string, ok bool) {
	if registry == "" {
		return "", false
	}
	kind := entityKindForHint(kindHint)
	if kind == "" {
		return "", false
	}
	identity, _, _ := splitVersionedURI(uri)
	ref, err := entityReference(registry, kind, identity)
	if err != nil {
		return "", false
	}
	return ref, true
}

// entityKindForHint maps a closure KindHint to the catalog kind the OCI location
// rule expects for a registry-resolved entity, covering both the core-catalog
// tier (ociKindForHint) and the sub-aggregate tier, whose bare-name pull binds
// through the same location rule under the "Aggregate" kind. It is the
// location-binding counterpart of the pull-tier dispatch, so an entity that
// resolves from a registry maps to a location for verify exactly as it did for
// the pull.
func entityKindForHint(hint string) string {
	if hint == "aggregate" {
		return "Aggregate"
	}
	return ociKindForHint(hint)
}

// entityReference binds a bare catalog identity to its physical OCI reference,
// `<registry>/<OCILocation(kind, provider, name)>` — the pull-side mirror of
// push's buildPushReference, reusing the same OCILocation rule so push-location
// == pull-location holds by construction. Recipe / FeatureRecipe identity is
// provider/name; it is split on the first "/" to feed the provider-scoped
// location branch. Other kinds pass the whole identity as the name.
//
// A Plugin identity must be bare (<socket>/<name>): the binder prefixes the
// registry and the plugin path segment itself, so an identity that already leads
// with a registry-host-shaped segment (the part before the first "/" carrying a
// "." or ":") would be double-prefixed into an unfetchable location. Such a
// reference is refused with ErrQualifiedPluginReference rather than bound. The
// guard is Plugin-scoped; the Component / Feature binders are unchanged.
//
// A Recipe / FeatureRecipe identity must be provider-scoped (provider/name): an
// unscoped identity (no "/") or one with an empty leading provider ("/name")
// would bind to a location with an empty provider segment (a double slash) that
// cannot resolve, so it is refused with ErrUnscopedRecipeReference before the
// location is formed.
func entityReference(registry, kind, identity string) (string, error) {
	if kind == "Plugin" && hasRegistryHostSegment(identity) {
		return "", fmt.Errorf("%w: %q", ErrQualifiedPluginReference, identity)
	}
	provider, name := "", identity
	switch kind {
	case "Recipe", "FeatureRecipe":
		i := strings.IndexByte(identity, '/')
		if i <= 0 {
			// i < 0: no "/" (unscoped). i == 0: empty leading provider ("/name").
			return "", fmt.Errorf("%w: %q", ErrUnscopedRecipeReference, identity)
		}
		provider, name = identity[:i], identity[i+1:]
	}
	return registry + "/" + OCILocation(kind, provider, name), nil
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
// push's buildPushReference, so a library is fetched from exactly where push
// published it. Libraries are not provider-scoped: the location is the
// lowercased kind segment plus the lowercased name. The identity stays cased in
// the lockfile and cache key; only the fetch location lowercases.
func libraryReference(registry, kind, name string) string {
	return registry + "/" + OCILocation(kind, "", name)
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
		return "", fmt.Errorf("oci: no semver-tagged version found for %s: %w", reference, oci.ErrFetchNotFound)
	}
	return version, nil
}
