// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package cacheslot owns cache-slot path computation and the
// combined "write blob + metadata" operation that every adapter
// performs after a successful pull. Centralising both keeps the
// slot layout consistent across kinds and gives the test suite a
// single point to assert path shape.
//
// Slot layout (under the workspace's configured cache directory):
//
//	<workspaceRoot>/<cacheDir>/source-artifacts/<kind>/<host>/<path>/<chart>/<version>/<digest-prefix>/
//
// The <chart> segment is present only for a repo-root helm source (where the
// URL path is the repository root and the chart name distinguishes artifacts
// served from it); it is dropped for kinds that pass an empty chart, keeping
// their slot layout unchanged. The blob lives at <slot>/blob.tgz and the metadata at
// <slot>/metadata.yaml. The extracted/ subdirectory is regenerated
// on every build read by a downstream cache layer (the rebuild
// discipline lives outside this package).
package cacheslot

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
)

// DigestPrefixLen is the number of hex characters of the digest used
// as the leaf cache-slot directory. Twelve characters is enough to
// avoid collisions across realistic per-kind/per-URL/per-version
// scopes while keeping paths short on length-constrained file
// systems.
const DigestPrefixLen = 12

// ErrEmptyWorkspaceRoot is returned by Slot when called with an
// empty workspaceRoot. Empty workspace roots produce relative paths
// that resolve against the process's CWD at file-write time, which
// silently produces a cache slot in an unexpected location — a
// programming error in every realistic call site (orchestrator,
// tests, debugging tools). The sentinel surfaces the mistake
// loudly rather than letting a buggy slot path survive into the
// cache.
func ErrEmptyWorkspaceRoot() error { return errEmptyWorkspaceRoot }

var errEmptyWorkspaceRoot = cerrors.Register("empty-workspace-root", cerrors.Meta{
	ShortCode: "E0187",
	Message:   "cacheslot: empty workspaceRoot",
	RulePlace: cerrors.NotARule("A cache slot was requested with an empty workspace root, which would resolve against the " +
		"process working directory at write time. It reports how this program called itself rather " +
		"than a rule over anything an author wrote."),
})

// Slot computes the absolute cache-slot directory path for a
// resolved source artifact. The URL host and URL-path components
// are extracted and used as intermediate directory levels so
// inspecting the cache by hand surfaces a recognisable structure.
//
// The digest argument may be the full "sha256:<hex>" form returned
// by internal/archive.Digest or just the hex. Either way, only the
// first DigestPrefixLen hex chars are used as the leaf segment.
//
// chart is the chart name for a repo-root helm source; it becomes an
// intermediate path segment so two charts served from one repo root at the same
// version occupy distinct slots. An empty chart is dropped by filepath.Join, so
// kinds that identify by URL alone (a direct .tgz, OCI, compose, git) keep the
// slot layout they had before chart entered the identity.
//
// Returns a wrapped ErrEmptyWorkspaceRoot when workspaceRoot is
// empty; callers that legitimately want a CWD-relative slot must
// pass "." explicitly.
func Slot(workspaceRoot, cacheDir, kind, sourceURL, chart, version, digest string) (string, error) {
	if workspaceRoot == "" {
		return "", fmt.Errorf("%w (kind=%s url=%s chart=%s version=%s)",
			ErrEmptyWorkspaceRoot(), kind, sourceURL, chart, version)
	}
	host, urlPath := splitURL(sourceURL)
	prefix := digestPrefix(digest)
	return filepath.Join(
		SourceArtifactsRoot(workspaceRoot, cacheDir),
		kind, host, urlPath, chart, version, prefix,
	), nil
}

// SourceArtifactsRoot returns the root of the source-artifact subtree of
// the workspace's artifact cache:
//
//	<workspaceRoot>/<cacheDir>/source-artifacts/
//
// It is the single site that names the `source-artifacts/` segment; Slot
// composes each per-slot path beneath it, and cache-inspection tools
// enumerate the subtree from it, so the segment is spelled once.
func SourceArtifactsRoot(workspaceRoot, cacheDir string) string {
	return filepath.Join(workspaceRoot, cacheDir, "source-artifacts")
}

// ManifestFile is the slot filename for the persisted OCI manifest — the
// authority the build-time read verifies the cached archive against. It sits
// beside blob.tgz and metadata.yaml. An OCI-distributed artifact writes it; a
// non-OCI source (an HTTPS .tgz with no manifest) passes nil manifestBytes and
// no file is written, so the read path treats the slot as manifest-less.
const ManifestFile = "manifest"

