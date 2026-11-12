// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// VerifyContentAgainstManifest checks fetched or cached content against the
// layer digest the OCI manifest declares for it. The manifest is the authority:
// the content is trusted only when its content digest equals the digest the
// manifest's content layer commits to. A mismatch — or a manifest that
// carries no matching layer — is wrapped ErrFetchIntegrity, since a layer digest
// that does not match the content IS a content-integrity failure.
//
// An optional expectedMediaType selects the content layer by media type rather
// than by position, so a manifest that bundles more than one layer (a signed
// chart's detached provenance layer beside the chart content) verifies the
// content against the layer that actually commits to it. It must match the
// expectation the corresponding pull selected the content with; passing a
// different expectation would verify against a different layer. Callers whose
// manifests carry a single artifact layer omit the argument.
//
// This is the single verify used by every path that trusts content against a
// manifest: the online pull and each class's offline cache read all route
// through here, so the manifest-authority check is spelled exactly once.
func VerifyContentAgainstManifest(manifestBytes, content []byte, expectedMediaType ...string) error {
	layer, _, err := descriptorFromManifest(manifestBytes, expectedMediaType...)
	if err != nil {
		return fmt.Errorf("oci: parse manifest for content verify: %w: %w", err, ErrFetchIntegrity())
	}
	if got := digest.ContentDigest(content); got != layer.Digest.String() {
		return fmt.Errorf("oci: content digest %s != manifest layer %s: %w",
			got, layer.Digest.String(), ErrFetchIntegrity())
	}
	return nil
}

// VerifyConfigAgainstManifest checks fetched or cached content against the
// CONFIG-descriptor digest an OCI manifest declares — the config-blob analogue
// of VerifyContentAgainstManifest. The single plugin manifest carries the Plugin
// entity YAML as its config blob (a layer carries the .wasm), so a plugin entity
// is trusted only when its content digest equals the digest the manifest's config
// descriptor commits to. A mismatch is wrapped ErrFetchIntegrity, since a config
// digest that does not match the content IS a content-integrity failure.
//
// It is the config-side sibling of VerifyContentAgainstManifest: layer-carried
// content verifies through that; config-carried content (the plugin entity)
// verifies through this. Both spell the manifest-authority check exactly once for
// their descriptor.
func VerifyConfigAgainstManifest(manifestBytes, content []byte) error {
	view, err := parseManifest(manifestBytes)
	if err != nil {
		return fmt.Errorf("oci: parse manifest for config verify: %w: %w", err, ErrFetchIntegrity())
	}
	if got := digest.ContentDigest(content); got != view.Config.Digest.String() {
		return fmt.Errorf("oci: config digest %s != manifest config %s: %w",
			got, view.Config.Digest.String(), ErrFetchIntegrity())
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
			got, pinnedManifestDigest, ErrCachedManifestIntegrity())
	}
	return nil
}
