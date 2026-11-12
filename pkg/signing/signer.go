// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package signing

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// BundleMediaType is the cosign-compatible Sigstore bundle media type
// carried as the OCI referrer artifactType when a signature is attached
// to a published artifact. A verifier discovers signatures by listing
// referrers of this artifactType on an artifact's manifest.
//
// It is the transport's own declaration, re-exported: the OCI package packs
// the referrer and names the file a standard registry client writes the
// bundle to, so the media type is declared there once and read here.
const BundleMediaType = oci.MediaTypeSignatureBundle

// Signer produces a Sigstore signature bundle binding a signature to an
// artifact's OCI manifest digest. The bundle bytes are attached to the
// published artifact as an OCI referrer whose artifactType is
// BundleMediaType.
//
// The digest argument is the artifact's manifest digest string (e.g.
// "sha256:..."), the immutable coordinate the signature commits to. A
// signer never re-pushes or mutates the artifact; it signs the digest of
// already-packed content and returns a bundle for the caller to attach.
type Signer interface {
	// Sign produces a Sigstore bundle committing to artifactDigest.
	// Returns the bundle bytes and the referrer artifactType
	// (BundleMediaType).
	Sign(ctx context.Context, artifactDigest string) (bundle []byte, artifactType string, err error)
}
