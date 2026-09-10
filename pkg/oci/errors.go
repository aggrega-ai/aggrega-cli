// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"errors"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrFetchNotFound is returned when a remote fetch finds no matching
// artifact at the requested URI + version. Call sites wrap this
// sentinel with URL + remediation context; downstream consumers
// detect the category via errors.Is.
func ErrFetchNotFound() error { return errFetchNotFound }

var errFetchNotFound = cerrors.Register("fetch-not-found", cerrors.Meta{
	ShortCode: "E0137",
	Message:   "oci: artifact not found at remote source",
})

// ErrFetchUnauthorized is returned when registry authentication is
// required or fails. Call sites wrap this sentinel with the registry
// host and a `docker login <registry>` remediation pointer.
func ErrFetchUnauthorized() error { return errFetchUnauthorized }

var errFetchUnauthorized = cerrors.Register("fetch-unauthorized", cerrors.Meta{
	ShortCode: "E0138",
	Message:   "oci: authentication required or failed",
})

// ErrFetchNetworkUnreachable is returned when the transport layer
// fails (DNS resolution, TCP connection, TLS handshake). Call sites
// wrap this sentinel with the URL.
func ErrFetchNetworkUnreachable() error { return errFetchNetworkUnreachable }

var errFetchNetworkUnreachable = cerrors.Register("fetch-network-unreachable", cerrors.Meta{
	ShortCode: "E0136",
	Message:   "oci: network unreachable",
})

// ErrFetchIntegrity is returned when pulled bytes don't match the
// manifest's claimed digest. Call sites wrap this sentinel with the
// URL, the expected digest, and an
// `aggrega pull --upgrade --release <release>` remediation pointer.
func ErrFetchIntegrity() error { return errFetchIntegrity }

var errFetchIntegrity = cerrors.Register("fetch-integrity", cerrors.Meta{
	ShortCode: "E0135",
	Message:   "oci: content digest mismatch",
})

// ErrNoContentLayer is returned when an OCI manifest carries no layer of
// the content media type the caller expected. A signed chart, for
// example, bundles a detached provenance layer beside the chart content;
// selecting the content by media type rather than by position means a
// manifest that is missing the expected content layer halts loudly
// instead of yielding the wrong blob. Call sites wrap this sentinel with
// the media type that was expected.
func ErrNoContentLayer() error { return errNoContentLayer }

var errNoContentLayer = cerrors.Register("no-content-layer", cerrors.Meta{
	ShortCode: "E0140",
	Message:   "oci: manifest has no layer of the expected content media type",
})

// ErrAmbiguousContentLayer is returned when an OCI manifest carries more
// than one layer of the expected content media type, so the content
// layer cannot be selected unambiguously. Selection halts loudly rather
// than pick one. Call sites wrap this sentinel with the media type and
// the count.
func ErrAmbiguousContentLayer() error { return errAmbiguousContentLayer }

var errAmbiguousContentLayer = cerrors.Register("ambiguous-content-layer", cerrors.Meta{
	ShortCode: "E0132",
	Message:   "oci: manifest has multiple layers of the expected content media type",
})

// ErrCachedManifestIntegrity is returned when a cached OCI manifest blob no
// longer hashes to the manifest digest the artifact commits to — the offline
// manifest self-verify. It is distinct from ErrFetchIntegrity (which covers a
// content blob that mismatches its pin): this sentinel names a corrupted or
// tampered manifest blob specifically. Call sites wrap it with the URI, the
// recomputed digest, and the expected digest; the remediation is
// `aggrega pull --upgrade --release <release>` to refresh the cache
// from authoritative bytes.
func ErrCachedManifestIntegrity() error { return errCachedManifestIntegrity }

var errCachedManifestIntegrity = cerrors.Register("cached-manifest-integrity", cerrors.Meta{
	ShortCode: "E0133",
	Message:   "oci: cached manifest digest mismatch",
})

// ErrImmutableTagConflict is returned by the publish path when the
// target tag already exists in the registry pointing at a different
// manifest digest than the content being pushed. Published bytes are
// frozen: a tag is never silently overwritten with different content.
// Call sites wrap this sentinel with the reference, the existing
// digest, the proposed digest, and a pointer to publish a new version
// (verified by aggrega lint --pre-publish) instead. Re-pushing
// byte-identical content under the same tag is not a conflict — it is
// an idempotent no-op.
func ErrImmutableTagConflict() error { return errImmutableTagConflict }

var errImmutableTagConflict = cerrors.Register("immutable-tag-conflict", cerrors.Meta{
	ShortCode: "E0139",
	Message:   "oci: tag already published with different content",
})

// ErrUnknownEntityKind is returned by MediaTypeForKind for kind
// strings not in the canonical entity-kind set. Programming error
// at the dispatch boundary, not a user-input error.
func ErrUnknownEntityKind() error { return errUnknownEntityKind }

