// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package sourcecache owns the build-time read-and-extract pipeline
// for source artifacts pinned in the lockfile. The package looks up
// a `source_artifacts:` entry by its location terms
// {kind, url, chart, module_subdir} (url is the authored
// identity URL; chart names the chart within a repo-root helm source and
// module_subdir the directory within a git-sourced repository, each empty
// for the kinds it does not apply to) together with what the author wrote to
// select a version — the declared `ref:` where there is one, the declared
// `version:` otherwise, so a source declared at a git tag is found in a record
// that pins the commit that tag resolved to —
// verifies the full SHA-256 of the cached archive against both the
// lockfile entry's digest and the cache slot's metadata.yaml
// digest, rebuilds the archive's tree under the `extracted/`
// subdirectory from the archive, and returns the absolute path of
// that tree for engines to consume.
//
// The package is the build-time complement to pkg/sourceresolver
// (the lock-time pull pipeline). The orchestrator at render time
// calls LookupAndExtract before dispatching to the engine. The
// returned path is the raw extracted-archive root; the consumer
// locates its own artifact within it — a Helm chart, for instance,
// untars under a single <chartname>/ directory, so the helm engine
// descends to the directory holding Chart.yaml before loader.Load.
//
// Two properties of that gate are easy to misread from the cache
// layout. The digest fragment embedded in a slot's path is
// namespacing only and is never what the gate compares: it keeps the
// same {kind, url, version} re-tagged at different content in
// distinct slots, and a full SHA-256 over the archive bytes is what
// actually decides. And because the extracted tree is rebuilt from the
// archive on every read, a hand-edit made in it is silently discarded
// on the next build, while a hand-edit to the archive itself is caught
// and refuses the build — so a local change that appears to work is a
// change that will not survive, and the two cases fail in opposite
// ways.
//
// The package re-exports the four fetch-error sentinels from pkg/oci
// so consumers can errors.Is against either name without importing
// pkg/oci directly.
package sourcecache

