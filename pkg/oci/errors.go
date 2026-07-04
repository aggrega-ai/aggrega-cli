// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import "errors"

// ErrFetchNotFound is returned when a remote fetch finds no matching
// artifact at the requested URI + version. Call sites wrap this
// sentinel with URL + remediation context; downstream consumers
// detect the category via errors.Is.
//
// Category integration and call-site wrapping in the compiler's
// diagnostic surface land at a follow-up implementation; this sentinel is
// a pure declaration today.
var ErrFetchNotFound = errors.New("oci: artifact not found at remote source")

// ErrFetchUnauthorized is returned when registry authentication is
// required or fails. Call sites wrap this sentinel with the registry
// host and a `docker login <registry>` remediation pointer.
var ErrFetchUnauthorized = errors.New("oci: authentication required or failed")

// ErrFetchNetworkUnreachable is returned when the transport layer
// fails (DNS resolution, TCP connection, TLS handshake). Call sites
// wrap this sentinel with the URL.
var ErrFetchNetworkUnreachable = errors.New("oci: network unreachable")

// ErrFetchIntegrity is returned when pulled bytes don't match the
// manifest's claimed digest. Call sites wrap this sentinel with the
// URL, the expected digest, and an `aggrega pull --regenerate`
// remediation pointer.
var ErrFetchIntegrity = errors.New("oci: content digest mismatch")

// ErrImmutableTagConflict is returned by the publish path when the
// target tag already exists in the registry pointing at a different
// manifest digest than the content being pushed. Published bytes are
// frozen: a tag is never silently overwritten with different content.
// Call sites wrap this sentinel with the reference, the existing
// digest, the proposed digest, and a pointer to publish a new version
// (verified by aggrega lint --pre-publish) instead. Re-pushing
// byte-identical content under the same tag is not a conflict — it is
// an idempotent no-op.
var ErrImmutableTagConflict = errors.New("oci: tag already published with different content")

// ErrUnknownEntityKind is returned by MediaTypeForKind for kind
// strings not in the canonical entity-kind set. Programming error
// at the dispatch boundary, not a user-input error.
var ErrUnknownEntityKind = errors.New("oci: unknown entity kind for mediatype dispatch")

// ErrEntityVersionPathMismatch is returned by the publish-time
// per-version-subdirectory validator when a publishable entity's
// filesystem path version segment (the parent directory of the entity
// file) doesn't match the entity's metadata.version field. It applies
// to every publishable kind — Component, Feature, Plugin, Aggregate,
// Recipe, FeatureRecipe, SchemaLibrary, MappingLibrary. Call sites wrap
// this sentinel with both values for the operator's diagnostic.
var ErrEntityVersionPathMismatch = errors.New("oci: entity per-version subdirectory mismatch")

// ErrOCILocationCollision fires when two DISTINCT catalog identities map to the
// same OCI repository location — e.g. two SchemaLibrary identities that differ
// only in case (AIDatastores vs AiDatastores both lower to schemalibrary/
// aidatastores), or, were the provider ever dropped from a recipe location, two
// provider-distinct recipes of the same name. Because the OCI location is a
// lowercased, identity-derived coordinate, a faithful catalog must keep
// identities collision-free under that mapping; a collision would silently
// overwrite one entity's published bytes with another's. The whole-catalog lint
// halts loudly on it rather than let population drop an entity.
var ErrOCILocationCollision = errors.New("oci: distinct catalog identities map to the same OCI repository location")

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
	case errors.Is(sentinel, ErrFetchNotFound):
		return "remediation: verify the URL and version are correct; for cache misses with offline mode set, disable offline or rerun aggrega pull"
	case errors.Is(sentinel, ErrFetchUnauthorized):
		return "remediation: docker login " + host + " (or helm registry login " + host + " for Helm-specific OCI) before re-running aggrega pull"
	case errors.Is(sentinel, ErrFetchNetworkUnreachable):
		return "remediation: check network connectivity to " + host + "; corporate proxies typically need HTTPS_PROXY exported"
	case errors.Is(sentinel, ErrFetchIntegrity):
		return "remediation: run aggrega pull --regenerate <release> to refresh the lockfile and cache"
	default:
		return ""
	}
}
