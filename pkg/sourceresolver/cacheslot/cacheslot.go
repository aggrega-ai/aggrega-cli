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
//	<workspaceRoot>/<cacheDir>/source-artifacts/<kind>/<host>/<path>/<chart>/<module-subdir>/<version>/<digest-prefix>/
//
// The <chart> segment is present only for a repo-root helm source (where the
// URL path is the repository root and the chart name distinguishes artifacts
// served from it); the <module-subdir> segment only for a git source that names
// a directory within the cloned repository. Both are dropped for kinds that pass
// them empty, keeping those kinds' slot layout unchanged. The two are separate
// segments rather than one shared one because they answer the same question for
// different kinds and a single segment would make one kind's slot path readable
// as the other's. The blob lives at <slot>/blob.tgz and the metadata at
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
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
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
// kinds that identify by URL alone (a direct .tgz, OCI, compose) keep the
// slot layout they had before chart entered the identity.
//
// moduleSubdir is the directory within a git-sourced repository that is the
// artifact; it becomes its own intermediate segment so two directories of one
// repository at one commit occupy distinct slots. Its segments are
// percent-escaped the way URL-path segments are. An empty moduleSubdir is
// dropped, so every other kind's layout is unchanged.
//
// Every value keeps to the place its position names. The kind, the URL's host,
// the chart, the version and the digest prefix each name one directory; the
// URL's path and the module subdirectory may descend beneath their place but
// not climb above it, since an escaped segment still reads `..` as a step up.
// A value that would leave its place returns the refusal
// oci.ErrCacheLocationOutsideCache names, quoting the value under the source
// key it was written as, and no slot is returned for it — see
// oci.JoinCacheLocation, which decides it.
//
// Returns a wrapped ErrEmptyWorkspaceRoot when workspaceRoot is
// empty; callers that legitimately want a CWD-relative slot must
// pass "." explicitly.
func Slot(workspaceRoot, cacheDir, kind, sourceURL, chart, moduleSubdir, version, digest string) (string, error) {
	if workspaceRoot == "" {
		return "", fmt.Errorf("%w (kind=%s url=%s chart=%s module_subdir=%s version=%s)",
			ErrEmptyWorkspaceRoot(), kind, sourceURL, chart, moduleSubdir, version)
	}
	host, urlPath := splitURL(sourceURL)
	return oci.JoinCacheLocation(SourceArtifactsRoot(workspaceRoot, cacheDir),
		fmt.Sprintf("%s source %q", kind, sourceURL),
		oci.CacheSegment{Key: "kind", Value: kind, Segment: kind},
		oci.CacheSegment{Key: "url", Value: sourceURL, Segment: host},
		oci.CacheSegment{Key: "url", Value: sourceURL, Segment: urlPath, Nested: true},
		oci.CacheSegment{Key: "chart", Value: chart, Segment: chart},
		oci.CacheSegment{Key: "module_subdir", Value: moduleSubdir, Segment: escapeRelPath(moduleSubdir), Nested: true},
		oci.CacheSegment{Key: "version", Value: version, Segment: version},
		oci.CacheSegment{Key: "digest", Value: digest, Segment: digestPrefix(digest)},
	)
}

// escapeRelPath percent-escapes each segment of a slash-separated relative path
// so no segment carries a separator of its own and the path contributes exactly
// as many directory levels as it has segments. Escaping leaves a `..` segment as
// it is, so it does not keep the path inside its place; Slot's containment does.
// Empty input returns empty, which filepath.Join drops.
func escapeRelPath(rel string) string {
	if rel == "" {
		return ""
	}
	segments := strings.Split(strings.Trim(rel, "/"), "/")
	encoded := make([]string, 0, len(segments))
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		encoded = append(encoded, url.PathEscape(seg))
	}
	if len(encoded) == 0 {
		return ""
	}
	return path.Join(encoded...)
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
//
// workspaceRoot and cacheDir are the workspace the cache belongs to and its
// artifact cache directory, as [Slot] was given them. The slot, and every file
// about to be written in it, is held, before anything is written, to the root
// that cache directory picks (writescope.RootForCache): the cache directory itself.
// A directory on the way to the slot, or a file of the slot, that is a link leading
// out of the cache directory — or a cache directory leading out of the workspace —
// is refused with the halt the write-scope rule declares, rather than written
// through.
func WriteBlobAndMetadata(workspaceRoot, cacheDir, slotDir string, archiveBytes []byte, kind, sourceURL, version, digest, mediatype, resolvedURL string, manifestBytes []byte) error {
	root := writescope.RootForCache(workspaceRoot, cacheDir)
	written := []string{slotDir, filepath.Join(slotDir, "blob.tgz"), filepath.Join(slotDir, "metadata.yaml")}
	if len(manifestBytes) > 0 {
		written = append(written, filepath.Join(slotDir, ManifestFile))
	}
	for _, p := range written {
		if err := writescope.CheckWrite(root, p); err != nil {
			return err
		}
	}
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
