// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

// A recorded signature fact copied whole from one row onto another — or from one
// entry of the record beside the lockfile onto another — names a cached bundle
// that is intact and hashes to its own name, so a check that only re-hashes the
// bundle accepts it. What gives the copy away is the bundle itself: it says which
// artifact the signature was made for, and that is the other row's.
//
// The bundles here are real ones, made by the toolchain's own keyed signer over a
// manifest digest exactly as a publish makes them, so what the check reads is what
// a pull would have cached. Everything else is laid down by hand, which keeps each
// case to one edit.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// copiedFactSigner is a keyed signer the cases sign their bundles with.
func copiedFactSigner(t *testing.T) *signing.KeyedSigner {
	t.Helper()
	priv, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("generate a signing key: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(priv, nil)
	if err != nil {
		t.Fatalf("load the signing key: %v", err)
	}
	return signer
}

// signedFactFor signs madeFor — a manifest digest — with signer, caches the bundle
// at the slot a pull caches a verified bundle in, and returns the fact a pull
// records for it.
func signedFactFor(t *testing.T, root, cacheDir string, signer *signing.KeyedSigner, madeFor string) *lockfile.VerifiedSignature {
	t.Helper()
	bundle, _, err := signer.Sign(context.Background(), madeFor)
	if err != nil {
		t.Fatalf("sign %s: %v", madeFor, err)
	}
	bundleDigest := digest.ContentDigest(bundle)
	path := oci.SignatureBundlePath(root, cacheDir, bundleDigest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir the bundle cache: %v", err)
	}
	if err := os.WriteFile(path, bundle, 0o644); err != nil {
		t.Fatalf("cache the bundle: %v", err)
	}
	return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:copied-fact-probe", BundleDigest: bundleDigest}
}

// TestTheBuildRefusesASignatureCopiedFromAnotherLockfileRow is the first store:
// two Components the content cache served, each read under its own manifest, and
// the second row carrying the first row's fact. The bundle is intact and in the
// cache, so it re-hashes to its name; it was made for the first Component.
func TestTheBuildRefusesASignatureCopiedFromAnotherLockfileRow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := requiring()
	cacheDir := cfg.ResolvedArtifactCacheDir()
	manifestA := digest.ContentDigest([]byte("the manifest test/a was read under"))
	manifestB := digest.ContentDigest([]byte("the manifest test/b was read under"))
	factA := signedFactFor(t, root, cacheDir, copiedFactSigner(t), manifestA)
	copied := *factA

	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "test/a", ResolvedVersion: "1.0.0", ManifestDigest: manifestA, ResolutionPath: closure.ResolutionPathCache, KindHint: "component"},
		{URI: "test/b", ResolvedVersion: "1.0.0", ManifestDigest: manifestB, ResolutionPath: closure.ResolutionPathCache, KindHint: "component"},
	}}
	lf := &lockfile.Lockfile{Version: 1, Entities: []lockfile.Entity{
		{Kind: "component", URI: "test/a", ResolvedVersion: "1.0.0", ManifestDigest: manifestA, Signature: factA},
		{Kind: "component", URI: "test/b", ResolvedVersion: "1.0.0", ManifestDigest: manifestB, Signature: &copied},
	}}

	err := enforceRecordedSignatures(gateInputs(root, cfg, lf), cl)
	if err == nil {
		t.Fatal("the build accepted a signature copied onto test/b from test/a: the bundle it names is intact, and was made for test/a")
	}
	text := err.Error()
	if !strings.Contains(text, `"test/b"`) {
		t.Errorf("the refusal does not name the row carrying the copied signature:\n%s", text)
	}
	if strings.Contains(text, `"test/a"`) {
		t.Errorf("the refusal names the row whose own signature it is:\n%s", text)
	}
	if errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
		t.Errorf("the refusal reports a damaged bundle, and the bundle is intact:\n%s", text)
	}
	if errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Errorf("the refusal reports a missing signature, and one is recorded:\n%s", text)
	}
}

// TestTheBuildRefusesASignatureCopiedFromAnotherTrustRecordEntry is the second
// store: two plugins attached at a verdict role, each served by the content cache
// under its own manifest, and the second plugin's entry in the record beside the
// lockfile carrying the first plugin's fact.
func TestTheBuildRefusesASignatureCopiedFromAnotherTrustRecordEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	a := verdicttest.SeedCached(t, root, catalogDir, "linter", "copied-fact-a", "1.0.0")
	b := verdicttest.SeedCached(t, root, catalogDir, "linter", "copied-fact-b", "1.0.0")
	if a.ManifestDigest == b.ManifestDigest {
		t.Fatal("the two plugins share a manifest, so a signature made for one is made for the other and this proves nothing")
	}
	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	releasePath := filepath.Join(root, "release.yaml")
	factA := signedFactFor(t, root, cacheDir, copiedFactSigner(t), a.ManifestDigest)
	copied := *factA

	trust := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
	trust.Upsert(pluginstrust.TrustedPlugin{Plugin: a.Coordinate, ResolvedVersion: "1.0.0", Digest: a.WasmDigest, Signature: factA})
	trust.Upsert(pluginstrust.TrustedPlugin{Plugin: b.Coordinate, ResolvedVersion: "1.0.0", Digest: b.WasmDigest, Signature: &copied})
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), trust); err != nil {
		t.Fatalf("write the record: %v", err)
	}
	cfg := requiring()
	cfg.Plugins.Linter = []workspaceconfig.PluginRef{{URI: a.URI}, {URI: b.URI}}
	view := catalog.NewLazyLookup(root, cacheDir, nil, nil)

	err := enforceRecordedSignatures(NewPreviewInputs(NoExternalInputs(), view, releasePath, root, cfg, "0.0.0-signature-gate-probe"), closure.Closure{})
	if err == nil {
		t.Fatalf("the build accepted a signature copied onto %s from %s: the bundle it names is intact, and was made for %s",
			b.Coordinate, a.Coordinate, a.Coordinate)
	}
	text := err.Error()
	if !strings.Contains(text, `"`+b.Coordinate+`"`) {
		t.Errorf("the refusal does not name the plugin whose entry carries the copied signature:\n%s", text)
	}
	if strings.Contains(text, `"`+a.Coordinate+`"`) {
		t.Errorf("the refusal names the plugin whose own signature it is:\n%s", text)
	}
	if errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
		t.Errorf("the refusal reports a damaged bundle, and the bundle is intact:\n%s", text)
	}
	if errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Errorf("the refusal reports a missing signature, and one is recorded:\n%s", text)
	}
}
