// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/sigstore/sigstore/pkg/cryptoutils"
)

// profileDigest is the artifact digest the bundles below are signed for.
const profileDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestKeyedSigning_RefusesEveryKeyButECDSAP256AtBothChecks drives keys generated
// in memory through both checks keyed signing makes — the held-key check the
// signer runs when it loads a private key, and the pinned-key check a pull runs
// before verifying a bundle against a trust policy's public key — and reads the
// verdict each gives.
//
// ECDSA on P-256 signs, and its bundle verifies against its pinned public half.
// Every other ECDSA curve is refused at both checks with the unsupported-key
// condition, naming the curve. For the pinned-key check the bundle is a VALID one,
// signed with that very key by the signer's own bundle construction, so the
// refusal can only be the profile and never a signature that failed. RSA and
// Ed25519 are refused at both checks as they always have been.
func TestKeyedSigning_RefusesEveryKeyButECDSAP256AtBothChecks(t *testing.T) {
	t.Parallel()

	type row struct {
		name     string
		priv     any
		pub      any
		accepted bool
		names    string
	}
	ecdsaRow := func(c elliptic.Curve, accepted bool) row {
		k, err := ecdsa.GenerateKey(c, rand.Reader)
		if err != nil {
			t.Fatalf("generate %s key: %v", c.Params().Name, err)
		}
		return row{name: "ECDSA " + c.Params().Name, priv: k, pub: k.Public(), accepted: accepted,
			names: "an ECDSA key on curve " + c.Params().Name}
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	rows := []row{
		ecdsaRow(elliptic.P256(), true),
		ecdsaRow(elliptic.P224(), false),
		ecdsaRow(elliptic.P384(), false),
		ecdsaRow(elliptic.P521(), false),
		{name: "RSA", priv: rsaKey, pub: rsaKey.Public(), names: "an RSA key"},
		{name: "Ed25519", priv: edPriv, pub: edPub, names: "an Ed25519 key"},
	}

	// A P-256 bundle, the pinned-key check's input for keys that cannot sign one of
	// their own here.
	p256Key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	p256Bundle, _, err := (&KeyedSigner{signer: p256Key}).Sign(context.Background(), profileDigest)
	if err != nil {
		t.Fatalf("sign the P-256 bundle: %v", err)
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			der, err := x509.MarshalPKCS8PrivateKey(r.priv)
			if err != nil {
				t.Fatalf("marshal private key: %v", err)
			}
			privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
			pubPEM, err := cryptoutils.MarshalPublicKeyToPEM(r.pub)
			if err != nil {
				t.Fatalf("marshal public key: %v", err)
			}

			// The held-key check.
			signer, err := LoadKeyedSigner(privPEM, nil)
			if r.accepted {
				if err != nil {
					t.Fatalf("the held-key check refused %s: %v", r.name, err)
				}
				bundle, _, err := signer.Sign(context.Background(), profileDigest)
				if err != nil {
					t.Fatalf("sign: %v", err)
				}
				if err := VerifyKeyedBundle(bundle, pubPEM, profileDigest); err != nil {
					t.Fatalf("the %s bundle did not verify against its own pinned key: %v", r.name, err)
				}
				return
			}
			assertProfileRefusal(t, "held", r.names, err)

			// The pinned-key check: a bundle this key signed itself, where it can,
			// otherwise the P-256 bundle.
			bundle := p256Bundle
			if ec, ok := r.priv.(*ecdsa.PrivateKey); ok {
				bundle, _, err = (&KeyedSigner{signer: ec}).Sign(context.Background(), profileDigest)
				if err != nil {
					t.Fatalf("sign with the %s key: %v", r.name, err)
				}
			}
			assertProfileRefusal(t, "pinned", r.names, VerifyKeyedBundle(bundle, pubPEM, profileDigest))
		})
	}
}

// assertProfileRefusal fails unless err is the unsupported-key condition naming
// the key's role and what the key is.
func assertProfileRefusal(t *testing.T, role, names string, err error) {
	t.Helper()
	if !errors.Is(err, ErrUnsupportedKey()) {
		t.Fatalf("the %s-key check did not refuse with the unsupported-key condition: %v", role, err)
	}
	if want := role + " key is " + names; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not say %q: %v", want, err)
	}
	if errors.Is(err, ErrSignatureVerificationFailed()) {
		t.Errorf("the %s-key refusal also reports a failed signature, so the profile was not what refused it: %v", role, err)
	}
}
