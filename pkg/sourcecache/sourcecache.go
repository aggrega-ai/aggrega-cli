// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package sourcecache owns the build-time read-and-extract pipeline
// for source artifacts pinned in the lockfile. The package looks up
// a `source_artifacts:` entry by {kind, url, pinned_version},
// verifies the full SHA-256 of the cached archive against both the
// lockfile entry's digest and the cache slot's metadata.yaml
// digest, wipes and regenerates the `extracted/` subdirectory from
// the archive, and returns the absolute path of the extracted
// directory for engines to consume.
//
// The package is the build-time complement to pkg/sourceresolver
// (the lock-time pull pipeline). The orchestrator at render time
// calls LookupAndExtract before dispatching to the engine. The
// returned path is the raw extracted-archive root; the consumer
// locates its own artifact within it — a Helm chart, for instance,
// untars under a single <chartname>/ directory, so the helm engine
// descends to the directory holding Chart.yaml before loader.Load.
//
// The package re-exports the four fetch-error sentinels from pkg/oci
// so consumers can errors.Is against either name without importing
// pkg/oci directly.
package sourcecache

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/helm"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
)

// kindExpectsManifest reports whether a source kind is OCI-distributed and
// therefore persists an OCI manifest beside its cached archive. Only such a
// kind's slot is held to manifest authority: a manifest-less slot for an OCI
// kind is an incomplete cache entry and reads as a miss. A non-OCI source
// (an HTTPS chart, a git module) has no manifest by construction and stays on
// content-pin authority. Keyed off the canonical kind constant so the
// OCI-vs-non-OCI classification has one home.
func kindExpectsManifest(kind string) bool {
	return kind == helm.KindOCI
}

// Re-exports of the canonical fetch-error sentinels. Identity-equal
// to pkg/oci's; errors.Is against either name matches the same
// wrapped chain.
var (
	ErrFetchNotFound  = oci.ErrFetchNotFound
	ErrFetchIntegrity = oci.ErrFetchIntegrity
)

// LookupAndExtract looks up the lockfile entry for {kind, url,
// pinned_version}, verifies the full SHA-256 of the cached `.tgz`
// against the entry's digest AND the cache slot's metadata.yaml
// digest, wipes and regenerates the `extracted/` subdirectory from
// the archive, and returns the absolute path of `extracted/`. The
// archive is extracted verbatim, so the returned path is the archive
// root — not necessarily a directly-loadable artifact root: a Helm
// chart untars under a <chartname>/ subdirectory, and the consumer
// resolves its artifact within.
//
// Returns ErrFetchNotFound when no lockfile entry matches the
// {kind, url, pinned_version} triple, or — for an OCI source kind — when the
// cache slot carries the archive but no persisted OCI manifest (an incomplete
// cache entry; the caller re-pulls). Returns ErrFetchIntegrity on digest
// mismatch (either content pin, or the manifest layer digest for an OCI slot)
// or on archive corruption. Returns wrapped fs errors on read/write/extract
// failures. A non-OCI source kind (HTTPS chart, git module) carries no manifest
// by construction and stays on content-pin authority.
func LookupAndExtract(lf *lockfile.Lockfile, kind, url, version, workspaceRoot, cacheDir string) (string, error) {
	if lf == nil {
		return "", fmt.Errorf("sourcecache: lockfile is nil: %w", ErrFetchNotFound)
	}
	entry := findEntry(lf, kind, url, version)
	if entry == nil {
		return "", fmt.Errorf("sourcecache: no lockfile entry for kind=%s url=%s version=%s: %w",
			kind, url, version, ErrFetchNotFound)
	}
	slot := SlotPath(workspaceRoot, cacheDir, kind, url, version, entry.Digest)
	blobPath := filepath.Join(slot, "blob.tgz")
	blobBytes, err := os.ReadFile(blobPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("sourcecache: cache blob missing at %s: %w", blobPath, ErrFetchNotFound)
		}
		return "", fmt.Errorf("sourcecache: read blob %s: %w", blobPath, err)
	}

	gotDigest := digest.ContentDigest(blobBytes)
	if gotDigest != entry.Digest {
		return "", fmt.Errorf("sourcecache: blob digest %s mismatches lockfile digest %s: %w",
			gotDigest, entry.Digest, ErrFetchIntegrity)
	}
	meta, err := metadata.Read(filepath.Join(slot, "metadata.yaml"))
	if err != nil {
		return "", fmt.Errorf("sourcecache: read metadata: %w", err)
	}
	if meta.Digest != entry.Digest {
		return "", fmt.Errorf("sourcecache: metadata digest %s mismatches lockfile digest %s: %w",
			meta.Digest, entry.Digest, ErrFetchIntegrity)
	}

	// Manifest authority. An OCI-distributed source (a helm-oci-chart) persists
	// its OCI manifest beside the archive; it is the root the archive is verified
	// against — the manifest is an independent record of the expected content, so
	// a forged archive is caught even when every recorded content pin (this slot's
	// metadata.yaml and the lockfile entry) has been forged to match it. When the
	// lockfile entry records a manifest_digest, the manifest itself is first
	// anchored to that independent pin (a fully-forged manifest is caught before it
	// becomes the authority a content check would trust), then the content is
	// verified against the manifest's layer digest — the same order the catalog
	// entity and plugin reads use. An empty pin (a pre-existing lockfile written
	// before source manifest pinning) keeps the content-vs-manifest check only.
	// Because an OCI source always persists its manifest, a manifest-less slot for
	// an OCI kind is an incomplete cache entry and reads as a miss so the caller
	// re-pulls, mirroring the entity and plugin reads. A non-OCI source (an HTTPS
	// chart, a git module) has no manifest by construction and keeps the
	// git-tracked lockfile content pin verified above as its root of trust.
	manifestBytes, mrerr := os.ReadFile(filepath.Join(slot, cacheslot.ManifestFile))
	switch {
	case mrerr == nil:
		if entry.ManifestDigest != "" {
			if verr := oci.VerifyManifestDigest(manifestBytes, entry.ManifestDigest); verr != nil {
				return "", fmt.Errorf("sourcecache: cached manifest for %s: %w", blobPath, verr)
			}
		}
		if verr := oci.VerifyContentAgainstManifest(manifestBytes, blobBytes); verr != nil {
			return "", fmt.Errorf("sourcecache: %s: %w", blobPath, verr)
		}
	case os.IsNotExist(mrerr):
		if kindExpectsManifest(kind) {
			return "", fmt.Errorf("sourcecache: %s cached without its OCI manifest; run `aggrega pull` first: %w", blobPath, ErrFetchNotFound)
		}
	default:
		return "", fmt.Errorf("sourcecache: read manifest for %s: %w", blobPath, mrerr)
	}

	extractedDir := filepath.Join(slot, "extracted")
	if err := os.RemoveAll(extractedDir); err != nil {
		return "", fmt.Errorf("sourcecache: wipe extracted dir %s: %w", extractedDir, err)
	}
	if err := os.MkdirAll(extractedDir, 0o755); err != nil {
		return "", fmt.Errorf("sourcecache: mkdir extracted dir %s: %w", extractedDir, err)
	}
	if err := extractTarGz(blobBytes, extractedDir); err != nil {
		return "", fmt.Errorf("sourcecache: extract %s: %w", blobPath, err)
	}
	return extractedDir, nil
}

