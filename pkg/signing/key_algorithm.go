// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
)

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
