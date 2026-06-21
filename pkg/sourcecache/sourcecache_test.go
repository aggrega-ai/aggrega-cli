// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourcecache_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
)

// buildTGZ writes a gzipped tar archive containing the named entries
// and returns the bytes. Used by every test that needs a cache blob
// at a fixture slot.
func buildTGZ(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0).UTC(),
			Format:   tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

// digestOf is intentionally an INDEPENDENT re-derivation of the content
// digest, NOT a call to pkg/digest.ContentDigest. These tests seed a cache
// slot and lockfile entry with this digest, then production recomputes via
// ContentDigest and compares (LookupAndExtract's integrity check). Keeping the
// test computation independent makes the round-trip a genuine cross-check: a
// format change in ContentDigest makes production disagree with this seed and
// the test fails. Delegating to the shared helper would change seed and
// production together and blind the check. The inputs here are gzip archives,
// whose compressed bytes are not stable across Go toolchains, so a hardcoded
// golden literal is not viable; the literal-format guard lives in pkg/digest's
// own test and the fixed-input catalog tests.
func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum[:])
}

// seedSlot writes blob.tgz + metadata.yaml into a fixture cache slot
// derived from SlotPath for the given identity. Returns the slot
// path so tests can inspect it.
func seedSlot(t *testing.T, workspaceRoot, cacheDir, kind, url, version string, blob []byte, metaDigest string) string {
	t.Helper()
	slot := sourcecache.SlotPath(workspaceRoot, cacheDir, kind, url, version, digestOf(blob))
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir slot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, "blob.tgz"), blob, 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	meta := metadata.CacheMetadata{
		Kind:      kind,
		URL:       url,
		Version:   version,
		Digest:    metaDigest,
		Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		PulledAt:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	}
	if err := metadata.Write(filepath.Join(slot, "metadata.yaml"), meta); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	return slot
}

func fixtureLockfile(kind, url, version, digest string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "./r.yaml",
		GeneratedAt: "2026-05-22T10:00:00Z",
		SourceArtifacts: []lockfile.SourceArtifact{
			{
				Kind:          kind,
				URL:           url,
				PinnedVersion: version,
				Digest:        digest,
				Mediatype:     "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "recipe", URI: "u", Field: "spec.source"},
				},
			},
		},
	}
}

func TestLookupAndExtractHappyPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZ(t, map[string]string{
		"Chart.yaml":         "apiVersion: v2\nname: mariadb\nversion: 25.0.8\n",
		"templates/dep.yaml": "kind: Deployment\n",
	})
	digest := digestOf(blob)
	seedSlot(t, root, ".aggrega/cache", "helm-oci-chart", "oci://r/c", "25.0.8", blob, digest)
	lf := fixtureLockfile("helm-oci-chart", "oci://r/c", "25.0.8", digest)

	extracted, err := sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "25.0.8", root, ".aggrega/cache")
	if err != nil {
		t.Fatalf("LookupAndExtract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extracted, "Chart.yaml")); err != nil {
		t.Errorf("Chart.yaml missing in extracted dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extracted, "templates/dep.yaml")); err != nil {
		t.Errorf("templates/dep.yaml missing in extracted dir: %v", err)
	}
}

// TestLookupAndExtractNestedChartLayout pins extraction of a REAL Helm OCI chart
// archive, whose contents live under a single top-level <chartname>/ directory
// (unlike the flat synthetic fixture above). Extraction is verbatim: the chart
// lands at extracted/<chartname>/Chart.yaml, NOT extracted/Chart.yaml. The
// consumer (the helm engine's resolveChartRoot) descends into <chartname>/;
// LookupAndExtract itself returns the raw extracted-archive root.
func TestLookupAndExtractNestedChartLayout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZ(t, map[string]string{
		"mariadb/Chart.yaml":         "apiVersion: v2\nname: mariadb\nversion: 25.0.8\n",
		"mariadb/templates/dep.yaml": "kind: Deployment\n",
	})
	digest := digestOf(blob)
	seedSlot(t, root, ".aggrega/cache", "helm-oci-chart", "oci://r/c", "25.0.8", blob, digest)
	lf := fixtureLockfile("helm-oci-chart", "oci://r/c", "25.0.8", digest)

	extracted, err := sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "25.0.8", root, ".aggrega/cache")
	if err != nil {
		t.Fatalf("LookupAndExtract: %v", err)
	}
	// The chart is nested under mariadb/ — the real OCI layout.
	if _, err := os.Stat(filepath.Join(extracted, "mariadb", "Chart.yaml")); err != nil {
		t.Errorf("Chart.yaml missing under extracted/mariadb/: %v", err)
	}
	// And NOT flattened to the extracted root — confirming the consumer must
	// descend (the blind spot the flat fixture above hid).
	if _, err := os.Stat(filepath.Join(extracted, "Chart.yaml")); err == nil {
		t.Error("Chart.yaml unexpectedly at the extracted root; the nested layout should be preserved verbatim")
	}
}

