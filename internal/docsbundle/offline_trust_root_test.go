// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docsbundle

// An offline read under a keyless check that pins no trust root — the shape of the
// official hub's built-in policy — verifies the kept signature against the trust
// root an earlier online fetch left on this machine, never fetching one.
//
// None of these cases is parallel, for the reason the admission-memo cases give:
// they count the checks Admission ran, and the counter is the process's.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	keylessIdentity = "publisher@example.test"
	keylessIssuer   = "https://issuer.example.test"
)

// keylessAuthority returns in-memory signing authorities and the trust-root
// document that anchors what they sign.
func keylessAuthority(t *testing.T) (*signingtest.KeylessAuthority, []byte) {
	t.Helper()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	doc, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	return authority, doc
}

// keylessCopy caches, in a fresh root, a bundle no other case shares, keeping a
// keyless signature by identity over its manifest — what an earlier fetch that
// verified that signature leaves in the slot — and returns the root.
func keylessCopy(t *testing.T, authority *signingtest.KeylessAuthority, identity string) string {
	t.Helper()
	store, _ := publishUnique(t)
	keep := func(_ context.Context, manifestDigest string) ([][]byte, error) {
		sig, err := authority.Sign(identity, keylessIssuer, manifestDigest, time.Now().Add(time.Minute))
		if err != nil {
			return nil, err
		}
		return [][]byte{sig.Bundle}, nil
	}
	root := t.TempDir()
	if _, err := docscache.ResolveAndCache(context.Background(), store, testRef, testVersion, root, keep); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	return root
}

// heldTrustRoot returns a TUF client cache directory holding doc where the client
// keeps the trust root it last fetched, standing in for an earlier online fetch.
func heldTrustRoot(t *testing.T, doc []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := livetrust.CachedTrustedRootPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create the cache layout: %v", err)
	}
	if err := os.WriteFile(path, doc, 0o644); err != nil {
		t.Fatalf("write the held trust root: %v", err)
	}
	return dir
}

// keylessOffline is an offline workspace whose check for the test registry is
// keyless, requires a signature, trusts identity, and pins no trust root.
func keylessOffline(identity string) workspaceconfig.Config {
	return workspaceconfig.Config{Offline: true, Registries: []workspaceconfig.RegistryConfig{{
		Host:              testHost,
		Signing:           workspaceconfig.SigningConfig{Required: true},
		TrustedIdentities: []workspaceconfig.TrustedIdentity{{Identity: identity, Issuer: keylessIssuer}},
	}}}
}

// readIn reads the page out of the copy in root as a workspace configured as cfg
// reads it, under what ctx states.
func readIn(ctx context.Context, root string, cfg workspaceconfig.Config) error {
	_, _, err := docscache.ReadPage(root, testRef, testVersion, testPage, Admission(ctx, cfg, testRef, nil))
	return err
}

// TestAnOfflineReadVerifiesAgainstTheTrustRootLastFetched walks the offline read
// through each thing it can find on the machine. With the trust root an earlier
// fetch left, the copy is admitted, and a second read is answered from that
// admission. A different trust root held is verified for itself. Nothing held, and
// a signer the workspace does not trust, are each refused as no bundle — the first
// saying the check could not run offline. And the stated cache travels with
// Declarations to a context the read did not start on.
func TestAnOfflineReadVerifiesAgainstTheTrustRootLastFetched(t *testing.T) {
	authority, doc := keylessAuthority(t)
	root := keylessCopy(t, authority, keylessIdentity)
	withHeld := WithTrustRootCache(context.Background(), heldTrustRoot(t, doc))
	cfg := keylessOffline(keylessIdentity)

	if runs := runsDuring(func() {
		for i := range 2 {
			if err := readIn(withHeld, root, cfg); err != nil {
				t.Fatalf("offline read %d, with the trust root an earlier fetch left, was refused: %v", i+1, err)
			}
		}
	}); runs != 1 {
		t.Errorf("two offline reads of one copy under one check ran %d verifications, want 1", runs)
	}

	_, otherDoc := keylessAuthority(t)
	withOther := WithTrustRootCache(context.Background(), heldTrustRoot(t, otherDoc))
	if runs := runsDuring(func() {
		var refused *docscache.NotAdmittedError
		if err := readIn(withOther, root, cfg); !errors.As(err, &refused) {
			t.Errorf("a trust root that does not anchor the signature answered %v, want the copy refused: "+
				"it was served what another trust root admitted", err)
		}
	}); runs != 1 {
		t.Errorf("a different held trust root ran %d verifications, want its own 1", runs)
	}

	withNothing := WithTrustRootCache(context.Background(), t.TempDir())
	err := readIn(withNothing, root, cfg)
	var refused *docscache.NotAdmittedError
	if !errors.As(err, &refused) || !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Fatalf("offline with no trust root held answered %v, want the copy refused as no bundle", err)
	}
	if !errors.Is(err, egress.ErrOfflineEgress()) || !strings.Contains(err.Error(), "could not run offline") {
		t.Errorf("the refusal does not say the check could not run offline: %v", err)
	}

	// Refused as an untrusted signer, which is reported only once the signature's
	// chain has verified against the trust root — so the held root anchored the check.
	if err := readIn(withHeld, root, keylessOffline("someone-else@example.test")); !errors.As(err, &refused) ||
		!errors.Is(err, signing.ErrUntrustedIdentity()) {
		t.Errorf("offline, a signer the workspace does not trust answered %v, want the copy refused for its signer", err)
	}

	carried := DeclarationsOf(withHeld).Onto(context.Background())
	if err := readIn(carried, root, cfg); err != nil {
		t.Errorf("the stated trust-root cache did not travel to the context the read was answered on: %v", err)
	}
}
