// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci/ocitest"
)

const reuseURI = "hub.aggrega.ai/schemalibrary/core/DbSizing:1.0.0"

func reuseLibBody(defaultSize string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "` + defaultSize + `" }
`)
}

// seedCacheSlot writes body as the library.yaml of the cache slot for
// reuseURI@1.0.0 — the layout writeEntityToCacheSlot uses for libraries
// and readCacheSlotBytes reads first — with the manifest a fetch of body
// persists beside it. A slot holding the content alone is not a cache entry
// and no reader serves it, so a slot seeded without its manifest would test
// the miss rather than the reuse decision these rows are about.
func seedCacheSlot(t *testing.T, ws, cacheDir string, body []byte) {
	t.Helper()
	slot := ocitest.MustCachePath(ws, cacheDir, "SchemaLibrary", reuseURI, "1.0.0")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir slot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, "library.yaml"), body, 0o644); err != nil {
		t.Fatalf("seed library.yaml: %v", err)
	}
	manifest, _ := faithfulManifest(context.Background(), oci.MediaTypeSchemaLibrary, body)
	if err := oci.WriteCachedManifest(ws, cacheDir, "SchemaLibrary", reuseURI, "1.0.0", manifest); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
}

func readCacheSlot(t *testing.T, ws, cacheDir string) []byte {
	t.Helper()
	slot := ocitest.MustCachePath(ws, cacheDir, "SchemaLibrary", reuseURI, "1.0.0")
	got, err := os.ReadFile(filepath.Join(slot, "library.yaml"))
	if err != nil {
		t.Fatalf("read slot library.yaml: %v", err)
	}
	return got
}

// TestClosureLookupWithReuseVerifiesCacheReuse pins the re-pull
// reuse-check: when a prior lockfile pins a URI, the Tier-2 cache reuse
// is verified against that pin. A verified slot is reused; a drifted
// slot is re-fetched from the registry (re-establishing the pin from
// authoritative bytes) and a notice distinguishes a benign refresh from
// a moved pin. The nil-reuse path (build / serve) reuses a complete slot
// without holding it to any pin — the no-regression anchor.
func TestClosureLookupWithReuseVerifiesCacheReuse(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	ctx := context.Background()

	good := reuseLibBody("10Gi")
	goodPin := sha256Hex(good)
	drifted := reuseLibBody("999Gi") // same shape, different bytes -> different digest

	t.Run("verified hit reuses the cache slot", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		seedCacheSlot(t, ws, cacheDir, good)
		// Load the catalog from a separate empty dir so the seeded cache
		// slot is NOT walked into the in-memory catalog (a library slot is
		// a library.yaml, which Load would index as a Tier-1 entity) — the
		// reuse-check governs Tier-2, so the entity must miss Tier-1.
		cat, err := catalog.LoadAll("", t.TempDir())
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		var notices []string
		reuse := &catalog.ReuseVerification{
			PriorPins: catalog.Pins{{Kind: "SchemaLibrary", URI: reuseURI}: {Digest: goodPin}},
			Warn:      func(n string) { notices = append(notices, n) },
		}
		// A puller that must not be reached on a verified hit.
		lookup := cat.ClosureLookupWithReuse(&failPuller{t: t}, ws, cacheDir, nil, nil, reuse)
		res, err := lookup.ResolveLibrary(context.Background(), closure.LibraryAxisNamed("SchemaLibraries"), reuseURI)
		if err != nil {
			t.Fatalf("ResolveSchemaLibrary: %v", err)
		}
		if res.Resolution.ResolutionPath != "tier-2-cache" {
			t.Errorf("ResolutionPath = %q; want tier-2-cache (verified reuse)", res.Resolution.ResolutionPath)
		}
		if len(notices) != 0 {
			t.Errorf("verified hit recorded notices: %v", notices)
		}
	})

	t.Run("drift with unchanged upstream re-fetches with a benign notice", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		seedCacheSlot(t, ws, cacheDir, drifted)
		// Load the catalog from a separate empty dir so the seeded cache
		// slot is NOT walked into the in-memory catalog (a library slot is
		// a library.yaml, which Load would index as a Tier-1 entity) — the
		// reuse-check governs Tier-2, so the entity must miss Tier-1.
		cat, err := catalog.LoadAll("", t.TempDir())
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		store := memory.New()
		if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeSchemaLibrary, good, nil); err != nil {
			t.Fatalf("oci.Push: %v", err)
		}
		var notices []string
		reuse := &catalog.ReuseVerification{
			PriorPins: catalog.Pins{{Kind: "SchemaLibrary", URI: reuseURI}: {Digest: goodPin}},
			Warn:      func(n string) { notices = append(notices, n) },
		}
		lookup := cat.ClosureLookupWithReuse(&orasPullerAdapter{target: store}, ws, cacheDir, nil, nil, reuse)
		res, err := lookup.ResolveLibrary(context.Background(), closure.LibraryAxisNamed("SchemaLibraries"), reuseURI)
		if err != nil {
			t.Fatalf("ResolveSchemaLibrary: %v", err)
		}
		if res.Resolution.ResolutionPath != "tier-3-pull" {
			t.Errorf("ResolutionPath = %q; want tier-3-pull (re-fetch on drift)", res.Resolution.ResolutionPath)
		}
		if res.Resolution.Digest != goodPin {
			t.Errorf("re-fetched digest = %q; want %q (re-pinned to authoritative bytes)", res.Resolution.Digest, goodPin)
		}
		if string(readCacheSlot(t, ws, cacheDir)) != string(good) {
			t.Error("cache slot was not rewritten from authoritative bytes")
		}
		if len(notices) != 1 || !strings.Contains(notices[0], "pin unchanged") {
			t.Errorf("notice = %v; want a single benign 'pin unchanged' notice", notices)
		}
	})

	t.Run("drift with re-tagged upstream HALTS (honor != launder)", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		seedCacheSlot(t, ws, cacheDir, drifted)
		// Load the catalog from a separate empty dir so the seeded cache
		// slot is NOT walked into the in-memory catalog (a library slot is
		// a library.yaml, which Load would index as a Tier-1 entity) — the
		// reuse-check governs Tier-2, so the entity must miss Tier-1.
		cat, err := catalog.LoadAll("", t.TempDir())
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		// The registry now serves DIFFERENT bytes for the pinned version (the
		// upstream tag was moved). Honoring does NOT re-pin the moved bytes — it
		// HALTS with ErrFetchIntegrity, so a changed pin is surfaced, never
		// laundered into a new lockfile.
		retagged := reuseLibBody("20Gi")
		store := memory.New()
		if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeSchemaLibrary, retagged, nil); err != nil {
			t.Fatalf("oci.Push: %v", err)
		}
		reuse := &catalog.ReuseVerification{
			PriorPins: catalog.Pins{{Kind: "SchemaLibrary", URI: reuseURI}: {ResolvedVersion: "1.0.0", Digest: goodPin}},
		}
		lookup := cat.ClosureLookupWithReuse(&orasPullerAdapter{target: store}, ws, cacheDir, nil, nil, reuse)
		_, err = lookup.ResolveLibrary(context.Background(), closure.LibraryAxisNamed("SchemaLibraries"), reuseURI)
		if !errors.Is(err, oci.ErrFetchIntegrity()) {
			t.Errorf("re-tagged upstream: err = %v; want ErrFetchIntegrity (halt, not a re-pinned launder)", err)
		}
	})

	t.Run("nil reuse reuses a drifted slot without holding it to a pin (build/serve parity)", func(t *testing.T) {
		t.Parallel()
		ws := t.TempDir()
		seedCacheSlot(t, ws, cacheDir, drifted)
		// Load the catalog from a separate empty dir so the seeded cache
		// slot is NOT walked into the in-memory catalog (a library slot is
		// a library.yaml, which Load would index as a Tier-1 entity) — the
		// reuse-check governs Tier-2, so the entity must miss Tier-1.
		cat, err := catalog.LoadAll("", t.TempDir())
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		// nil reuse + nil puller: the hermetic build / serve construction.
		lookup := cat.ClosureLookupWithReuse(nil, ws, cacheDir, nil, nil, nil)
		res, err := lookup.ResolveLibrary(context.Background(), closure.LibraryAxisNamed("SchemaLibraries"), reuseURI)
		if err != nil {
			t.Fatalf("ResolveSchemaLibrary: %v", err)
		}
		if res.Resolution.ResolutionPath != "tier-2-cache" {
			t.Errorf("ResolutionPath = %q; want tier-2-cache (unverified reuse)", res.Resolution.ResolutionPath)
		}
		if res.Resolution.Digest != sha256Hex(drifted) {
			t.Errorf("reused digest = %q; want the drifted slot's digest (no verification)", res.Resolution.Digest)
		}
	})
}

// failPuller satisfies catalog.OCIPuller and fails the test if reached —
// asserting a verified cache hit never falls through to a network pull.
type failPuller struct{ t *testing.T }

func (p *failPuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	p.t.Errorf("unexpected Tier-3 pull of %s:%s on a verified cache hit", reference, tag)
	return nil, oci.ErrFetchNotFound()
}
