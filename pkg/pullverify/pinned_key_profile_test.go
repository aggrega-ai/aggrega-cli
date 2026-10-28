// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullverify_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/sigstore/sigstore/pkg/cryptoutils"

	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestVerify_APinnedKeyOnAnyCurveButP256IsRefusedNamingTheCurve runs the pull's
// verify pass over a signed artifact in an in-memory store, with the registry's
// trust policy pinning a public key generated in memory. A P-256 pinned key — the
// signer's own — verifies; a pinned ECDSA key on P-224, P-384 or P-521 is refused
// with the unsupported-key condition naming the curve, before any signature is
// checked, so the refusal is the key's profile rather than a signature that failed
// against the wrong key.
func TestVerify_APinnedKeyOnAnyCurveButP256IsRefusedNamingTheCurve(t *testing.T) {
	t.Parallel()
	store, dg, signerPub := signedStore(t)

	verify := func(pubPEM []byte) error {
		cfg := workspaceconfig.Config{
			Registries: []workspaceconfig.RegistryConfig{
				{Host: "keyed.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(pubPEM)},
			},
		}
		ent := pullverify.Entity{URI: "keyed.example.com/component/core/api-server", ManifestDigest: dg, Host: "keyed.example.com"}
		_, err := pullverify.Verify(context.Background(), []pullverify.Entity{ent}, pullverify.Options{
			Config: cfg,
			Target: targetForStore(store, "1.0.0"),
		})
		return err
	}

	if err := verify(signerPub); err != nil {
		t.Fatalf("the signer's own P-256 pinned key did not verify: %v", err)
	}
	for _, c := range []elliptic.Curve{elliptic.P224(), elliptic.P384(), elliptic.P521()} {
		t.Run(c.Params().Name, func(t *testing.T) {
			t.Parallel()
			k, err := ecdsa.GenerateKey(c, rand.Reader)
			if err != nil {
				t.Fatalf("generate key: %v", err)
			}
			pubPEM, err := cryptoutils.MarshalPublicKeyToPEM(k.Public())
			if err != nil {
				t.Fatalf("marshal public key: %v", err)
			}
			err = verify(pubPEM)
			if !errors.Is(err, signing.ErrUnsupportedKey()) {
				t.Fatalf("a pinned key on %s was not refused with the unsupported-key condition: %v", c.Params().Name, err)
			}
			if want := "pinned key is an ECDSA key on curve " + c.Params().Name; !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
			if errors.Is(err, signing.ErrSignatureVerificationFailed()) {
				t.Errorf("the refusal also reports a failed signature, so the curve was not what refused it: %v", err)
			}
		})
	}
}
