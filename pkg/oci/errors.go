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
