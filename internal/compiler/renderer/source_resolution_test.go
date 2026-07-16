// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"oras.land/oras-go/v2/content/memory"
)

// mustWriteChartManifest writes the OCI manifest a real helm-oci-chart pull
// persists beside blob.tgz — the manifest whose single layer commits to blob's
// content digest. An OCI-chart cache slot without it reads as a fail-closed miss,
// so every faithful helm-oci-chart fixture slot must carry it.
func mustWriteChartManifest(t *testing.T, slot string, blob []byte) {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "seed", chartMediatype, blob, nil); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	res, err := oci.Pull(ctx, store, "seed")
	if err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	mustWriteFile(t, filepath.Join(slot, cacheslot.ManifestFile), res.ManifestBytes)
}

func TestResolveSourceForNodeSourcelessRecipeBypassesResolver(t *testing.T) {
	t.Parallel()
	nodeCtx := NodeRenderContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "r", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Source: nil},
	}
	if err := resolveSourceForNode(RendererInput{}, recipe, &nodeCtx); err != nil {
		t.Fatalf("sourceless recipe should not error: %v", err)
	}
	if nodeCtx.Source != nil {
		t.Errorf("Source should stay nil; got %v", nodeCtx.Source)
	}
	if nodeCtx.OriginalSource != nil {
		t.Errorf("OriginalSource should stay nil; got %v", nodeCtx.OriginalSource)
	}
}

func TestResolveSourceForNodeNilRecipeBypassesResolver(t *testing.T) {
	t.Parallel()
	nodeCtx := NodeRenderContext{}
	if err := resolveSourceForNode(RendererInput{}, nil, &nodeCtx); err != nil {
		t.Fatalf("nil recipe should not error: %v", err)
	}
	if nodeCtx.Source != nil || nodeCtx.OriginalSource != nil {
		t.Errorf("both fields should stay nil; got Source=%v, OriginalSource=%v",
			nodeCtx.Source, nodeCtx.OriginalSource)
	}
}

func TestResolveSourceForNodeLocalSourceBypassesResolver(t *testing.T) {
	t.Parallel()
	nodeCtx := NodeRenderContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "r", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Source: "./local-chart"},
	}
	// Even with a fully-configured RendererInput, a local source must
	// pass through verbatim — no lockfile lookup, no extract.
	input := RendererInput{
		Lockfile:      &lockfile.Lockfile{Version: 1},
		WorkspaceRoot: "/ws",
		CacheDir:      ".aggrega/cache",
	}
	if err := resolveSourceForNode(input, recipe, &nodeCtx); err != nil {
		t.Fatalf("local source should not error: %v", err)
	}
	if got, _ := nodeCtx.Source.(string); got != "./local-chart" {
		t.Errorf("Source = %v; want './local-chart' (verbatim)", nodeCtx.Source)
	}
	if got, _ := nodeCtx.OriginalSource.(string); got != "./local-chart" {
		t.Errorf("OriginalSource = %v; want './local-chart' (verbatim)", nodeCtx.OriginalSource)
	}
}

func TestResolveSourceForNodeFallsBackWhenLockfileNil(t *testing.T) {
	t.Parallel()
	nodeCtx := NodeRenderContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "r", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Source: "oci://hub.aggrega.ai/charts/mariadb"},
	}
	// Lockfile is nil — orchestrator falls back to verbatim forwarding.
	if err := resolveSourceForNode(RendererInput{}, recipe, &nodeCtx); err != nil {
		t.Fatalf("nil lockfile should fall back, not error: %v", err)
	}
	if got, _ := nodeCtx.Source.(string); got != "oci://hub.aggrega.ai/charts/mariadb" {
		t.Errorf("Source = %v; want verbatim under nil-Lockfile fallback", nodeCtx.Source)
	}
	if got, _ := nodeCtx.OriginalSource.(string); got != "oci://hub.aggrega.ai/charts/mariadb" {
		t.Errorf("OriginalSource = %v; want verbatim under nil-Lockfile fallback", nodeCtx.OriginalSource)
	}
}

