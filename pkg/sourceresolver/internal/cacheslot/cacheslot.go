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
//	<workspaceRoot>/<cacheDir>/source-artifacts/<kind>/<host>/<path>/<version>/<digest-prefix>/
//
// The blob lives at <slot>/blob.tgz and the metadata at
// <slot>/metadata.yaml. The extracted/ subdirectory is regenerated
// on every build read by a downstream cache layer (the rebuild
// discipline lives outside this package).
package cacheslot

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/metadata"
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
var ErrEmptyWorkspaceRoot = errors.New("cacheslot: empty workspaceRoot")

// Slot computes the absolute cache-slot directory path for a
// resolved source artifact. The URL host and URL-path components
// are extracted and used as intermediate directory levels so
// inspecting the cache by hand surfaces a recognisable structure.
//
// The digest argument may be the full "sha256:<hex>" form returned
// by internal/archive.Digest or just the hex. Either way, only the
// first DigestPrefixLen hex chars are used as the leaf segment.
//
// Returns a wrapped ErrEmptyWorkspaceRoot when workspaceRoot is
// empty; callers that legitimately want a CWD-relative slot must
// pass "." explicitly.
func Slot(workspaceRoot, cacheDir, kind, sourceURL, version, digest string) (string, error) {
	if workspaceRoot == "" {
		return "", fmt.Errorf("%w (kind=%s url=%s version=%s)",
			ErrEmptyWorkspaceRoot, kind, sourceURL, version)
	}
	host, urlPath := splitURL(sourceURL)
	prefix := digestPrefix(digest)
	return filepath.Join(
		workspaceRoot, cacheDir, "source-artifacts",
		kind, host, urlPath, version, prefix,
	), nil
}

// WriteBlobAndMetadata creates the cache slot at slotDir if missing,
// writes blob.tgz with the archive bytes, and writes metadata.yaml
// with the CacheMetadata. The two writes are not transactional —
// crash between them leaves a half-populated slot, which the next
// pull rewrites. Adapters re-pull on any digest mismatch, so partial
// state is self-healing rather than load-bearing.
//
// Mediatype is the canonical media-type string for the kind (e.g.,
// "application/vnd.cncf.helm.chart.content.v1.tar+gzip" for helm
// charts, "application/x-yaml" for compose fragments). The caller
// supplies it; this package does not infer.
//
// PulledAt is set to time.Now().UTC() at write time.
func WriteBlobAndMetadata(slotDir string, archiveBytes []byte, kind, sourceURL, version, digest, mediatype string) error {
	if err := os.MkdirAll(slotDir, 0o755); err != nil {
		return fmt.Errorf("cacheslot: mkdir %s: %w", slotDir, err)
	}
	blobPath := filepath.Join(slotDir, "blob.tgz")
	if err := os.WriteFile(blobPath, archiveBytes, 0o644); err != nil {
		return fmt.Errorf("cacheslot: write blob %s: %w", blobPath, err)
	}
	meta := metadata.CacheMetadata{
		Kind:      kind,
		URL:       sourceURL,
		Version:   version,
		Digest:    digest,
		Mediatype: mediatype,
		PulledAt:  time.Now().UTC(),
	}
	metaPath := filepath.Join(slotDir, "metadata.yaml")
	if err := metadata.Write(metaPath, meta); err != nil {
		return fmt.Errorf("cacheslot: write metadata %s: %w", metaPath, err)
	}
	return nil
}

func splitURL(sourceURL string) (host, urlPath string) {
	trimmed := sourceURL
	trimmed = strings.TrimPrefix(trimmed, "oci://")
	trimmed = strings.TrimPrefix(trimmed, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	trimmed = strings.TrimPrefix(trimmed, "git+ssh://")
	trimmed = strings.TrimPrefix(trimmed, "ssh://")
	trimmed = strings.TrimPrefix(trimmed, "git://")

	idx := strings.Index(trimmed, "/")
	if idx < 0 {
		host = trimmed
		urlPath = ""
		return
	}
	host = trimmed[:idx]
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

func digestPrefix(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	if len(hex) > DigestPrefixLen {
		return hex[:DigestPrefixLen]
	}
	return hex
}
