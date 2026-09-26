// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// These tests hold a cache hit to one description: what the registry read that
// filled the slot reported — the version, the content digest and the manifest
// digest — marked as served from the cache. A re-pull records whatever the hit
// reports, so a hit that reports less than the cold pull did writes a different
// lockfile, and a hit reported as a fresh fetch is sent to the registry to have
// its signature checked again.

// TestACacheHitReportsTheManifestPersistedBesideIt drives a cold pull of a library
// through the registry tier and then a re-pull that honours the pin the cold pull
// recorded, with a puller that fails the test if reached. The re-pull is served
// from the cache slot, and it must report the manifest digest the cold pull did,
// derived from the manifest persisted beside the content.
func TestACacheHitReportsTheManifestPersistedBesideIt(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	ctx := context.Background()
	ws := t.TempDir()
	cat, err := catalog.LoadAll("", t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeSchemaLibrary, reuseLibBody("10Gi"), nil); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}
	axis := closure.LibraryAxisNamed("SchemaLibraries")

	cold, err := cat.ClosureLookup(&orasPullerAdapter{target: store}, ws, cacheDir).ResolveLibrary(ctx, axis, reuseURI)
	if err != nil {
		t.Fatalf("cold ResolveLibrary: %v", err)
	}
	if cold.Resolution.ResolutionPath != closure.ResolutionPathRegistry || cold.Resolution.ManifestDigest == "" {
		t.Fatalf("cold pull = %+v; want a registry fetch that records a manifest digest", cold.Resolution)
	}

	reuse := &catalog.ReuseVerification{PriorPins: map[string]catalog.Pin{reuseURI: {
		ResolvedVersion: cold.Resolution.ResolvedVersion,
		Digest:          cold.Resolution.Digest,
		ManifestDigest:  cold.Resolution.ManifestDigest,
	}}}
	warm, err := cat.ClosureLookupWithReuse(&failPuller{t: t}, ws, cacheDir, nil, nil, reuse).ResolveLibrary(ctx, axis, reuseURI)
	if err != nil {
		t.Fatalf("warm ResolveLibrary: %v", err)
	}
	if warm.Resolution.ResolutionPath != closure.ResolutionPathCache {
		t.Errorf("warm ResolutionPath = %q; want %q", warm.Resolution.ResolutionPath, closure.ResolutionPathCache)
	}
	if warm.Resolution.ResolvedVersion != cold.Resolution.ResolvedVersion || warm.Resolution.Digest != cold.Resolution.Digest {
		t.Errorf("warm (version, digest) = (%s, %s); want the cold pull's (%s, %s)",
			warm.Resolution.ResolvedVersion, warm.Resolution.Digest, cold.Resolution.ResolvedVersion, cold.Resolution.Digest)
	}
	if warm.Resolution.ManifestDigest != cold.Resolution.ManifestDigest {
		t.Errorf("warm ManifestDigest = %q; want the cold pull's %q, the digest of the manifest persisted beside the content",
			warm.Resolution.ManifestDigest, cold.Resolution.ManifestDigest)
	}
}

// TestACachedPluginArtifactIsReportedAsServedFromTheCache drives a cold pull of a
// plugin's .wasm through the registry tier and then an offline re-pull that honours
// the recorded pin. The re-pull reads the content-addressed slot and must say so:
// it is not a registry fetch, so nothing downstream should treat it as one — above
// all the signature pass, which asks the registry about every fetch it is handed.
func TestACachedPluginArtifactIsReportedAsServedFromTheCache(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	ctx := context.Background()
	dir := t.TempDir()
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wasmBytes := []byte("\x00asm\x01\x00\x00\x00 cached-artifact fixture")
	entityYAML := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: frp/cached\n  version: 1.0.0\n")
	const artifactURI = "frp/cached"
	const version = "1.0.0"
	store := memory.New()
	if _, err := oci.PushPluginManifest(ctx, store, version, entityYAML, wasmBytes, nil); err != nil {
		t.Fatalf("oci.PushPluginManifest: %v", err)
	}

	cold, err := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, cacheDir).ResolvePlugin(ctx, artifactURI, version)
	if err != nil {
		t.Fatalf("cold ResolvePlugin: %v", err)
	}
	if !closure.ResolvedFromRegistry(cold.ResolutionPath) || cold.ManifestDigest == "" {
		t.Fatalf("cold pull = %+v; want a registry fetch that records a manifest digest", cold)
	}

	reuse := &catalog.ReuseVerification{PriorPins: map[string]catalog.Pin{artifactURI: {
		ResolvedVersion: cold.ResolvedVersion, Digest: cold.Digest, ManifestDigest: cold.ManifestDigest,
	}}}
	warm, err := cat.ClosureLookupWithReuse(nil, dir, cacheDir, nil, nil, reuse).ResolvePlugin(ctx, artifactURI, version)
	if err != nil {
		t.Fatalf("offline ResolvePlugin: %v", err)
	}
	if warm.NotFound {
		t.Fatal("offline re-pull: got NotFound; want a cache hit")
	}
	if warm.ResolutionPath != closure.ResolutionPathCache {
		t.Errorf("cached .wasm ResolutionPath = %q; want %q — it was read from the cache, not fetched", warm.ResolutionPath, closure.ResolutionPathCache)
	}
	if closure.ResolvedFromRegistry(warm.ResolutionPath) {
		t.Error("a cached .wasm is reported as fetched from the registry, which sends it to the signature pass")
	}
	if warm.ResolvedVersion != cold.ResolvedVersion || warm.Digest != cold.Digest || warm.ManifestDigest != cold.ManifestDigest {
		t.Errorf("cached .wasm = (%s, %s, %s); want the cold pull's (%s, %s, %s)",
			warm.ResolvedVersion, warm.Digest, warm.ManifestDigest, cold.ResolvedVersion, cold.Digest, cold.ManifestDigest)
	}
}

