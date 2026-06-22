// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import "errors"

// ErrUnsupportedKey is returned when a held key supplied to the keyed
// signer is not an ECDSA P-256 key — the only key profile the keyed
// escape hatch supports, matching cosign's default.
var ErrUnsupportedKey = errors.New("signing: unsupported held-key type")

// ErrSignatureInvalid is returned when a signature does not verify
// against the supplied public key and payload. It is the negative
// outcome of an offline verification — the bytes were tampered, the
// wrong key was supplied, or the payload differs from what was signed.
var ErrSignatureInvalid = errors.New("signing: signature does not verify")

// ErrKeylessLive is returned by the keyless signer when invoked without
// a reachable identity provider, Fulcio, and Rekor. The keyless path is
// live-only: it issues an ephemeral certificate from an OIDC identity,
// records the signature in a transparency log, and discards the key. It
// cannot run offline, so it surfaces this sentinel rather than a partial
// or simulated result.
var ErrKeylessLive = errors.New("signing: keyless signing requires a live identity provider, Fulcio, and Rekor")

// ErrUnsignedPushRefused is returned when an unsigned push targets a
// registry that requires signatures. Publishing unsigned content to a
// signature-requiring registry is refused outright — there is no flag to
// override it. The build halts and the operator must sign the artifact
// (or target a registry that does not require signatures) before the
// push can proceed.
var ErrUnsignedPushRefused = errors.New("signing: registry requires signatures; refusing to push unsigned content")
