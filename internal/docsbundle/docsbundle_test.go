// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package docsbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	testHost    = "reg.example.invalid"
	testRef     = testHost + "/docs/aggrega-docs"
	testVersion = "1.0.0"
	testPage    = "language-reference/entities/component.md"
	testBody    = "# Component\n"
)

// signedFixture publishes a documentation bundle carrying one page to an
// in-memory store, signs its manifest with a fresh key, attaches the signature,
// and returns the store and the key's public PEM.
func signedFixture(t *testing.T) (*memory.Store, []byte) {
	t.Helper()
	ctx := context.Background()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: testPage, Mode: 0o644, Size: int64(len(testBody)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(testBody)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	dg, err := oci.Push(ctx, store, testVersion, oci.MediaTypeDocs, buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig, artifactType, err := signer.Sign(ctx, dg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oci.AttachReferrer(ctx, store, testVersion, artifactType, sig); err != nil {
		t.Fatal(err)
	}
	return store, signer.PublicKeyPEM()
}

// requiring is a workspace configuration requiring a signature from the test
// registry and pinning key as the one it trusts.
func requiring(key []byte) workspaceconfig.Config {
	return workspaceconfig.Config{Registries: []workspaceconfig.RegistryConfig{
		{Host: testHost, Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(key)},
	}}
}

// skipping is a workspace configuration that turns signature verification off
// for the test registry.
func skipping() workspaceconfig.Config {
	return workspaceconfig.Config{Registries: []workspaceconfig.RegistryConfig{
		{Host: testHost, SkipSignatureVerification: true},
	}}
}

// countingTarget returns a TargetFor that counts its reaches and serves store
// with the production verification, built with Verifier.
func countingTarget(store oras.ReadOnlyTarget, reaches *int) TargetFor {
	return func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		*reaches++
		return store, Verifier(cfg, ref, version, store, warn), nil
	}
}

// TestCacheRootIsTheStatedOneOrElseTheProcessOwn pins the one resolution of where
// the bundle is kept: a root the context states wins, a stated empty root is a
// machine with none and says why, and with nothing stated it is the root every
// machine-wide cache of this process derives from — never a second derivation.
func TestCacheRootIsTheStatedOneOrElseTheProcessOwn(t *testing.T) {
	t.Parallel()
	stated := t.TempDir()
	if got, err := CacheRoot(WithCacheRoot(context.Background(), stated)); err != nil || got != stated {
		t.Errorf("a stated root resolved as (%q, %v), want %q", got, err, stated)
	}
	if got, err := CacheRoot(WithCacheRoot(context.Background(), "")); err == nil || got != "" {
		t.Errorf("a stated empty root resolved as (%q, %v), want no root and the reason", got, err)
	} else if !strings.Contains(err.Error(), "per-user cache directory") {
		t.Errorf("the reason does not say where the bundle is kept: %v", err)
	}
	process, ok := wasm.AggregaCacheRoot()
	got, err := CacheRoot(context.Background())
	if ok && (err != nil || got != process) {
		t.Errorf("with nothing stated the root is (%q, %v), want the process's own %q", got, err, process)
	}
	if !ok && err == nil {
		t.Errorf("no process root resolves, yet the bundle's root resolved as %q", got)
	}
}

// TestAWarmCopyTheWorkspaceAdmitsReachesNoRegistry pins the "no second download"
// half at the acquisition: one workspace acquires, and a second whose own check
// admits the kept signature finds the copy and reaches for nothing.
func TestAWarmCopyTheWorkspaceAdmitsReachesNoRegistry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, key := signedFixture(t)
	reaches := 0
	target := countingTarget(store, &reaches)

	if acquired, err := ensureCached(context.Background(), requiring(key), root, testRef, testVersion, target, nil); err != nil || !acquired {
		t.Fatalf("the first acquisition answered (%v, %v), want an acquired bundle", acquired, err)
	}
	if acquired, err := ensureCached(context.Background(), requiring(key), root, testRef, testVersion, target, nil); err != nil || acquired {
		t.Fatalf("the second acquisition answered (%v, %v), want the warm copy", acquired, err)
	}
	if reaches != 1 {
		t.Errorf("the registry was reached %d times, want once", reaches)
	}
}

// TestACopyTheWorkspaceRefusesIsFetchedAgainUnderItsCheck is the other half: a copy
// fetched under a skipping workspace keeps no signature, so a workspace requiring
// one refuses it and fetches the bundle again under its own check, after which the
// copy keeps the signature that check verified and is admitted. Where fetching
// again fails, the error says both what the copy here lacked and why the fetch
// failed, and the copy is answered as no bundle.
func TestACopyTheWorkspaceRefusesIsFetchedAgainUnderItsCheck(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, key := signedFixture(t)
	reaches := 0
	target := countingTarget(store, &reaches)

	if _, err := ensureCached(context.Background(), skipping(), root, testRef, testVersion, target, nil); err != nil {
		t.Fatalf("the skipping workspace's acquisition: %v", err)
	}
	if err := docscache.Admitted(root, testRef, testVersion, Admission(context.Background(), requiring(key), testRef, nil)); err == nil {
		t.Fatal("the requiring workspace admits a copy that keeps no signature, so the case below proves nothing")
	}

	// Fetching again fails: the refusal and the fetch's failure are both reported.
	errNoRegistry := errors.New("the test reaches no registry")
	broken := func(workspaceconfig.Config, string, string, func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		return nil, nil, errNoRegistry
	}
	_, err := ensureCached(context.Background(), requiring(key), root, testRef, testVersion, broken, nil)
	var refused *docscache.NotAdmittedError
	if !errors.As(err, &refused) || !errors.Is(err, errNoRegistry) {
		t.Fatalf("a failed second fetch answered %v, want the refusal and the fetch's own failure", err)
	}
	if !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Errorf("the refused copy must answer as no bundle: %v", err)
	}

	// Fetching again succeeds: the copy is replaced and admitted.
	if acquired, err := ensureCached(context.Background(), requiring(key), root, testRef, testVersion, target, nil); err != nil || !acquired {
		t.Fatalf("the requiring workspace's second fetch answered (%v, %v), want an acquired bundle", acquired, err)
	}
	if _, _, err := docscache.ReadPage(root, testRef, testVersion, testPage, Admission(context.Background(), requiring(key), testRef, nil)); err != nil {
		t.Errorf("the copy fetched under the requiring workspace's check is refused by it: %v", err)
	}
	if reaches != 2 {
		t.Errorf("the registry was reached %d times, want twice", reaches)
	}
}

// TestAnAcquisitionNamingNoTargetReachesTheOneTheContextStates pins the seam the
// command line and the language server are driven through: their acquisition
// names no target, so a target the context states is the one reached.
func TestAnAcquisitionNamingNoTargetReachesTheOneTheContextStates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, key := signedFixture(t)
	reaches := 0
	ctx := WithTarget(context.Background(), countingTarget(store, &reaches))
	if acquired, err := ensureCached(ctx, requiring(key), root, testRef, testVersion, nil, nil); err != nil || !acquired {
		t.Fatalf("the acquisition answered (%v, %v), want an acquired bundle", acquired, err)
	}
	if reaches != 1 {
		t.Errorf("the stated target was reached %d times, want once", reaches)
	}
}
