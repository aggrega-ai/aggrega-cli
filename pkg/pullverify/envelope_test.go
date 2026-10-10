// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullverify_test

// A pull accepts a signature in either of two forms: the message signature the
// toolchain's signers write, and a signed envelope whose statement names the
// artifact as a subject. These tests drive the second form through the pull's own
// verification pass, keyless under a pinned trust root and keyed under a pinned
// key, and carry what the pass records on to the one decision a build makes over
// a recorded fact: the fact holds for the artifact it was verified for, and the
// same fact copied onto another artifact is refused as made for a different one.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	envelopeIdentity = "publisher@example.test"
	envelopeIssuer   = "https://issuer.example.test"
)

// envelopeSigned publishes an entity under tag to store, attaches sign's bundle
// over the manifest digest as a signature referrer, and returns the digest.
func envelopeSigned(t *testing.T, store *memory.Store, tag string, body []byte, sign func(manifestDigest string) []byte) string {
	t.Helper()
	ctx := context.Background()
	dg, err := oci.Push(ctx, store, tag, oci.MediaTypeComponent, body, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, tag, signing.BundleMediaType, sign(dg)); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}
	return dg
}

// TestThePullVerifiesASignedEnvelopeAndABuildHoldsItsFactToItsArtifact is the
// envelope form end to end through the pull's pass and the build's decision.
func TestThePullVerifiesASignedEnvelopeAndABuildHoldsItsFactToItsArtifact(t *testing.T) {
	t.Parallel()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	rootJSON, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	rootPath := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(rootPath, rootJSON, 0o644); err != nil {
		t.Fatalf("write trusted root: %v", err)
	}
	keylessEnvelope := func(manifestDigest string) []byte {
		sig, err := authority.Attest(envelopeIdentity, envelopeIssuer, signingtest.Statement(manifestDigest), time.Now().Add(2*time.Minute))
		if err != nil {
			t.Fatalf("Attest: %v", err)
		}
		return sig.Bundle
	}

	var keyedPub []byte
	keyedEnvelope := func(manifestDigest string) []byte {
		b, pub, err := signingtest.KeyedEnvelope(signingtest.Statement(manifestDigest))
		if err != nil {
			t.Fatalf("KeyedEnvelope: %v", err)
		}
		if keyedPub != nil {
			t.Fatal("the keyed case signs once per registry, under one pinned key")
		}
		keyedPub = pub
		return b
	}

	rows := []struct {
		name     string
		mode     string
		sign     func(string) []byte
		registry func() workspaceconfig.RegistryConfig
	}{
		{name: "keyless, under a pinned trust root", mode: "keyless", sign: keylessEnvelope,
			registry: func() workspaceconfig.RegistryConfig {
				return workspaceconfig.RegistryConfig{
					TrustedIdentities: []workspaceconfig.TrustedIdentity{{Identity: envelopeIdentity, Issuer: envelopeIssuer}},
					TrustedRoot:       rootPath,
				}
			}},
		{name: "keyed, under a pinned key", mode: "keyed", sign: keyedEnvelope,
			registry: func() workspaceconfig.RegistryConfig {
				return workspaceconfig.RegistryConfig{PublicKey: string(keyedPub)}
			}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			const host = "envelope.example.com"
			store := memory.New()
			signedDigest := envelopeSigned(t, store, "1.0.0",
				[]byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/signed\n  version: 1.0.0\n"), row.sign)
			otherDigest, err := oci.Push(context.Background(), store, "2.0.0", oci.MediaTypeComponent,
				[]byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/other\n  version: 2.0.0\n"), nil)
			if err != nil {
				t.Fatalf("Push: %v", err)
			}
			if otherDigest == signedDigest {
				t.Fatal("the two artifacts share a manifest, so this proves nothing")
			}
			registry := row.registry()
			registry.Host = host
			registry.Signing = workspaceconfig.SigningConfig{Required: true}
			cfg := workspaceconfig.Config{Registries: []workspaceconfig.RegistryConfig{registry}}
			ent := pullverify.Entity{URI: host + "/component/core/signed", ManifestDigest: signedDigest, Host: host}

			results, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
				Config: cfg,
				Target: targetForStore(store, "1.0.0"),
			})
			if err != nil {
				t.Fatalf("the pull's pass refused a signed envelope made for the artifact: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("got %d verified facts, want 1", len(results))
			}
			fact := results[0]
			if fact.Mode != row.mode || fact.BundleDigest == "" || len(fact.Bundle) == 0 {
				t.Fatalf("verified fact = mode %q, bundle digest %q, %d bundle bytes; want a %s fact carrying its bundle",
					fact.Mode, fact.BundleDigest, len(fact.Bundle), row.mode)
			}

			// The same envelope, asked about for another artifact, does not verify.
			if _, err := pullverify.VerifyBundle(cfg, fact.Bundle, otherDigest, registry); !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
				t.Fatalf("the envelope verified for an artifact it does not name: %v", err)
			}

			// What the pass recorded, cached as a pull caches it, is held by the build's
			// one decision to the artifact it was verified for, and refused where the
			// fact is copied onto another.
			ws := t.TempDir()
			const cacheDir = ".aggrega/cache"
			path := oci.SignatureBundlePath(ws, cacheDir, fact.BundleDigest)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, fact.Bundle, 0o644); err != nil {
				t.Fatal(err)
			}
			recorded := oci.RecordedBundle{Digest: fact.BundleDigest, RecordedIn: "release.lock"}
			if _, err := oci.HoldRecordedSignature(ws, cacheDir, recorded, oci.MadeForManifest(signedDigest)); err != nil {
				t.Fatalf("a build refuses the fact the pull recorded, on the artifact it was verified for: %v", err)
			}
			if _, err := oci.HoldRecordedSignature(ws, cacheDir, recorded, oci.MadeForManifest(otherDigest)); !errors.Is(err, oci.ErrSignatureForAnotherArtifact()) {
				t.Fatalf("a build accepted the fact copied onto another artifact; want the signature-for-another-artifact refusal, got %v", err)
			}
		})
	}
}
