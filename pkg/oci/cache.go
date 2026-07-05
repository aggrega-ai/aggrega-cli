// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// CachePath returns the cache slot for the given URI + version under
// the workspace's configured cache directory's `entities/` subtree:
//
//	<workspaceRoot>/<cacheDir>/entities/<URI-encoded>/<version>/
//
// The `entities/` segment partitions the cache root from the
// sibling `<cacheDir>/source-artifacts/<kind>/<host>/<path>/<version>/<digest-prefix>/`
// subtree the source-resolver populates. Catalog pulls and source-
// artifact pulls live in parallel sibling directories so cache
// inspection by hand surfaces a clean two-subtree structure and
// the future `aggrega pull` invocation populates both axes
// uniformly.
//
// URI encoding replaces filesystem-unsafe characters (the path
// separator `/`, the scheme separator `://`, the version-pin `:`,
// the registry-port `:`) with URL-percent-encoded forms so the slot
// is a stable, single-segment directory across operating systems.
// Two CachePath invocations with the same inputs produce
// byte-identical paths.
func CachePath(workspaceRoot, cacheDir, uri, version string) string {
	encoded := encodeURI(uri)
	return filepath.Join(workspaceRoot, cacheDir, "entities", encoded, version)
}

// ReadCached returns the bytes previously stored at the cache slot
// for uri@version. Returns a wrapped ErrFetchNotFound when the slot
// is empty (cache miss); other errors surface as wrapped read
// failures.
//
// The single-blob layout assumes one artifact per (uri, version)
// pair. Future cache layouts that store multi-file extracted
// artifacts (e.g., un-tarred Recipe directories) replace this
// function with a directory-walking variant; callers should consult
// the layout convention in effect at their entry point.
func ReadCached(workspaceRoot, cacheDir, uri, version string) ([]byte, error) {
	blob := filepath.Join(CachePath(workspaceRoot, cacheDir, uri, version), "blob")
	data, err := os.ReadFile(blob)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("oci: cache miss for %s@%s: %w", uri, version, ErrFetchNotFound)
		}
		return nil, fmt.Errorf("oci: cache read %s@%s: %w", uri, version, err)
	}
	return data, nil
}

// WriteCached stores bytes at the cache slot for uri@version,
// creating parent directories as needed. The blob filename is
// "blob"; callers wanting a multi-file layout should use the
// directory-aware variants that future implementations introduce.
func WriteCached(workspaceRoot, cacheDir, uri, version string, data []byte) error {
	dir := CachePath(workspaceRoot, cacheDir, uri, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("oci: cache mkdir %s: %w", dir, err)
	}
	blob := filepath.Join(dir, "blob")
	if err := os.WriteFile(blob, data, 0o644); err != nil {
		return fmt.Errorf("oci: cache write %s: %w", blob, err)
	}
	return nil
}

// ReadCachedManifest returns the raw OCI manifest bytes previously stored
// alongside the content blob in the cache slot for uri@version. It is the
// second, self-verifying blob: a reader re-hashes these bytes and confirms they
// match the manifest digest the artifact commits to. Returns a wrapped
// ErrFetchNotFound when the manifest blob is absent (an incomplete slot — a
// content blob written without its manifest); other errors surface as wrapped
// read failures. Mirrors ReadCached; the filename is "manifest".
func ReadCachedManifest(workspaceRoot, cacheDir, uri, version string) ([]byte, error) {
	blob := filepath.Join(CachePath(workspaceRoot, cacheDir, uri, version), "manifest")
	data, err := os.ReadFile(blob)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("oci: manifest cache miss for %s@%s: %w", uri, version, ErrFetchNotFound)
		}
		return nil, fmt.Errorf("oci: manifest cache read %s@%s: %w", uri, version, err)
	}
	return data, nil
}

// WriteCachedManifest stores the raw OCI manifest bytes as a second blob in the
// same cache slot as the content blob for uri@version, creating parent
// directories as needed. The filename is "manifest"; it sits beside the "blob"
// content file WriteCached writes. Persisting the manifest lets a later offline
// read re-derive the manifest digest from a stored artifact rather than trusting
// a recorded value with nothing to check it against.
func WriteCachedManifest(workspaceRoot, cacheDir, uri, version string, manifestBytes []byte) error {
	dir := CachePath(workspaceRoot, cacheDir, uri, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("oci: manifest cache mkdir %s: %w", dir, err)
	}
	blob := filepath.Join(dir, "manifest")
	if err := os.WriteFile(blob, manifestBytes, 0o644); err != nil {
		return fmt.Errorf("oci: manifest cache write %s: %w", blob, err)
	}
	return nil
}

// ContentDigestTag converts a content digest ("sha256:<hex>") into a valid OCI
// tag: a tag may not contain ":", so the algorithm and hex are joined with "-"
// ("sha256-<hex>"). An artifact addressed by content can then be tagged by this
// digest-derived tag, distinct from any version tag in the same repository. The
// push that publishes an artifact and the pull that fetches it both derive the
// tag here, so the two sides never disagree on the tag string.
func ContentDigestTag(contentDigest string) string {
	return strings.ReplaceAll(contentDigest, ":", "-")
}

// encodeURI maps an OCI URI (e.g.,
// `oci://hub.aggrega.ai/component/core/api-server` or
// `hub.aggrega.ai/component/core/api-server`) to a single
// filesystem-safe segment via standard URL path-segment encoding.
// Slashes within the URI body are percent-encoded so the entire URI
// occupies one directory level. Two encodings of the same URI are
// identical; decoding (not implemented here; not needed by current
// consumers) is round-trippable via url.PathUnescape.
func encodeURI(uri string) string {
	uri = strings.TrimPrefix(uri, "oci://")
	return url.PathEscape(uri)
}
