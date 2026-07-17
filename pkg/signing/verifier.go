// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"time"

	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

// TrustedIdentity is one acceptable keyless signing identity: a
// certificate subject (the OIDC identity, e.g. an email or a CI workflow
// URI) paired with the OIDC issuer that vouched for it. A keyless
// verification policy carries an OR-set of these — a bundle verifies when
// its certificate matches ANY one identity in the set.
type TrustedIdentity struct {
	// Identity is the certificate subject alternative name — the OIDC
	// identity bound into the short-lived signing certificate (e.g.
	// "maintainer@example.com" or a CI workflow URI).
	Identity string
	// IdentityRegexp, when set, matches the certificate subject as a
	// regular expression instead of an exact string. Exactly one of
	// Identity or IdentityRegexp is set per entry.
	IdentityRegexp string
	// Issuer is the OIDC issuer URL that issued the identity token (e.g.
	// "https://accounts.google.com" or
	// "https://token.actions.githubusercontent.com").
	Issuer string
}

// VerifyKeyedBundle verifies a Sigstore bundle (as attached to an OCI
// referrer) against a pinned public key, fully offline. The bundle's
// message signature must verify against pubPEM, and the signed artifact
// must equal artifactDigest — the immutable manifest digest the publisher
// committed to. No identity provider, transparency log, or timestamp
// authority is contacted: a keyed bundle carries no certificate, so the
// trust anchor is the pinned key alone.
//
// This is the bundle-level counterpart to VerifyKeyed. VerifyKeyed checks
// a detached ECDSA signature over a raw payload; VerifyKeyedBundle checks
// a full Sigstore bundle's message signature against the pinned key and
// binds it to the artifact digest. They are deliberately distinct
// operations over distinct inputs — do not merge them.
//
// Returns nil on success; a wrapped ErrSignatureVerificationFailed when
// the bundle does not verify against the key or the artifact binding
// fails; ErrUntrustedIdentity is never returned here (keyed mode has no
// identity).
func VerifyKeyedBundle(bundleBytes, pubPEM []byte, artifactDigest string) error {
	b, err := LoadBundle(bundleBytes)
	if err != nil {
		return err
	}
	pub, err := cryptoutils.UnmarshalPEMToPublicKey(pubPEM)
	if err != nil {
		return fmt.Errorf("signing: parse pinned public key: %w", err)
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("signing: %w: pinned key is %T, want ECDSA P-256", ErrUnsupportedKey, pub)
	}
	verifier, err := signature.LoadECDSAVerifier(ecPub, crypto.SHA256)
	if err != nil {
		return fmt.Errorf("signing: load pinned-key verifier: %w", err)
	}
	// A keyed bundle has no certificate validity window, so the verifier
	// is unconstrained in time. WithNoObserverTimestamps tells the policy
	// the bundle carries no observer timestamp to require, matching the
	// offline keyed bundle (no tlog, no TSA).
	tm := root.NewTrustedPublicKeyMaterial(func(string) (root.TimeConstrainedVerifier, error) {
		return &timeUnconstrainedVerifier{verifier}, nil
	})

	sev, err := verify.NewVerifier(tm, verify.WithNoObserverTimestamps())
	if err != nil {
		return fmt.Errorf("signing: build keyed verifier: %w", err)
	}
	// The signed artifact is the digest string bytes (the keyed signer
	// signs PlainData over the digest), so the artifact binding is the
	// digest string itself.
	policy := verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader([]byte(artifactDigest))),
		verify.WithKey(),
	)
	if _, err := sev.Verify(b, policy); err != nil {
		return fmt.Errorf("signing: %w: %v", ErrSignatureVerificationFailed, err)
	}
	return nil
}

