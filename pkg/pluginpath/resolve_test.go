// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// writeColocated writes content as the co-located artifact for uri under dir —
// the .wasm beside its Plugin entity — and returns dir so the caller can map the
// URI to it via ResolutionContext.ColocatedDirs. The leaf is the shared
// pluginLeaf mapping the co-located tier reads.
func writeColocated(t *testing.T, dir, uri string, content []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir colocated dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pluginLeaf(uri)), content, 0o644); err != nil {
		t.Fatalf("write colocated: %v", err)
	}
	return dir
}

// stubPuller returns canned bytes for any URI — stands in for the Tier-3
// registry pull without a network call.
type stubPuller struct{ bytes []byte }

func (p stubPuller) PullPlugin(_ context.Context, _ string) ([]byte, error) { return p.bytes, nil }

// TestResolveOverride: an explicit override reads the artifact's bytes,
// short-circuiting the cascade.
func TestResolveOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ovr.wasm")
	content := []byte("override-bytes")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Resolver{override: path}.Resolve("any/uri:1.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got.Bytes) != string(content) || got.Tier != TierLocal {
		t.Errorf("got tier=%s bytes=%q; want local + override bytes", got.Tier, got.Bytes)
	}
}

// TestResolveUnset: no URI and no override yields the load-bearing
// no-plugin-path-configured diagnostic.
func TestResolveUnset(t *testing.T) {
	_, err := Resolver{}.Resolve("")
	if err == nil || !strings.Contains(err.Error(), "no plugin path configured") {
		t.Fatalf("want unset diagnostic; got %v", err)
	}
}

// TestEmbedCheckedFirst: a seeded embedded entry wins even when a cache slot
// also resolves the URI — proving the embed tier is consulted ahead of the
// lower tiers.
func TestEmbedCheckedFirst(t *testing.T) {
	const uri = "core/embedded-only:1.0.0"
	embedBytes := []byte("embedded-content")
	restore := embedded.SeedForTest(uri, embedBytes)
	defer restore()

	ws := t.TempDir()
	cacheDir := ".aggrega/cache"
	if err := oci.WriteCached(ws, cacheDir, uri, "1.0.0", []byte("cache-content")); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	got, err := Resolver{workspaceRoot: ws, cacheDir: cacheDir, pinVersions: map[string]string{uri: "1.0.0"}}.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Tier != TierEmbed || string(got.Bytes) != string(embedBytes) {
		t.Errorf("got tier=%s bytes=%q; want embed + embedded content", got.Tier, got.Bytes)
	}
}

// TestNonEmbeddedURIFallsThroughToLocal: a reference not present in the embed
// set falls through to the local tier — proving the embed tier short-circuits
// only the URIs it actually holds, not every resolution.
func TestNonEmbeddedURIFallsThroughToLocal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.wasm")
	content := []byte("local-content")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := embedded.Lookup(path); ok {
		t.Fatalf("test precondition: %q must not be in the embed set", path)
	}
	got, err := Resolver{}.Resolve(path)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Tier != TierLocal || string(got.Bytes) != string(content) {
		t.Errorf("got tier=%s; want local fall-through", got.Tier)
	}
}

// TestPrecedenceOrder asserts the cascade order by peeling sources away tier by
// tier. Embed (0a) wins over every lower tier; the co-located tier (0c) wins once
// no embed shadows it; cache (2) once neither embed nor co-located resolves;
// registry (3) once the cache slot is gone. A bare catalog URI carries no local
// path, so Tier 1 (local) is inapplicable here and exercised separately by the
// local-tier tests.
func TestPrecedenceOrder(t *testing.T) {
	const uri = "core/all-tiers:1.0.0"
	ws := t.TempDir()
	cacheDir := ".aggrega/cache"
	colocatedDir := filepath.Join(t.TempDir(), "colocated")

	embedBytes := []byte("tier-embed")
	colocatedBytes := []byte("tier-colocated")
	cacheBytes := []byte("tier-cache")
	registryBytes := []byte("tier-registry")

	if err := oci.WriteCached(ws, cacheDir, uri, "1.0.0", cacheBytes); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	writeColocated(t, colocatedDir, uri, colocatedBytes)

	base := Resolver{
		colocatedDirs: map[string]string{uri: colocatedDir},
		workspaceRoot: ws,
		cacheDir:      cacheDir,
		pinVersions:   map[string]string{uri: "1.0.0"},
		puller:        stubPuller{bytes: registryBytes},
	}

	// 0a embed wins over every lower tier, including a co-located artifact of the
	// same URI.
	restore := embedded.SeedForTest(uri, embedBytes)
	got, err := base.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve embed-over-colocated: %v", err)
	}
	if got.Tier != TierEmbed {
		t.Errorf("with embed+colocated: got %s; want embed", got.Tier)
	}
	restore()

	// 0c co-located wins once no embed shadows it — and over cache + registry.
	got, err = base.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve colocated: %v", err)
	}
	if got.Tier != TierColocated {
		t.Errorf("with colocated (no embed): got %s; want colocated", got.Tier)
	}

	// 2 cache wins once neither embed nor co-located resolves.
	noColocated := base
	noColocated.colocatedDirs = nil
	got, err = noColocated.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve cache: %v", err)
	}
	if got.Tier != TierCache {
		t.Errorf("with cache present: got %s; want cache", got.Tier)
	}

	// 3 registry wins once the cache slot is gone.
	noCache := noColocated
	noCache.workspaceRoot = t.TempDir() // empty workspace = cache miss
	got, err = noCache.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve registry: %v", err)
	}
	if got.Tier != TierRegistry {
		t.Errorf("with only registry: got %s; want registry", got.Tier)
	}
}