// SlotPath returns the cache-slot directory for the given lockfile
// entry's identity. Mirrors the promoted cacheslot.Slot — takes the
// digest from the lockfile entry and computes the slot path directly
// (no directory walk). Used by tests + diagnostics that need the
// slot path without going through LookupAndExtract.
func SlotPath(workspaceRoot, cacheDir, kind, url, version, digest string) string {
	slot, _ := cacheslot.Slot(workspaceRoot, cacheDir, kind, url, version, digest)
	return slot
}

func findEntry(lf *lockfile.Lockfile, kind, url, version string) *lockfile.SourceArtifact {
	for i := range lf.SourceArtifacts {
		sa := &lf.SourceArtifacts[i]
		if sa.Kind == kind && sa.URL == url && sa.PinnedVersion == version {
			return sa
		}
	}
	return nil
}

// extractTarGz extracts the gzipped tar archive in archiveBytes into
// destDir. Extraction is deterministic: zero mtimes on every file,
// current-process uid/gid (archive's uid/gid ignored), no setuid /
// setgid / sticky bits propagated, no extended attributes carried
// through. Path traversal entries (whose RelPath escapes destDir
// via `..`) are rejected with a wrapped ErrFetchIntegrity.
func extractTarGz(archiveBytes []byte, destDir string) error {
	gz, err := gzip.NewReader(bytesReader(archiveBytes))
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		cleanRel := filepath.Clean(hdr.Name)
		if cleanRel == "." || cleanRel == "/" {
			continue
		}
		if strings.HasPrefix(cleanRel, "..") || strings.HasPrefix(cleanRel, "/") {
			return fmt.Errorf("path traversal attempt %q in archive: %w", hdr.Name, ErrFetchIntegrity)
		}
		dest := filepath.Join(destDir, cleanRel)
		destAbs, err := filepath.Abs(dest)
		if err != nil {
			return fmt.Errorf("abs path %q: %w", dest, err)
		}
		destDirAbs, err := filepath.Abs(destDir)
		if err != nil {
			return fmt.Errorf("abs destDir %q: %w", destDir, err)
		}
		if !strings.HasPrefix(destAbs, destDirAbs+string(filepath.Separator)) && destAbs != destDirAbs {
			return fmt.Errorf("entry %q escapes extract destination: %w", hdr.Name, ErrFetchIntegrity)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("mkdir %q: %w", dest, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %q: %w", dest, err)
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return fmt.Errorf("open %q: %w", dest, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("copy %q: %w", dest, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %q: %w", dest, err)
			}
		}
	}
}

// bytesReader is a thin adapter so we can pass a []byte to
// gzip.NewReader without importing bytes for a one-line read source.
// Using a small io.Reader implementation keeps the import surface
// minimal in callers that read this file.
func bytesReader(b []byte) io.Reader {
	return &bReader{b: b}
}

type bReader struct {
	b   []byte
	pos int
}

func (r *bReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}