func TestResolveSourceForNodeResolvesRemoteSourceFromLockfile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZForResolveTest(t, map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: mariadb\nversion: 25.0.8\n",
	})
	digest := digestOfBytes(blob)
	slot := sourcecache.SlotPath(root, ".aggrega/cache", "helm-oci-chart",
		"oci://hub.aggrega.ai/charts/mariadb", "", "25.0.8", digest)
	mustMkdirAll(t, slot)
	mustWriteFile(t, filepath.Join(slot, "blob.tgz"), blob)
	mustWriteChartManifest(t, slot, blob)
	mustWriteMetadata(t, filepath.Join(slot, "metadata.yaml"), metadata.CacheMetadata{
		Kind:      "helm-oci-chart",
		URL:       "oci://hub.aggrega.ai/charts/mariadb",
		Version:   "25.0.8",
		Digest:    digest,
		Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		PulledAt:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	})

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-bitnami", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Source: map[string]any{
				"kind":    "helm-oci-chart",
				"url":     "oci://hub.aggrega.ai/charts/mariadb",
				"version": "25.0.8",
			},
		},
	}
	input := RendererInput{
		Lockfile: &lockfile.Lockfile{
			Version:     1,
			Release:     "./r.yaml",
			GeneratedAt: "2026-05-22T10:00:00Z",
			SourceArtifacts: []lockfile.SourceArtifact{{
				Kind:          "helm-oci-chart",
				URL:           "oci://hub.aggrega.ai/charts/mariadb",
				PinnedVersion: "25.0.8",
				Digest:        digest,
				Mediatype:     "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "recipe", URI: "mariadb-bitnami:1.0.0", Field: "spec.source"},
				},
			}},
		},
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	}

	nodeCtx := NodeRenderContext{}
	if err := resolveSourceForNode(input, recipe, &nodeCtx); err != nil {
		t.Fatalf("resolveSourceForNode: %v", err)
	}
	resolvedPath, _ := nodeCtx.Source.(string)
	if resolvedPath == "" {
		t.Fatalf("Source should be a non-empty path; got %v", nodeCtx.Source)
	}
	if _, err := os.Stat(filepath.Join(resolvedPath, "Chart.yaml")); err != nil {
		t.Errorf("Chart.yaml missing in resolved Source path: %v", err)
	}
	if m, ok := nodeCtx.OriginalSource.(map[string]any); !ok || m["kind"] != "helm-oci-chart" {
		t.Errorf("OriginalSource should be the verbatim structured map; got %T %v",
			nodeCtx.OriginalSource, nodeCtx.OriginalSource)
	}
}

// TestResolveEntitySource_FeatureRecipeRemoteResolvesFromLockfile pins the build-time
// FeatureRecipe source contract: a FeatureRecipe's REMOTE source is resolved + verified + cache-extracted
// through the SAME shared helper the component path uses (the FR render sites call
// resolveEntitySource with the FR's source/locals/uri/infra). A real pinned fixture
// (lockfile entry + cached archive matching {kind,url,version}) proves the cache HIT —
// the resolved value is the extracted path, not the verbatim URL.
func TestResolveEntitySource_FeatureRecipeRemoteResolvesFromLockfile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZForResolveTest(t, map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: tls\nversion: 1.2.3\n",
	})
	digest := digestOfBytes(blob)
	slot := sourcecache.SlotPath(root, ".aggrega/cache", "helm-oci-chart",
		"oci://hub.aggrega.ai/charts/tls", "", "1.2.3", digest)
	mustMkdirAll(t, slot)
	mustWriteFile(t, filepath.Join(slot, "blob.tgz"), blob)
	mustWriteChartManifest(t, slot, blob)
	mustWriteMetadata(t, filepath.Join(slot, "metadata.yaml"), metadata.CacheMetadata{
		Kind:      "helm-oci-chart",
		URL:       "oci://hub.aggrega.ai/charts/tls",
		Version:   "1.2.3",
		Digest:    digest,
		Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		PulledAt:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	})
	input := RendererInput{
		Lockfile: &lockfile.Lockfile{
			Version:     1,
			Release:     "./r.yaml",
			GeneratedAt: "2026-05-22T10:00:00Z",
			SourceArtifacts: []lockfile.SourceArtifact{{
				Kind:          "helm-oci-chart",
				URL:           "oci://hub.aggrega.ai/charts/tls",
				PinnedVersion: "1.2.3",
				Digest:        digest,
				Mediatype:     "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "feature_recipe", URI: "tls-helm:1.0.0", Field: "spec.source"},
				},
			}},
		},
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	}
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/tls",
		"version": "1.2.3",
	}
	resolved, err := resolveEntitySource(input, source, types.OrderedMap[string]{}, "tls-helm:1.0.0", map[string]any{}, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("resolveEntitySource (FR remote): %v", err)
	}
	resolvedPath, _ := resolved.(string)
	if resolvedPath == "" {
		t.Fatalf("FR Source should be a non-empty extracted path; got %v", resolved)
	}
	if _, err := os.Stat(filepath.Join(resolvedPath, "Chart.yaml")); err != nil {
		t.Errorf("Chart.yaml missing in resolved FR Source path — cache HIT not proven: %v", err)
	}
}

