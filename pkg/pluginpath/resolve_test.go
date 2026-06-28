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

// writeSidecar writes content as the flat sidecar artifact for uri under dir.
func writeSidecar(t *testing.T, dir, uri string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sidecar dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sidecarLeaf(uri)), content, 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
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
	got, err := Resolver{Override: path}.Resolve("any/uri:1.0.0")
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
// lower tiers when no sidecar override is present.
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

	got, err := Resolver{WorkspaceRoot: ws, CacheDir: cacheDir, PinVersions: map[string]string{uri: "1.0.0"}}.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Tier != TierEmbed || string(got.Bytes) != string(embedBytes) {
		t.Errorf("got tier=%s bytes=%q; want embed + embedded content", got.Tier, got.Bytes)
	}
}

// TestEmbedEmptySetFallThrough: with the production-shape empty embed set, a URI
// not embedded falls through to the local tier — proving the empty scaffold does
// not short-circuit resolution.
func TestEmbedEmptySetFallThrough(t *testing.T) {
	if len(embedded.URIs()) != 0 {
		t.Fatalf("embed set expected empty by default; got %v", embedded.URIs())
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "local.wasm")
	content := []byte("local-content")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
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
// tier. The sidecar tier is an OVERRIDE tier: a sidecar artifact deliberately
// wins over an embedded one of the same URI (the loud-logged override), so with
// both present the sidecar wins. Embed wins once no sidecar shadows it; cache
// once neither embed nor sidecar resolves; registry once the cache slot is gone.
// A bare catalog URI carries no local path, so Tier 1 (local) is inapplicable
// here and exercised separately by the local-tier tests.
func TestPrecedenceOrder(t *testing.T) {
	const uri = "core/all-tiers:1.0.0"
	ws := t.TempDir()
	cacheDir := ".aggrega/cache"
	sidecarDir := filepath.Join(t.TempDir(), "sidecar")

	embedBytes := []byte("tier-embed")
	sidecarBytes := []byte("tier-sidecar")
	cacheBytes := []byte("tier-cache")
	registryBytes := []byte("tier-registry")

	if err := oci.WriteCached(ws, cacheDir, uri, "1.0.0", cacheBytes); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	writeSidecar(t, sidecarDir, uri, sidecarBytes)

	base := Resolver{
		SidecarDir:    sidecarDir,
		WorkspaceRoot: ws,
		CacheDir:      cacheDir,
		PinVersions:   map[string]string{uri: "1.0.0"},
		Puller:        stubPuller{bytes: registryBytes},
	}

	// 0b sidecar overrides 0a embed when both are present (the loud-logged
	// override) and wins over the lower tiers too.
	restore := embedded.SeedForTest(uri, embedBytes)
	got, err := base.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve sidecar-over-embed: %v", err)
	}
	if got.Tier != TierSidecar {
		t.Errorf("with embed+sidecar: got %s; want sidecar (override)", got.Tier)
	}

	// 0a embed wins once no sidecar shadows it — and over cache + registry.
	noSidecar := base
	noSidecar.SidecarDir = ""
	got, err = noSidecar.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve embed: %v", err)
	}
	if got.Tier != TierEmbed {
		t.Errorf("with embed (no sidecar): got %s; want embed", got.Tier)
	}
	restore()

	// 2 cache wins once neither embed nor sidecar resolves.
	got, err = noSidecar.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve cache: %v", err)
	}
	if got.Tier != TierCache {
		t.Errorf("with cache present: got %s; want cache", got.Tier)
	}

	// 3 registry wins once the cache slot is gone.
	noCache := noSidecar
	noCache.WorkspaceRoot = t.TempDir() // empty workspace = cache miss
	got, err = noCache.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve registry: %v", err)
	}
	if got.Tier != TierRegistry {
		t.Errorf("with only registry: got %s; want registry", got.Tier)
	}
}

// TestSidecarOverridesEmbedLogsLoudly: when a sidecar artifact shadows an
// embedded one of the same URI, the override notice fires and the sidecar wins.
func TestSidecarOverridesEmbedLogsLoudly(t *testing.T) {
	const uri = "core/override-me:1.0.0"
	restore := embedded.SeedForTest(uri, []byte("embedded"))
	defer restore()

	sidecarDir := filepath.Join(t.TempDir(), "sidecar")
	sidecarBytes := []byte("sidecar-override")
	writeSidecar(t, sidecarDir, uri, sidecarBytes)

	var logged string
	got, err := Resolver{SidecarDir: sidecarDir, OverrideLog: func(m string) { logged = m }}.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Tier != TierSidecar || string(got.Bytes) != string(sidecarBytes) {
		t.Errorf("got tier=%s; want sidecar override", got.Tier)
	}
	if !strings.Contains(logged, "overrides the embedded") {
		t.Errorf("expected loud override notice; got %q", logged)
	}
}

// TestPinVerifyMatchAcrossTiers (determinism): a sidecar artifact whose bytes
// match the pin resolves to the SAME bytes a registry source would — the pinned
// digest, not the source tier, governs output. A sidecar override of a pinned
// plugin is therefore output-identical to the pinned artifact.
func TestPinVerifyMatchAcrossTiers(t *testing.T) {
	const uri = "core/pinned:1.0.0"
	content := []byte("the-pinned-bytes")
	pin := digest.ContentDigest(content)

	regGot, err := Resolver{
		Puller: stubPuller{bytes: content},
		Pins:   map[string]string{uri: pin},
	}.Resolve(uri)
	if err != nil {
		t.Fatalf("registry resolve: %v", err)
	}

	sidecarDir := filepath.Join(t.TempDir(), "sidecar")
	writeSidecar(t, sidecarDir, uri, content)
	sideGot, err := Resolver{
		SidecarDir: sidecarDir,
		Pins:       map[string]string{uri: pin},
	}.Resolve(uri)
	if err != nil {
		t.Fatalf("sidecar resolve: %v", err)
	}

	if string(regGot.Bytes) != string(sideGot.Bytes) {
		t.Errorf("sidecar bytes differ from registry bytes despite matching pin")
	}
	if regGot.Tier != TierRegistry || sideGot.Tier != TierSidecar {
		t.Errorf("tiers: registry=%s sidecar=%s; want registry/sidecar", regGot.Tier, sideGot.Tier)
	}
}

// TestPinMismatchHalts: a sidecar artifact whose bytes do NOT match the pin
// halts with a wrapped oci.ErrFetchIntegrity rather than resolving drifted
// content.
func TestPinMismatchHalts(t *testing.T) {
	const uri = "core/drifted:1.0.0"
	pin := digest.ContentDigest([]byte("the-pinned-bytes"))

	sidecarDir := filepath.Join(t.TempDir(), "sidecar")
	writeSidecar(t, sidecarDir, uri, []byte("DIFFERENT-bytes"))

	_, err := Resolver{
		SidecarDir: sidecarDir,
		Pins:       map[string]string{uri: pin},
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
	sidecarDir := filepath.Join(t.TempDir(), "sidecar")
	writeSidecar(t, sidecarDir, uri, []byte("whatever"))
	got, err := Resolver{SidecarDir: sidecarDir}.Resolve(uri)
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
	_, err := Resolver{}.Resolve("oci://hub.aggrega.ai/wasm/linter/aggrega-publishing:1.0.0")
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
