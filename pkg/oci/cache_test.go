// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

func TestCachePathDeterministic(t *testing.T) {
	t.Parallel()
	a := oci.CachePath("/ws", ".aggrega/cache", "hub.aggrega.ai/component/core/api-server", "1.0.0")
	b := oci.CachePath("/ws", ".aggrega/cache", "hub.aggrega.ai/component/core/api-server", "1.0.0")
	if a != b {
		t.Errorf("two CachePath invocations returned different paths:\n  a: %s\n  b: %s", a, b)
	}
	if !strings.Contains(a, "cache") {
		t.Errorf("path should contain cache; got %s", a)
	}
	if !strings.Contains(a, "1.0.0") {
		t.Errorf("path should contain the version; got %s", a)
	}
	if strings.Contains(a, "/component/core/") {
		t.Errorf("URI separators should be percent-encoded inside the path segment; got %s", a)
	}
	if !strings.Contains(a, "/entities/") {
		t.Errorf("path should contain the entities subtree segment; got %s", a)
	}
}

// TestSignatureBundlePathStem pins the on-disk bundle filename form so routing
// the digest transform onto the shared digest-segment spelling stays
// byte-identical: a colon-form digest renders to the dash-form segment under
// the signatures/ subtree with the .bundle suffix.
func TestSignatureBundlePathStem(t *testing.T) {
	t.Parallel()
	const hex = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	got := oci.SignatureBundlePath(".aggrega/cache", "sha256:"+hex)
	want := filepath.Join(".aggrega/cache", "signatures", "sha256-"+hex+".bundle")
	if got != want {
		t.Errorf("SignatureBundlePath: got %q; want %q", got, want)
	}
}

func TestReadCachedMissReturnsErrFetchNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := oci.ReadCached(dir, ".aggrega/cache", "hub.aggrega.ai/component/foo", "1.0.0")
	if err == nil {
		t.Fatal("expected cache miss error; got nil")
	}
	if !errors.Is(err, oci.ErrFetchNotFound) {
		t.Errorf("error should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestWriteReadCachedRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := []byte("bundled-bytes-payload")
	if err := oci.WriteCached(dir, ".aggrega/cache", "hub.aggrega.ai/recipe/core/helm/mariadb", "1.0.0", want); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	got, err := oci.ReadCached(dir, ".aggrega/cache", "hub.aggrega.ai/recipe/core/helm/mariadb", "1.0.0")
	if err != nil {
		t.Fatalf("ReadCached: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("round-trip bytes mismatch; got %q want %q", got, want)
	}
}

func TestReadCachedManifestMissReturnsErrFetchNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A content blob without a manifest blob is an incomplete slot; the manifest
	// read must report the miss so the caller re-pulls rather than trusts it.
	if err := oci.WriteCached(dir, ".aggrega/cache", "hub.aggrega.ai/component/foo", "1.0.0", []byte("content")); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	_, err := oci.ReadCachedManifest(dir, ".aggrega/cache", "hub.aggrega.ai/component/foo", "1.0.0")
	if err == nil {
		t.Fatal("expected manifest cache miss error; got nil")
	}
	if !errors.Is(err, oci.ErrFetchNotFound) {
		t.Errorf("error should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestWriteReadCachedManifestRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const uri = "hub.aggrega.ai/recipe/core/helm/mariadb"
	content := []byte("content-blob")
	manifest := []byte(`{"schemaVersion":2,"layers":[]}`)
	if err := oci.WriteCached(dir, ".aggrega/cache", uri, "1.0.0", content); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	if err := oci.WriteCachedManifest(dir, ".aggrega/cache", uri, "1.0.0", manifest); err != nil {
		t.Fatalf("WriteCachedManifest: %v", err)
	}
	// Both blobs coexist in the same slot: the content blob is unaffected.
	gotContent, err := oci.ReadCached(dir, ".aggrega/cache", uri, "1.0.0")
	if err != nil {
		t.Fatalf("ReadCached: %v", err)
	}
	if string(gotContent) != string(content) {
		t.Errorf("content round-trip mismatch; got %q want %q", gotContent, content)
	}
	gotManifest, err := oci.ReadCachedManifest(dir, ".aggrega/cache", uri, "1.0.0")
	if err != nil {
		t.Fatalf("ReadCachedManifest: %v", err)
	}
	if string(gotManifest) != string(manifest) {
		t.Errorf("manifest round-trip mismatch; got %q want %q", gotManifest, manifest)
	}
}
