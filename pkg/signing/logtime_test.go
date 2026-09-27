// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

const logTimeDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// keyedBundle signs digest with a fresh held key through the product's keyed
// signer, which records no log entry and no timestamp.
func keyedBundle(t *testing.T, digest string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	b, _, err := signer.Sign(context.Background(), digest)
	if err != nil {
		t.Fatalf("keyed Sign: %v", err)
	}
	return b
}

// TestCarriesLogTimeTellsAKeylessBundleFromEveryOther pins the one question a
// publication asks of the signature it attached: does it already carry a time a
// transparency log attests? Each row names the defect it catches.
func TestCarriesLogTimeTellsAKeylessBundleFromEveryOther(t *testing.T) {
	t.Parallel()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	keyless, err := authority.Sign("publisher@example.test", "https://issuer.example.test", logTimeDigest, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("keyless Sign: %v", err)
	}
	cases := []struct {
		name   string
		defect string
		bundle []byte
		want   bool
	}{
		{name: "a keyless bundle carries one", defect: "a keyless publication also states its own date", bundle: keyless.Bundle, want: true},
		{name: "a keyed bundle carries none", defect: "a keyed publication states no date at all", bundle: keyedBundle(t, logTimeDigest), want: false},
		{name: "no bundle carries none", defect: "an unsigned publication states no date", bundle: nil, want: false},
		{name: "bytes that are not a bundle carry none", defect: "an unreadable signature is taken to carry a time", bundle: []byte(`{"not":"a bundle"}`), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := signing.CarriesLogTime(tc.bundle); got != tc.want {
				t.Fatalf("CarriesLogTime = %v; want %v (defect: %s)", got, tc.want, tc.defect)
			}
		})
	}
}

// TestAPinnedTrustRootVerifiesTheAuthoritysSignatureAndKeepsItsLogTime drives the
// byte-level keyless path a registry policy takes — a trusted root read from a
// file, a serialized bundle — over what signingtest.KeylessAuthority produces, so
// the tests that rely on that helper rely on something the product's own reader
// accepts. It also proves the logged time survives the serialization.
func TestAPinnedTrustRootVerifiesTheAuthoritysSignatureAndKeepsItsLogTime(t *testing.T) {
	t.Parallel()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	rootJSON, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	rootPath := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(rootPath, rootJSON, 0o644); err != nil {
		t.Fatalf("write trusted root: %v", err)
	}
	tm, err := signing.TrustedMaterialFromRootPath(rootPath)
	if err != nil {
		t.Fatalf("TrustedMaterialFromRootPath: %v", err)
	}
	sig, err := authority.Sign("publisher@example.test", "https://issuer.example.test", logTimeDigest, time.Now().Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	ids := []signing.TrustedIdentity{{Identity: "publisher@example.test", Issuer: "https://issuer.example.test"}}
	matched, err := signing.VerifyKeylessBundle(sig.Bundle, tm, ids, logTimeDigest)
	if err != nil {
		t.Fatalf("VerifyKeylessBundle under the pinned root: %v", err)
	}
	if !matched.LoggedAt.Equal(sig.LoggedAt) {
		t.Errorf("LoggedAt = %v; want the log's %v", matched.LoggedAt, sig.LoggedAt)
	}
	if matched.LoggedAt.Equal(sig.SignedAt.Truncate(time.Second)) {
		t.Errorf("LoggedAt %v equals the signing time; the timestamp authority's time was taken", matched.LoggedAt)
	}

	// Control: another authority's root does not verify it, so the row above is not
	// passing against a policy that accepts anything.
	foreign, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority (foreign): %v", err)
	}
	foreignJSON, err := foreign.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot (foreign): %v", err)
	}
	foreignPath := filepath.Join(t.TempDir(), "foreign_root.json")
	if err := os.WriteFile(foreignPath, foreignJSON, 0o644); err != nil {
		t.Fatalf("write foreign root: %v", err)
	}
	ftm, err := signing.TrustedMaterialFromRootPath(foreignPath)
	if err != nil {
		t.Fatalf("TrustedMaterialFromRootPath (foreign): %v", err)
	}
	if _, err := signing.VerifyKeylessBundle(sig.Bundle, ftm, ids, logTimeDigest); err == nil {
		t.Fatal("a foreign trust root verified the signature; the pinned-root row proves nothing")
	}
}
