// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"oras.land/oras-go/v2/content/memory"
)

const (
	remoteSrcAggregatePath = "../../../internal/compiler/testdata/remote-source-pipeline/aggregate.yaml"
	remoteSrcReleasePath   = "../../../internal/compiler/testdata/remote-source-pipeline/release.yaml"
	remoteSrcManifestPath  = "../../../internal/compiler/testdata/remote-source-pipeline/manifest.yaml"
	remoteSrcCatalogPath   = "../../../internal/compiler/testdata/remote-source-pipeline/catalog"
	remoteSrcKind          = "helm-oci-chart"
	remoteSrcURL           = "oci://demo.example/charts/server"
	remoteSrcVersion       = "1.0.0"
)

// tgzNestedChart builds a Helm chart archive in the REAL OCI layout: all files
// nested under a single top-level <chartname>/ directory. The flat synthetic
// fixtures elsewhere hid the chart-root-resolution gap; this mirrors reality.
func tgzNestedChart(t *testing.T) []byte {
	t.Helper()
	entries := map[string]string{
		"mychart/Chart.yaml":        "apiVersion: v2\nname: mychart\nversion: 1.0.0\n",
		"mychart/templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-marker\ndata:\n  source: cache-extracted\n",
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func remoteSrcDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func seedChartCache(t *testing.T, root string, blob []byte, digest string) {
	t.Helper()
	slot := sourcecache.SlotPath(root, ".aggrega/cache", remoteSrcKind, remoteSrcURL, remoteSrcVersion, digest)
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir slot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, "blob.tgz"), blob, 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	writeChartManifest(t, slot, blob)
	meta := metadata.CacheMetadata{
		Kind: remoteSrcKind, URL: remoteSrcURL, Version: remoteSrcVersion, Digest: digest,
		Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		PulledAt:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	}
	if err := metadata.Write(filepath.Join(slot, "metadata.yaml"), meta); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
}

// writeChartManifest writes the OCI manifest a real helm-oci-chart pull persists
// beside blob.tgz — the manifest whose single layer commits to blob's content
// digest. An OCI-chart cache slot without it reads as a fail-closed miss.
func writeChartManifest(t *testing.T, slot string, blob []byte) {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "seed", "application/vnd.cncf.helm.chart.content.v1.tar+gzip", blob); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	res, err := oci.Pull(ctx, store, "seed")
	if err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, cacheslot.ManifestFile), res.ManifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func remoteSrcLockfile(digest string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version: 1, Release: "./release.yaml", GeneratedAt: "2026-05-22T10:00:00Z",
		SourceArtifacts: []lockfile.SourceArtifact{{
			Kind: remoteSrcKind, URL: remoteSrcURL, PinnedVersion: remoteSrcVersion, Digest: digest,
			Mediatype:    "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
			ReferencedBy: []lockfile.SourceArtifactReference{{Entity: "recipe", URI: "demo-server-remote:1.0.0", Field: "spec.source"}},
		}},
	}
}

func loadRemoteSrcCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.LoadAll("", remoteSrcCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %v", extErrs[0])
	}
	return cat
}

// TestRunPipelineResolvesRemoteSourceThroughProductionPath is the production-path
// proof the existing tests skip: it drives pipeline.RunPipeline (NOT a directly-
// constructed RendererInput) with a lockfile + a cached remote chart, and asserts
// the remote Recipe source resolves to the cached, cache-extracted chart and
// renders. RunPipeline populating RendererInput's Lockfile/WorkspaceRoot/CacheDir
// from Inputs is exactly the wiring this exercises.
func TestRunPipelineResolvesRemoteSourceThroughProductionPath(t *testing.T) {
	t.Parallel()
	blob := tgzNestedChart(t)
	digest := remoteSrcDigest(blob)
	root := t.TempDir()
	seedChartCache(t, root, blob, digest)

	rel, err := resolver.LoadRelease(remoteSrcReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(remoteSrcManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(remoteSrcAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat := loadRemoteSrcCatalog(t)

	result, err := pipeline.RunPipeline(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     remoteSrcAggregatePath,
			WorkspaceConfig:   workspaceconfig.Default(),
			Lockfile:          remoteSrcLockfile(digest),
			WorkspaceRoot:     root,
			CompilerVersion:   fixtureCompilerVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		pipeline.Options{IncludeTrace: false},
	)
	if err != nil {
		t.Fatalf("RunPipeline (remote source through production path): %v", err)
	}

	var found bool
	for _, body := range result.Artifacts {
		if strings.Contains(string(body), "kind: ConfigMap") && strings.Contains(string(body), "source: cache-extracted") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(result.Artifacts))
		for k := range result.Artifacts {
			names = append(names, k)
		}
		t.Fatalf("the cached chart's ConfigMap is absent from rendered output — the remote source did not resolve through RunPipeline; artifacts=%v", names)
	}
}

// TestRunPipelineRemoteSourceFailsWithoutLockfile is the non-vacuous counterpart:
// with no lockfile in Inputs, RunPipeline leaves RendererInput's source-resolution
// fields unset, so the structured remote source forwards verbatim and the engine
// halts. This proves the resolution above is what the lockfile wiring buys —
// without it, the same input fails.
func TestRunPipelineRemoteSourceFailsWithoutLockfile(t *testing.T) {
	t.Parallel()
	rel, err := resolver.LoadRelease(remoteSrcReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(remoteSrcManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(remoteSrcAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat := loadRemoteSrcCatalog(t)

	_, err = pipeline.RunPipeline(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     remoteSrcAggregatePath,
			WorkspaceConfig:   workspaceconfig.Default(),
			CompilerVersion:   fixtureCompilerVersion,
			// Lockfile + WorkspaceRoot intentionally unset.
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		pipeline.Options{IncludeTrace: false},
	)
	if err == nil {
		t.Fatal("expected a render error when no lockfile is supplied (the structured remote source forwards verbatim and the engine halts)")
	}
}
