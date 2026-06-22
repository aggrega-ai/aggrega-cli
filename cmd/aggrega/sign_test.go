// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// TestSignAttachesReferrerWithoutMutatingArtifact proves `aggrega sign`
// signs an already-published artifact and attaches the signature as a
// referrer, leaving the artifact's bytes and tag untouched — the
// immutability-safe amendment.
func TestSignAttachesReferrerWithoutMutatingArtifact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	published, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("published content"))
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	fake := &signingtest.FakeSigner{}
	err = signRun(signConfig{
		Reference:  "hub.aggrega.ai/component/core/api-server:1.0.0",
		SubjectRef: "1.0.0",
		Target:     store,
		Signer:     fake,
	})
	if err != nil {
		t.Fatalf("signRun: %v", err)
	}

	// The signer was asked to sign the published manifest digest.
	if len(fake.Signed) != 1 || fake.Signed[0] != published {
		t.Fatalf("signer saw %v, want one call for digest %q", fake.Signed, published)
	}

	// The artifact tag still points at the same digest (no re-push).
	after, exists, err := oci.Resolve(ctx, store, "1.0.0")
	if err != nil || !exists {
		t.Fatalf("Resolve after sign: exists=%v err=%v", exists, err)
	}
	if after != published {
		t.Errorf("tag now points at %q, want unchanged %q", after, published)
	}

	// The signature is discoverable as a referrer.
	refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d signature referrers, want 1", len(refs))
	}
}

// TestSignUnpublishedArtifactFails proves signing a reference the registry
// does not hold fails loudly rather than publishing anything.
func TestSignUnpublishedArtifactFails(t *testing.T) {
	t.Parallel()
	store := memory.New()

	err := signRun(signConfig{
		Reference:  "hub.aggrega.ai/component/core/missing:9.9.9",
		SubjectRef: "9.9.9",
		Target:     store,
		Signer:     &signingtest.FakeSigner{},
	})
	if err == nil {
		t.Fatal("signRun should fail for an unpublished reference")
	}
	if !strings.Contains(err.Error(), "not published") {
		t.Errorf("error = %v, want a not-published diagnostic", err)
	}
}

// TestSignWithRealKeyedSignerRoundTrips proves the keyed path produces a
// real, parseable Sigstore bundle attached to the artifact — a fully
// offline end-to-end sign through the command layer.
func TestSignWithRealKeyedSignerRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	if _, err := oci.Push(ctx, store, "2.0.0", oci.MediaTypeComponent, []byte("keyed content")); err != nil {
		t.Fatalf("Push: %v", err)
	}

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyed, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	if err := signRun(signConfig{
		Reference:  "hub.aggrega.ai/component/core/api-server:2.0.0",
		SubjectRef: "2.0.0",
		Target:     store,
		Signer:     keyed,
	}); err != nil {
		t.Fatalf("signRun: %v", err)
	}

	refs, err := oci.ListReferrers(ctx, store, "2.0.0", signing.BundleMediaType)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListReferrers: refs=%d err=%v", len(refs), err)
	}
	payload, err := oci.FetchReferrerPayload(ctx, store, refs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}
	if _, err := signing.LoadBundle(payload); err != nil {
		t.Fatalf("attached bundle does not parse as a Sigstore bundle: %v", err)
	}
}

// TestSignKeylessRefusesWithoutLiveInfra proves the keyless signer surfaces
// ErrKeylessLive rather than silently producing anything when the live
// identity/Fulcio/Rekor endpoints are absent — there is no offline keyless
// shortcut.
func TestSignKeylessRefusesWithoutLiveInfra(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	signer := &signing.KeylessSigner{} // no IDToken/Fulcio/Rekor configured
	_, _, err := signer.Sign(ctx, "sha256:abc")
	if !errors.Is(err, signing.ErrKeylessLive) {
		t.Fatalf("want ErrKeylessLive, got %v", err)
	}
}