// TestResolveEntitySource_RecipePinDisagreesWithLockfileHalts proves the
// recipe-side pin verify (matrix row 14): a recipe-carried sourceDigests pin
// whose digest disagrees with the lockfile source-artifact record for the same
// {kind, url, version} is an integrity fault — both derive from one resolution,
// so a disagreement halts loudly with ErrFetchIntegrity.
func TestResolveEntitySource_RecipePinDisagreesWithLockfileHalts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZForResolveTest(t, map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: tls\nversion: 1.2.3\n",
	})
	digest := digestOfBytes(blob)
	slot := sourcecache.SlotPath(root, ".aggrega/cache", "helm-oci-chart",
		"oci://hub.aggrega.ai/charts/tls", "", "1.2.3", digest)
	mustMkdirAll(t, slot)
	mustWriteFile(t, filepath.Join(slot, "blob.tgz"), blob)
	mustWriteChartManifest(t, slot, blob)
	mustWriteMetadata(t, filepath.Join(slot, "metadata.yaml"), metadata.CacheMetadata{
		Kind: "helm-oci-chart", URL: "oci://hub.aggrega.ai/charts/tls", Version: "1.2.3",
		Digest: digest, Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		PulledAt: time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	})
	input := RendererInput{
		Lockfile: &lockfile.Lockfile{
			Version: 1, Release: "./r.yaml", GeneratedAt: "2026-05-22T10:00:00Z",
			SourceArtifacts: []lockfile.SourceArtifact{{
				Kind: "helm-oci-chart", URL: "oci://hub.aggrega.ai/charts/tls", PinnedVersion: "1.2.3",
				Digest: digest, Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
			}},
		},
		WorkspaceRoot: root, CacheDir: ".aggrega/cache",
	}
	source := map[string]any{"kind": "helm-oci-chart", "url": "oci://hub.aggrega.ai/charts/tls", "version": "1.2.3"}
	// The recipe pin disagrees with the lockfile digest.
	badPins := []types.SourceDigest{{Kind: "helm-oci-chart", URL: "oci://hub.aggrega.ai/charts/tls", Version: "1.2.3", Digest: "sha256:deadbeef"}}
	_, err := resolveEntitySource(input, source, types.OrderedMap[string]{}, "tls-helm:1.0.0", map[string]any{}, map[string]any{}, badPins)
	if err == nil {
		t.Fatalf("disagreeing recipe pin resolved without halting")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Errorf("recipe-pin disagreement error = %v; want ErrFetchIntegrity", err)
	}
	// An agreeing pin resolves cleanly.
	goodPins := []types.SourceDigest{{Kind: "helm-oci-chart", URL: "oci://hub.aggrega.ai/charts/tls", Version: "1.2.3", Digest: digest}}
	if _, err := resolveEntitySource(input, source, types.OrderedMap[string]{}, "tls-helm:1.0.0", map[string]any{}, map[string]any{}, goodPins); err != nil {
		t.Fatalf("agreeing recipe pin should resolve: %v", err)
	}
}

// TestResolveEntitySource_LocalForwardsVerbatim is the MANDATORY local-source
// regression lock: even with lockfile + workspace + cache all set, a local/inline
// source short-circuits to verbatim — only REMOTE sources route through
// resolve+extract. This holds the FR local-source path byte-identical (the FR render
// sites set frNodeCtx.Source = this verbatim value, identical to the prior
// verbatim-forward behaviour for local sources).
func TestResolveEntitySource_LocalForwardsVerbatim(t *testing.T) {
	t.Parallel()
	input := RendererInput{
		Lockfile:      &lockfile.Lockfile{Version: 1},
		WorkspaceRoot: "/ws",
		CacheDir:      ".aggrega/cache",
	}
	resolved, err := resolveEntitySource(input, "./charts/local-tls", types.OrderedMap[string]{}, "tls-helm:1.0.0", map[string]any{}, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("resolveEntitySource (local): %v", err)
	}
	if resolved != "./charts/local-tls" {
		t.Errorf("local source must forward verbatim (no cache route); got %v", resolved)
	}
}

