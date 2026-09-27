// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullverify_test

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

	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const logTimeSubject = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

// TestVerifyBundleKeepsTheLogTimeOfAKeylessSignatureOnly proves the one
// verification home carries a keyless signature's logged time into the verified
// result, and that a keyed signature — which no log records — carries none. Each
// row names the defect it catches.
func TestVerifyBundleKeepsTheLogTimeOfAKeylessSignatureOnly(t *testing.T) {
	t.Parallel()
	const identity, issuer = "publisher@example.test", "https://issuer.example.test"

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
	keyless, err := authority.Sign(identity, issuer, logTimeSubject, time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatalf("keyless Sign: %v", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	keyed, _, err := signer.Sign(context.Background(), logTimeSubject)
	if err != nil {
		t.Fatalf("keyed Sign: %v", err)
	}

	cases := []struct {
		name     string
		defect   string
		bundle   []byte
		policy   workspaceconfig.RegistryConfig
		wantMode string
		want     time.Time
	}{
		{
			name:     "keyless under a pinned root",
			defect:   "the verified result drops the log's time",
			bundle:   keyless.Bundle,
			policy:   workspaceconfig.RegistryConfig{TrustedIdentities: []workspaceconfig.TrustedIdentity{{Identity: identity, Issuer: issuer}}, TrustedRoot: rootPath},
			wantMode: "keyless",
			want:     keyless.LoggedAt,
		},
		{
			name:     "keyed under the pinned key",
			defect:   "a keyed signature is given a time nothing attests",
			bundle:   keyed,
			policy:   workspaceconfig.RegistryConfig{PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))},
			wantMode: "keyed",
			want:     time.Time{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := pullverify.VerifyBundle(workspaceconfig.Config{}, tc.bundle, logTimeSubject, tc.policy)
			if err != nil {
				t.Fatalf("VerifyBundle: %v", err)
			}
			if res.Mode != tc.wantMode {
				t.Fatalf("mode = %q; want %q", res.Mode, tc.wantMode)
			}
			if !res.LoggedAt.Equal(tc.want) {
				t.Fatalf("LoggedAt = %v; want %v (defect: %s)", res.LoggedAt, tc.want, tc.defect)
			}
		})
	}
}
