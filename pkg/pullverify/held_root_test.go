// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullverify_test

// A held trust root: the file a read of kept signatures verifies a keyless check
// that pins no trust root against, in place of the live public root. These cases
// pin that it stands in for the live root and for nothing else — a skip, a pinned
// key, a pinned trust root and an artifact keeping no signature decide as they
// would without it — that it is an anchor and never an admission, and that a
// remembered verdict is keyed by what it holds.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	heldHost     = "registry.example.com"
	heldSubject  = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	heldIdentity = "publisher@example.test"
	heldIssuer   = "https://issuer.example.test"
)

// heldFile writes content to a fresh file and returns a HeldRoot naming it.
func heldFile(t *testing.T, content []byte) (pullverify.HeldRoot, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write the held trust root: %v", err)
	}
	return func() (string, error) { return path, nil }, path
}

// keylessRequiring is an offline configuration whose check for the test registry
// is keyless, requires a signature, trusts identity, and pins no trust root — the
// shape of the official hub's built-in policy. Offline, so the live root is
// refused rather than fetched wherever a case reaches for it.
func keylessRequiring(identity string) workspaceconfig.Config {
	return workspaceconfig.Config{Offline: true, Registries: []workspaceconfig.RegistryConfig{{
		Host:              heldHost,
		Signing:           workspaceconfig.SigningConfig{Required: true},
		TrustedIdentities: []workspaceconfig.TrustedIdentity{{Identity: identity, Issuer: heldIssuer}},
	}}}
}

// TestAHeldTrustRootStandsInForTheLiveOneOnlyWhereTheCheckPinsNone verifies one
// keyless signature over a grid of checks and held roots. Each row names the
// defect it catches.
func TestAHeldTrustRootStandsInForTheLiveOneOnlyWhereTheCheckPinsNone(t *testing.T) {
	t.Parallel()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	doc, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	sig, err := authority.Sign(heldIdentity, heldIssuer, heldSubject, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("keyless Sign: %v", err)
	}
	held, rootPath := heldFile(t, doc)
	errNone := errors.New("no trust root is held for this case")
	refusing := pullverify.HeldRoot(func() (string, error) { return "", errNone })

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyedSigner, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	keyedSig, _, err := keyedSigner.Sign(context.Background(), heldSubject)
	if err != nil {
		t.Fatalf("keyed Sign: %v", err)
	}
	pinning := func(mod func(*workspaceconfig.RegistryConfig)) workspaceconfig.Config {
		cfg := keylessRequiring(heldIdentity)
		mod(&cfg.Registries[0])
		return cfg
	}
	notRequiring := keylessRequiring(heldIdentity)
	notRequiring.Registries[0].Signing.Required = false

	entity := pullverify.Entity{URI: heldHost + "/docs/aggrega-docs", ManifestDigest: heldSubject, Host: heldHost}
	for _, tc := range []struct {
		name, defect string
		cfg          workspaceconfig.Config
		kept         [][]byte
		held         pullverify.HeldRoot
		admit        bool
		refusedFor   error
	}{
		{
			name: "keyless, no pinned root, the signer trusted", defect: "the held root does not stand in for the live one",
			cfg: keylessRequiring(heldIdentity), kept: [][]byte{sig.Bundle}, held: held, admit: true,
		},
		{
			name: "keyless, no pinned root, the signer not trusted", defect: "a held root admits what it anchors, whoever signed it, or never anchors the check at all",
			cfg: keylessRequiring("someone-else@example.test"), kept: [][]byte{sig.Bundle}, held: held,
			refusedFor: signing.ErrUntrustedIdentity(),
		},
		{
			name: "the same untrusted signer under the same root pinned", defect: "the held root decides differently from the same root pinned",
			cfg: pinning(func(r *workspaceconfig.RegistryConfig) {
				r.TrustedIdentities[0].Identity = "someone-else@example.test"
				r.TrustedRoot = rootPath
			}), kept: [][]byte{sig.Bundle}, held: refusing, refusedFor: signing.ErrUntrustedIdentity(),
		},
		{
			name: "keyless, no pinned root, nothing held", defect: "a missing held root is not the refusal",
			cfg: keylessRequiring(heldIdentity), kept: [][]byte{sig.Bundle}, held: refusing, refusedFor: errNone,
		},
		{
			name: "keyless, no pinned root, no held root handed", defect: "a read handed no held root does not refuse offline as before",
			cfg: keylessRequiring(heldIdentity), kept: [][]byte{sig.Bundle}, held: nil, refusedFor: egress.ErrOfflineEgress(),
		},
		{
			name: "a pinned trust root, nothing held", defect: "a pinned trust root is made to depend on a held one",
			cfg:  pinning(func(r *workspaceconfig.RegistryConfig) { r.TrustedRoot = rootPath }),
			kept: [][]byte{sig.Bundle}, held: refusing, admit: true,
		},
		{
			name: "a pinned key, nothing held", defect: "a keyed check is made to depend on a held root",
			cfg:  pinning(func(r *workspaceconfig.RegistryConfig) { r.PublicKey = string(keyedSigner.PublicKeyPEM()) }),
			kept: [][]byte{keyedSig}, held: refusing, admit: true,
		},
		{
			name: "no signature kept, none required, nothing held", defect: "an absent signature is refused for want of a root nothing needed",
			cfg: notRequiring, kept: nil, held: refusing, admit: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := pullverify.VerifyStored(tc.cfg, entity, tc.kept, tc.held, nil)
			if tc.admit {
				if err != nil {
					t.Fatalf("refused (%v); defect: %s", err, tc.defect)
				}
				return
			}
			if err == nil {
				t.Fatalf("admitted as %+v; defect: %s", res, tc.defect)
			}
			if tc.refusedFor != nil && !errors.Is(err, tc.refusedFor) {
				t.Errorf("refused for %v, want %v; defect: %s", err, tc.refusedFor, tc.defect)
			}
		})
	}
}

