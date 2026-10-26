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

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
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
// signature must verify against pubPEM, and the artifact it was made for
// must be artifactDigest — the immutable manifest digest the publisher
// committed to. No identity provider, transparency log, or timestamp
// authority is contacted: a keyed bundle carries no certificate, so the
// trust anchor is the pinned key alone.
//
// This is the bundle-level counterpart to VerifyKeyed. VerifyKeyed checks
// a detached ECDSA signature over a raw payload; VerifyKeyedBundle checks
// a full Sigstore bundle's signature against the pinned key and
// binds it to the artifact digest. They are deliberately distinct
// operations over distinct inputs — do not merge them.
//
// The bundle may be a message signature, the form the toolchain's signers
// write, or a signed envelope whose statement names the artifact as a subject;
// the verification library accepts both. Once it has, the bundle is held to the
// artifact a second time, by the toolchain's own reading of which artifact a
// signature was made for (see holdToTheArtifactVerified), so a bundle is
// accepted here only where that reading agrees — the same reading a build holds
// the recorded fact to.
//
// Returns nil on success; a wrapped ErrSignatureVerificationFailed when
// the bundle does not verify against the key, the artifact binding
// fails, or the toolchain's reading does not agree with it;
// ErrUntrustedIdentity is never returned here (keyed mode has no
// identity).
func VerifyKeyedBundle(bundleBytes, pubPEM []byte, artifactDigest string) error {
	if err := verifyKeyedWithLibrary(bundleBytes, pubPEM, artifactDigest); err != nil {
		return err
	}
	return holdToTheArtifactVerified(bundleBytes, artifactDigest)
}

// verifyKeyedWithLibrary is the verification library's half of
// VerifyKeyedBundle: the bundle verified against the pinned key and bound to
// the artifact digest by the library alone, before the toolchain's own reading
// is asked anything.
func verifyKeyedWithLibrary(bundleBytes, pubPEM []byte, artifactDigest string) error {
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
		return fmt.Errorf("%w: pinned key is %T, want ECDSA P-256", ErrUnsupportedKey(), pub)
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
		return fmt.Errorf("%w: %v", ErrSignatureVerificationFailed(), err)
	}
	return nil
}

// KeylessIdentity is what a keyless verification established about one
// bundle's signer: the certificate identity it actually matched — the
// signer's certificate subject (the OIDC identity bound into the signing
// certificate) and the OIDC issuer that vouched for it — and when the
// transparency log recorded the signature. It is the verified provenance of
// one bundle — the truth read from the matched certificate and the verified
// log entry, not the policy that admitted it — so two bundles can be compared
// for "same signer" by Identity and Issuer.
type KeylessIdentity struct {
	// Identity is the certificate subject alternative name — the OIDC
	// identity the signing certificate was issued for.
	Identity string
	// Issuer is the OIDC issuer recorded in the signing certificate.
	Issuer string
	// LoggedAt is the time the transparency log integrated the signature, in
	// UTC, as a verified log entry attests it with the log's signed promise.
	// Where several verified entries carry one, it is the earliest. It is the
	// zero time when no verified entry carries a time: a signed timestamp from
	// a timestamp authority is not read here.
	LoggedAt time.Time
}

// logTimestampType is the verification library's name for a verified
// timestamp that a transparency-log entry's integrated time supplied, as
// distinct from one a timestamp authority signed.
const logTimestampType = "Tlog"

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
// bundles for a shared signer, together with the time the transparency log
// recorded the signature, read from the verified log entry. The error is nil; ErrNoTrustedIdentities
// when identities is empty; a wrapped ErrUntrustedIdentity when the
// certificate matches no identity; a wrapped ErrSignatureVerificationFailed
// for any other verification failure (broken chain, missing tlog entry,
// artifact-digest mismatch, tampered bundle), and where the toolchain's own
// reading of which artifact the signature was made for does not agree with the
// library's verification (see holdToTheArtifactVerified). The bundle may be a
// message signature or a signed envelope whose statement names the artifact as
// a subject, as for VerifyKeyedBundle.
func VerifyKeylessBundle(bundleBytes []byte, trustedMaterial root.TrustedMaterial, identities []TrustedIdentity, artifactDigest string) (KeylessIdentity, error) {
	matched, err := verifyKeylessWithLibrary(bundleBytes, trustedMaterial, identities, artifactDigest)
	if err != nil {
		return KeylessIdentity{}, err
	}
	if err := holdToTheArtifactVerified(bundleBytes, artifactDigest); err != nil {
		return KeylessIdentity{}, err
	}
	return matched, nil
}