func TestResolveSourceForNodeHaltsOnMissingLockfileEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "missing", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Source: map[string]any{
				"kind":    "helm-oci-chart",
				"url":     "oci://nonexistent/chart",
				"version": "9.9.9",
			},
		},
	}
	input := RendererInput{
		Lockfile:      &lockfile.Lockfile{Version: 1, Release: "./r.yaml", GeneratedAt: "x"},
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	}
	err := resolveSourceForNode(input, recipe, &NodeRenderContext{})
	if !errors.Is(err, sourcecache.ErrFetchNotFound) {
		t.Errorf("missing lockfile entry should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestSourceForDiagnosticPrefersOriginalSource(t *testing.T) {
	t.Parallel()
	nodeCtx := NodeRenderContext{
		Source:         "/tmp/cache/extracted",
		OriginalSource: "oci://hub.aggrega.ai/charts/mariadb:25.0.8",
	}
	got := SourceForDiagnostic(nodeCtx)
	if got != "oci://hub.aggrega.ai/charts/mariadb:25.0.8" {
		t.Errorf("SourceForDiagnostic = %q; want OriginalSource over Source", got)
	}
}

func TestSourceForDiagnosticFallsBackToSource(t *testing.T) {
	t.Parallel()
	nodeCtx := NodeRenderContext{Source: "/local/chart"}
	if got := SourceForDiagnostic(nodeCtx); got != "/local/chart" {
		t.Errorf("SourceForDiagnostic = %q; want Source fallback", got)
	}
}

func TestSourceForDiagnosticReturnsSourcelessSentinel(t *testing.T) {
	t.Parallel()
	if got := SourceForDiagnostic(NodeRenderContext{}); got != "<sourceless>" {
		t.Errorf("SourceForDiagnostic = %q; want <sourceless>", got)
	}
}

func buildTGZForResolveTest(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)),
			Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(),
			Format: tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// digestOfBytes is intentionally an INDEPENDENT re-derivation of the content
// digest, NOT a call to pkg/digest.ContentDigest. These tests seed a cache slot
// and lockfile with this digest, then production recomputes via ContentDigest
// and compares. Keeping the test computation independent makes the round-trip a
// genuine cross-check: a format change in ContentDigest makes production
// disagree with this seed and the test fails; delegating to the shared helper
// would change both together and blind the check. The inputs are gzip archives,
// whose compressed bytes are not stable across Go toolchains, so a hardcoded
// golden literal is not viable; the literal-format guard lives in pkg/digest's
// own test and the fixed-input catalog tests.
func digestOfBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustWriteMetadata(t *testing.T, path string, m metadata.CacheMetadata) {
	t.Helper()
	if err := metadata.Write(path, m); err != nil {
		t.Fatal(err)
	}
}

func TestMatchFetchSentinelDetectsEachSentinel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sent error
	}{
		{"NotFound", oci.ErrFetchNotFound},
		{"Unauthorized", oci.ErrFetchUnauthorized},
		{"NetworkUnreachable", oci.ErrFetchNetworkUnreachable},
		{"Integrity", oci.ErrFetchIntegrity},
	}
	for _, tc := range cases {
		wrapped := fmt.Errorf("outer wrap: %w", tc.sent)
		got, ok := matchFetchSentinel(wrapped)
		if !ok {
			t.Errorf("%s: matchFetchSentinel should return ok=true for wrapped sentinel", tc.name)
			continue
		}
		if got != tc.sent {
			t.Errorf("%s: matchFetchSentinel returned %v; want %v", tc.name, got, tc.sent)
		}
	}
}

func TestMatchFetchSentinelReturnsFalseForNonFetchErrors(t *testing.T) {
	t.Parallel()
	got, ok := matchFetchSentinel(fmt.Errorf("some unrelated error"))
	if ok || got != nil {
		t.Errorf("non-fetch error should return (nil, false); got (%v, %v)", got, ok)
	}
	if g, ok := matchFetchSentinel(closure.ErrSourceTemplateAtLockTime); ok {
		t.Errorf("ErrSourceTemplateAtLockTime is not a fetch sentinel; got (%v, ok=true)", g)
	}
}

func TestHostFromSourceErrParsesURLFromWrappedMessage(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("sourceresolver: kind=helm-oci-chart url=oci://hub.aggrega.ai/charts/mariadb version=25.0.8: pull failed")
	if got := hostFromSourceErr(err); got != "hub.aggrega.ai" {
		t.Errorf("hostFromSourceErr = %q; want hub.aggrega.ai", got)
	}
}

func TestHostFromSourceErrReturnsEmptyForCacheError(t *testing.T) {
	t.Parallel()
	// pkg/sourcecache errors don't carry "url=" — orchestrator's
	// fallback to "<registry>" placeholder is correct.
	err := fmt.Errorf("sourcecache: blob digest mismatch")
	if got := hostFromSourceErr(err); got != "" {
		t.Errorf("hostFromSourceErr should return empty for cache error; got %q", got)
	}
}