// TestPinVerifyMatchAcrossTiers (determinism): a co-located artifact whose bytes
// match the pin resolves to the SAME bytes a registry source would — the pinned
// digest, not the source tier, governs output. A co-located plugin is therefore
// output-identical to the pinned artifact a registry pull produces.
func TestPinVerifyMatchAcrossTiers(t *testing.T) {
	const uri = "core/pinned:1.0.0"
	content := []byte("the-pinned-bytes")
	pin := digest.ContentDigest(content)

	regGot, err := Resolver{
		puller: stubPuller{bytes: content},
		pins:   map[string]string{uri: pin},
	}.Resolve(uri)
	if err != nil {
		t.Fatalf("registry resolve: %v", err)
	}

	colocatedDir := writeColocated(t, filepath.Join(t.TempDir(), "colocated"), uri, content)
	coGot, err := Resolver{
		colocatedDirs: map[string]string{uri: colocatedDir},
		pins:          map[string]string{uri: pin},
	}.Resolve(uri)
	if err != nil {
		t.Fatalf("colocated resolve: %v", err)
	}

	if string(regGot.Bytes) != string(coGot.Bytes) {
		t.Errorf("colocated bytes differ from registry bytes despite matching pin")
	}
	if regGot.Tier != TierRegistry || coGot.Tier != TierColocated {
		t.Errorf("tiers: registry=%s colocated=%s; want registry/colocated", regGot.Tier, coGot.Tier)
	}
}

// TestPinMismatchHalts: a co-located artifact whose bytes do NOT match the pin
// halts with a wrapped oci.ErrFetchIntegrity rather than resolving drifted
// content.
func TestPinMismatchHalts(t *testing.T) {
	const uri = "core/drifted:1.0.0"
	pin := digest.ContentDigest([]byte("the-pinned-bytes"))

	colocatedDir := writeColocated(t, filepath.Join(t.TempDir(), "colocated"), uri, []byte("DIFFERENT-bytes"))

	_, err := Resolver{
		colocatedDirs: map[string]string{uri: colocatedDir},
		pins:          map[string]string{uri: pin},
	}.Resolve(uri)
	if err == nil {
		t.Fatal("expected integrity halt; got nil")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Errorf("want ErrFetchIntegrity; got %v", err)
	}
}

// TestNoPinSkipsVerification: with no pin for the URI (the publish-time linter
// path), bytes pass through unverified — any artifact resolves.
func TestNoPinSkipsVerification(t *testing.T) {
	const uri = "core/unpinned:1.0.0"
	colocatedDir := writeColocated(t, filepath.Join(t.TempDir(), "colocated"), uri, []byte("whatever"))
	got, err := Resolver{colocatedDirs: map[string]string{uri: colocatedDir}}.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got.Bytes) != "whatever" {
		t.Errorf("unpinned resolve altered bytes")
	}
}

// TestOCIUnresolvableNoPuller: an OCI URI with no puller and nothing cached
// halts loudly with a not-resolvable message wrapping oci.ErrFetchNotFound.
func TestOCIUnresolvableNoPuller(t *testing.T) {
	_, err := Resolver{}.Resolve("oci://hub.aggrega.ai/plugin/linter/aggrega-publishing:1.0.0")
	if err == nil {
		t.Fatal("expected not-resolvable error; got nil")
	}
	if !errors.Is(err, oci.ErrFetchNotFound) {
		t.Errorf("want ErrFetchNotFound; got %v", err)
	}
	if !strings.Contains(err.Error(), "not resolvable") {
		t.Errorf("error should say not resolvable; got %v", err)
	}
}
