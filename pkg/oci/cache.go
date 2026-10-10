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
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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

// VerifyCachedSignatureBundle re-hashes the cached signature-bundle blob a
// recorded signature fact names, and refuses one that is missing, unreadable, or
// hashes to anything other than bundleDigest. It returns the blob's path either
// way, so a caller naming the artifact the bundle belongs to can name the file
// too.
//
// It answers whether the cached bundle is the one recorded, and only that. Whether
// a build accepts a recorded fact is HoldRecordedSignature's question, which runs
// this same re-hash first and then asks which artifact the bundle was made for;
// no product code asks this one in its place, and a guard test holds that. It is
// exported for a reader whose question is the cache's own — whether a bundle a
// pull wrote is still the one its fact names.
//
// It is a byte comparison and nothing more: no signature is re-verified, no
// network is reached and no trust material is read, so it holds in a build with
// no network access at all. The blob location is the one SignatureBundlePath
// spells, the slot the pull's write populated whatever directory either was run
// from, and the hash is digest.ContentDigest, the spelling the pull recorded.
//
// The blob is held, before it is opened, to the root the pull's write of it is
// held to (readscope.CheckCacheRead over writescope.RootForCache): a cache
// directory, or the blob itself, that leads through a link out of that root is
// not read, and the read hold's refusal is returned as itself, naming the link,
// rather than as a bundle that does not match.
//
// The refusals wrap ErrSignatureBundleDigestMismatch and name the file and both
// digests; naming the artifact is the caller's, which holds it.
func VerifyCachedSignatureBundle(workspaceRoot, cacheDir, bundleDigest string) (string, error) {
	path, _, err := readCachedSignatureBundle(workspaceRoot, cacheDir, bundleDigest)
	return path, err
}

// readCachedSignatureBundle reads the cached signature-bundle blob bundleDigest
// names and returns it with its path, refusing one that is missing, unreadable or
// hashes to anything else, under the read hold VerifyCachedSignatureBundle states.
// It is the one read of a cached bundle, so the bytes HoldRecordedSignature reads
// the signed artifact from are the bytes it just re-hashed, not a second read of
// the same file that could find something else there.
func readCachedSignatureBundle(workspaceRoot, cacheDir, bundleDigest string) (string, []byte, error) {
	path := SignatureBundlePath(workspaceRoot, cacheDir, bundleDigest)
	if err := readscope.CheckCacheRead(writescope.RootForCache(workspaceRoot, cacheDir), path); err != nil {
		return path, nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return path, nil, fmt.Errorf("cached signature bundle %s is unreadable: %w", path, ErrSignatureBundleDigestMismatch())
	}
	if got := digest.ContentDigest(b); got != bundleDigest {
		return path, nil, fmt.Errorf("cached signature bundle %s has digest %s, recorded digest is %s: %w",
			path, got, bundleDigest, ErrSignatureBundleDigestMismatch())
	}
	return path, b, nil
}

// RecordedBundle is a recorded signature fact as HoldRecordedSignature holds it:
// the digest the fact records for its cached signature bundle, and the file that
// records the fact — the lockfile, or the record of plugin identities beside it —
// which a refusal names. An empty RecordedIn names no file.
type RecordedBundle struct {
	Digest     string
	RecordedIn string
}

// MadeFor is the artifact a recorded signature is held to having been made for,
// as the reading side of the reference established it: the digest of the manifest
// the read held the bytes it serves to, or none where the read held them to no
// manifest at all — bytes read from a catalog directory, from the copy inside the
// compiler binary, or from beside the Release, none of which comes with one.
//
// It is never a field of the record that carries the fact. A record states what it
// states, and the fact being checked is part of it; the manifest is what the build
// read from the content cache and held the bytes to, which an edit to the record
// does not move.
type MadeFor struct {
	manifestDigest string
}

// MadeForManifest is the artifact whose manifest digest is manifestDigest — the
// digest of the manifest the reading side read and held the reference's bytes to.
// An empty digest names no manifest, as NoManifestRead does.
func MadeForManifest(manifestDigest string) MadeFor {
	return MadeFor{manifestDigest: manifestDigest}
}

// NoManifestRead is a reference whose read held its bytes to no manifest, so
// there is nothing to say which artifact its signature had to be made for.
func NoManifestRead() MadeFor {
	return MadeFor{}
}

