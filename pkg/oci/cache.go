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
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
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
// URI encoding replaces the path separators `/` and `\` with
// URL-percent-encoded forms so the URI occupies one directory level.
// Two CachePath invocations with the same inputs produce
// byte-identical paths.
//
// The reference and the version each name one directory, and a slot
// that either would move elsewhere is refused rather than built: a
// version that carries a separator or names `.` or `..`, or a reference
// whose encoding is `.` or `..`, returns the refusal
// [ErrCacheLocationOutsideCache] names, quoting the value. The version
// comes from a record read back from disk, from a reference an author
// wrote, or from a body a registry served, so nothing upstream of this
// builder is relied on to have held it to that shape; see
// [JoinCacheLocation].
func CachePath(workspaceRoot, cacheDir, uri, version string) (string, error) {
	return JoinCacheLocation(EntitiesRoot(workspaceRoot, cacheDir), fmt.Sprintf("entity %q", uri),
		CacheSegment{Key: "reference", Value: uri, Segment: encodeURI(uri)},
		CacheSegment{Key: "version", Value: version, Segment: version},
	)
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
// cache uses, so a host maps to one stable filesystem segment. The base path
// doubles as the directory holding the host's per-repository index files
// (IndexRepoPath composes each repo file beneath it) and as the stem of the
// freshness-timestamp sibling (base + ".updated"), which sits beside that
// directory without colliding with it. The encoder is spelled once here, so an
// index writer and an index reader cannot disagree about a host's on-disk
// segment.
func IndexBasePath(workspaceRoot, cacheDir, host string) string {
	return filepath.Join(IndexRoot(workspaceRoot, cacheDir), encodeURI(host))
}

// IndexRepoPath returns the per-repository index file path under a host's index
// directory:
//
//	<workspaceRoot>/<cacheDir>/index/<host-encoded>/<repo-encoded>.json
//
// The host segment is the same single encoded segment IndexBasePath spells (the
// per-host base doubles as the directory holding these per-repo files), and the
// repository is encoded through the same single-segment URI encoder, so a
// repository maps to one stable filesystem segment. The per-repo spelling lives
// once here, so an index writer and reader cannot disagree about a repository's
// on-disk path.
func IndexRepoPath(workspaceRoot, cacheDir, host, repo string) string {
	return filepath.Join(IndexBasePath(workspaceRoot, cacheDir, host), encodeURI(repo)+".json")
}

// SignatureBundlePath returns the content-addressed filesystem path of the
// cached signature-bundle blob named by bundleDigest under the `signatures/`
// subtree of the workspace's artifact cache:
//
//	<workspaceRoot>/<cacheDir>/signatures/<digest>.bundle
//
// The digest is rendered as a single filesystem segment via the shared
// digest-segment spelling (digest.Segment), so the blob name is a stable
// single segment. It is the one site that spells the bundle-blob location,
// shared by the pull path that writes each verified signature bundle and the
// build path that re-hashes the cached blob against its recorded digest.
//
// The path is joined to the workspace root the way every sibling subtree of the
// cache is (see [EntitiesRoot]), so the write and the read land on one file
// whatever directory each was run from.
func SignatureBundlePath(workspaceRoot, cacheDir, bundleDigest string) string {
	name := digest.Segment(bundleDigest) + ".bundle"
	return filepath.Join(workspaceRoot, cacheDir, "signatures", name)
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
	slot, err := CachePath(workspaceRoot, cacheDir, uri, version)
	if err != nil {
		return nil, err
	}
	blob := filepath.Join(slot, "blob")
	data, err := os.ReadFile(blob)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("oci: cache miss for %s@%s: %w", uri, version, ErrFetchNotFound())
		}
		return nil, fmt.Errorf("oci: cache read %s@%s: %w", uri, version, err)
	}
	return data, nil
}

// WriteCached stores bytes at the cache slot for uri@version,
// creating parent directories as needed. The blob filename is
// "blob"; callers wanting a multi-file layout should use the
// directory-aware variants that future implementations introduce.
//
// The slot is held to the workspace first: see [WriteCachedFile].
func WriteCached(workspaceRoot, cacheDir, uri, version string, data []byte) error {
	return WriteCachedFile(workspaceRoot, cacheDir, uri, version, "blob", data)
}

// WriteCachedFile stores data under name in the cache slot for uri@version,
// creating the slot as needed. It is the one write every file a slot holds goes
// through, so the slot is held to the workspace in one place.
//
// The slot is held, before anything is written, to the root the cache directory
// picks ([writescope.RootForCache]): the workspace at workspaceRoot, or the cache
// directory itself where the configuration names one outside the workspace. A
// cache directory, or a directory on the way to the slot, that is a link leading
// out of that root is refused with the halt the write-scope rule declares, rather
// than written through.
func WriteCachedFile(workspaceRoot, cacheDir, uri, version, name string, data []byte) error {
	dir, err := CachePath(workspaceRoot, cacheDir, uri, version)
	if err != nil {
		return err
	}
	file := filepath.Join(dir, name)
	if err := writescope.CheckWrite(writescope.RootForCache(workspaceRoot, cacheDir), file); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("oci: cache mkdir %s: %w", dir, err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return fmt.Errorf("oci: cache write %s: %w", file, err)
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
	slot, err := CachePath(workspaceRoot, cacheDir, uri, version)
	if err != nil {
		return nil, err
	}
	blob := filepath.Join(slot, "manifest")
	data, err := os.ReadFile(blob)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("oci: manifest cache miss for %s@%s: %w", uri, version, ErrFetchNotFound())
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
	return WriteCachedFile(workspaceRoot, cacheDir, uri, version, "manifest", manifestBytes)
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
