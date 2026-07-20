// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
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
	return filepath.Join(EntitiesRoot(workspaceRoot, cacheDir), encoded, version)
}

// EntitiesRoot returns the root of the catalog-entity subtree of the
// workspace's artifact cache:
//
//	<workspaceRoot>/<cacheDir>/entities/
//
// It is the single site that names the `entities/` segment; CachePath
// composes each per-slot path beneath it, and cache-inspection tools
// enumerate the subtree from it, so the segment is spelled once.
func EntitiesRoot(workspaceRoot, cacheDir string) string {
	return filepath.Join(workspaceRoot, cacheDir, "entities")
}

// IndexRoot returns the root of the local searchable-index subtree of the
// workspace's artifact cache:
//
//	<workspaceRoot>/<cacheDir>/index/
//
// It is the single site that names the `index/` segment; IndexBasePath composes
// each per-host file path beneath it, so the segment is spelled once. It is the
// index sibling of EntitiesRoot: where the entities subtree caches pulled
// artifact bytes, the index subtree caches the per-host searchable records a
// registry update builds.
func IndexRoot(workspaceRoot, cacheDir string) string {
	return filepath.Join(workspaceRoot, cacheDir, "index")
}

// IndexBasePath returns the per-host base path (without file extension) under
// IndexRoot for a registry host:
//
//	<workspaceRoot>/<cacheDir>/index/<host-encoded>
//
// The host is encoded through the same single-segment URI encoder the entity
// cache uses, so a host maps to one stable filesystem segment and both the
// records file (base + ".json") and the freshness-timestamp sibling
// (base + ".updated") share it. The encoder is spelled once here, so an index
// writer and an index reader cannot disagree about a host's on-disk segment.
func IndexBasePath(workspaceRoot, cacheDir, host string) string {
	return filepath.Join(IndexRoot(workspaceRoot, cacheDir), encodeURI(host))
}

// SignatureBundlePath returns the content-addressed filesystem path of the
// cached signature-bundle blob named by bundleDigest under the cache's
// `signatures/` subtree:
//
//	<cacheDir>/signatures/<digest>.bundle
//
// The digest is rendered as a single filesystem segment via the shared
// digest-segment spelling (digest.Segment), so the blob name is a stable
// single segment. It is the one site that spells the bundle-blob location,
// shared by the pull path that writes each verified signature bundle and the
// build path that re-hashes the cached blob against its recorded digest.
func SignatureBundlePath(cacheDir, bundleDigest string) string {
	name := digest.Segment(bundleDigest) + ".bundle"
	return filepath.Join(cacheDir, "signatures", name)
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
// ("sha256-<hex>") via the shared digest-segment spelling (digest.Segment). An
// artifact addressed by content can then be tagged by this digest-derived tag,
// distinct from any version tag in the same repository. The plugin-describe
// cache uses this transform to address each cached descriptor file by its
// content pin: the read and the write derive the filename here, so the two
// sides never disagree on the on-disk name.
func ContentDigestTag(contentDigest string) string {
	return digest.Segment(contentDigest)
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
