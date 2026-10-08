// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/filefailure"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// CachePath returns the cache slot for the given URI + version under
// the workspace's configured cache directory's `entities/` subtree:
//
//	<workspaceRoot>/<cacheDir>/entities/<encoded-reference>/<encoded-version>/
//
// The `entities/` segment partitions the cache root from the
// sibling `<cacheDir>/source-artifacts/<kind>/<host>/<path>/<version>/<digest-prefix>/`
// subtree the source-resolver populates. Catalog pulls and source-
// artifact pulls live in parallel sibling directories so cache
// inspection by hand surfaces a clean two-subtree structure and
// the future `aggrega pull` invocation populates both axes
// uniformly.
//
// The reference, without any `oci://` scheme, occupies one directory, its
// separators spelled like every other character a name cannot carry, and the
// version occupies the one beneath it; both are spelled by [JoinCacheLocation]
// through [pathsegment.Encode], so `core/devtool/adminer:1.0.0` at `1.0.0` is
// the slot `core+zsdevtool+zsadminer+zx00003a1.0.0/1.0.0`, and a version that
// is a content digest is spelled `sha256+zx00003a<hex>`. Every name is one
// Windows, Linux and macOS accept, and two CachePath invocations with the same
// inputs produce byte-identical paths.
//
// The reference and the version each name one directory, and a slot
// that either would move elsewhere is refused rather than built: a
// version that carries a separator or names `.` or `..`, or a reference
// that is `.` or `..`, returns the refusal
// [ErrCacheLocationOutsideCache] names, quoting the value. The version
// comes from a record read back from disk, from a reference an author
// wrote, or from a body a registry served, so nothing upstream of this
// builder is relied on to have held it to that shape; see
// [JoinCacheLocation].
func CachePath(workspaceRoot, cacheDir, uri, version string) (string, error) {
	return JoinCacheLocation(EntitiesRoot(workspaceRoot, cacheDir), fmt.Sprintf("entity %q", uri),
		CacheSegment{Key: "reference", Value: uri, Segment: bareReference(uri), Whole: true},
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
//	<workspaceRoot>/<cacheDir>/index/<encoded-host>
//
// The host, without any `oci://` scheme, is spelled through the one spelling of
// a derived name ([pathsegment.Encode]), so a host — `localhost:5000` included —
// maps to one stable name every platform accepts. The base path doubles as the
// directory holding the host's per-repository index files (IndexRepoPath
// composes each repo file beneath it) and as the stem of the freshness-timestamp
// sibling (base + ".updated"), which sits beside that directory without colliding
// with it. The spelling is applied once here, so an index writer and an index
// reader cannot disagree about a host's on-disk name.
func IndexBasePath(workspaceRoot, cacheDir, host string) string {
	return filepath.Join(IndexRoot(workspaceRoot, cacheDir), pathsegment.Encode(bareReference(host)))
}

// IndexRepoPath returns the per-repository index file path under a host's index
// directory:
//
//	<workspaceRoot>/<cacheDir>/index/<encoded-host>/<encoded-repository>.json
//
// The host is the same name IndexBasePath spells (the per-host base doubles as
// the directory holding these per-repo files), and the repository is spelled
// whole, its separators included, through [pathsegment.Encode], so a repository
// maps to one stable file name every platform accepts. The per-repo spelling
// lives once here, so an index writer and reader cannot disagree about a
// repository's on-disk path.
func IndexRepoPath(workspaceRoot, cacheDir, host, repo string) string {
	return filepath.Join(IndexBasePath(workspaceRoot, cacheDir, host), pathsegment.Encode(bareReference(repo))+".json")
}

// SignatureBundlePath returns the content-addressed filesystem path of the
// cached signature-bundle blob named by bundleDigest under the `signatures/`
// subtree of the workspace's artifact cache:
//
//	<workspaceRoot>/<cacheDir>/signatures/<encoded-digest>.bundle
//
// The digest is spelled through the one spelling of a derived name
// ([pathsegment.Encode]), so `sha256:<hex>` is the file
// `sha256+zx00003a<hex>.bundle`, a name every platform accepts. It is the one
// site that spells the bundle-blob location, shared by the pull path that writes
// each verified signature bundle and the build path that re-hashes the cached
// blob against its recorded digest.
//
// The path is joined to the workspace root the way every sibling subtree of the
// cache is (see [EntitiesRoot]), so the write and the read land on one file
// whatever directory each was run from.
func SignatureBundlePath(workspaceRoot, cacheDir, bundleDigest string) string {
	name := pathsegment.Encode(bundleDigest) + ".bundle"
	return filepath.Join(workspaceRoot, cacheDir, "signatures", name)
}

// ReadCached returns the bytes previously stored at the cache slot
// for uri@version. Returns a wrapped ErrFetchNotFound when the slot
// is empty (cache miss); a slot read through a link leading out of
// the root the cache is held to returns the read hold's refusal as
// itself (see [ReadCachedFile]); other errors surface as wrapped read
// failures.
//
// The single-blob layout assumes one artifact per (uri, version)
// pair. Future cache layouts that store multi-file extracted
// artifacts (e.g., un-tarred Recipe directories) replace this
// function with a directory-walking variant; callers should consult
// the layout convention in effect at their entry point.
func ReadCached(workspaceRoot, cacheDir, uri, version string) ([]byte, error) {
	data, err := ReadCachedFile(workspaceRoot, cacheDir, uri, version, "blob")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("oci: cache miss for %s@%s: %w", uri, version, ErrFetchNotFound())
		}
		if heldOrMisplaced(err) {
			return nil, err
		}
		return nil, fmt.Errorf("oci: cache read %s@%s: %w", uri, version, err)
	}
	return data, nil
}

