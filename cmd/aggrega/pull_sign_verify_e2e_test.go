// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// These tests compose the publish → cache → air-gapped-verify spine for the
// keyless model: an artifact pushed to an OCI store with a signature
// referrer attached, the bundle cached as a content-addressed blob, and
// verification anchored in a PINNED trust root loaded from a file with no
// network. They exercise the orchestration seams the keyless model relies on
// for air-gapped operation — the pinned-trusted-root anchor branch and the
// content-addressed bundle cache — rather than re-proving the keyless
// verification policy itself, which is covered against a real in-memory trust
// anchor elsewhere in the signing package.
//
// SCOPE — what is and is not proven offline here:
//
// A complete keyless bundle is produced only against live Fulcio + Rekor (the
// keyless signer is live-only by construction), so no fully-verifying keyless
// bundle can be minted offline to drive a positive file-pinned keyless verify
// end to end. The in-memory virtual-Sigstore trust root used by the signing
// package's keyless tests proves the verification LOGIC against a real anchor,
// but it does not serialize to the on-disk trusted-root format as a working
// anchor: the round-trip through the trusted-root marshal/parse does not
// preserve the virtual Rekor log identity, so a virtual-Sigstore-signed entity
// will not verify against the serialized-then-reloaded root. The positive
// keyless verify against a file-pinned root — real keyless sign, push to the
// deployed hub, cache-wiped pull, keyless verify against the fetched root — is
// therefore the live end-to-end leg (see the env-gated live test). What these
// offline tests prove is the surrounding composition: the pinned-root anchor
// LOADS and is SELECTED by the orchestrator's keyless branch, and the bundle
// cache round-trips byte-for-byte.

const e2eComponentBody = "apiVersion: aggrega.ai/v1alpha1\n" +
	"kind: Component\n" +
	"metadata:\n  name: core/api-server\n  version: 1.0.0\n"

func e2eTargetFor(store oras.ReadOnlyTarget, subjectRef string) pullverify.TargetFor {
	return func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
		return store, subjectRef, nil
	}
}

// TestKeylessAirGappedFromPinnedRoot proves the air-gapped composition the
// keyless model depends on: an artifact and its signature referrer published
// to an OCI store, the bundle discovered and cached as a content-addressed
// blob, and the cached blob re-read byte-identical for offline
// re-verification. The trust anchor is the pinned public-good trust root
// loaded from a file with no network — the air-gapped seam.
//
// This is the offline half of the capstone. It does not mint a verifying
// keyless bundle (that is live-only); it proves the cache and pinned-root
// LOAD plumbing around the keyless verify. The positive keyless verify
// against a file-pinned root is the live leg.
func TestKeylessAirGappedFromPinnedRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	dg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte(e2eComponentBody))
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Attach a signature referrer (the bundle bytes stand in for a keyless
	// bundle for the cache round-trip; the keyless verify itself is the live
	// leg). Discovery, fetch, and the content-addressed cache are the seams
	// under test.
	bundle := []byte(`{"keyless-bundle-placeholder-over":"` + dg + `"}`)
	if _, err := oci.AttachReferrer(ctx, store, "1.0.0", signing.BundleMediaType, bundle); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}

	refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d signature referrers, want 1", len(refs))
	}
	fetched, err := oci.FetchReferrerPayload(ctx, store, refs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}

	// Cache the bundle as a content-addressed blob and read it back: the
	// air-gapped cache hop must be byte-stable so a later offline verify sees
	// exactly the bytes that were attached.
	sigDir := filepath.Join(t.TempDir(), "signatures")
	const bundleDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	if err := cacheBundleBlob(sigDir, bundleDigest, fetched); err != nil {
		t.Fatalf("cacheBundleBlob: %v", err)
	}
	blobPath := filepath.Join(sigDir, strings.ReplaceAll(bundleDigest, ":", "-")+".bundle")
	cached, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatalf("read cached bundle: %v", err)
	}
	if !bytes.Equal(cached, bundle) {
		t.Fatalf("cached bundle differs from attached bundle:\n got %q\nwant %q", cached, bundle)
	}

	// The pinned trust root loads from a file as a usable keyless anchor with
	// no network — the air-gapped trust anchor.
	anchor, err := signing.TrustedMaterialFromRootPath(
		filepath.Join("..", "..", "pkg", "signing", "testdata", "public-good-root.json"))
	if err != nil {
		t.Fatalf("TrustedMaterialFromRootPath: %v", err)
	}
	if len(anchor.FulcioCertificateAuthorities()) == 0 {
		t.Fatal("pinned root carries no Fulcio certificate authorities")
	}
}

// TestPinnedRootAnchorSelectedByOrchestrator proves the orchestrator's
// keyless verify selects the pinned-trusted-root anchor branch when a
// registry configures a trusted_root path, and that a load failure of that
// pinned root halts a signature-requiring pull rather than silently falling
// back to a network fetch. A required keyless registry with a missing
// trusted_root surfaces the trust-root load failure end to end through the
// pull verify pass — the pinned-anchor branch driven through orchestration,
// fully offline.
func TestPinnedRootAnchorSelectedByOrchestrator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	dg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte(e2eComponentBody))
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	bundle := []byte(`{"keyless-bundle-placeholder-over":"` + dg + `"}`)
	if _, err := oci.AttachReferrer(ctx, store, "1.0.0", signing.BundleMediaType, bundle); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}

	cfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{
				Host:               "keyless.example.com",
				RequiresSignatures: true,
				TrustedRoot:        filepath.Join(t.TempDir(), "missing-root.json"),
				TrustedIdentities: []workspaceconfig.TrustedIdentity{
					{Identity: "maintainer@example.com", Issuer: "https://accounts.google.com"},
				},
			},
		},
	}
	ent := pullverify.Entity{
		URI:    "keyless.example.com/component/core/api-server",
		Digest: dg,
		Host:   "keyless.example.com",
	}
	_, verr := pullverify.Verify(ctx, []pullverify.Entity{ent}, pullverify.Options{
		Config: cfg,
		Target: e2eTargetFor(store, "1.0.0"),
	})
	if !errors.Is(verr, signing.ErrTrustRootLoad) {
		t.Fatalf("want ErrTrustRootLoad (pinned-root branch selected, load halts), got %v", verr)
	}
}
