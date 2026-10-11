// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docscache_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	testRef       = "reg.example.com/docs/aggrega-docs"
	testVersion   = "1.0.0"
	componentPage = "language-reference/entities/component.md"
	componentBody = "# Component\n\nA Component is a unit of compute.\n"
	authoringPage = "authoring/getting-started.md"
	authoringBody = "# Getting started\n\nDeclare an Aggregate.\n"
)

// admitAll is the admission of a reader whose check admits whatever the slot
// holds. It is what the cases that are not about admission read with, so each of
// them still passes through the read's admission step rather than around it.
func admitAll(string, [][]byte) error { return nil }

// buildBundle packs a deterministic gzipped tar of the given page->body entries.
func buildBundle(t *testing.T, pages map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// Sorted iteration for byte-stable archives.
	for _, name := range sortedKeys(pages) {
		body := []byte(pages[name])
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion sort — small maps, no import needed
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// signedBundleStore pushes a documentation bundle to an in-memory store, signs
// its manifest digest with a fresh held key, attaches the bundle as a signature
// referrer, and returns the store plus the pinned public-key PEM. It mirrors the
// pull-time signing pattern the catalog-entity and plugin paths use, so a
// documentation bundle verifies through the identical path.
func signedBundleStore(t *testing.T, bundle []byte) (*memory.Store, []byte) {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	dg, err := oci.Push(ctx, store, testVersion, oci.MediaTypeDocs, bundle, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	sig, artifactType, err := signer.Sign(ctx, dg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, testVersion, artifactType, sig); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}
	return store, signer.PublicKeyPEM()
}

// keyedPolicy is a configuration requiring a signature from the test registry and
// pinning pubPEM as the one key it trusts.
func keyedPolicy(pubPEM []byte) workspaceconfig.Config {
	return workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{
			{Host: "reg.example.com", Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(pubPEM)},
		},
	}
}

// keyedVerifier returns a VerifyAtPull that runs the SAME pull-time signature
// verification a catalog entity or plugin uses (pullverify.Verify), against a
// registry policy that requires signatures and pins the bundle's key, and hands
// back the bundles it verified.
func keyedVerifier(store oras.ReadOnlyTarget, pubPEM []byte) docscache.VerifyAtPull {
	cfg := keyedPolicy(pubPEM)
	return func(ctx context.Context, manifestDigest string) ([][]byte, error) {
		results, err := pullverify.Verify(ctx, []pullverify.Entity{
			{URI: testRef, ManifestDigest: manifestDigest, Host: "reg.example.com"},
		}, pullverify.Options{
			Config: cfg,
			Target: func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
				return store, testVersion, nil
			},
		})
		if err != nil {
			return nil, err
		}
		var verified [][]byte
		for _, r := range results {
			verified = append(verified, r.Bundle)
		}
		return verified, nil
	}
}

// keyedAdmission returns the read-time admission of a reader whose check pins
// pubPEM for the test registry, through the same offline decision a pull makes.
func keyedAdmission(pubPEM []byte) docscache.AdmitAtRead {
	cfg := keyedPolicy(pubPEM)
	return func(manifestDigest string, signatures [][]byte) error {
		_, err := pullverify.VerifyStored(cfg, pullverify.Entity{
			URI: testRef, ManifestDigest: manifestDigest, Host: "reg.example.com",
		}, signatures, nil)
		return err
	}
}

// TestResolveCacheAndServeOffline proves a signed documentation bundle resolves
// and caches under the cache root it is given on first use, keeping the
// signature its pull verified, and serves pages thereafter from that copy — the
// read takes no target and can never reach a registry.
func TestResolveCacheAndServeOffline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()

	bundle := buildBundle(t, map[string]string{
		componentPage: componentBody,
		authoringPage: authoringBody,
	})
	store, pubPEM := signedBundleStore(t, bundle)

	// First use: resolve through the OCI transport, verify-at-pull, cache locally.
	slot, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, keyedVerifier(store, pubPEM))
	if err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	if want := docscache.DocsBundlesRoot(root); filepath.Dir(filepath.Dir(slot)) != want {
		t.Errorf("the slot %s is not two levels beneath %s", slot, want)
	}
	for _, name := range []string{"blob", "manifest"} {
		if _, statErr := os.Stat(filepath.Join(slot, name)); statErr != nil {
			t.Fatalf("cache slot missing %s: %v", name, statErr)
		}
	}
	kept, err := os.ReadDir(filepath.Join(slot, "signatures"))
	if err != nil || len(kept) != 1 {
		t.Fatalf("the slot keeps %d signature(s) (%v), want the one the pull verified", len(kept), err)
	}

	// Offline thereafter: ReadPage takes no puller/target — structurally cannot
	// reach the network — yet serves the page from the warm cache, admitted by a
	// reader whose own check verifies the kept signature.
	data, mimeType, err := docscache.ReadPage(root, testRef, testVersion, componentPage, keyedAdmission(pubPEM))
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
	if string(data) != componentBody {
		t.Errorf("page body = %q, want %q", data, componentBody)
	}
	if mimeType != "text/markdown" {
		t.Errorf("mimeType = %q, want text/markdown", mimeType)
	}

	// A second page also serves from the same warm slot.
	authData, _, err := docscache.ReadPage(root, testRef, testVersion, authoringPage, keyedAdmission(pubPEM))
	if err != nil {
		t.Fatalf("ReadPage authoring: %v", err)
	}
	if string(authData) != authoringBody {
		t.Errorf("authoring body = %q, want %q", authData, authoringBody)
	}
}