// ReadCachedFile returns the bytes stored under name in the cache slot for
// uri@version. It is the one read every file a slot holds goes through, as
// [WriteCachedFile] is the one write, so a slot is held to the cache directory in
// one place for reading as for writing.
//
// The file is held, before it is opened, to the root the cache directory picks
// ([writescope.RootForCache]) — the root its write is held to: the cache directory
// itself. A directory on the way to the slot, or the file itself, that is a link
// leading out of the cache directory — or a cache directory leading out of the
// workspace — is refused with the read hold's condition
// ([readscope.ErrReadLeadsOutOfWorkspace]), naming the link, and nothing is read
// through it. The refusal is returned as itself, so a reader that takes an absent
// file for a miss does not take it for one.
//
// A file that is not there is reported as [os.ReadFile] reports it, so
// [os.IsNotExist] tells a miss apart; a version or a reference that cannot address
// a slot is the refusal [CachePath] returns.
func ReadCachedFile(workspaceRoot, cacheDir, uri, version, name string) ([]byte, error) {
	dir, err := CachePath(workspaceRoot, cacheDir, uri, version)
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, name)
	if err := readscope.CheckCacheRead(writescope.RootForCache(workspaceRoot, cacheDir), file); err != nil {
		return nil, err
	}
	return os.ReadFile(file)
}

// heldOrMisplaced reports whether err refuses where a cache read would go — the
// read hold's refusal of a link leading out, or a value that cannot address a
// slot — rather than reporting what the read found there. A cache reader returns
// either as itself: the refusal names what to fix, and a wrapped one would bury it
// behind a read that never happened.
func heldOrMisplaced(err error) bool {
	return errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) || errors.Is(err, ErrCacheLocationOutsideCache())
}

// WriteCached stores bytes at the cache slot for uri@version,
// creating parent directories as needed. The blob filename is
// "blob"; callers wanting a multi-file layout should use the
// directory-aware variants that future implementations introduce.
//
// The slot is held to the cache directory first: see [WriteCachedFile].
func WriteCached(workspaceRoot, cacheDir, uri, version string, data []byte) error {
	return WriteCachedFile(workspaceRoot, cacheDir, uri, version, "blob", data)
}

// WriteCachedFile stores data under name in the cache slot for uri@version,
// creating the slot as needed. It is the one write every file a slot holds goes
// through, so the slot is held to the cache directory in one place.
//
// The slot is held, before anything is written, to the root the cache directory
// picks ([writescope.RootForCache]): the cache directory itself. A directory on
// the way to the slot, or the file, that is a link leading out of the cache
// directory — or a cache directory leading out of the workspace — is refused with
// the halt the write-scope rule declares, rather than written through.
//
// A failure to create the slot or write the file is said as what was being done
// and with which path, followed by the operating system's reason. Which entity the
// slot holds is the caller's to say, so it is not said here as well.
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
		return filefailure.Wrap("create the cache directory", dir, err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return filefailure.Wrap("write the cache file", file, err)
	}
	return nil
}

// ReadCachedManifest returns the raw OCI manifest bytes previously stored
// alongside the content blob in the cache slot for uri@version. It is the
// second, self-verifying blob: a reader re-hashes these bytes and confirms they
// match the manifest digest the artifact commits to. Returns a wrapped
// ErrFetchNotFound when the manifest blob is absent (an incomplete slot — a
// content blob written without its manifest); the read hold's refusal of a link
// leading out is returned as itself; other errors surface as wrapped read
// failures. Mirrors ReadCached; the filename is "manifest".
func ReadCachedManifest(workspaceRoot, cacheDir, uri, version string) ([]byte, error) {
	data, err := ReadCachedFile(workspaceRoot, cacheDir, uri, version, "manifest")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("oci: manifest cache miss for %s@%s: %w", uri, version, ErrFetchNotFound())
		}
		if heldOrMisplaced(err) {
			return nil, err
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

// bareReference returns an OCI reference (such as
// `oci://hub.aggrega.ai/component/core/api-server`) without its `oci://` scheme,
// so a scheme-qualified and a bare reference to one artifact name one place in
// the cache.
func bareReference(uri string) string {
	return strings.TrimPrefix(uri, "oci://")
}
