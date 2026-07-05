// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// VerifyContentAgainstManifest checks fetched or cached content against the
// layer digest the OCI manifest declares for it. The manifest is the authority:
// the content is trusted only when its content digest equals the digest the
// manifest's single artifact layer commits to. A mismatch — or a manifest that
// carries no layer — is wrapped ErrFetchIntegrity, since a layer digest that
// does not match the content IS a content-integrity failure.
//
// This is the single verify used by every path that trusts content against a
// manifest: the online pull and each class's offline cache read all route
// through here, so the manifest-authority check is spelled exactly once.
func VerifyContentAgainstManifest(manifestBytes, content []byte) error {
	layer, _, err := descriptorFromManifest(manifestBytes)
	if err != nil {
		return fmt.Errorf("oci: parse manifest for content verify: %w: %w", err, ErrFetchIntegrity)
	}
	if got := digest.ContentDigest(content); got != layer.Digest.String() {
		return fmt.Errorf("oci: content digest %s != manifest layer %s: %w",
			got, layer.Digest.String(), ErrFetchIntegrity)
	}
	return nil
}

// VerifyManifestDigest checks a persisted OCI manifest blob against the manifest
// digest an independent record — the lockfile / prior-lockfile pin — committed
// to. It is the offline manifest self-verify: a cached manifest is trusted only
// when its own content digest equals the pinned manifest digest, so a tampered
// or corrupted manifest blob halts loudly rather than becoming the authority a
// later content check trusts. A mismatch is wrapped ErrCachedManifestIntegrity.
//
// The pin is the root of trust: it comes from the independent record, never from
// re-deriving the manifest out of the cached content. That independence is what
// distinguishes manifest authority from a self-consistency check.
func VerifyManifestDigest(manifestBytes []byte, pinnedManifestDigest string) error {
	if got := digest.ContentDigest(manifestBytes); got != pinnedManifestDigest {
		return fmt.Errorf("oci: cached manifest digest %s != pinned %s: %w",
			got, pinnedManifestDigest, ErrCachedManifestIntegrity)
	}
	return nil
}
