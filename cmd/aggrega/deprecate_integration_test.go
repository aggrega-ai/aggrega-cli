// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestIntegration_DeprecateAttachesSignedMarker drives the production
// `aggrega deprecate` flow against a real in-process registry with a keyed
// signer, then proves two facts through the referrer API:
//
//   - a deprecation referrer of the subject exists with the right
//     artifactType, and its payload decodes to the given severity + message;
//   - a signature referrer of THAT deprecation referrer exists and verifies
//     against the signer's public key through the existing keyed-bundle verify
//     path — the marker is a signed artifact.
//
// The subject artifact's own tag is untouched: the marker and its signature
// are sibling manifests, so deprecation amends an immutable published artifact.
func TestIntegration_DeprecateAttachesSignedMarker(t *testing.T) {
	host := startRegistry(t, nil)
	ctx := context.Background()

	// A keyed signer, used for the deprecation flow and the verify assertion.
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	// Publish a subject artifact to the real registry.
	repo, err := oci.NewRemoteRepository(host + "/component/core/legacy-api")
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	subjectDigest, err := oci.Push(ctx, repo, "1.0.0", oci.MediaTypeComponent, []byte("legacy component bytes"))
	if err != nil {
		t.Fatalf("Push subject: %v", err)
	}

	const wantSeverity = "retracted"
	const wantMessage = "withdrawn; use core/api-server:2 instead"

	// Run the production deprecate flow. No Target injection: the flow
	// constructs its own remote repository from the reference, exercising the
	// real oci.NewRemoteRepository path. The keyed Signer is injected so the
	// keyless branch is skipped.
	if err := deprecateRun(deprecateConfig{
		Reference: host + "/component/core/legacy-api:1.0.0",
		Severity:  wantSeverity,
		Message:   wantMessage,
		Signer:    signer,
		EnvLookup: func(string) string { return "" },
	}); err != nil {
		t.Fatalf("deprecateRun: %v", err)
	}

	// The subject tag still points at the same digest — no re-push.
	after, exists, err := oci.Resolve(ctx, repo, "1.0.0")
	if err != nil || !exists {
		t.Fatalf("Resolve subject after deprecate: exists=%v err=%v", exists, err)
	}
	if after != subjectDigest {
		t.Errorf("subject tag now points at %q, want unchanged %q", after, subjectDigest)
	}

	// (a) A deprecation referrer of the subject exists with the right
	// artifactType and a decodable payload.
	markers, err := oci.ListReferrers(ctx, repo, "1.0.0", deprecation.ArtifactType)
	if err != nil {
		t.Fatalf("ListReferrers (deprecation): %v", err)
	}
	if len(markers) != 1 {
		t.Fatalf("got %d deprecation referrers, want 1", len(markers))
	}
	if markers[0].ArtifactType != deprecation.ArtifactType {
		t.Errorf("marker artifactType = %q, want %q", markers[0].ArtifactType, deprecation.ArtifactType)
	}
	payload, err := oci.FetchReferrerPayload(ctx, repo, markers[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload (marker): %v", err)
	}
	marker, err := deprecation.Unmarshal(payload)
	if err != nil {
		t.Fatalf("decode marker payload: %v", err)
	}
	if string(marker.Severity) != wantSeverity || marker.Message != wantMessage {
		t.Errorf("marker = %+v, want severity=%q message=%q", marker, wantSeverity, wantMessage)
	}

	// (b) A signature referrer of the deprecation marker exists and verifies
	// against the signer's key for the marker's own manifest digest. The marker
	// is an untagged referrer; a real registry resolves it by manifest digest,
	// so the signature is discovered by listing referrers of the marker digest.
	markerDigest := markers[0].Digest.String()
	sigs, err := oci.ListReferrers(ctx, repo, markerDigest, signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers (signature of marker): %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("got %d signature referrers of the marker, want 1", len(sigs))
	}
	bundle, err := oci.FetchReferrerPayload(ctx, repo, sigs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload (signature): %v", err)
	}
	if verr := signing.VerifyKeyedBundle(bundle, signer.PublicKeyPEM(), markerDigest); verr != nil {
		t.Fatalf("marker signature does not verify against the marker digest: %v", verr)
	}
}

// TestIntegration_DeprecateUnpublishedFails proves deprecating a reference the
// registry does not hold fails loudly rather than attaching anything.
func TestIntegration_DeprecateUnpublishedFails(t *testing.T) {
	host := startRegistry(t, nil)

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	err = deprecateRun(deprecateConfig{
		Reference: host + "/component/core/missing:9.9.9",
		Severity:  "warning",
		Message:   "does not matter",
		Signer:    signer,
		EnvLookup: func(string) string { return "" },
	})
	if err == nil {
		t.Fatal("deprecateRun should fail for an unpublished reference")
	}
}
