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

// ErrUnknownEntityKind is returned by MediaTypeForKind for kind
// strings not in the canonical entity-kind set. Programming error
// at the dispatch boundary, not a user-input error.
var ErrUnknownEntityKind = errors.New("oci: unknown entity kind for mediatype dispatch")

// ErrLibraryVersionMismatch is returned by the publish-time
// per-version-subdirectory validator when a SchemaLibrary or
// MappingLibrary's filesystem path version segment doesn't match the
// entity's metadata.version field. Call sites wrap this sentinel
// with both values for the operator's diagnostic.
var ErrLibraryVersionMismatch = errors.New("oci: library per-version subdirectory mismatch")

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