// TestAReadIsServedOnlyWhatTheReadersCheckAdmits is the read-side admission at
// this layer: one cached copy, read by a reader whose check verifies the kept
// signature and by one whose check pins a different key. The first is served; the
// second is told the copy is not admitted, and that answer reads as no bundle.
func TestAReadIsServedOnlyWhatTheReadersCheckAdmits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, pubPEM := signedBundleStore(t, buildBundle(t, map[string]string{componentPage: componentBody}))
	_, otherPub := signedBundleStore(t, buildBundle(t, map[string]string{componentPage: componentBody}))
	if _, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, keyedVerifier(store, pubPEM)); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}

	if _, _, err := docscache.ReadPage(root, testRef, testVersion, componentPage, keyedAdmission(pubPEM)); err != nil {
		t.Fatalf("the reader whose check verifies the kept signature was refused: %v", err)
	}

	data, _, err := docscache.ReadPage(root, testRef, testVersion, componentPage, keyedAdmission(otherPub))
	var refused *docscache.NotAdmittedError
	if !errors.As(err, &refused) {
		t.Fatalf("a reader whose check pins another key was answered %q (%v), want a refusal", data, err)
	}
	if !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Errorf("a refused copy must answer as no bundle: %v", err)
	}
	if !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
		t.Errorf("the refusal must carry the check's own reason: %v", err)
	}
	if data != nil {
		t.Errorf("a refused copy served %q", data)
	}

	// The acquisition's question gets the same two answers.
	if err := docscache.Admitted(root, testRef, testVersion, keyedAdmission(pubPEM)); err != nil {
		t.Errorf("Admitted refused what the read serves: %v", err)
	}
	if err := docscache.Admitted(root, testRef, testVersion, keyedAdmission(otherPub)); !errors.As(err, &refused) {
		t.Errorf("Admitted admitted what the read refuses: %v", err)
	}
}

// TestAReadWithNoCheckIsRefused pins the fail-closed half: a read handed no
// admission check serves nothing, rather than serving bytes no check admitted.
func TestAReadWithNoCheckIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, _ := signedBundleStore(t, buildBundle(t, map[string]string{componentPage: componentBody}))
	if _, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	data, _, err := docscache.ReadPage(root, testRef, testVersion, componentPage, nil)
	var refused *docscache.NotAdmittedError
	if !errors.As(err, &refused) || data != nil {
		t.Fatalf("a read with no check answered %q (%v), want a refusal", data, err)
	}
}

// TestReadPageCacheMissIsLoud proves an unresolved bundle reads as a loud cache
// miss (ErrFetchNotFound), never a fetch — the read path is offline-only.
func TestReadPageCacheMissIsLoud(t *testing.T) {
	t.Parallel()
	_, _, err := docscache.ReadPage(t.TempDir(), testRef, testVersion, componentPage, admitAll)
	if !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Fatalf("want ErrFetchNotFound, got %v", err)
	}
}

// TestARelativeCacheRootIsRefused pins that a cache root is never resolved against
// wherever the process happens to stand: a write and a read under a relative root
// are refused and nothing is written.
func TestARelativeCacheRootIsRefused(t *testing.T) {
	t.Parallel()
	store, _ := signedBundleStore(t, buildBundle(t, map[string]string{componentPage: componentBody}))
	if _, err := docscache.ResolveAndCache(context.Background(), store, testRef, testVersion, "relative-root", nil); err == nil {
		t.Error("a write under a relative cache root was accepted")
	}
	if _, err := os.Stat("relative-root"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused write left something behind: %v", err)
	}
	if _, _, err := docscache.ReadPage("relative-root", testRef, testVersion, componentPage, admitAll); err == nil {
		t.Error("a read under a relative cache root was answered")
	}
}

