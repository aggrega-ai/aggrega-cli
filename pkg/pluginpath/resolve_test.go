// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
// tier. Embed (0a) wins over every lower tier; the co-located tier (0b) wins once
// no embed shadows it; cache (2) once neither embed nor co-located resolves; and
// once the cache slot is gone nothing is left, so the resolution halts as an absent
// artifact. A bare catalog URI carries no local path, so Tier 1 (local) is
// inapplicable here and exercised separately by the local-tier tests.
func TestPrecedenceOrder(t *testing.T) {
	const uri = "core/all-tiers:1.0.0"
	ws := t.TempDir()
	cacheDir := ".aggrega/cache"
	colocatedDir := filepath.Join(t.TempDir(), "colocated")

	embedBytes := []byte("tier-embed")
	colocatedBytes := []byte("tier-colocated")
	cacheBytes := []byte("tier-cache")

	if err := oci.WriteCached(ws, cacheDir, uri, "1.0.0", cacheBytes); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	writeColocated(t, colocatedDir, uri, colocatedBytes)

	base := Resolver{
		colocatedDirs: map[string]ColocatedDir{uri: {Dir: colocatedDir}},
		workspaceRoot: ws,
		cacheDir:      cacheDir,
		pinVersions:   map[string]string{uri: "1.0.0"},
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

	// 0b co-located wins once no embed shadows it — and over the cache.
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

	// Nothing is left once the cache slot is gone: the cascade has no further
	// source, so it halts as an absent artifact rather than reaching anywhere else.
	noCache := noColocated
	noCache.workspaceRoot = t.TempDir() // empty workspace = cache miss
	if _, err = noCache.Resolve(uri); !errors.Is(err, ErrPluginArtifactNotFound()) {
		t.Errorf("with every tier emptied: want ErrPluginArtifactNotFound; got %v", err)
	}
}

// TestPinVerifyMatchAcrossTiers (determinism): a co-located artifact whose bytes
// match the pin resolves to the SAME bytes the cache holds for a pulled artifact —
// the pinned digest, not the source tier, governs output. A co-located plugin is
// therefore output-identical to the pinned artifact a pull caches.
func TestPinVerifyMatchAcrossTiers(t *testing.T) {
	const uri = "core/pinned:1.0.0"
	content := []byte("the-pinned-bytes")
	pin := digest.ContentDigest(content)

	ws := t.TempDir()
	cacheDir := ".aggrega/cache"
	if err := oci.WriteCached(ws, cacheDir, uri, pin, content); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	cacheGot, err := Resolver{
		workspaceRoot: ws,
		cacheDir:      cacheDir,
		pins:          map[string]string{uri: pin},
		pinVersions:   map[string]string{uri: pin},
	}.Resolve(uri)
	if err != nil {
		t.Fatalf("cache resolve: %v", err)
	}

	colocatedDir := writeColocated(t, filepath.Join(t.TempDir(), "colocated"), uri, content)
	coGot, err := Resolver{
		colocatedDirs: map[string]ColocatedDir{uri: {Dir: colocatedDir}},
		pins:          map[string]string{uri: pin},
	}.Resolve(uri)
	if err != nil {
		t.Fatalf("colocated resolve: %v", err)
	}

	if string(cacheGot.Bytes) != string(coGot.Bytes) {
		t.Errorf("colocated bytes differ from cached bytes despite matching pin")
	}
	if cacheGot.Tier != TierCache || coGot.Tier != TierColocated {
		t.Errorf("tiers: cache=%s colocated=%s; want cache/colocated", cacheGot.Tier, coGot.Tier)
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
		colocatedDirs: map[string]ColocatedDir{uri: {Dir: colocatedDir}},
		pins:          map[string]string{uri: pin},
	}.Resolve(uri)
	if err == nil {
		t.Fatal("expected integrity halt; got nil")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("want ErrFetchIntegrity; got %v", err)
	}
}

// TestNoPinSkipsVerification: with no pin for the URI (the publish-time linter
// path), bytes pass through unverified — any artifact resolves.
func TestNoPinSkipsVerification(t *testing.T) {
	const uri = "core/unpinned:1.0.0"
	colocatedDir := writeColocated(t, filepath.Join(t.TempDir(), "colocated"), uri, []byte("whatever"))
	got, err := Resolver{colocatedDirs: map[string]ColocatedDir{uri: {Dir: colocatedDir}}}.Resolve(uri)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got.Bytes) != "whatever" {
		t.Errorf("unpinned resolve altered bytes")
	}
}

// TestAnExhaustedCascadeReportsAnAbsentArtifact: a reference no tier answers halts
// with ErrPluginArtifactNotFound, whose identifiers are the ones the reference
// publishes, for the registry-shaped form and the bare form alike. The condition
// reports an absence, so it is not the remote-fetch condition: nothing was fetched.
func TestAnExhaustedCascadeReportsAnAbsentArtifact(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"a reference shaped like a registry location", "oci://hub.aggrega.ai/plugin/linter/aggrega-publishing:1.0.0"},
		{"a bare plugin coordinate", "pipeline-step/absent-everywhere"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolver{workspaceRoot: t.TempDir(), cacheDir: ".aggrega/cache"}.Resolve(tc.uri)
			if err == nil {
				t.Fatal("expected the resolution to halt; got nil")
			}
			if !errors.Is(err, ErrPluginArtifactNotFound()) {
				t.Errorf("want ErrPluginArtifactNotFound; got %v", err)
			}
			if errors.Is(err, oci.ErrFetchNotFound()) {
				t.Errorf("the exhaustion must not report a remote fetch, which never happened; got %v", err)
			}
			if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0306" {
				t.Errorf("short code = %q (reported %v); want E0306", code, ok)
			}
			if !strings.Contains(err.Error(), "not resolvable") {
				t.Errorf("error should say not resolvable; got %v", err)
			}
		})
	}
}