// KeylessIdentity is the certificate identity a keyless verification
// actually matched: the signer's certificate subject (the OIDC identity
// bound into the signing certificate) and the OIDC issuer that vouched for
// it. It is the verified provenance of one bundle — the truth read from the
// matched certificate, not the policy that admitted it — so two bundles can
// be compared for "same signer".
type KeylessIdentity struct {
	// Identity is the certificate subject alternative name — the OIDC
	// identity the signing certificate was issued for.
	Identity string
	// Issuer is the OIDC issuer recorded in the signing certificate.
	Issuer string
}

// VerifyKeylessBundle verifies a Sigstore bundle against a trust anchor
// (trustedMaterial) and an OR-set of acceptable signing identities. The
// bundle's certificate chain must root in the trust anchor, the signature
// must be recorded in a transparency log the anchor knows (the tlog
// threshold is one entry), the certificate identity must match ANY one of
// the identities, and the signed artifact must equal artifactDigest.
//
// trustedMaterial is the trust anchor: in production it is built from the
// real public-good Sigstore trust root (see VerifyKeylessLive) or from a
// pinned trust-root bundle (see TrustedMaterialFromRootPath); in tests it
// is an in-memory virtual Sigstore. The verification LOGIC is identical
// across anchors — only the anchor differs.
//
// On success it returns the certificate identity that actually matched —
// the verified signer's subject and issuer, read from the matched
// certificate — so callers can record the provenance and compare two
// bundles for a shared signer. The error is nil; ErrNoTrustedIdentities
// when identities is empty; a wrapped ErrUntrustedIdentity when the
// certificate matches no identity; a wrapped ErrSignatureVerificationFailed
// for any other verification failure (broken chain, missing tlog entry,
// artifact-digest mismatch, tampered bundle).
func VerifyKeylessBundle(bundleBytes []byte, trustedMaterial root.TrustedMaterial, identities []TrustedIdentity, artifactDigest string) (KeylessIdentity, error) {
	b, err := LoadBundle(bundleBytes)
	if err != nil {
		return KeylessIdentity{}, err
	}
	return verifyKeylessEntity(b, trustedMaterial, identities, artifactDigest)
}

// verifyKeylessEntity runs the keyless verification policy against an
// already-parsed signed entity. It is the seam the byte-oriented
// VerifyKeylessBundle wraps after LoadBundle, and the seam tests exercise
// directly with an in-memory virtual-Sigstore entity — the same
// verify.Verifier.Verify code path runs in both, so the keyless policy
// (cert-identity OR-set, tlog threshold, artifact-digest binding) is
// proven against a real trust anchor offline.
//
// On success it returns the matched certificate's verified identity (the
// signer subject and issuer), read from the verification result rather than
// from the policy, so the returned identity is the bundle's actual signer.
func verifyKeylessEntity(entity verify.SignedEntity, trustedMaterial root.TrustedMaterial, identities []TrustedIdentity, artifactDigest string) (KeylessIdentity, error) {
	if len(identities) == 0 {
		return KeylessIdentity{}, ErrNoTrustedIdentities
	}

	certIDs := make([]verify.CertificateIdentity, 0, len(identities))
	for _, id := range identities {
		ci, cerr := verify.NewShortCertificateIdentity(id.Issuer, "", id.Identity, id.IdentityRegexp)
		if cerr != nil {
			return KeylessIdentity{}, fmt.Errorf("signing: build identity policy for %q/%q: %w", id.Identity, id.Issuer, cerr)
		}
		certIDs = append(certIDs, ci)
	}

	sev, err := verify.NewVerifier(trustedMaterial,
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return KeylessIdentity{}, fmt.Errorf("signing: build keyless verifier: %w", err)
	}

	policyOpts := make([]verify.PolicyOption, 0, len(certIDs))
	for _, ci := range certIDs {
		policyOpts = append(policyOpts, verify.WithCertificateIdentity(ci))
	}
	policy := verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader([]byte(artifactDigest))),
		policyOpts...,
	)
	result, err := sev.Verify(entity, policy)
	if err != nil {
		// The library reports an identity mismatch distinctly from other
		// failures; map it to the identity sentinel so callers can tell
		// "wrong signer" from "broken signature".
		if isIdentityMismatch(err) {
			// The library detail names the certificate SAN it saw but not the
			// OIDC issuer, so a signer who presented the right identity under
			// an untrusted issuer cannot tell why the trusted-identity OR-set
			// rejected it. Name both when they can be read from the leaf
			// certificate; fall back to the library detail alone otherwise.
			if issuer, san, ok := mismatchCertIdentity(entity); ok {
				return KeylessIdentity{}, fmt.Errorf("signing: %w: %v; signer presented issuer %q with identity %q, but no trusted identity permits that issuer", ErrUntrustedIdentity, err, issuer, san)
			}
			return KeylessIdentity{}, fmt.Errorf("signing: %w: %v", ErrUntrustedIdentity, err)
		}
		return KeylessIdentity{}, fmt.Errorf("signing: %w: %v", ErrSignatureVerificationFailed, err)
	}
	return matchedKeylessIdentity(result), nil
}

