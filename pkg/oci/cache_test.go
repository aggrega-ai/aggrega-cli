// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

func TestCachePathDeterministic(t *testing.T) {
	t.Parallel()
	a, aerr := oci.CachePath("/ws", ".aggrega/cache", "hub.aggrega.ai/component/core/api-server", "1.0.0")
	b, berr := oci.CachePath("/ws", ".aggrega/cache", "hub.aggrega.ai/component/core/api-server", "1.0.0")
	if aerr != nil || berr != nil {
		t.Fatalf("CachePath refused a well-formed reference and version: %v / %v", aerr, berr)
	}
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

// TestCachePathRefusesAVersionThatLeavesTheCache holds the entity slot builder to
// refusing a version that would place the slot somewhere other than the place the
// version names under the cache: one row per way a value leaves its own directory,
// each asserted against the cache-location condition and against the value being
// named under its key, plus a positive control that a well-formed version is
// accepted.
func TestCachePathRefusesAVersionThatLeavesTheCache(t *testing.T) {
	t.Parallel()
	const uri = "hub.aggrega.ai/component/core/api-server"
	for _, tc := range []struct {
		name    string
		version string
		refused bool
	}{
		{"a plain semver is accepted", "1.0.0", false},
		{"a prerelease-and-build semver is accepted", "1.0.0-rc.1+build.5", false},
		{"a traversal", "../../../../../etc", true},
		{"an interior climb that resolves to a parent", "1.2.3/../../../x", true},
		{"a bare parent step", "..", true},
		{"a forward-slash separator", "1.0.0/x", true},
		{"a backslash separator", `1.0.0\x`, true},
		{"the current directory", ".", true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := oci.CachePath("/ws", ".aggrega/cache", uri, tc.version)
			if !tc.refused {
				if err != nil {
					t.Fatalf("CachePath refused a well-formed version %q: %v", tc.version, err)
				}
				return
			}
			if !errors.Is(err, oci.ErrCacheLocationOutsideCache()) {
				t.Fatalf("CachePath(version=%q): err = %v, path = %q; want the cache-location refusal", tc.version, err, got)
			}
			if got != "" {
				t.Errorf("a refused slot came back with a path: %q", got)
			}
			if !strings.Contains(err.Error(), "version "+strconv.Quote(tc.version)) {
				t.Errorf("refusal does not name the value under its key: %v", err)
			}
		})
	}
}

// TestCachePathRefusesAReferenceThatEncodesToADotDirectory holds the same builder
// to refusing a reference whose encoded segment is `.` or `..` — the reference
// occupies one directory too, and one that names a dot directory would move every
// slot for it. An ordinary reference encodes to a percent-escaped single segment
// and is accepted.
func TestCachePathRefusesAReferenceThatEncodesToADotDirectory(t *testing.T) {
	t.Parallel()
	if _, err := oci.CachePath("/ws", ".aggrega/cache", "..", "1.0.0"); !errors.Is(err, oci.ErrCacheLocationOutsideCache()) {
		t.Errorf("a reference of .. was not refused: %v", err)
	}
	if _, err := oci.CachePath("/ws", ".aggrega/cache", "hub.aggrega.ai/component/core/api-server", "1.0.0"); err != nil {
		t.Errorf("a well-formed reference was refused: %v", err)
	}
}

// TestSignatureBundlePathStem pins the on-disk bundle filename form so routing
// the digest transform onto the shared digest-segment spelling stays
// byte-identical: a colon-form digest renders to the dash-form segment under
// the signatures/ subtree of the workspace's cache, with the .bundle suffix.
func TestSignatureBundlePathStem(t *testing.T) {
	t.Parallel()
	const hex = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	got := oci.SignatureBundlePath("/ws", ".aggrega/cache", "sha256:"+hex)
	want := filepath.Join("/ws", ".aggrega/cache", "signatures", "sha256-"+hex+".bundle")
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
	if !errors.Is(err, oci.ErrFetchNotFound()) {
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
	if !errors.Is(err, oci.ErrFetchNotFound()) {
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