// TestReadMissingPage proves a page absent from a present bundle reports the
// distinct ErrPageNotFound rather than a cache miss.
func TestReadMissingPage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, _ := signedBundleStore(t, buildBundle(t, map[string]string{componentPage: componentBody}))
	if _, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	_, _, err := docscache.ReadPage(root, testRef, testVersion, "language-reference/entities/missing.md", admitAll)
	if !errors.Is(err, docscache.ErrPageNotFound()) {
		t.Fatalf("want ErrPageNotFound, got %v", err)
	}
}

// TestVerifyAtPullRejectsWrongKey proves a bundle whose signature fails the
// pinned-key policy is rejected at pull and never populates the cache.
func TestVerifyAtPullRejectsWrongKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()

	store, _ := signedBundleStore(t, buildBundle(t, map[string]string{componentPage: componentBody}))
	_, otherPub := signedBundleStore(t, buildBundle(t, map[string]string{componentPage: componentBody}))

	_, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, keyedVerifier(store, otherPub))
	if !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
		t.Fatalf("want ErrSignatureVerificationFailed, got %v", err)
	}
	// The cache slot must not have been populated by a failed verify.
	if _, _, rerr := docscache.ReadPage(root, testRef, testVersion, componentPage, admitAll); !errors.Is(rerr, docscache.ErrFetchNotFound()) {
		t.Fatalf("failed verify should leave the cache empty, got %v", rerr)
	}
}

// TestResolveRejectsNonDocsMediaType proves a reference resolving to a non-docs
// artifact is rejected rather than cached as a documentation bundle.
func TestResolveRejectsNonDocsMediaType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, testVersion, oci.MediaTypeComponent, []byte("kind: Component\n"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	_, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, t.TempDir(), nil)
	if !errors.Is(err, docscache.ErrNotDocsBundle()) {
		t.Fatalf("want ErrNotDocsBundle, got %v", err)
	}
}

// TestReadPageDetectsTamper proves a bundle blob mutated on disk fails the
// on-read integrity self-verify rather than serving forged bytes.
func TestReadPageDetectsTamper(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	store, _ := signedBundleStore(t, buildBundle(t, map[string]string{componentPage: componentBody}))
	slot, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, nil)
	if err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, "blob"), []byte("forged"), 0o644); err != nil {
		t.Fatalf("tamper blob: %v", err)
	}
	_, _, rerr := docscache.ReadPage(root, testRef, testVersion, componentPage, admitAll)
	if !errors.Is(rerr, oci.ErrFetchIntegrity()) {
		t.Fatalf("want ErrFetchIntegrity, got %v", rerr)
	}
}

// TestReadPageAcceptsLeadingDoubleDotEntry is the third of four per-site cases
// covering a deliberate loosening.
//
// The bundle reader previously judged an entry name with a bare two-dot prefix
// test, so a page under a directory whose name begins with two dots read as a
// traversal attempt: the whole bundle became unreadable with an integrity
// refusal, because the scan halts on the first refused entry rather than
// skipping it. The predicate judges where the name lands, so the page serves.
//
// The refusal the loosening must not have taken with it is pinned separately by
// TestReadPageRejectsEscapingEntry below.
func TestReadPageAcceptsLeadingDoubleDotEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()

	const oddPage = "..shared/notes.md"
	const oddBody = "# Shared notes\n"
	store, _ := signedBundleStore(t, buildBundle(t, map[string]string{
		componentPage: componentBody,
		oddPage:       oddBody,
	}))
	if _, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}

	data, _, err := docscache.ReadPage(root, testRef, testVersion, oddPage, admitAll)
	if err != nil {
		t.Fatalf("ReadPage(%q): %v — a name beginning with two dots is an ordinary name, not a traversal", oddPage, err)
	}
	if string(data) != oddBody {
		t.Errorf("page body = %q, want %q", data, oddBody)
	}

	// A neighbouring page in the same bundle still serves, so the case above is
	// not passing because the whole scan was skipped.
	if _, _, err := docscache.ReadPage(root, testRef, testVersion, componentPage, admitAll); err != nil {
		t.Fatalf("ReadPage(%q): %v", componentPage, err)
	}
}

// TestReadPageRejectsEscapingEntry pins the refusal the loosening above must not
// have widened into: an entry that genuinely climbs above the bundle root is
// still an integrity failure.
func TestReadPageRejectsEscapingEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()

	store, _ := signedBundleStore(t, buildBundle(t, map[string]string{
		"../../etc/passwd": "root:x:0:0\n",
	}))
	if _, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, root, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	_, _, err := docscache.ReadPage(root, testRef, testVersion, componentPage, admitAll)
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("an escaping bundle entry must halt with ErrFetchIntegrity; got %v", err)
	}
}