// matchedKeylessIdentity extracts the verified signer identity from a
// verification result. The matched certificate's own subject and issuer
// (result.Signature.Certificate) are the truth — they carry the actual
// signer even when the policy matched by regexp — so they are preferred;
// the matched policy identity (result.VerifiedIdentity) is the fallback when
// no certificate summary is present.
func matchedKeylessIdentity(result *verify.VerificationResult) KeylessIdentity {
	if result == nil {
		return KeylessIdentity{}
	}
	if sig := result.Signature; sig != nil && sig.Certificate != nil {
		return KeylessIdentity{
			Identity: sig.Certificate.SubjectAlternativeName,
			Issuer:   sig.Certificate.Issuer,
		}
	}
	if vi := result.VerifiedIdentity; vi != nil {
		return KeylessIdentity{
			Identity: vi.SubjectAlternativeName.SubjectAlternativeName,
			Issuer:   vi.Issuer.Issuer,
		}
	}
	return KeylessIdentity{}
}

// mismatchCertIdentity reads the leaf certificate's OIDC issuer and subject
// alternative name from a signed entity for an identity-mismatch diagnostic.
// It surfaces only those two fields — the SAN and the OIDC issuer bound into
// the certificate — and nothing else the certificate carries.
//
// It is best-effort: a missing verification content, a nil leaf certificate,
// or a certificate that cannot be summarized yields ok=false, so the caller
// falls back to the library detail alone. A verification failure must never
// become an extraction panic.
func mismatchCertIdentity(entity verify.SignedEntity) (issuer, san string, ok bool) {
	vc, verr := entity.VerificationContent()
	if verr != nil {
		return "", "", false
	}
	leaf := vc.Certificate()
	if leaf == nil {
		return "", "", false
	}
	summary, serr := certificate.SummarizeCertificate(leaf)
	if serr != nil {
		return "", "", false
	}
	return summary.Extensions.Issuer, summary.SubjectAlternativeName, true
}

// timeUnconstrainedVerifier wraps a signature.Verifier as a
// root.TimeConstrainedVerifier that is valid at every instant. A keyed
// bundle carries no certificate, so there is no validity window to honor;
// the trust anchor is the pinned key, which does not expire.
type timeUnconstrainedVerifier struct {
	signature.Verifier
}

func (*timeUnconstrainedVerifier) ValidAtTime(_ time.Time) bool { return true }

// isIdentityMismatch reports whether err is the verify library's
// "certificate matched no trusted identity" failure, as opposed to a
// broken-chain / missing-tlog / artifact-mismatch failure. Callers map
// this to ErrUntrustedIdentity so "wrong signer" surfaces distinctly from
// "broken signature".
func isIdentityMismatch(err error) bool {
	var e *verify.ErrNoMatchingCertificateIdentity
	return errors.As(err, &e)
}
