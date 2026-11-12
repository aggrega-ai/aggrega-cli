// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package signing

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"

	"github.com/sigstore/sigstore/pkg/cryptoutils"
)

// TestKeyAlgorithm_NamesTheAlgorithmNeverTheType pins the words a refusal of an
// unsupported key uses — the key's algorithm — and that a PEM holding one is refused
// with them on both paths, the held key and the pinned key.
func TestKeyAlgorithm_NamesTheAlgorithmNeverTheType(t *testing.T) {
	t.Parallel()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	for _, tc := range []struct {
		key  any
		want string
	}{
		{&rsaKey.PublicKey, "an RSA key"},
		{rsaKey, "an RSA key"},
		{edPub, "an Ed25519 key"},
		{edPriv, "an Ed25519 key"},
		{&ecKey.PublicKey, "an ECDSA key on curve P-384"},
		{"not a key", "a key of an algorithm this signing does not use"},
	} {
		if got := keyAlgorithm(tc.key); got != tc.want {
			t.Errorf("keyAlgorithm(%T) = %q, want %q", tc.key, got, tc.want)
		}
	}

	privPEM, err := cryptoutils.MarshalPrivateKeyToPEM(rsaKey)
	if err != nil {
		t.Fatalf("marshal RSA private key: %v", err)
	}
	_, err = LoadKeyedSigner(privPEM, nil)
	if !errors.Is(err, ErrUnsupportedKey()) {
		t.Fatalf("an RSA held key must be refused as unsupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "held key is an RSA key") || strings.Contains(err.Error(), "*rsa.") {
		t.Errorf("the refusal must name the algorithm, not the type: %v", err)
	}
}
