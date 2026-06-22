// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"errors"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"
)

// These tests exercise the keyless verification policy against an
// in-memory virtual Sigstore: a complete Fulcio + Rekor + TSA whose
// TrustedMaterial feeds the SAME verify.Verifier.Verify code path the
// production keyless verify uses. The trust ANCHOR is virtual; the
// verification LOGIC (cert-identity OR-set matching, transparency-log
// threshold, certificate chain, artifact-digest binding) is the real one,
// proven offline. The only path not covered here is verification against
// the REAL public-good Sigstore (VerifyKeylessLive), which is live-only by
// nature — the anchor differs, the logic does not.
//
// The tests call the unexported verifyKeylessEntity seam because the
// virtual Sigstore produces an in-memory signed entity rather than
// serialized bundle bytes; the byte-oriented VerifyKeylessBundle is the
// thin LoadBundle wrapper over this same seam.

const keylessDigest = "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

const (
	maintainerIdentity = "maintainer@example.com"
	googleIssuer       = "https://accounts.google.com"
	ciIdentity         = "https://github.com/example/repo/.github/workflows/release.yml@refs/heads/main"
	actionsIssuer      = "https://token.actions.githubusercontent.com"
)

// TestVerifyKeylessAcceptsMatchingIdentity proves a bundle signed under an
// identity in the trusted OR-set verifies against the virtual trust
// anchor.
func TestVerifyKeylessAcceptsMatchingIdentity(t *testing.T) {
	t.Parallel()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	entity, err := vs.Sign(maintainerIdentity, googleIssuer, []byte(keylessDigest))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	ids := []TrustedIdentity{
		{Identity: maintainerIdentity, Issuer: googleIssuer},
		{Identity: ciIdentity, Issuer: actionsIssuer},
	}
	if err := verifyKeylessEntity(entity, vs, ids, keylessDigest); err != nil {
		t.Fatalf("verifyKeylessEntity: want success, got %v", err)
	}
}

// TestVerifyKeylessAcceptsSecondIdentityInOrSet proves the OR semantics:
// a bundle signed under the CI identity verifies when the CI identity is
// any member of the set, even if listed second.
func TestVerifyKeylessAcceptsSecondIdentityInOrSet(t *testing.T) {
	t.Parallel()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	entity, err := vs.Sign(ciIdentity, actionsIssuer, []byte(keylessDigest))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	ids := []TrustedIdentity{
		{Identity: maintainerIdentity, Issuer: googleIssuer},
		{Identity: ciIdentity, Issuer: actionsIssuer},
	}
	if err := verifyKeylessEntity(entity, vs, ids, keylessDigest); err != nil {
		t.Fatalf("verifyKeylessEntity: want success, got %v", err)
	}
}

// TestVerifyKeylessRejectsWrongIdentity proves a bundle signed under an
// identity NOT in the trusted set fails with ErrUntrustedIdentity — a real
// signature from an unexpected signer.
func TestVerifyKeylessRejectsWrongIdentity(t *testing.T) {
	t.Parallel()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	entity, err := vs.Sign("attacker@evil.example", googleIssuer, []byte(keylessDigest))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	ids := []TrustedIdentity{{Identity: maintainerIdentity, Issuer: googleIssuer}}
	err = verifyKeylessEntity(entity, vs, ids, keylessDigest)
	if !errors.Is(err, ErrUntrustedIdentity) {
		t.Fatalf("want ErrUntrustedIdentity, got %v", err)
	}
}

// TestVerifyKeylessRejectsWrongIssuer proves issuer matching: the right
// identity under the wrong issuer is not trusted.
func TestVerifyKeylessRejectsWrongIssuer(t *testing.T) {
	t.Parallel()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	entity, err := vs.Sign(maintainerIdentity, "https://evil-issuer.example", []byte(keylessDigest))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	ids := []TrustedIdentity{{Identity: maintainerIdentity, Issuer: googleIssuer}}
	err = verifyKeylessEntity(entity, vs, ids, keylessDigest)
	if !errors.Is(err, ErrUntrustedIdentity) {
		t.Fatalf("want ErrUntrustedIdentity, got %v", err)
	}
}

// TestVerifyKeylessRejectsWrongArtifactDigest proves the artifact binding:
// a bundle over one digest does not verify against a different digest even
// when the identity matches (tampered/substituted artifact).
func TestVerifyKeylessRejectsWrongArtifactDigest(t *testing.T) {
	t.Parallel()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	entity, err := vs.Sign(maintainerIdentity, googleIssuer, []byte(keylessDigest))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	ids := []TrustedIdentity{{Identity: maintainerIdentity, Issuer: googleIssuer}}
	const otherDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	err = verifyKeylessEntity(entity, vs, ids, otherDigest)
	if !errors.Is(err, ErrSignatureVerificationFailed) {
		t.Fatalf("want ErrSignatureVerificationFailed, got %v", err)
	}
}

// TestVerifyKeylessRejectsForeignTrustAnchor proves a bundle from one
// virtual Sigstore does not verify against a different virtual Sigstore's
// trust material — the certificate chain must root in the anchor.
func TestVerifyKeylessRejectsForeignTrustAnchor(t *testing.T) {
	t.Parallel()
	signer, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore (signer): %v", err)
	}
	entity, err := signer.Sign(maintainerIdentity, googleIssuer, []byte(keylessDigest))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	foreign, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore (foreign): %v", err)
	}
	ids := []TrustedIdentity{{Identity: maintainerIdentity, Issuer: googleIssuer}}
	err = verifyKeylessEntity(entity, foreign, ids, keylessDigest)
	if !errors.Is(err, ErrSignatureVerificationFailed) {
		t.Fatalf("want ErrSignatureVerificationFailed (foreign anchor), got %v", err)
	}
}

// TestVerifyKeylessRejectsEmptyIdentitySet proves an empty trusted-identity
// set is refused rather than treated as "trust everyone".
func TestVerifyKeylessRejectsEmptyIdentitySet(t *testing.T) {
	t.Parallel()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	entity, err := vs.Sign(maintainerIdentity, googleIssuer, []byte(keylessDigest))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := verifyKeylessEntity(entity, vs, nil, keylessDigest); !errors.Is(err, ErrNoTrustedIdentities) {
		t.Fatalf("want ErrNoTrustedIdentities, got %v", err)
	}
}
