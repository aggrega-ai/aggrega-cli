// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package publishops

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// TestBuildSignerDefaultsToPublicGood proves that with no supplied URLs or
// config, buildSigner produces a keyless signer that defaults Fulcio and Rekor
// to the public-good Sigstore instances. The RFC3161 timestamp authority is
// NOT defaulted — it is opt-in, so TimestampURL stays empty unless supplied;
// the keyless trusted timestamp comes from Rekor's transparency-log inclusion.
func TestBuildSignerDefaultsToPublicGood(t *testing.T) {
	t.Parallel()
	signer, err := buildSigner(signerParams{IDToken: signingtest.SubjectfulIDToken(t)})
	if err != nil {
		t.Fatalf("buildSigner: %v", err)
	}
	keyless, ok := signer.(*signing.KeylessSigner)
	if !ok {
		t.Fatalf("buildSigner returned %T, want *KeylessSigner", signer)
	}
	if keyless.FulcioURL != signing.DefaultFulcioURL {
		t.Errorf("FulcioURL = %q, want default %q", keyless.FulcioURL, signing.DefaultFulcioURL)
	}
	if keyless.RekorURL != signing.DefaultRekorURL {
		t.Errorf("RekorURL = %q, want default %q", keyless.RekorURL, signing.DefaultRekorURL)
	}
	if keyless.TimestampURL != "" {
		t.Errorf("TimestampURL = %q, want empty (timestamp authority is opt-in, not defaulted)", keyless.TimestampURL)
	}
}

// TestBuildSignerTimestampOptIn proves the RFC3161 timestamp authority is
// honored when supplied (the opt-in path), while Fulcio and Rekor still take
// their public-good defaults for the unset fields.
func TestBuildSignerTimestampOptIn(t *testing.T) {
	t.Parallel()
	signer, err := buildSigner(signerParams{
		IDToken:      signingtest.SubjectfulIDToken(t),
		TimestampURL: "https://tsa.private/api/v1/timestamp",
	})
	if err != nil {
		t.Fatalf("buildSigner: %v", err)
	}
	keyless := signer.(*signing.KeylessSigner)
	if keyless.TimestampURL != "https://tsa.private/api/v1/timestamp" {
		t.Errorf("TimestampURL = %q, want the explicitly-supplied endpoint", keyless.TimestampURL)
	}
	if keyless.FulcioURL != signing.DefaultFulcioURL {
		t.Errorf("FulcioURL = %q, want default for the unset field", keyless.FulcioURL)
	}
	if keyless.RekorURL != signing.DefaultRekorURL {
		t.Errorf("RekorURL = %q, want default for the unset field", keyless.RekorURL)
	}
}

// TestBuildSignerSuppliedURLOverridesDefault proves an explicitly supplied URL
// beats the public-good default in buildSigner.
func TestBuildSignerSuppliedURLOverridesDefault(t *testing.T) {
	t.Parallel()
	signer, err := buildSigner(signerParams{
		IDToken:   signingtest.SubjectfulIDToken(t),
		FulcioURL: "https://fulcio.private",
	})
	if err != nil {
		t.Fatalf("buildSigner: %v", err)
	}
	keyless := signer.(*signing.KeylessSigner)
	if keyless.FulcioURL != "https://fulcio.private" {
		t.Errorf("FulcioURL = %q, want explicit override", keyless.FulcioURL)
	}
	if keyless.RekorURL != signing.DefaultRekorURL {
		t.Errorf("RekorURL = %q, want default for the unset field", keyless.RekorURL)
	}
}
