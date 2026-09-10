// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrUnsupportedKey is returned when a held key supplied to the keyed
// signer is not an ECDSA P-256 key — the only key profile the keyed
// escape hatch supports, matching cosign's default.
func ErrUnsupportedKey() error { return errUnsupportedKey }

var errUnsupportedKey = cerrors.Register("unsupported-key", cerrors.Meta{
	ShortCode: "E0184",
	Message:   "signing: unsupported held-key type",
})

// ErrSignatureInvalid is returned when a signature does not verify
// against the supplied public key and payload. It is the negative
// outcome of an offline verification — the bytes were tampered, the
// wrong key was supplied, or the payload differs from what was signed.
func ErrSignatureInvalid() error { return errSignatureInvalid }

var errSignatureInvalid = cerrors.Register("signature-invalid", cerrors.Meta{
	ShortCode: "E0179",
	Message:   "signing: signature does not verify",
})

// ErrKeylessLive is returned by the keyless signer when invoked without
// a reachable identity provider, Fulcio, and Rekor. The keyless path is
// live-only: it issues an ephemeral certificate from an OIDC identity,
// records the signature in a transparency log, and discards the key. It
// cannot run offline, so it surfaces this sentinel rather than a partial
// or simulated result.
func ErrKeylessLive() error { return errKeylessLive }

var errKeylessLive = cerrors.Register("keyless-signing-requires-network", cerrors.Meta{
	ShortCode: "E0176",
	Message:   "signing: keyless signing requires a live identity provider, Fulcio, and Rekor",
})

// ErrSignatureVerificationFailed is returned when a signature bundle does
// not verify against the trust anchor and policy at pull time — a broken
// certificate chain, a missing transparency-log entry, an artifact-digest
// mismatch, a tampered bundle, or a keyed bundle that does not verify
// against the pinned public key. It is the catch-all negative outcome of
// bundle verification, distinct from ErrUntrustedIdentity (a structurally
// valid signature whose signer is not trusted).
func ErrSignatureVerificationFailed() error { return errSignatureVerificationFailed }

var errSignatureVerificationFailed = cerrors.Register("signature-verification-failed", cerrors.Meta{
	ShortCode: "E0180",
	Message:   "signing: signature bundle does not verify",
})

// ErrUntrustedIdentity is returned when a signature bundle verifies
// structurally — the chain roots in the trust anchor and the transparency
// record checks out — but the certificate's identity matches none of the
// registry's trusted identities. The artifact was signed by a real but
// unexpected signer; the operator must add the identity to the registry's
// trust policy or reject the artifact.
func ErrUntrustedIdentity() error { return errUntrustedIdentity }

var errUntrustedIdentity = cerrors.Register("untrusted-identity", cerrors.Meta{
	ShortCode: "E0185",
	Message:   "signing: signature identity is not trusted for this registry",
})

// ErrNoSignatureFound is returned when a registry requires signatures (or
// the hub baseline mandates them) but the pulled artifact carries no
// signature referrer to verify. An unsigned artifact published to a
// signature-requiring registry cannot be admitted; the operator must sign
// it or pull from a registry that does not require signatures.
func ErrNoSignatureFound() error { return errNoSignatureFound }

var errNoSignatureFound = cerrors.Register("no-signature-found", cerrors.Meta{
	ShortCode: "E0177",
	Message:   "signing: no signature found for an artifact that requires one",
})

// ErrNoTrustedIdentities is returned when a keyless verification is
// requested with an empty trusted-identity set. A keyless policy with no
// acceptable identities would admit any signer, so it is refused rather
// than treated as "trust everyone".
func ErrNoTrustedIdentities() error { return errNoTrustedIdentities }

var errNoTrustedIdentities = cerrors.Register("no-trusted-identities", cerrors.Meta{
	ShortCode: "E0178",
	Message:   "signing: keyless verification requires at least one trusted identity",
})

// ErrTrustRootLoad is returned when a pinned trust-root bundle cannot be
// loaded from the configured path — a missing file, malformed JSON, or a
// bundle the trust-root parser rejects. Air-gapped verification depends on
// the pinned root, so a load failure halts rather than silently falling
// back to a network fetch.
func ErrTrustRootLoad() error { return errTrustRootLoad }

var errTrustRootLoad = cerrors.Register("trust-root-not-loadable", cerrors.Meta{
	ShortCode: "E0182",
	Message:   "signing: cannot load pinned trust root",
})

// ErrUnsignedPushRefused is returned when an unsigned push targets a
// registry that requires signatures. Publishing unsigned content to a
// signature-requiring registry is refused outright — there is no flag to
// override it. The build halts and the operator must sign the artifact
// (or target a registry that does not require signatures) before the
// push can proceed.
func ErrUnsignedPushRefused() error { return errUnsignedPushRefused }

var errUnsignedPushRefused = cerrors.Register("unsigned-push-refused", cerrors.Meta{
	ShortCode: "E0183",
	Message:   "signing: registry requires signatures; refusing to push unsigned content",
})

// ErrSigningPolicyRequiresSignature is returned when the workspace signing
// policy requires a signature for the artifact kind being published and the
// push is unsigned. It is the org-policy counterpart of
// ErrUnsignedPushRefused: the requirement comes from the workspace policy
// (which applies to a scoped set of artifact kinds regardless of the
// destination registry), not from the destination registry's own
// signature requirement. Publishing an in-scope kind unsigned is refused
// outright — there is no flag to override it — and the operator must sign
// the artifact before the push can proceed.
func ErrSigningPolicyRequiresSignature() error { return errSigningPolicyRequiresSignature }

var errSigningPolicyRequiresSignature = cerrors.Register("signing-policy-requires-signature", cerrors.Meta{
	ShortCode: "E0181",
	Message:   "signing: signing policy requires a signature for this artifact kind; refusing to push unsigned content",
})
