// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"fmt"
)

// requireKeyProfile refuses a key that is not of the one key profile keyed
// signing uses — ECDSA on curve P-256, signing a SHA-256 digest, cosign's default
// — and returns nil for one that is. role names which key the refusal is about,
// as the sentence a holder reads puts it: "held" for the private key the keyed
// signer is handed, "pinned" for the public key a registry's trust policy pins.
//
// It is the one statement of that profile, and both checks call it: the signer
// when it loads a held key, and the verification when it reads a pinned one. A
// key on any other curve is refused as a key of another algorithm is, under the
// unsupported-key condition, naming what the key is — "an ECDSA key on curve
// P-384" — because the bundle a keyed signer writes records its digest and a key
// hint, not the curve, so a profile the two sides do not both hold to is not one
// the format carries.
//
// A key the holder chose is named by its algorithm, never by the type the decoder
// returned it as; see keyAlgorithm.
func requireKeyProfile(role string, key any) error {
	var curve elliptic.Curve
	switch k := key.(type) {
	case *ecdsa.PrivateKey:
		curve = k.Curve
	case *ecdsa.PublicKey:
		curve = k.Curve
	}
	if curve != nil && curve == elliptic.P256() {
		return nil
	}
	return fmt.Errorf("%w: %s key is %s, want ECDSA P-256", ErrUnsupportedKey(), role, keyAlgorithm(key))
}

// keyAlgorithm names the algorithm of a key a PEM block decoded into, in the
// words its holder knows it by, for a refusal of a key this package does not
// sign or verify with. The holder chose the key, not the type the decoder
// returned it as, so the refusal names the algorithm and never that type.
func keyAlgorithm(key any) string {
	switch k := key.(type) {
	case *rsa.PublicKey, *rsa.PrivateKey:
		return "an RSA key"
	case ed25519.PublicKey, ed25519.PrivateKey, *ed25519.PrivateKey:
		return "an Ed25519 key"
	case *ecdsa.PublicKey:
		return "an ECDSA key on curve " + k.Curve.Params().Name
	case *ecdsa.PrivateKey:
		return "an ECDSA key on curve " + k.Curve.Params().Name
	}
	return "a key of an algorithm this signing does not use"
}