var errUnknownEntityKind = cerrors.Register("unknown-entity-kind", cerrors.Meta{
	ShortCode: "E0143",
	Message:   "oci: unknown entity kind for mediatype dispatch",
})

// ErrEntityVersionPathMismatch is returned by the publish-time
// per-version-subdirectory validator when a publishable entity's
// filesystem path version segment (the parent directory of the entity
// file) doesn't match the entity's metadata.version field. It applies
// to every publishable kind — Component, Feature, Plugin, Aggregate,
// Recipe, FeatureRecipe, SchemaLibrary, MappingLibrary. Call sites wrap
// this sentinel with both values for the operator's diagnostic.
func ErrEntityVersionPathMismatch() error { return errEntityVersionPathMismatch }

var errEntityVersionPathMismatch = cerrors.Register("entity-version-path-mismatch", cerrors.Meta{
	ShortCode: "E0134",
	Message:   "oci: entity per-version subdirectory mismatch",
})

// ErrOCILocationCollision fires when two DISTINCT catalog identities map to the
// same OCI repository location — e.g. two SchemaLibrary identities that differ
// only in case (AIDatastores vs AiDatastores both lower to schemalibrary/
// aidatastores), or, were the provider ever dropped from a recipe location, two
// provider-distinct recipes of the same name. Because the OCI location is a
// lowercased, identity-derived coordinate, a faithful catalog must keep
// identities collision-free under that mapping; a collision would silently
// overwrite one entity's published bytes with another's. The whole-catalog lint
// halts loudly on it rather than let population drop an entity.
func ErrOCILocationCollision() error { return errOCILocationCollision }

var errOCILocationCollision = cerrors.Register("oci-location-collision", cerrors.Meta{
	ShortCode: "E0141",
	Message:   "oci: distinct catalog identities map to the same OCI repository location",
})

// ErrReferrerDiscoveryUnavailable is returned when an artifact's referrers
// could not be looked up at all: the target cannot serve referrer discovery,
// the subject the referrers hang off could not be located, or the discovery
// request itself failed. It names a question that went UNANSWERED.
//
// That is a different outcome from an answered question that found nothing,
// and the two must not be conflated. An artifact that simply carries no
// referrer resolves normally and yields an empty list; this sentinel says the
// list was never obtained, so nothing at all is known about what is attached.
//
// The distinction is what lets a caller act correctly on a missing referrer.
// A caller that consumes referrers only when they are present may continue
// past this sentinel, because an unanswered lookup produced nothing to
// consume either way. A caller that REQUIRES a referrer must treat it as a
// failure: a check that could not run has not passed, and admitting the
// artifact would rest on a question nobody managed to put. Call sites wrap
// this sentinel with the subject reference and the underlying cause.
func ErrReferrerDiscoveryUnavailable() error { return errReferrerDiscoveryUnavailable }

var errReferrerDiscoveryUnavailable = cerrors.Register("referrer-discovery-unavailable", cerrors.Meta{
	ShortCode: "E0142",
	Message:   "oci: referrer discovery could not be completed",
})

// RemediationFor returns the operator-actionable remediation hint
// for a fetch-sentinel error. The registryHost substring substitutes
// into auth/network messages; pass an empty string when the host is
// not derivable and the literal `<registry>` placeholder remains in
// the message.
//
// Returns the empty string for non-sentinel errors (or when sentinel
// is nil), so callers can decide whether to fall back to a generic
// message or omit the remediation entirely. The function uses
// errors.Is to traverse wrapped chains, so callers may pass either
// the bare sentinel or any error that wraps one.
//
// Co-located with the sentinel declarations so both the lock-time
// pull pipeline (`pkg/sourceresolver.Wrap`) and the build-time read
// pipeline (the orchestrator's fetch-error branch) call the same
// mapping — keeping the operator-facing remediation strings DRY
// across both axes.
func RemediationFor(sentinel error, registryHost string) string {
	if sentinel == nil {
		return ""
	}
	host := registryHost
	if host == "" {
		host = "<registry>"
	}
	switch {
	case errors.Is(sentinel, ErrFetchNotFound()):
		return "remediation: verify the URL and version are correct; for cache misses with offline mode set, disable offline or rerun aggrega pull --release <release>"
	case errors.Is(sentinel, ErrFetchUnauthorized()):
		return "remediation: docker login " + host + " (or helm registry login " + host + " for Helm-specific OCI) before re-running aggrega pull --release <release>"
	case errors.Is(sentinel, ErrFetchNetworkUnreachable()):
		return "remediation: check network connectivity to " + host + "; corporate proxies typically need HTTPS_PROXY exported"
	case errors.Is(sentinel, ErrFetchIntegrity()), errors.Is(sentinel, ErrCachedManifestIntegrity()):
		return "remediation: run aggrega pull --upgrade --release <release> to refresh the lockfile and cache"
	default:
		return ""
	}
}
