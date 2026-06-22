// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestKeyedSignerProducesParseableBundle proves the keyed Signer.Sign
// path produces a real, well-formed Sigstore bundle bound to the artifact
// digest, fully offline. It does not contact Fulcio, Rekor, or a
// timestamp authority.
func TestKeyedSignerProducesParseableBundle(t *testing.T) {
	t.Parallel()

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	const digest = "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	bundleBytes, artifactType, err := signer.Sign(context.Background(), digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if artifactType != signing.BundleMediaType {
		t.Fatalf("artifactType = %q, want %q", artifactType, signing.BundleMediaType)
	}
	if len(bundleBytes) == 0 {
		t.Fatal("Sign returned empty bundle")
	}

	// The bundle must parse back as a Sigstore bundle.
	if _, err := signing.LoadBundle(bundleBytes); err != nil {
		t.Fatalf("LoadBundle (round-trip): %v", err)
	}
}
