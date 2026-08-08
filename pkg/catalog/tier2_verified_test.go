// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"oras.land/oras-go/v2/content/memory"
)

// seedVerifiedEntitySlot pushes body to an in-memory CAS, pulls it to obtain the
// authoritative manifest bytes, and writes BOTH the content blob and the manifest
// blob into the workspace cache slot for uri@version — the exact two-blob slot a
// real Tier-3 pull writes. It returns the content pin and the manifest digest the
// pull recorded, so callers pass the same pins a lockfile would carry.
func seedVerifiedEntitySlot(t *testing.T, ws, cacheDir, uri, version, mediaType string, body []byte) (contentPin, manifestPin string) {
	t.Helper()
	mb, md := manifestBytesFor(t, mediaType, body)
	if err := oci.WriteCached(ws, cacheDir, uri, version, body); err != nil {
		t.Fatalf("seed write content: %v", err)
	}
	if err := oci.WriteCachedManifest(ws, cacheDir, uri, version, mb); err != nil {
		t.Fatalf("seed write manifest: %v", err)
	}
	return contentDigest(body), md
}

// manifestBytesFor round-trips body through an in-memory CAS to obtain the
// authoritative OCI manifest bytes and digest a real pull carries — the manifest
// whose single layer commits to contentDigest(body). Shared by the internal-package
// tests that seed the two-blob cache slots the manifest-authority read requires.
func manifestBytesFor(t *testing.T, mediaType string, body []byte) (manifestBytes []byte, manifestDigest string) {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "seed", mediaType, body, nil); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	res, err := oci.Pull(ctx, store, "seed")
	if err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	return res.ManifestBytes, res.Digest
}

// TestTier2ReadVerified pins the verifying Tier-2 cache read under the manifest-
// authority model: the persisted manifest is the root the cached content is
// checked against. A faithful two-blob slot verifies; a wrong content pin, a
// tampered manifest, an incomplete slot (content without manifest — a
// fail-closed miss for the always-OCI entity tier), a missing slot, and an empty
// pin each halt with the expected sentinel.
func TestTier2ReadVerified(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const uri = "hub.aggrega.ai/schemalibrary/core/DbSizing:1.0.0"
	const version = "1.0.0"
	body := []byte("apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: SchemaLibrary\nmetadata:\n  name: DbSizing\n  version: 1.0.0\n" +
		"spec:\n  schemas:\n    Storage: {}\n")

	contentPin, manifestPin := seedVerifiedEntitySlot(t, ws, cacheDir, uri, version, oci.MediaTypeSchemaLibrary, body)

	t.Run("faithful slot returns the bytes", func(t *testing.T) {
		got, err := tier2ReadVerified(ws, cacheDir, uri, contentPin, manifestPin)
		if err != nil {
			t.Fatalf("verified read (match): %v", err)
		}
		if string(got) != string(body) {
			t.Errorf("bytes = %q; want %q", got, body)
		}
	})

	t.Run("content pin mismatch is ErrFetchIntegrity", func(t *testing.T) {
		_, err := tier2ReadVerified(ws, cacheDir, uri, "sha256:0000000000000000000000000000000000000000000000000000000000000000", manifestPin)
		if !errors.Is(err, oci.ErrFetchIntegrity()) {
			t.Errorf("error = %v; want ErrFetchIntegrity", err)
		}
	})

	t.Run("tampered manifest is ErrCachedManifestIntegrity", func(t *testing.T) {
		tws := t.TempDir()
		cp, mp := seedVerifiedEntitySlot(t, tws, cacheDir, uri, version, oci.MediaTypeSchemaLibrary, body)
		slot := oci.CachePath(tws, cacheDir, uri, version)
		if err := os.WriteFile(filepath.Join(slot, "manifest"), []byte("tampered-manifest"), 0o644); err != nil {
			t.Fatalf("tamper manifest: %v", err)
		}
		_, err := tier2ReadVerified(tws, cacheDir, uri, cp, mp)
		if !errors.Is(err, oci.ErrCachedManifestIntegrity()) {
			t.Errorf("error = %v; want ErrCachedManifestIntegrity", err)
		}
	})

	t.Run("manifest-less slot fails closed as a cache miss", func(t *testing.T) {
		iws := t.TempDir()
		if err := oci.WriteCached(iws, cacheDir, uri, version, body); err != nil {
			t.Fatalf("write content only: %v", err)
		}
		// A catalog entity is always OCI-distributed, so a slot that carries content
		// but no persisted manifest is an incomplete cache entry. The read fails
		// closed as a miss so the caller re-pulls — it does NOT fall back to the
		// content pin, even for faithful content matching the pin.
		if _, err := tier2ReadVerified(iws, cacheDir, uri, contentPin, manifestPin); !errors.Is(err, oci.ErrFetchNotFound()) {
			t.Errorf("manifest-less slot: err = %v; want ErrFetchNotFound (fail-closed miss)", err)
		}
	})

	t.Run("missing cache slot is ErrFetchNotFound", func(t *testing.T) {
		_, err := tier2ReadVerified(ws, cacheDir, "hub.aggrega.ai/schemalibrary/core/Absent:1.0.0", contentPin, manifestPin)
		if !errors.Is(err, oci.ErrFetchNotFound()) {
			t.Errorf("error = %v; want ErrFetchNotFound", err)
		}
	})

	t.Run("empty pin is a programming error, not a skip", func(t *testing.T) {
		if _, err := tier2ReadVerified(ws, cacheDir, uri, "", manifestPin); err == nil {
			t.Error("empty expected digest must error (no skip mode); got nil")
		}
	})
}

// TestTier2ReadVerifiedManifestAuthorityBeatsForgedPins is the entity tamper
// proof: with the content blob AND both recorded content pins (the lockfile
// content pin passed here, mirroring the metadata a real slot carries) forged to
// agree with a forged body, the untampered manifest — an independent record of
// the original content — still halts the read. The manifest is a root of trust
// beyond the content pin.
func TestTier2ReadVerifiedManifestAuthorityBeatsForgedPins(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const uri = "hub.aggrega.ai/schemalibrary/core/DbSizing:1.0.0"
	const version = "1.0.0"
	body := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: SchemaLibrary\nmetadata:\n  name: DbSizing\n  version: 1.0.0\nspec: {}\n")

	// Seed a faithful two-blob slot (content + manifest committing to the original
	// body), then overwrite ONLY the content blob with a forged body.
	_, manifestPin := seedVerifiedEntitySlot(t, ws, cacheDir, uri, version, oci.MediaTypeSchemaLibrary, body)
	forged := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: SchemaLibrary\nmetadata:\n  name: DbSizing\n  version: 1.0.0\nspec: {evil: true}\n")
	slot := oci.CachePath(ws, cacheDir, uri, version)
	if err := os.WriteFile(filepath.Join(slot, "blob"), forged, 0o644); err != nil {
		t.Fatalf("forge content: %v", err)
	}

	// The attacker also forges the content pin to match the forged body (as if the
	// lockfile / metadata were rewritten). The manifest self-verify pin is left as
	// the real one, mirroring the git-tracked independent record.
	forgedContentPin := contentDigest(forged)
	_, err := tier2ReadVerified(ws, cacheDir, uri, forgedContentPin, manifestPin)
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("forged content + forged content pin: err = %v; want ErrFetchIntegrity from the manifest-authority check", err)
	}
}
