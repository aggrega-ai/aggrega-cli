// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"github.com/sigstore/sigstore-go/pkg/verify"

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
	matched, err := verifyKeylessEntity(entity, vs, ids, keylessDigest)
	if err != nil {
		t.Fatalf("verifyKeylessEntity: want success, got %v", err)
	}
	if matched.Identity != maintainerIdentity || matched.Issuer != googleIssuer {
		t.Fatalf("matched identity = %q/%q; want the signer %q/%q", matched.Identity, matched.Issuer, maintainerIdentity, googleIssuer)
	}
}

// TestVerifyKeylessAcceptsSecondIdentityInOrSet proves the OR semantics:
// a bundle signed under the CI identity verifies when the CI identity is
// any member of the set, even if listed second. The returned matched
// identity is the ACTUAL signer (the second OR-set entry), proving the
// match is read from the verified certificate rather than defaulting to the
// first configured identity.
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
	matched, err := verifyKeylessEntity(entity, vs, ids, keylessDigest)
	if err != nil {
		t.Fatalf("verifyKeylessEntity: want success, got %v", err)
	}
	if matched.Identity != ciIdentity || matched.Issuer != actionsIssuer {
		t.Fatalf("matched identity = %q/%q; want the actual signer %q/%q (not the first OR-set entry)", matched.Identity, matched.Issuer, ciIdentity, actionsIssuer)
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
	_, err = verifyKeylessEntity(entity, vs, ids, keylessDigest)
	if !errors.Is(err, ErrUntrustedIdentity()) {
		t.Fatalf("want ErrUntrustedIdentity, got %v", err)
	}
}

// TestVerifyKeylessRejectsWrongIssuer proves issuer matching: the right
// identity under the wrong issuer is not trusted. The diagnostic names BOTH
// the certificate SAN and the OIDC issuer, so a signer who presented the
// expected identity under an untrusted issuer can see that the issuer — not
// the identity — was the reason the trusted-identity OR-set rejected the
// bundle.
func TestVerifyKeylessRejectsWrongIssuer(t *testing.T) {
	t.Parallel()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	const untrustedIssuer = "https://evil-issuer.example"
	entity, err := vs.Sign(maintainerIdentity, untrustedIssuer, []byte(keylessDigest))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	ids := []TrustedIdentity{{Identity: maintainerIdentity, Issuer: googleIssuer}}
	_, err = verifyKeylessEntity(entity, vs, ids, keylessDigest)
	if !errors.Is(err, ErrUntrustedIdentity()) {
		t.Fatalf("want ErrUntrustedIdentity, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, untrustedIssuer) {
		t.Errorf("diagnostic must name the certificate OIDC issuer %q; got: %s", untrustedIssuer, msg)
	}
	if !strings.Contains(msg, maintainerIdentity) {
		t.Errorf("diagnostic must name the certificate SAN %q; got: %s", maintainerIdentity, msg)
	}
}

// TestMismatchCertIdentityBestEffort proves the identity-mismatch diagnostic
// extraction is best-effort: an entity whose verification content is
// unavailable, or whose leaf certificate is nil, yields ok=false without a
// panic, so a verify failure never becomes an extraction crash and the
// caller falls back to the SAN-only library detail.
func TestMismatchCertIdentityBestEffort(t *testing.T) {
	t.Parallel()
	cases := map[string]verify.SignedEntity{
		"verification-content-error": stubSignedEntity{vcErr: errors.New("no verification content")},
		"nil-leaf-certificate":       stubSignedEntity{vc: stubVerificationContent{}},
	}
	for name, entity := range cases {
		entity := entity
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			issuer, san, ok := mismatchCertIdentity(entity)
			if ok || issuer != "" || san != "" {
				t.Fatalf("want best-effort miss (\"\",\"\",false); got (%q,%q,%v)", issuer, san, ok)
			}
		})
	}
}

// stubSignedEntity is a minimal verify.SignedEntity whose only meaningful
// behavior is its VerificationContent result; the remaining methods satisfy
// the interface and are unused by mismatchCertIdentity.
type stubSignedEntity struct {
	vc    verify.VerificationContent
	vcErr error
}

func (s stubSignedEntity) VerificationContent() (verify.VerificationContent, error) {
	return s.vc, s.vcErr
}
func (stubSignedEntity) HasInclusionPromise() bool                          { return false }
func (stubSignedEntity) HasInclusionProof() bool                            { return false }
func (stubSignedEntity) SignatureContent() (verify.SignatureContent, error) { return nil, nil }
func (stubSignedEntity) Timestamps() ([][]byte, error)                      { return nil, nil }
func (stubSignedEntity) TlogEntries() ([]*tlog.Entry, error)                { return nil, nil }
func (stubSignedEntity) Version() (string, error)                           { return "", nil }

// stubVerificationContent is a verify.VerificationContent whose leaf
// certificate is nil, exercising the nil-certificate guard.
type stubVerificationContent struct{}

func (stubVerificationContent) Certificate() *x509.Certificate      { return nil }
func (stubVerificationContent) PublicKey() verify.PublicKeyProvider { return nil }
func (stubVerificationContent) CompareKey(any, root.TrustedMaterial) bool {
	return false
}
func (stubVerificationContent) ValidAtTime(time.Time, root.TrustedMaterial) bool {
	return false
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
	_, err = verifyKeylessEntity(entity, vs, ids, otherDigest)
	if !errors.Is(err, ErrSignatureVerificationFailed()) {
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
	_, err = verifyKeylessEntity(entity, foreign, ids, keylessDigest)
	if !errors.Is(err, ErrSignatureVerificationFailed()) {
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
	if _, err := verifyKeylessEntity(entity, vs, nil, keylessDigest); !errors.Is(err, ErrNoTrustedIdentities()) {
		t.Fatalf("want ErrNoTrustedIdentities, got %v", err)
	}
}