func TestLookupAndExtractMissingLockfileEntryWrapsNotFound(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lf := &lockfile.Lockfile{Version: 1, Release: "./r.yaml", GeneratedAt: "2026-05-22T10:00:00Z"}
	_, err := sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "1.0.0", root, ".aggrega/cache")
	if !errors.Is(err, sourcecache.ErrFetchNotFound) {
		t.Errorf("missing lockfile entry should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestLookupAndExtractTamperedArchiveWrapsIntegrity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	originalBlob := buildTGZ(t, map[string]string{"Chart.yaml": "real\n"})
	originalDigest := digestOf(originalBlob)

	// Seed lockfile + metadata.yaml with the ORIGINAL digest, then
	// overwrite the blob.tgz with tampered bytes (different digest).
	slot := seedSlot(t, root, ".aggrega/cache", "helm-oci-chart", "oci://r/c", "1.0.0", originalBlob, originalDigest)
	tamperedBlob := buildTGZ(t, map[string]string{"Chart.yaml": "tampered\n"})
	if err := os.WriteFile(filepath.Join(slot, "blob.tgz"), tamperedBlob, 0o644); err != nil {
		t.Fatalf("tamper blob: %v", err)
	}

	lf := fixtureLockfile("helm-oci-chart", "oci://r/c", "1.0.0", originalDigest)
	_, err := sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "1.0.0", root, ".aggrega/cache")
	if !errors.Is(err, sourcecache.ErrFetchIntegrity) {
		t.Errorf("tampered .tgz should wrap ErrFetchIntegrity; got %v", err)
	}
}

func TestLookupAndExtractMetadataMismatchWrapsIntegrity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZ(t, map[string]string{"Chart.yaml": "real\n"})
	blobDigest := digestOf(blob)

	// Seed cache slot with the CORRECT blob but a metadata.yaml
	// recording a DIFFERENT digest.
	seedSlot(t, root, ".aggrega/cache", "helm-oci-chart", "oci://r/c", "1.0.0", blob, "sha256:1111111111111111111111111111111111111111111111111111111111111111")

	lf := fixtureLockfile("helm-oci-chart", "oci://r/c", "1.0.0", blobDigest)
	_, err := sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "1.0.0", root, ".aggrega/cache")
	if !errors.Is(err, sourcecache.ErrFetchIntegrity) {
		t.Errorf("metadata digest mismatch should wrap ErrFetchIntegrity; got %v", err)
	}
}

func TestExtractTarGzDeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZ(t, map[string]string{
		"a.txt":     "alpha\n",
		"sub/b.txt": "beta\n",
	})
	digest := digestOf(blob)
	seedSlot(t, root, ".aggrega/cache", "helm-oci-chart", "oci://r/c", "1.0.0", blob, digest)
	lf := fixtureLockfile("helm-oci-chart", "oci://r/c", "1.0.0", digest)

	extracted1, err := sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "1.0.0", root, ".aggrega/cache")
	if err != nil {
		t.Fatalf("Extract 1: %v", err)
	}
	a1, _ := os.ReadFile(filepath.Join(extracted1, "a.txt"))
	b1, _ := os.ReadFile(filepath.Join(extracted1, "sub/b.txt"))

	extracted2, err := sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "1.0.0", root, ".aggrega/cache")
	if err != nil {
		t.Fatalf("Extract 2: %v", err)
	}
	a2, _ := os.ReadFile(filepath.Join(extracted2, "a.txt"))
	b2, _ := os.ReadFile(filepath.Join(extracted2, "sub/b.txt"))

	if !bytes.Equal(a1, a2) || !bytes.Equal(b1, b2) {
		t.Errorf("extracted bytes not deterministic across runs")
	}
}

func TestExtractedDirectoryRegeneratedFromArchive(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZ(t, map[string]string{"real.yaml": "original\n"})
	digest := digestOf(blob)
	slot := seedSlot(t, root, ".aggrega/cache", "helm-oci-chart", "oci://r/c", "1.0.0", blob, digest)

	// Pre-populate extracted/ with a "tampered" file that should NOT
	// survive the next LookupAndExtract.
	tamperedDir := filepath.Join(slot, "extracted")
	if err := os.MkdirAll(tamperedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tamperedFile := filepath.Join(tamperedDir, "tampered.yaml")
	if err := os.WriteFile(tamperedFile, []byte("inserted by attacker\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lf := fixtureLockfile("helm-oci-chart", "oci://r/c", "1.0.0", digest)
	extracted, err := sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "1.0.0", root, ".aggrega/cache")
	if err != nil {
		t.Fatalf("LookupAndExtract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extracted, "tampered.yaml")); !os.IsNotExist(err) {
		t.Errorf("tampered file should be gone after re-extract; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(extracted, "real.yaml")); err != nil {
		t.Errorf("real.yaml should be present after re-extract: %v", err)
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Build an archive containing an entry whose name escapes the
	// destination via ../.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:     "../../etc/passwd-escape",
		Mode:     0o644,
		Size:     int64(len("malicious\n")),
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0).UTC(),
		Format:   tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("malicious\n")); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	blob := buf.Bytes()
	digest := digestOf(blob)
	seedSlot(t, root, ".aggrega/cache", "helm-oci-chart", "oci://r/c", "1.0.0", blob, digest)
	lf := fixtureLockfile("helm-oci-chart", "oci://r/c", "1.0.0", digest)

	_, err := sourcecache.LookupAndExtract(lf, "helm-oci-chart", "oci://r/c", "1.0.0", root, ".aggrega/cache")
	if !errors.Is(err, sourcecache.ErrFetchIntegrity) {
		t.Errorf("path-traversal entry should halt with ErrFetchIntegrity; got %v", err)
	}
	// And the malicious file MUST NOT have been written to the parent.
	if _, statErr := os.Stat(filepath.Join(root, ".aggrega/cache/source-artifacts", "etc/passwd-escape")); !os.IsNotExist(statErr) {
		t.Errorf("malicious file leaked outside destination; stat err = %v", statErr)
	}
}
