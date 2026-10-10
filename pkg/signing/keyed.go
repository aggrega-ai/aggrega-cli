// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"fmt"

	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

// keyedHash is the digest algorithm the keyed path signs over. ECDSA
// P-256 signs a SHA-256 digest, matching cosign's default key profile so
// a keypair generated here is interchangeable with a cosign-held keypair.
const keyedHash = crypto.SHA256

// KeyedSigner signs payloads with a held ECDSA P-256 private key — the
// offline escape hatch for environments that cannot reach a keyless
// identity provider. It produces a detached signature over the SHA-256
// digest of the payload; the matching public key (distributed out of
// band as a cosign-compatible PEM) verifies it. No identity provider,
// transparency log, or timestamp authority is contacted: the signature
// is a pure function of the payload and the private key.
//
// The keypair is cosign-compatible: a PEM produced by GenerateKeyedPEM
// loads in cosign, and a PEM produced by cosign loads in LoadKeyedSigner.
type KeyedSigner struct {
	sv     signature.SignerVerifier
	signer crypto.Signer
	pubPEM []byte
}

// GenerateKeyedPEM generates a fresh ECDSA P-256 keypair and returns the
// private and public keys as cosign-compatible PEM blocks. The private
// PEM is unencrypted; callers that need an encrypted key at rest supply
// their own passphrase handling around the returned bytes.
func GenerateKeyedPEM() (privPEM, pubPEM []byte, err error) {
	privPEM, pubPEM, err = cryptoutils.GeneratePEMEncodedECDSAKeyPair(elliptic.P256(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("signing: generate keypair: %w", err)
	}
	return privPEM, pubPEM, nil
}

// LoadKeyedSigner loads a held ECDSA private key from a cosign-compatible
// PEM block and returns a KeyedSigner backed by it. pf supplies the
// passphrase for an encrypted PEM; pass nil for an unencrypted key.
func LoadKeyedSigner(privPEM []byte, pf cryptoutils.PassFunc) (*KeyedSigner, error) {
	priv, err := cryptoutils.UnmarshalPEMToPrivateKey(privPEM, pf)
	if err != nil {
		return nil, fmt.Errorf("signing: parse private key: %w", err)
	}
	ecKey, ok := priv.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("signing: %w: held key is %T, want ECDSA P-256", ErrUnsupportedKey(), priv)
	}
	sv, err := signature.LoadSignerVerifier(priv, keyedHash)
	if err != nil {
		return nil, fmt.Errorf("signing: load signer: %w", err)
	}
	pubPEM, err := cryptoutils.MarshalPublicKeyToPEM(ecKey.Public())
	if err != nil {
		return nil, fmt.Errorf("signing: marshal public key: %w", err)
	}
	return &KeyedSigner{sv: sv, signer: ecKey, pubPEM: pubPEM}, nil
}

// PublicKeyPEM returns the cosign-compatible PEM block for the signer's
// public key, for distribution in a trust policy.
func (k *KeyedSigner) PublicKeyPEM() []byte {
	out := make([]byte, len(k.pubPEM))
	copy(out, k.pubPEM)
	return out
}

// SignPayload produces a detached ECDSA signature over the SHA-256 digest
// of payload. The returned bytes are the raw signature; VerifyKeyed (or
// any cosign-compatible verifier holding the public key) checks them.
// This is the low-level primitive; Sign wraps it in a Sigstore bundle for
// referrer attachment.
func (k *KeyedSigner) SignPayload(payload []byte) ([]byte, error) {
	sig, err := k.sv.SignMessage(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("signing: sign payload: %w", err)
	}
	return sig, nil
}

// VerifyKeyed checks sig against payload using the public key in pubPEM
// (a cosign-compatible PEM). It returns nil when the signature is valid
// and a non-nil error otherwise. Verification is fully offline.
//
// This verifies a DETACHED raw ECDSA signature over a payload — a
// different operation from VerifyKeyedBundle, which verifies a full
// Sigstore bundle's signature and binds it to an artifact digest.
// They consume different inputs (a raw sig vs a bundle) and serve different
// surfaces; do not merge them into one.
func VerifyKeyed(pubPEM, payload, sig []byte) error {
	pub, err := cryptoutils.UnmarshalPEMToPublicKey(pubPEM)
	if err != nil {
		return fmt.Errorf("signing: parse public key: %w", err)
	}
	verifier, err := signature.LoadVerifier(pub, keyedHash)
	if err != nil {
		return fmt.Errorf("signing: load verifier: %w", err)
	}
	if err := verifier.VerifySignature(bytes.NewReader(sig), bytes.NewReader(payload)); err != nil {
		return fmt.Errorf("signing: %w: %v", ErrSignatureInvalid(), err)
	}
	return nil
}