// verifyKeylessWithLibrary is the verification library's half of
// VerifyKeylessBundle: the serialized bundle parsed and verified by the library
// alone, before the toolchain's own reading is asked anything.
func verifyKeylessWithLibrary(bundleBytes []byte, trustedMaterial root.TrustedMaterial, identities []TrustedIdentity, artifactDigest string) (KeylessIdentity, error) {
	b, err := LoadBundle(bundleBytes)
	if err != nil {
		return KeylessIdentity{}, err
	}
	return verifyKeylessEntity(b, trustedMaterial, identities, artifactDigest)
}

// holdToTheArtifactVerified holds a bundle the verification library has just
// verified for artifactDigest to the toolchain's own reading of which artifact
// it was made for (oci.SignedArtifactOf), and refuses it where the two do not
// agree: where the reading recognises the bundle's form and does not find it made
// for artifactDigest, and where it does not recognise the form at all.
//
// It is the pull's half of one agreement. A build cannot verify a signature, so
// it holds a recorded fact to its artifact through that same reading of the
// cached bundle the fact names; a bundle a pull verified and the reading disagreed
// about would be one the build could not hold to its artifact, or would hold to
// another. Refusing it here keeps every recorded fact one the build's reading
// agrees with, and turns any divergence between the two readers into a refusal at
// the pull that met it rather than a mistake at some later build. The reading
// mirrors the library's own binding, and a parity test holds the two together, so
// over a bundle the library verifies neither refusal is expected; each is a
// guard, not a policy.
//
// Both refusals wrap ErrSignatureVerificationFailed — the artifact binding did not
// hold as the toolchain reads it — and say which case it is. No signature is
// verified here; the bundle's bytes are decoded and hashes compared.
func holdToTheArtifactVerified(bundleBytes []byte, artifactDigest string) error {
	signed, recognised := oci.SignedArtifactOf(bundleBytes)
	if !recognised {
		return fmt.Errorf("%w: its cryptographic check holds, but it is in a form from which the artifact it "+
			"was made for cannot be read", ErrSignatureVerificationFailed())
	}
	if !signed.MadeFor(artifactDigest) {
		return fmt.Errorf("%w: its cryptographic check holds, but it does not name %s as the artifact it "+
			"was made for", ErrSignatureVerificationFailed(), artifactDigest)
	}
	return nil
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
		return KeylessIdentity{}, ErrNoTrustedIdentities()
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
				return KeylessIdentity{}, fmt.Errorf("%w: %v; signer presented issuer %q with identity %q, but no trusted identity permits that issuer", ErrUntrustedIdentity(), err, issuer, san)
			}
			return KeylessIdentity{}, fmt.Errorf("%w: %v", ErrUntrustedIdentity(), err)
		}
		return KeylessIdentity{}, fmt.Errorf("%w: %v", ErrSignatureVerificationFailed(), err)
	}
	return matchedKeylessIdentity(result), nil
}

// matchedKeylessIdentity extracts the verified signer identity from a
// verification result. The matched certificate's own subject and issuer
// (result.Signature.Certificate) are the truth — they carry the actual
// signer even when the policy matched by regexp — so they are preferred;
// the matched policy identity (result.VerifiedIdentity) is the fallback when
// no certificate summary is present. The log time is read from the result's
// verified timestamps whichever of the two supplied the identity.
func matchedKeylessIdentity(result *verify.VerificationResult) KeylessIdentity {
	if result == nil {
		return KeylessIdentity{}
	}
	loggedAt := verifiedLogTime(result.VerifiedTimestamps)
	if sig := result.Signature; sig != nil && sig.Certificate != nil {
		return KeylessIdentity{
			Identity: sig.Certificate.SubjectAlternativeName,
			Issuer:   sig.Certificate.Issuer,
			LoggedAt: loggedAt,
		}
	}
	if vi := result.VerifiedIdentity; vi != nil {
		return KeylessIdentity{
			Identity: vi.SubjectAlternativeName.SubjectAlternativeName,
			Issuer:   vi.Issuer.Issuer,
			LoggedAt: loggedAt,
		}
	}
	return KeylessIdentity{LoggedAt: loggedAt}
}

// verifiedLogTime returns the earliest time among the verified timestamps a
// transparency-log entry supplied, in UTC, or the zero time when none did.
// Only those timestamps are read: a timestamp authority's signed time is a
// different source, and the log's time is the one a keyless verification here
// requires. The earliest is taken so the answer does not depend on the order
// the library lists several entries in.
func verifiedLogTime(timestamps []verify.TimestampVerificationResult) time.Time {
	var earliest time.Time
	for _, ts := range timestamps {
		if ts.Type != logTimestampType || ts.Timestamp.IsZero() {
			continue
		}
		if earliest.IsZero() || ts.Timestamp.Before(earliest) {
			earliest = ts.Timestamp
		}
	}
	if earliest.IsZero() {
		return time.Time{}
	}
	return earliest.UTC()
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