// TestAHeldTrustRootIsKeyedByWhatItHolds pins the verdict key to the held root's
// content, as a pinned root's is keyed by its bytes: a different root keys
// differently, the same bytes at another path key alike, a held root that cannot
// be named yields no key, and a held root answering "" — online — keys exactly as
// a read handed none. A check that pins a key or a trust root never asks it.
func TestAHeldTrustRootIsKeyedByWhatItHolds(t *testing.T) {
	t.Parallel()
	entity := pullverify.Entity{URI: heldHost + "/docs/aggrega-docs", ManifestDigest: heldSubject, Host: heldHost}
	kept := [][]byte{[]byte("a kept signature bundle")}
	cfg := keylessRequiring(heldIdentity)
	key := func(cfg workspaceconfig.Config, held pullverify.HeldRoot) string {
		t.Helper()
		k, ok := pullverify.StoredVerdictKey(cfg, entity, kept, held)
		if !ok {
			t.Fatal("no key was taken over a held root that can be read")
		}
		return k
	}

	first, _ := heldFile(t, []byte("the first trust root"))
	second, _ := heldFile(t, []byte("the second trust root"))
	sameBytesElsewhere, _ := heldFile(t, []byte("the first trust root"))
	original := key(cfg, first)
	if key(cfg, second) == original {
		t.Error("a different held trust root keyed like the first, so a verdict reached over one would answer for the other")
	}
	if key(cfg, sameBytesElsewhere) != original {
		t.Error("the same trust root at another path keyed differently: the key names a location, not what is held")
	}
	if k, ok := pullverify.StoredVerdictKey(cfg, entity, kept, func() (string, error) {
		return "", errors.New("nothing held")
	}); ok {
		t.Errorf("a held root that cannot be named yielded the key %s, so a verdict over it could be remembered", k)
	}

	online := keylessRequiring(heldIdentity)
	online.Offline = false
	if key(online, func() (string, error) { return "", nil }) != key(online, nil) {
		t.Error("a held root answering nothing keyed differently from none, so the online key moved")
	}

	asked := 0
	counting := pullverify.HeldRoot(func() (string, error) { asked++; return "", errors.New("must not be asked") })
	pinnedKey := keylessRequiring(heldIdentity)
	pinnedKey.Registries[0].PublicKey = "a pinned key"
	_, pinnedRootPath := heldFile(t, []byte("a pinned trust root"))
	pinnedRoot := keylessRequiring(heldIdentity)
	pinnedRoot.Registries[0].TrustedRoot = pinnedRootPath
	for _, c := range []workspaceconfig.Config{pinnedKey, pinnedRoot} {
		if _, ok := pullverify.StoredVerdictKey(c, entity, kept, counting); !ok {
			t.Error("a check pinning its own anchor yielded no key")
		}
	}
	if asked != 0 {
		t.Errorf("the held root was asked %d times by checks that pin their own anchor", asked)
	}
}
