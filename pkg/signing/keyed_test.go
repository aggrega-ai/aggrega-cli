// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestKeyedSignVerifyRoundTrip proves the offline keyed escape hatch end
// to end: generate a cosign-compatible ECDSA P-256 keypair, sign a
// payload with the held private key, and verify the signature against the
// public key — with no identity provider, transparency log, or timestamp
// authority contacted.
func TestKeyedSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()

	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}

	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	payload := []byte("sha256:0123456789abcdef artifact manifest digest")
	sig, err := signer.SignPayload(payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) == 0 {
		t.Fatal("Sign returned empty signature")
	}

	if err := signing.VerifyKeyed(pubPEM, payload, sig); err != nil {
		t.Fatalf("VerifyKeyed (valid case): %v", err)
	}

	// The signer's own PublicKeyPEM must verify identically to the PEM
	// returned at generation time.
	if err := signing.VerifyKeyed(signer.PublicKeyPEM(), payload, sig); err != nil {
		t.Fatalf("VerifyKeyed (signer's PublicKeyPEM): %v", err)
	}
}

// TestKeyedVerifyRejectsTamperedPayload proves verification fails closed:
// a payload that differs from the signed one does not verify, surfacing
// ErrSignatureInvalid.
func TestKeyedVerifyRejectsTamperedPayload(t *testing.T) {
	t.Parallel()

	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	sig, err := signer.SignPayload([]byte("original payload"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	err = signing.VerifyKeyed(pubPEM, []byte("tampered payload"), sig)
	if err == nil {
		t.Fatal("VerifyKeyed accepted a tampered payload")
	}
	if !errors.Is(err, signing.ErrSignatureInvalid) {
		t.Fatalf("want ErrSignatureInvalid, got %v", err)
	}
}

// TestKeyedVerifyRejectsWrongKey proves a signature does not verify under
// a different keypair's public key.
func TestKeyedVerifyRejectsWrongKey(t *testing.T) {
	t.Parallel()

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM (signing key): %v", err)
	}
	_, otherPubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM (other key): %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	payload := []byte("payload")
	sig, err := signer.SignPayload(payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := signing.VerifyKeyed(otherPubPEM, payload, sig); !errors.Is(err, signing.ErrSignatureInvalid) {
		t.Fatalf("want ErrSignatureInvalid for wrong key, got %v", err)
	}
}