// TestAReusedSlotWhoseManifestMovedIsFetchedAgain holds the re-pull's reuse check
// to the whole pin. Once a cache hit reports the manifest it holds, a check on the
// content alone would write a replaced manifest's digest into the lockfile under
// the old content pin. So a slot whose content still matches its pin but whose
// persisted manifest no longer hashes to the pinned manifest digest is not reused:
// it is fetched again from the registry, which rewrites the manifest, and the
// refresh is reported.
func TestAReusedSlotWhoseManifestMovedIsFetchedAgain(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	ctx := context.Background()
	ws := t.TempDir()
	cat, err := catalog.LoadAll("", t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeSchemaLibrary, reuseLibBody("10Gi"), nil); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}
	axis := closure.LibraryAxisNamed("SchemaLibraries")
	cold, err := cat.ClosureLookup(&orasPullerAdapter{target: store}, ws, cacheDir).ResolveLibrary(ctx, axis, reuseURI)
	if err != nil {
		t.Fatalf("cold ResolveLibrary: %v", err)
	}

	// Replace the persisted manifest and leave the content alone, so only the
	// manifest half of the pin is violated.
	manifestPath := filepath.Join(mustCachePath(ws, cacheDir, reuseURI, "1.0.0"), "manifest")
	if err := os.WriteFile(manifestPath, []byte(`{"schemaVersion":2,"replaced":true}`), 0o644); err != nil {
		t.Fatalf("replace the persisted manifest: %v", err)
	}

	var notices []string
	reuse := &catalog.ReuseVerification{
		PriorPins: map[string]catalog.Pin{reuseURI: {
			ResolvedVersion: cold.Resolution.ResolvedVersion,
			Digest:          cold.Resolution.Digest,
			ManifestDigest:  cold.Resolution.ManifestDigest,
		}},
		Warn: func(n string) { notices = append(notices, n) },
	}
	res, err := cat.ClosureLookupWithReuse(&orasPullerAdapter{target: store}, ws, cacheDir, nil, nil, reuse).ResolveLibrary(ctx, axis, reuseURI)
	if err != nil {
		t.Fatalf("re-pull ResolveLibrary: %v", err)
	}
	if res.Resolution.ResolutionPath != closure.ResolutionPathRegistry {
		t.Errorf("ResolutionPath = %q; want %q — a slot whose manifest moved must be fetched again, not reused",
			res.Resolution.ResolutionPath, closure.ResolutionPathRegistry)
	}
	if res.Resolution.ManifestDigest != cold.Resolution.ManifestDigest {
		t.Errorf("ManifestDigest = %q; want the pinned %q", res.Resolution.ManifestDigest, cold.Resolution.ManifestDigest)
	}
	rewritten, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read the persisted manifest after the re-pull: %v", err)
	}
	if got := sha256Hex(rewritten); got != cold.Resolution.ManifestDigest {
		t.Errorf("persisted manifest digest after the re-pull = %s; want the registry's %s", got, cold.Resolution.ManifestDigest)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "pin unchanged") {
		t.Errorf("notices = %v; want one refresh notice saying the pin is unchanged", notices)
	}
}