// HoldRecordedSignature is the one decision whether a recorded signature fact is
// accepted. Every reader that asks whether a build would accept a fact asks it
// here: the build's check of every fact the lockfile and the record beside it
// carry, before anything renders; the moment a plugin's bytes are resolved,
// which is what reaches the surfaces that load a plugin outside a build; and a
// pull's signature pass, which asks the registry again about exactly the
// cache-served rows whose recorded fact this refuses, so whatever this comes to
// refuse, the next pull repairs.
//
// A fact that records no bundle digest names no cached bundle, so there is
// nothing to hold it to: it is accepted, with an empty path.
//
// It holds the fact to two things, in order:
//
//   - The cached bundle the fact names is the one recorded: re-hashed against the
//     digest the fact records (VerifyCachedSignatureBundle's check, through the
//     same read). A missing, unreadable or altered bundle is refused with
//     ErrSignatureBundleDigestMismatch.
//   - The bundle was made for the artifact read: where madeFor names a manifest,
//     one of the hashes the bundle names for what the signature was made for
//     (SignedArtifactOf) is the hash of that manifest's digest — what a pull's
//     verification compared when it accepted the bundle, whether the bundle is a
//     message signature or a signed envelope whose statement names the artifact.
//     A bundle made for another artifact is refused with
//     ErrSignatureForAnotherArtifact, which names the file that records the fact,
//     the cached bundle and the manifest the build read.
//
// Neither depends on a signing requirement: a recorded fact is held to what it
// claims whether or not one is required.
//
// Two bounds, both deliberate. A reference whose read held its bytes to no
// manifest (NoManifestRead) is held to the re-hash alone, because nothing it read
// says which artifact its signature had to be made for. And a bundle in a form
// SignedArtifactOf does not recognise is held to the re-hash alone too. Such a
// bundle is in neither of the forms a pull's verification accepts, and a pull
// refuses one that verifies in a form this reading does not recognise, so no pull
// records it: it reaches the cache only by an edit to the cache, which is the
// bound stated below.
//
// It verifies no signature, reaches no network and reads no trust material: it
// reads the cached bundle and compares hashes, so it holds in a build with no
// network access at all. What it cannot therefore tell is a bundle written into
// the cache by hand, made out for the artifact read, from one a pull verified.
//
// A refusal names the condition, the cached bundle and, for a bundle made for
// another artifact, the file and the manifest; it names no remedy, because the
// remedy names the Release, which the surface reporting the refusal holds:
// WithPullRemedy adds it there.
//
// It returns the bundle's path either way.
func HoldRecordedSignature(workspaceRoot, cacheDir string, fact RecordedBundle, madeFor MadeFor) (string, error) {
	if fact.Digest == "" {
		return "", nil
	}
	path, bundle, err := readCachedSignatureBundle(workspaceRoot, cacheDir, fact.Digest)
	if err != nil {
		return path, err
	}
	if madeFor.manifestDigest == "" {
		return path, nil
	}
	signed, recognised := SignedArtifactOf(bundle)
	if !recognised || signed.MadeFor(madeFor.manifestDigest) {
		return path, nil
	}
	return path, signatureForAnotherArtifact(path, fact.RecordedIn, madeFor.manifestDigest)
}

// signatureForAnotherArtifact composes one occurrence of
// ErrSignatureForAnotherArtifact: the file that records the fact, the cached
// bundle it names and the manifest the build read. Naming the reference and the
// remedy is the caller's, which holds both, as it is for the cached-bundle
// mismatch.
func signatureForAnotherArtifact(bundlePath, recordedIn, manifestDigest string) error {
	var values []string
	if recordedIn != "" {
		values = append(values, recordedIn+" records it")
	}
	values = append(values, "cached signature bundle "+bundlePath, "the artifact read has manifest "+manifestDigest)
	return &signatureForAnotherArtifactError{message: cerrors.ComposeMessage(errSignatureForAnotherArtifact, values, "")}
}

// signatureForAnotherArtifactError renders the composed refusal and unwraps to
// the condition.
type signatureForAnotherArtifactError struct {
	message string
}

func (e *signatureForAnotherArtifactError) Error() string { return e.message }

func (e *signatureForAnotherArtifactError) Unwrap() error { return errSignatureForAnotherArtifact }

// WithPullRemedy returns refusal — a refusal HoldRecordedSignature returned —
// with the remedy that repairs it after it: running the pull of release. A pull
// holds every lockfile row the content cache served to HoldRecordedSignature, and
// verifies the signature of an artifact whose recorded fact it refuses again,
// through the verification every artifact it fetches gets — a plugin attached at
// a verdict role is verified again on every pull whatever its entry records; it
// records the fact that gives —
// none, where the workspace's settings admit the artifact unsigned — in the file
// the fact is recorded in, and caches the bundle the fact names, rewriting a
// cached bundle that no longer matches its digest. So a removed or altered bundle
// and a fact copied from another row are each repaired by it. It is the one
// spelling of that remedy, so the build's check and the moment a plugin's bytes
// are resolved name it alike.
//
// release is the Release as the reader named it; where the reporting surface holds
// none it is empty, and the remedy names a placeholder in its place. recordedIn is
// the file the fact is recorded in, and an empty one names no file.
//
// Only the two conditions HoldRecordedSignature decides —
// ErrSignatureBundleDigestMismatch and ErrSignatureForAnotherArtifact — are given
// the remedy. Any other refusal, such as the read hold's refusal of a link leading
// out of the cache, is returned as it is: a pull writes the bundle through the
// same hold and is refused there too. A nil refusal is returned as nil.
//
// The result unwraps to refusal, so errors.Is and the condition's codes reach it.
func WithPullRemedy(refusal error, release, recordedIn string) error {
	if refusal == nil {
		return nil
	}
	if !errors.Is(refusal, ErrSignatureBundleDigestMismatch()) && !errors.Is(refusal, ErrSignatureForAnotherArtifact()) {
		return refusal
	}
	if release == "" {
		release = "<release>"
	}
	remedy := "remediation: run `aggrega pull --release " + release + "` to verify the artifact's signature at its registry again, record it"
	if recordedIn != "" {
		remedy += " in " + recordedIn
	}
	remedy += " and cache the bundle it names"
	return &pullRemedyError{refusal: refusal, remedy: remedy}
}

// pullRemedyError renders a refusal with the remedy after it and unwraps to the
// refusal.
type pullRemedyError struct {
	refusal error
	remedy  string
}

func (e *pullRemedyError) Error() string { return e.refusal.Error() + ". " + e.remedy }

func (e *pullRemedyError) Unwrap() error { return e.refusal }

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