// WriteBlobAndMetadata creates the cache slot at slotDir if missing,
// writes blob.tgz with the archive bytes, writes metadata.yaml with
// the CacheMetadata, and — when manifestBytes is non-empty — persists
// the OCI manifest beside them. The writes are not transactional —
// crash between them leaves a half-populated slot, which the next
// pull rewrites. Adapters re-pull on any digest mismatch, so partial
// state is self-healing rather than load-bearing.
//
// Mediatype is the canonical media-type string for the kind (e.g.,
// "application/vnd.cncf.helm.chart.content.v1.tar+gzip" for helm
// charts, "application/x-yaml" for compose fragments). The caller
// supplies it; this package does not infer.
//
// manifestBytes is the raw OCI manifest JSON for an OCI-distributed
// artifact; the build-time read verifies the cached archive against
// the layer digest the manifest declares. Pass nil for a non-OCI
// source that has no manifest.
//
// sourceURL is the authored identity URL recorded on the metadata; resolvedURL
// is the concrete artifact location it resolved to (a repo-root helm source's
// direct .tgz), recorded as provenance. Pass an empty resolvedURL when the
// resolved location equals the authored URL, so the field is omitted and the
// metadata stays byte-identical for kinds that identify by URL alone.
//
// PulledAt is set to time.Now().UTC() at write time.
func WriteBlobAndMetadata(slotDir string, archiveBytes []byte, kind, sourceURL, version, digest, mediatype, resolvedURL string, manifestBytes []byte) error {
	if err := os.MkdirAll(slotDir, 0o755); err != nil {
		return fmt.Errorf("cacheslot: mkdir %s: %w", slotDir, err)
	}
	blobPath := filepath.Join(slotDir, "blob.tgz")
	if err := os.WriteFile(blobPath, archiveBytes, 0o644); err != nil {
		return fmt.Errorf("cacheslot: write blob %s: %w", blobPath, err)
	}
	if len(manifestBytes) > 0 {
		manifestPath := filepath.Join(slotDir, ManifestFile)
		if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
			return fmt.Errorf("cacheslot: write manifest %s: %w", manifestPath, err)
		}
	}
	meta := metadata.CacheMetadata{
		Kind:        kind,
		URL:         sourceURL,
		Version:     version,
		Digest:      digest,
		Mediatype:   mediatype,
		ResolvedURL: resolvedURL,
		PulledAt:    time.Now().UTC(),
	}
	metaPath := filepath.Join(slotDir, "metadata.yaml")
	if err := metadata.Write(metaPath, meta); err != nil {
		return fmt.Errorf("cacheslot: write metadata %s: %w", metaPath, err)
	}
	return nil
}

func splitURL(sourceURL string) (host, urlPath string) {
	// Host comes from the shared extractor so the cache-slot path agrees with
	// the remediation strings on one host per artifact and never carries URL
	// userinfo (credentials) into the on-disk path.
	host = registryhost.Of(sourceURL)

	trimmed := sourceURL
	trimmed = strings.TrimPrefix(trimmed, "oci://")
	trimmed = strings.TrimPrefix(trimmed, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	trimmed = strings.TrimPrefix(trimmed, "git+ssh://")
	trimmed = strings.TrimPrefix(trimmed, "ssh://")
	trimmed = strings.TrimPrefix(trimmed, "git://")

	idx := strings.Index(trimmed, "/")
	if idx < 0 {
		urlPath = ""
		return
	}
	rest := trimmed[idx+1:]
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		urlPath = ""
		return
	}
	segments := strings.Split(rest, "/")
	encoded := make([]string, len(segments))
	for i, seg := range segments {
		encoded[i] = url.PathEscape(seg)
	}
	urlPath = path.Join(encoded...)
	return
}

// digestPrefix intentionally keeps a case-preserving prefix strip rather than
// routing through the shared lowercasing normalizer: its input can be a digest
// read straight from the user-editable lockfile, which is not normalized to
// lowercase on load. Lowercasing here would shift the on-disk cache-slot path
// segment for a non-lowercase digest, so the strip stays local. The 12-char
// truncation is likewise local to this slot-naming rule.
func digestPrefix(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	if len(hex) > DigestPrefixLen {
		return hex[:DigestPrefixLen]
	}
	return hex
}