// TestResolveCatalogArtifactAsksTheCascade drives the lock-time question about a
// Plugin entity authored in a catalog directory through each tier that can answer
// it, and through the case none can. An artifact any tier holds satisfies it — the
// question is the cascade's, not "is there a file beside the entity" — and the
// answer describes the bytes that tier read: their content digest as digest and
// version, the provenance tag of that tier, and no manifest digest, since nothing
// came through a registry. Only a reference no tier answers is refused, naming the
// entity, the file and the directory.
func TestResolveCatalogArtifactAsksTheCascade(t *testing.T) {
	const entityURI = "pipeline-step/patch:1.0.0"
	const artifactRef = "pipeline-step/patch"
	leaf := pluginLeaf(artifactRef)

	cases := []struct {
		name     string
		setup    func(t *testing.T) Resolver
		refused  bool
		wantPath string
		bytes    string
	}{
		{
			name: "the artifact sits beside the entity",
			setup: func(t *testing.T) Resolver {
				dir := writeColocated(t, filepath.Join(t.TempDir(), "catalog", "patch"), artifactRef, []byte("guest"))
				return Resolver{colocatedDirs: map[string]ColocatedDir{artifactRef: {Dir: dir}}}
			},
			wantPath: closure.ResolutionPathCatalog,
			bytes:    "guest",
		},
		{
			name: "the binary carries the artifact and nothing sits beside the entity",
			setup: func(t *testing.T) Resolver {
				t.Cleanup(embedded.SeedForTest(artifactRef, []byte("embedded-guest")))
				return Resolver{colocatedDirs: map[string]ColocatedDir{artifactRef: {Dir: t.TempDir()}}}
			},
			wantPath: closure.ResolutionPathEmbedded,
			bytes:    "embedded-guest",
		},
		{
			name: "the content cache holds the artifact and nothing sits beside the entity",
			setup: func(t *testing.T) Resolver {
				ws := t.TempDir()
				if err := oci.WriteCached(ws, ".aggrega/cache", artifactRef, "", []byte("cached-guest")); err != nil {
					t.Fatalf("seed cache: %v", err)
				}
				return Resolver{colocatedDirs: map[string]ColocatedDir{artifactRef: {Dir: t.TempDir()}}, workspaceRoot: ws, cacheDir: ".aggrega/cache"}
			},
			wantPath: closure.ResolutionPathCache,
			bytes:    "cached-guest",
		},
		{
			name: "no tier holds the artifact",
			setup: func(t *testing.T) Resolver {
				return Resolver{colocatedDirs: map[string]ColocatedDir{artifactRef: {Dir: t.TempDir()}}, workspaceRoot: t.TempDir(), cacheDir: ".aggrega/cache"}
			},
			refused: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.setup(t)
			res, err := r.ResolveCatalogArtifact(entityURI, artifactRef)
			if !tc.refused {
				if err != nil {
					t.Fatalf("an artifact a tier holds must satisfy the question; got %v", err)
				}
				want := closure.Resolution{
					ResolvedVersion: digest.ContentDigest([]byte(tc.bytes)),
					Digest:          digest.ContentDigest([]byte(tc.bytes)),
					ResolutionPath:  tc.wantPath,
				}
				if res != want {
					t.Errorf("resolution = %+v; want %+v — the content digest of the bytes that tier read, "+
						"that tier's provenance tag, and no manifest digest", res, want)
				}
				return
			}
			if res != (closure.Resolution{}) {
				t.Errorf("a refusal carried a resolution %+v; nothing was read, so there is nothing to describe", res)
			}
			if !errors.Is(err, ErrPluginArtifactNotFound()) {
				t.Fatalf("want ErrPluginArtifactNotFound; got %v", err)
			}
			var ce *cerrors.CompilerError
			if !errors.As(err, &ce) {
				t.Fatalf("the refusal must be a categorised diagnostic; got %T %v", err, err)
			}
			if ce.Category() != cerrors.CatalogError {
				t.Errorf("category = %q; want %q", ce.Category(), cerrors.CatalogError)
			}
			dir := r.colocatedDirs[artifactRef].Dir
			for _, want := range []string{entityURI, leaf, dir, "Place the built .wasm beside the entity", "publish the plugin"} {
				if !strings.Contains(ce.Message(), want) {
					t.Errorf("refusal message does not name %q: %s", want, ce.Message())
				}
			}
			if ce.Source().File != dir {
				t.Errorf("refusal points at %q; want the directory the file is expected in, %q", ce.Source().File, dir)
			}
		})
	}
}
