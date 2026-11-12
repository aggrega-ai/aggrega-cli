// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package signing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

const testDigest = "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// keyedBundleForTest signs digest with a fresh held key and returns the
// bundle bytes and the matching public-key PEM.
func keyedBundleForTest(t *testing.T, digest string) (bundleBytes, pubPEM []byte) {
	t.Helper()
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundleBytes, _, err = signer.Sign(context.Background(), digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return bundleBytes, signer.PublicKeyPEM()
}

// TestVerifyKeyedBundleAcceptsValidSignature proves the offline keyed
// bundle verify accepts a bundle this process signed against the matching
// pinned public key and digest — no network, no identity provider.
func TestVerifyKeyedBundleAcceptsValidSignature(t *testing.T) {
	t.Parallel()
	bundleBytes, pubPEM := keyedBundleForTest(t, testDigest)
	if err := signing.VerifyKeyedBundle(bundleBytes, pubPEM, testDigest); err != nil {
		t.Fatalf("VerifyKeyedBundle: want success, got %v", err)
	}
}

// TestVerifyKeyedBundleRejectsWrongKey proves a bundle signed by one key
// does not verify against a different pinned key.
func TestVerifyKeyedBundleRejectsWrongKey(t *testing.T) {
	t.Parallel()
	bundleBytes, _ := keyedBundleForTest(t, testDigest)
	_, otherPub := keyedBundleForTest(t, testDigest)
	err := signing.VerifyKeyedBundle(bundleBytes, otherPub, testDigest)
	if !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
		t.Fatalf("want ErrSignatureVerificationFailed, got %v", err)
	}
}

// TestVerifyKeyedBundleRejectsWrongDigest proves the artifact binding
// holds: a bundle over one digest does not verify against a different
// digest, even with the right key.
func TestVerifyKeyedBundleRejectsWrongDigest(t *testing.T) {
	t.Parallel()
	bundleBytes, pubPEM := keyedBundleForTest(t, testDigest)
	const otherDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	err := signing.VerifyKeyedBundle(bundleBytes, pubPEM, otherDigest)
	if !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
		t.Fatalf("want ErrSignatureVerificationFailed, got %v", err)
	}
}

// TestVerifyKeyedBundleRejectsTamperedBundle proves a corrupted bundle
// does not verify.
func TestVerifyKeyedBundleRejectsTamperedBundle(t *testing.T) {
	t.Parallel()
	bundleBytes, pubPEM := keyedBundleForTest(t, testDigest)
	tampered := make([]byte, len(bundleBytes))
	copy(tampered, bundleBytes)
	// Flip a byte in the middle of the bundle JSON.
	tampered[len(tampered)/2] ^= 0xFF
	if err := signing.VerifyKeyedBundle(tampered, pubPEM, testDigest); err == nil {
		t.Fatal("VerifyKeyedBundle accepted a tampered bundle")
	}
}
