// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build live_e2e

// Package main's live keyless end-to-end test. It is the one leg of the
// signing model that cannot run offline: a real keyless signature issued by a
// live OIDC identity provider, Fulcio, and Rekor, pushed to a deployed hub
// registry, then pulled into a wiped local cache and verified against the
// Sigstore trust root fetched via TUF. Every other leg of the model — keyed
// round-trips, the keyless verification policy against a real anchor, the
// air-gapped cache and pinned-root plumbing, the sign-on-publish guardrail —
// is proven offline in the unit and integration suites; only the live keyless
// publish/verify against the deployed hub is irreducibly live.
//
// This file is excluded from both the default unit run and the integration
// run by the `live_e2e` build tag, so it never runs by accident and never
// pulls live infrastructure into a routine test. It runs only when built with
// `-tags=live_e2e` AND the required credentials are present in the
// environment; with any credential absent it skips cleanly. It makes no faked
// assertions and never simulates a live success — without live infrastructure
// there is nothing to assert, so it skips rather than pretending.
//
// Run it explicitly against a deployed hub:
//
//	AGGREGA_LIVE_E2E_HUB=hub.example.com \
//	AGGREGA_LIVE_E2E_OIDC_TOKEN=<oidc-id-token> \
//	AGGREGA_LIVE_E2E_FULCIO_URL=https://fulcio.sigstore.dev \
//	AGGREGA_LIVE_E2E_REKOR_URL=https://rekor.sigstore.dev \
//	go test -tags=live_e2e -run TestLiveE2E ./cmd/aggrega/...
package main

import (
	"context"
	"os"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// liveE2EConfig holds the live-infrastructure coordinates and credentials the
// keyless end-to-end test needs. Every required field must be present or the
// test skips.
type liveE2EConfig struct {
	Hub          string
	OIDCToken    string
	FulcioURL    string
	RekorURL     string
	TimestampURL string
}

// liveE2EConfigFromEnv reads the live-e2e coordinates from the environment and
// reports whether every required credential is present. The timestamp
// authority is optional. When ok is false, missing names the first absent
// required variable for a clear skip message.
func liveE2EConfigFromEnv() (cfg liveE2EConfig, missing string, ok bool) {
	cfg = liveE2EConfig{
		Hub:          os.Getenv("AGGREGA_LIVE_E2E_HUB"),
		OIDCToken:    os.Getenv("AGGREGA_LIVE_E2E_OIDC_TOKEN"),
		FulcioURL:    os.Getenv("AGGREGA_LIVE_E2E_FULCIO_URL"),
		RekorURL:     os.Getenv("AGGREGA_LIVE_E2E_REKOR_URL"),
		TimestampURL: os.Getenv("AGGREGA_LIVE_E2E_TSA_URL"),
	}
	for _, req := range []struct {
		name, val string
	}{
		{"AGGREGA_LIVE_E2E_HUB", cfg.Hub},
		{"AGGREGA_LIVE_E2E_OIDC_TOKEN", cfg.OIDCToken},
		{"AGGREGA_LIVE_E2E_FULCIO_URL", cfg.FulcioURL},
		{"AGGREGA_LIVE_E2E_REKOR_URL", cfg.RekorURL},
	} {
		if req.val == "" {
			return cfg, req.name, false
		}
	}
	return cfg, "", true
}

// TestLiveE2E_KeylessPublishVerifyRoundTrip encodes the live keyless leg of
// the signing model: keyless-sign an artifact's manifest digest against live
// Fulcio + Rekor, push the artifact and its signature referrer to the
// deployed hub, then verify the signature against the Sigstore trust root
// fetched via TUF — the air-gapped pinned-root path's live counterpart. It
// runs only with real credentials; without them it skips cleanly and makes no
// assertions.
func TestLiveE2E_KeylessPublishVerifyRoundTrip(t *testing.T) {
	cfg, missing, ok := liveE2EConfigFromEnv()
	if !ok {
		t.Skipf("live e2e requires AGGREGA_LIVE_E2E_* credentials; %s is unset — skipping", missing)
	}

	ctx := context.Background()

	// Push the artifact to the deployed hub and capture the manifest digest
	// the signature commits to.
	const repo = "component/core/api-server"
	const tag = "1.0.0"
	artifact := []byte("apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: Component\n" +
		"metadata:\n  name: core/api-server\n  version: 1.0.0\n")

	target, err := remote.NewRepository(cfg.Hub + "/" + repo)
	if err != nil {
		t.Fatalf("open hub repository: %v", err)
	}
	manifestDigest, err := oci.Push(ctx, target, tag, oci.MediaTypeComponent, artifact)
	if err != nil {
		t.Fatalf("push to deployed hub: %v", err)
	}

	// Keyless-sign the manifest digest against live Fulcio + Rekor.
	signer := &signing.KeylessSigner{
		IDToken:      cfg.OIDCToken,
		FulcioURL:    cfg.FulcioURL,
		RekorURL:     cfg.RekorURL,
		TimestampURL: cfg.TimestampURL,
	}
	bundleBytes, artifactType, err := signer.Sign(ctx, manifestDigest)
	if err != nil {
		t.Fatalf("keyless sign against live Fulcio/Rekor: %v", err)
	}

	// Attach the signature bundle as a referrer to the pushed artifact on the
	// deployed hub.
	if _, err := oci.AttachReferrer(ctx, target, tag, artifactType, bundleBytes); err != nil {
		t.Fatalf("attach signature referrer on hub: %v", err)
	}

	// Pull leg: discover the signature referrer from the hub and fetch the
	// bundle bytes back, simulating a cache-wiped client that holds nothing
	// locally and must re-fetch from the registry.
	refs, err := oci.ListReferrers(ctx, target, tag, signing.BundleMediaType)
	if err != nil {
		t.Fatalf("list signature referrers on hub: %v", err)
	}
	if len(refs) == 0 {
		t.Fatalf("no signature referrer found on hub for %s", manifestDigest)
	}
	pulled, err := oci.FetchReferrerPayload(ctx, target, refs[0])
	if err != nil {
		t.Fatalf("fetch signature bundle from hub: %v", err)
	}

	// Verify the pulled bundle against the Sigstore trust root fetched via
	// TUF — the live keyless anchor. The trusted identity must match the
	// identity bound into the OIDC token used to sign; supply it via the same
	// environment so the assertion is meaningful only with real credentials.
	identity := os.Getenv("AGGREGA_LIVE_E2E_IDENTITY")
	issuer := os.Getenv("AGGREGA_LIVE_E2E_ISSUER")
	if identity == "" || issuer == "" {
		t.Skip("live keyless verify requires AGGREGA_LIVE_E2E_IDENTITY and AGGREGA_LIVE_E2E_ISSUER to assert the signer identity")
	}
	ids := []signing.TrustedIdentity{{Identity: identity, Issuer: issuer}}
	if err := signing.VerifyKeylessLive(pulled, ids, manifestDigest); err != nil {
		t.Fatalf("keyless verify against TUF-fetched root: %v", err)
	}
}