import (
	"fmt"
	"os"
	"path/filepath"

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

// ErrFetchNotFound re-exports the canonical fetch-error identity declared in
// pkg/oci. It yields that identity itself rather than a second one carrying the
// same text, so errors.Is against either name matches the same wrapped chain,
// and it registers no message of its own: the message is declared once, beside
// the canonical declaration.
func ErrFetchNotFound() error { return oci.ErrFetchNotFound() }

// ErrFetchIntegrity re-exports the canonical fetch-error identity declared in
// pkg/oci, on the same terms as ErrFetchNotFound above.
func ErrFetchIntegrity() error { return oci.ErrFetchIntegrity() }

// LookupAndExtract looks up the lockfile entry for the artifact the caller
// resolved, verifies the full SHA-256 of the cached `.tgz`
// against the entry's digest AND the cache slot's metadata.yaml
// digest, rebuilds the archive's tree under the `extracted/`
// subdirectory from the archive ([RegenerateExtracted]), and returns
// the absolute path of that tree. The archive is extracted verbatim,
// so the returned path is the archive root — not necessarily a
// directly-loadable artifact root: a Helm chart untars under a
// <chartname>/ subdirectory, and the consumer resolves its artifact
// within.
//
// The row is found by what the author wrote — ref where the source declares one,
// version otherwise — which is what lets a source declared at a git tag or branch
// be found in a record that pins the commit it resolved to. The lookup itself is
// the lockfile's, so this read and the pin comparison beside it cannot come to
// disagree about which row describes the artifact.
//
// Returns ErrFetchNotFound when no lockfile entry matches, or — for an OCI source kind — when the
// cache slot carries the archive but no persisted OCI manifest (an incomplete
// cache entry; the caller re-pulls). Returns ErrFetchIntegrity on digest
// mismatch (either content pin, or the manifest layer digest for an OCI slot)
// or on archive corruption. Returns wrapped fs errors on read/write/extract
// failures. A non-OCI source kind (HTTPS chart, git module) carries no manifest
// by construction and stays on content-pin authority.
func LookupAndExtract(lf *lockfile.Lockfile, kind, url, chart, moduleSubdir, version, ref, workspaceRoot, cacheDir string) (string, error) {
	if lf == nil {
		return "", fmt.Errorf("sourcecache: lockfile is nil: %w", ErrFetchNotFound())
	}
	entry, found := lf.FindSourceArtifact(kind, url, chart, moduleSubdir, version, ref)
	if !found {
		// The reference is named only where the source declares one, so the line
		// reports the terms the lookup actually used rather than an empty field the
		// reader would have to know to ignore.
		refPart := ""
		if ref != "" {
			refPart = fmt.Sprintf(" ref=%s", ref)
		}
		return "", fmt.Errorf("no lockfile entry for kind=%s url=%s chart=%s module_subdir=%s version=%s%s: %w",
			kind, url, chart, moduleSubdir, version, refPart, ErrFetchNotFound())
	}
	// The slot is addressed by the version the row PINS, never by the one the
	// document authored. Acquisition wrote the slot under whatever it resolved to,
	// so the resolved value is the only one that addresses it — the two are the
	// same string for a kind that pins the authored version verbatim, which is why
	// reading the authored one worked for as long as those were the only sources a
	// build could reach.
	slot, err := SlotPath(workspaceRoot, cacheDir, kind, url, chart, moduleSubdir, entry.PinnedVersion, entry.Digest)
	if err != nil {
		return "", err
	}
	blobPath := filepath.Join(slot, "blob.tgz")
	blobBytes, err := os.ReadFile(blobPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("sourcecache: cache blob missing at %s: %w", blobPath, ErrFetchNotFound())
		}
		return "", fmt.Errorf("sourcecache: read blob %s: %w", blobPath, err)
	}

	gotDigest := digest.ContentDigest(blobBytes)
	if gotDigest != entry.Digest {
		return "", fmt.Errorf("sourcecache: blob digest %s mismatches lockfile digest %s: %w",
			gotDigest, entry.Digest, ErrFetchIntegrity())
	}
	meta, err := metadata.Read(filepath.Join(slot, "metadata.yaml"))
	if err != nil {
		return "", fmt.Errorf("sourcecache: read metadata: %w", err)
	}
	if meta.Digest != entry.Digest {
		return "", fmt.Errorf("sourcecache: metadata digest %s mismatches lockfile digest %s: %w",
			meta.Digest, entry.Digest, ErrFetchIntegrity())
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
		if verr := oci.VerifyContentAgainstManifest(manifestBytes, blobBytes, meta.Mediatype); verr != nil {
			return "", fmt.Errorf("sourcecache: %s: %w", blobPath, verr)
		}
	case os.IsNotExist(mrerr):
		if kindExpectsManifest(kind) {
			return "", fmt.Errorf("sourcecache: %s cached without its OCI manifest; run `aggrega pull --release <release>` first: %w", blobPath, ErrFetchNotFound())
		}
	default:
		return "", fmt.Errorf("sourcecache: read manifest for %s: %w", blobPath, mrerr)
	}

	return RegenerateExtracted(workspaceRoot, slot, blobBytes)
}

// SlotPath returns the cache-slot directory for the given lockfile
// entry's identity. It is cacheslot.Slot — the digest taken from the
// lockfile entry, the slot path computed directly (no directory walk) —
// and it returns Slot's refusal unchanged: a value that would leave its
// place in the cache, or an empty workspace root, yields no slot, so an
// empty path never stands for one.
func SlotPath(workspaceRoot, cacheDir, kind, url, chart, moduleSubdir, version, digest string) (string, error) {
	return cacheslot.Slot(workspaceRoot, cacheDir, kind, url, chart, moduleSubdir, version, digest)
}
