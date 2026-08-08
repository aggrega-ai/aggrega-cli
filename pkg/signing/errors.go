// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrUnsupportedKey is returned when a held key supplied to the keyed
// signer is not an ECDSA P-256 key — the only key profile the keyed
// escape hatch supports, matching cosign's default.
const ErrUnsupportedKey cerrors.Code = "signing/unsupported-key"

var _ = cerrors.Register(ErrUnsupportedKey, cerrors.Meta{
	Message: "signing: unsupported held-key type",
})

// ErrSignatureInvalid is returned when a signature does not verify
// against the supplied public key and payload. It is the negative
// outcome of an offline verification — the bytes were tampered, the
// wrong key was supplied, or the payload differs from what was signed.
const ErrSignatureInvalid cerrors.Code = "signing/signature-invalid"

var _ = cerrors.Register(ErrSignatureInvalid, cerrors.Meta{
	Message: "signing: signature does not verify",
})

// ErrKeylessLive is returned by the keyless signer when invoked without
// a reachable identity provider, Fulcio, and Rekor. The keyless path is
// live-only: it issues an ephemeral certificate from an OIDC identity,
// records the signature in a transparency log, and discards the key. It
// cannot run offline, so it surfaces this sentinel rather than a partial
// or simulated result.
const ErrKeylessLive cerrors.Code = "signing/keyless-live"

var _ = cerrors.Register(ErrKeylessLive, cerrors.Meta{
	Message: "signing: keyless signing requires a live identity provider, Fulcio, and Rekor",
})

// ErrSignatureVerificationFailed is returned when a signature bundle does
// not verify against the trust anchor and policy at pull time — a broken
// certificate chain, a missing transparency-log entry, an artifact-digest
// mismatch, a tampered bundle, or a keyed bundle that does not verify
// against the pinned public key. It is the catch-all negative outcome of
// bundle verification, distinct from ErrUntrustedIdentity (a structurally
// valid signature whose signer is not trusted).
const ErrSignatureVerificationFailed cerrors.Code = "signing/signature-verification-failed"

var _ = cerrors.Register(ErrSignatureVerificationFailed, cerrors.Meta{
	Message: "signing: signature bundle does not verify",
})

// ErrUntrustedIdentity is returned when a signature bundle verifies
// structurally — the chain roots in the trust anchor and the transparency
// record checks out — but the certificate's identity matches none of the
// registry's trusted identities. The artifact was signed by a real but
// unexpected signer; the operator must add the identity to the registry's
// trust policy or reject the artifact.
const ErrUntrustedIdentity cerrors.Code = "signing/untrusted-identity"

var _ = cerrors.Register(ErrUntrustedIdentity, cerrors.Meta{
	Message: "signing: signature identity is not trusted for this registry",
})

// ErrNoSignatureFound is returned when a registry requires signatures (or
// the hub baseline mandates them) but the pulled artifact carries no
// signature referrer to verify. An unsigned artifact published to a
// signature-requiring registry cannot be admitted; the operator must sign
// it or pull from a registry that does not require signatures.
const ErrNoSignatureFound cerrors.Code = "signing/no-signature-found"

var _ = cerrors.Register(ErrNoSignatureFound, cerrors.Meta{
	Message: "signing: no signature found for an artifact that requires one",
})

// ErrNoTrustedIdentities is returned when a keyless verification is
// requested with an empty trusted-identity set. A keyless policy with no
// acceptable identities would admit any signer, so it is refused rather
// than treated as "trust everyone".
const ErrNoTrustedIdentities cerrors.Code = "signing/no-trusted-identities"

var _ = cerrors.Register(ErrNoTrustedIdentities, cerrors.Meta{
	Message: "signing: keyless verification requires at least one trusted identity",
})

// ErrTrustRootLoad is returned when a pinned trust-root bundle cannot be
// loaded from the configured path — a missing file, malformed JSON, or a
// bundle the trust-root parser rejects. Air-gapped verification depends on
// the pinned root, so a load failure halts rather than silently falling
// back to a network fetch.
const ErrTrustRootLoad cerrors.Code = "signing/trust-root-load"

var _ = cerrors.Register(ErrTrustRootLoad, cerrors.Meta{
	Message: "signing: cannot load pinned trust root",
})

// ErrUnsignedPushRefused is returned when an unsigned push targets a
// registry that requires signatures. Publishing unsigned content to a
// signature-requiring registry is refused outright — there is no flag to
// override it. The build halts and the operator must sign the artifact
// (or target a registry that does not require signatures) before the
// push can proceed.
const ErrUnsignedPushRefused cerrors.Code = "signing/unsigned-push-refused"

var _ = cerrors.Register(ErrUnsignedPushRefused, cerrors.Meta{
	Message: "signing: registry requires signatures; refusing to push unsigned content",
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
const ErrSigningPolicyRequiresSignature cerrors.Code = "signing/signing-policy-requires-signature"

var _ = cerrors.Register(ErrSigningPolicyRequiresSignature, cerrors.Meta{
	Message: "signing: signing policy requires a signature for this artifact kind; refusing to push unsigned content",
})
