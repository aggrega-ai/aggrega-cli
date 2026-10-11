// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package docscache resolves and locally caches a documentation bundle — a
// standalone, non-entity data artifact carrying rendered documentation pages
// — and reads individual pages back out of the cache offline.
//
// A documentation bundle is a gzipped tar published as its own OCI artifact
// under the reserved docs namespace, distinct from any catalog entity. Like a
// source artifact it rides the kind-agnostic OCI transport rather than an
// entity envelope. Unlike one, its bytes depend only on the registry it is
// published to and the version it is published at, never on a workspace, so it
// is kept once per machine: in its own subtree of the cache root the caller
// names, keyed by reference and version. The package is the
// documentation-payload complement to the catalog-entity cache (pkg/oci) and
// the source-artifact cache (pkg/sourcecache): the resolve half pulls, verifies,
// and stores; the read half reads a stored bundle and returns one page's bytes.
//
// The split mirrors the ahead-of-time boundary the rest of the compiler holds:
// ResolveAndCache is the network-touching acquisition step (content integrity
// is always enforced, and signature verification runs at pull when the caller
// supplies a verifier), while ReadPage takes no puller or target and can only
// ever read what a prior resolve already cached, so a cache miss is a loud
// not-found rather than a fetch.
//
// # One copy, admitted by each reader
//
// A slot is shared by every reader on the machine, and readers need not trust
// the same signers. So a slot keeps, beside the bundle's content and the
// manifest it is verified against, the signature bundles its fetches verified,
// and every read hands those to an admission check the READER supplies before a
// page is served. A slot the reader's check does not admit is answered as no
// bundle at all ([NotAdmittedError]), which a later fetch under that reader's
// own check may replace. Keeping a signature is not trusting it: whatever a slot
// holds, a read serves only what its own check admits.
//
// # Whole or absent
//
// Several processes may acquire the same bundle at once, so a slot is written in
// a staging directory beside it and moved into place whole. A reader therefore
// meets either a complete slot or none, and never one a writer is still filling.
package docscache

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// ErrFetchNotFound is re-exported from pkg/oci so a caller can errors.Is a
// cache miss against either name. ReadPage returns it (wrapped) when no bundle
// has been resolved for the requested reference and version, and when the one
// that has is not admitted by the reader's check. It is the canonical identity
// itself, and restates no message: the message is registered once, beside the
// canonical declaration.
func ErrFetchNotFound() error { return oci.ErrFetchNotFound() }

// ErrNotDocsBundle reports that a resolved artifact does not carry the
// documentation-bundle media type. A reference that resolves to some other
// artifact kind (a catalog entity, a .wasm plugin) is rejected rather than
// cached as a documentation bundle, so a mis-pointed reference fails loudly at
// resolve time instead of surfacing as an unreadable bundle later.
func ErrNotDocsBundle() error { return errNotDocsBundle }

var errNotDocsBundle = cerrors.Register("not-docs-bundle", cerrors.Meta{
	ShortCode: "E0123",
	Message:   "docscache: artifact is not a documentation bundle",
	RulePlace: cerrors.NotARule("A reference resolved to an artifact that does not carry the documentation-bundle media type. " +
		"The artifact is fetched in order to be inspected, so the fact the comparison turns on is " +
		"obtained when it runs; what it reports is a resolution that yielded the wrong kind of thing."),
})

// ErrPageNotFound reports that the requested page is absent from an otherwise
// present, integrity-verified bundle. It is distinct from ErrFetchNotFound (no
// bundle cached at all): the bundle resolved and cached, but carries no entry
// at the requested path.
func ErrPageNotFound() error { return errPageNotFound }

var errPageNotFound = cerrors.Register("page-not-found", cerrors.Meta{
	ShortCode: "E0124",
	Message:   "docscache: page not found in documentation bundle",
	RulePlace: cerrors.NotARule("A requested page is absent from a bundle that is present and integrity-verified. It reports " +
		"a lookup in fetched content that found nothing rather than a rule refusing content."),
})

// NotAdmittedError reports that a cached bundle is present and that the reader's
// admission check refused it. It answers as no bundle: errors.Is matches it
// against ErrFetchNotFound, so every reader that treats a cache miss as "no
// documentation" treats a refused slot the same way, and errors.As recovers it
// for a reader that says which of the two it met.
type NotAdmittedError struct {
	// Ref and Version name the bundle the slot holds.
	Ref, Version string
	// Reason is the admission check's refusal.
	Reason error
}

// Error states that the copy cached on this machine was not admitted, and why.
func (e *NotAdmittedError) Error() string {
	return fmt.Sprintf("docscache: the copy of %s@%s cached on this machine is not admitted: %v", e.Ref, e.Version, e.Reason)
}

// Unwrap returns the refusal, and the cache miss a refused slot answers as.
func (e *NotAdmittedError) Unwrap() []error { return []error{e.Reason, ErrFetchNotFound()} }

// VerifyAtPull verifies a resolved documentation bundle's signature at pull
// time, given the OCI manifest digest the signature commits to, and returns the
// signature bundles it verified, which the slot keeps for the checks of later
// readers. It is the signature seam the resolve path drives: the caller wires the
// same pull-time verification a catalog entity or plugin uses, so a documentation
// bundle — a non-executable artifact — verifies through the identical path rather
// than a bespoke one. A nil VerifyAtPull skips signature verification and keeps no
// signature; content integrity against the manifest is enforced regardless.
type VerifyAtPull func(ctx context.Context, manifestDigest string) ([][]byte, error)

// AdmitAtRead decides whether a reader may be served a cached bundle: it is
// handed the digest of the manifest the bundle's content is verified against —
// recomputed from the stored manifest, never read from a recorded value — and
// the signature bundles the slot keeps, and returns nil to admit or the refusal.
// The read path requires one: a read handed none refuses rather than serving
// bytes no check admitted.
type AdmitAtRead func(manifestDigest string, signatures [][]byte) error

// DocsBundlesRoot returns the root of the documentation-bundle subtree of a
// cache root:
//
//	<cacheRoot>/docs-bundles/
//
// It is the single site that names the `docs-bundles/` segment; the per-slot
// path composes beneath it and cache-inspection tools enumerate the subtree
// from it, so the segment is spelled once. Which cache root that is — the one
// every machine-wide cache derives from — is the caller's to supply.
func DocsBundlesRoot(cacheRoot string) string {
	return filepath.Join(cacheRoot, "docs-bundles")
}

// IsSlotName reports whether name, a directory beneath one reference's directory
// of the subtree, is a version's slot rather than a write in progress or a slot
// a write is replacing.
//
// A write stages under a name an encoded version never takes — pathsegment writes
// no tilde — so an enumeration tells the two apart by the name alone and never
// lists a slot that is not whole.
func IsSlotName(name string) bool {
	return !strings.HasPrefix(name, transientPrefix)
}

// transientPrefix opens the name of a directory a write stages a slot in or moves
// a replaced slot aside into.
const transientPrefix = "~"

// blobFile, manifestFile and signaturesDir name what a cache slot holds: the
// bundle's content blob, the OCI manifest it is verified against, and the
// directory of signature bundles its fetches verified. They are spelled once so
// the write, the presence probe and the read cannot disagree about which files a
// populated slot contains.
const (
	blobFile      = "blob"
	manifestFile  = "manifest"
	signaturesDir = "signatures"
)

// Cached reports whether a documentation bundle for (ref, version) is already in
// the cache under cacheRoot. It answers from the same slot ReadPage reads, so the
// two can never disagree about where a bundle lives.
//
// It reports presence and nothing more: a slot whose content is corrupt still
// reports cached, because integrity is re-checked against the stored manifest on
// every read and is that read's answer to give, not this probe's; and a slot a
// reader's check would refuse still reports cached, because admission is the
// reader's question ([Admitted]).
func Cached(cacheRoot, ref, version string) (bool, error) {
	slot, err := slotPath(cacheRoot, ref, version)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(slot, blobFile))
	return err == nil, nil
}

// Admitted reports whether the cache under cacheRoot holds a bundle for (ref,
// version) that admit admits: nil when it does, a wrapped ErrFetchNotFound when
// no bundle is cached, and a [NotAdmittedError] when one is cached and admit
// refuses it.
//
// It is the question an acquisition asks before deciding to fetch, and it asks
// the same check the read asks, over the same kept signatures, so a slot the
// acquisition leaves in place is one the read will serve. Like [Cached] it does
// not re-check content integrity, which is every read's own answer to give.
func Admitted(cacheRoot, ref, version string, admit AdmitAtRead) error {
	slot, err := slotPath(cacheRoot, ref, version)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(slot, blobFile)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("docscache: no cached bundle for %s@%s: %w", ref, version, ErrFetchNotFound())
		}
		return fmt.Errorf("docscache: read cached bundle %s@%s: %w", ref, version, pathless(err))
	}
	manifest, err := readManifest(slot, ref, version)
	if err != nil {
		return err
	}
	return admitSlot(slot, ref, version, manifest, admit)
}

// slotPath returns the cache slot for a documentation bundle addressed by
// reference and version:
//
//	<DocsBundlesRoot>/<encoded-reference>/<encoded-version>/
//
// The reference, without any oci:// scheme — so a scheme-qualified and a bare
// reference to the same artifact share a slot — and the version are each spelled
// as one directory name through the one spelling of a derived name
// (pathsegment.Encode), so the slot is one every operating system accepts and
// neither value can name a directory outside it. Two slotPath calls with the
// same inputs produce byte-identical paths.
//
// The cache root must be absolute: a relative one would place the slot wherever
// the process happens to stand.
func slotPath(cacheRoot, ref, version string) (string, error) {
	if !filepath.IsAbs(cacheRoot) {
		return "", fmt.Errorf("docscache: the cache root %q is not an absolute path", cacheRoot)
	}
	return filepath.Join(refDir(cacheRoot, ref), pathsegment.Encode(version)), nil
}

// refDir returns the directory every version of ref is kept beneath.
func refDir(cacheRoot, ref string) string {
	return filepath.Join(DocsBundlesRoot(cacheRoot), pathsegment.Encode(strings.TrimPrefix(ref, "oci://")))
}

// ResolveAndCache pulls the documentation bundle tagged version from target,
// enforces its content integrity against the OCI manifest, verifies its
// signature at pull when verify is non-nil, and writes the bundle's content
// blob, its manifest and the signature bundles verify returned into the cache
// slot for (ref, version) under cacheRoot. It returns the slot directory.
//
// oci.Pull verifies the content against the manifest layer digest inside the
// fetch, so a corrupt or substituted blob fails before it is ever cached. The
// media type is checked against the documentation-bundle type so a reference
// that resolves to a different artifact kind is rejected. Signature
// verification — when wired — runs before the bundle is written, so an
// unverifiable bundle never populates the cache. The manifest is persisted
// beside the blob so a later offline read can re-derive the manifest digest from
// stored bytes rather than trusting an unchecked recorded value.
//
// The slot is staged beside its place and moved into it whole, replacing the slot
// that stood there when the write was ready to stage, if any. A slot being
// replaced that holds the same manifest hands on the signatures it kept, so a
// fetch under one reader's check does not take away the signature another
// reader's check admitted. Where another writer places a slot after that moment,
// that slot stands as this write's result, re-checked as a reader re-checks it,
// and this write's staging is discarded: both are whole.
func ResolveAndCache(ctx context.Context, target oras.ReadOnlyTarget, ref, version, cacheRoot string, verify VerifyAtPull) (string, error) {
	slot, err := slotPath(cacheRoot, ref, version)
	if err != nil {
		return "", err
	}
	pr, err := oci.Pull(ctx, target, version)
	if err != nil {
		return "", fmt.Errorf("docscache: pull %s@%s: %w", ref, version, err)
	}
	if pr.MediaType != oci.MediaTypeDocs {
		return "", fmt.Errorf("docscache: %s@%s has media type %q, want %q: %w",
			ref, version, pr.MediaType, oci.MediaTypeDocs, ErrNotDocsBundle())
	}
	var signatures [][]byte
	if verify != nil {
		signatures, err = verify(ctx, pr.Digest)
		if err != nil {
			return "", fmt.Errorf("docscache: verify %s@%s: %w", ref, version, err)
		}
	}
	// What stands in the slot's place now is the slot this write replaces: its
	// signatures are read before it can be replaced, and it alone is moved aside
	// when the staged slot is placed.
	replacing, err := slotIdentity(slot)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("docscache: cache inspect for %s@%s: %w", ref, version, err)
	}
	signatures = append(signatures, keptSignatures(slot, pr.Digest)...)

	parent := filepath.Dir(slot)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("docscache: cache mkdir %s: %w", parent, err)
	}
	staging, err := os.MkdirTemp(parent, transientPrefix+"staging-*")
	if err != nil {
		return "", fmt.Errorf("docscache: stage %s@%s: %w", ref, version, err)
	}
	defer os.RemoveAll(staging)
	if err := writeSlot(staging, pr.Bytes, pr.ManifestBytes, signatures); err != nil {
		return "", fmt.Errorf("docscache: cache write for %s@%s: %w", ref, version, err)
	}
	if err := placeSlot(staging, slot, ref, version, replacing); err != nil {
		return "", fmt.Errorf("docscache: cache place for %s@%s: %w", ref, version, err)
	}
	return slot, nil
}

// writeSlot writes a slot's content, manifest and signatures into dir. Each
// signature is kept under its own content digest, so the same signature handed
// on twice is kept once.
func writeSlot(dir string, blob, manifest []byte, signatures [][]byte) error {
	if err := os.WriteFile(filepath.Join(dir, blobFile), blob, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFile), manifest, 0o644); err != nil {
		return err
	}
	if len(signatures) == 0 {
		return nil
	}
	sigDir := filepath.Join(dir, signaturesDir)
	if err := os.Mkdir(sigDir, 0o755); err != nil {
		return err
	}
	for _, sig := range signatures {
		if err := os.WriteFile(filepath.Join(sigDir, pathsegment.Encode(digest.ContentDigest(sig))), sig, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// maxPlacements bounds how many times a write tries to move its staged slot into
// place. A write tries again only when the slot it lost to was moved aside before
// it could re-check it — by a writer replacing that slot, which each writer does at
// most once — so the attempts one placement needs are at most one more than the
// writers acquiring the same bundle at once. The bound ends only a move that keeps
// failing for a reason no other writer's slot explains.
const maxPlacements = 64

// placeSlot moves the staged slot into place and removes the slot it replaces.
//
// replacing is the slot that stood in place when this write was ready to stage,
// or nil where none did, and it is the only slot this write moves aside: a slot
// another writer placed since is that writer's result, never this one's to remove.
// Between moving it aside and moving the staged slot in, a reader finds no slot,
// which reads as a cache miss rather than as a partial bundle.
//
// A move into place that fails because another writer's slot stands there has lost
// the race, and that slot is this write's result: it is re-checked as a read
// re-checks it ([openSlot]), and the caller discards the staging. Where the slot it
// lost to was moved aside before that re-check — by a writer replacing it — the
// place is empty again and the move is tried again, so a write never reports a
// failure for a race a whole slot settled.
func placeSlot(staging, slot, ref, version string, replacing os.FileInfo) error {
	if replacing != nil {
		aside, err := os.MkdirTemp(filepath.Dir(slot), transientPrefix+"replaced-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(aside)
		if err := setAside(slot, filepath.Join(aside, "slot"), replacing); err != nil {
			return err
		}
	}
	var moveErr error
	for range maxPlacements {
		if moveErr = os.Rename(staging, slot); moveErr == nil {
			return nil
		}
		_, _, err := openSlot(slot, ref, version)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrFetchNotFound()) {
			return err
		}
	}
	return moveErr
}

// setAside moves the slot this write replaces out of its place, into moved, and
// leaves any other slot standing.
//
// It looks before it moves, and looks again at what it moved: another writer may
// have replaced the slot in between, and what was moved is then that writer's
// whole slot, which is put back. Where yet another writer has placed a slot in
// that instant the place is taken and the put-back fails; the slot standing is
// whole, and the one moved is removed with the aside directory.
func setAside(slot, moved string, replacing os.FileInfo) error {
	current, err := slotIdentity(slot)
	if errors.Is(err, fs.ErrNotExist) {
		// Another writer replacing the same slot has moved it aside already.
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(current, replacing) {
		return nil
	}
	if err := os.Rename(slot, moved); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if took, err := slotIdentity(moved); err == nil && !os.SameFile(took, replacing) {
		// Whether or not it can go back, a whole slot stands or the place is free
		// for the staged one, so the outcome of the put-back changes nothing here.
		_ = os.Rename(moved, slot)
	}
	return nil
}

// slotIdentity returns the identity of the directory at path as it stands now, for
// os.SameFile to compare with what stands there later. On Windows a FileInfo from
// os.Stat looks the file's identity up only when it is first compared, from
// whatever the path names at that moment, so it is compared with itself here to
// settle it on the directory the path names now.
func slotIdentity(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	os.SameFile(info, info)
	return info, nil
}

// keptSignatures returns the signatures the slot already keeps, when the slot
// holds the bundle whose manifest has manifestDigest, and none otherwise: a
// signature commits to one manifest, so one kept for another is of no use to a
// reader of this one.
func keptSignatures(slot, manifestDigest string) [][]byte {
	manifest, err := os.ReadFile(filepath.Join(slot, manifestFile))
	if err != nil || digest.ContentDigest(manifest) != manifestDigest {
		return nil
	}
	signatures, err := readSignatures(slot)
	if err != nil {
		return nil
	}
	return signatures
}

// pathless returns err without the file location a filesystem error names, so a
// read's failure names the bundle it was reading rather than where on this
// machine the bundle is kept. That location is the machine's own, and a reader
// may be a caller elsewhere that this machine's layout is not reported to.
func pathless(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %w", pe.Op, pe.Err)
	}
	return err
}

// readSignatures reads the signature bundles a slot keeps, in name order so a
// check is handed them in the same order every time.
func readSignatures(slot string) ([][]byte, error) {
	entries, err := os.ReadDir(filepath.Join(slot, signaturesDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, pathless(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	signatures := make([][]byte, 0, len(names))
	for _, name := range names {
		sig, err := os.ReadFile(filepath.Join(slot, signaturesDir, name))
		if err != nil {
			return nil, pathless(err)
		}
		signatures = append(signatures, sig)
	}
	return signatures, nil
}

// readManifest reads a slot's stored manifest, answering an absent one as a
// cache miss.
func readManifest(slot, ref, version string) ([]byte, error) {
	manifest, err := os.ReadFile(filepath.Join(slot, manifestFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("docscache: cached bundle %s@%s missing its manifest: %w", ref, version, ErrFetchNotFound())
		}
		return nil, fmt.Errorf("docscache: read cached manifest %s@%s: %w", ref, version, pathless(err))
	}
	return manifest, nil
}

// admitSlot hands admit the digest of the stored manifest and the signatures the
// slot keeps, and returns its refusal as a [NotAdmittedError]. A nil admit is
// refused: a read with no check would serve bytes nothing admitted.
func admitSlot(slot, ref, version string, manifest []byte, admit AdmitAtRead) error {
	if admit == nil {
		return &NotAdmittedError{Ref: ref, Version: version, Reason: errors.New("no admission check was given for the read")}
	}
	signatures, err := readSignatures(slot)
	if err != nil {
		return fmt.Errorf("docscache: read the signatures kept for %s@%s: %w", ref, version, err)
	}
	if err := admit(digest.ContentDigest(manifest), signatures); err != nil {
		return &NotAdmittedError{Ref: ref, Version: version, Reason: err}
	}
	return nil
}

// Bundle is a cached documentation bundle opened for reading: its content
// re-checked against its stored manifest, and admitted by the reader's check.
// Pages are read out of it without touching the slot again, so the pages one
// answer takes come from one bundle.
type Bundle struct {
	ref, version string
	blob         []byte
}

// Open reads the cached documentation bundle for (ref, version) under cacheRoot
// for a reader whose check is admit. It is fully offline: it takes no puller or
// target and reads only what a prior ResolveAndCache stored, so a missing bundle
// is a wrapped ErrFetchNotFound (loud cache miss) rather than a fetch. The cached
// content is re-verified against its persisted manifest on every open, so a
// bundle tampered with on disk halts with ErrFetchIntegrity instead of serving
// forged bytes; and the slot's kept signatures are handed to admit before
// anything is served, a refusal returning a [NotAdmittedError].
func Open(cacheRoot, ref, version string, admit AdmitAtRead) (*Bundle, error) {
	slot, err := slotPath(cacheRoot, ref, version)
	if err != nil {
		return nil, err
	}
	blob, manifest, err := openSlot(slot, ref, version)
	if err != nil {
		return nil, err
	}
	if err := admitSlot(slot, ref, version, manifest, admit); err != nil {
		return nil, err
	}
	return &Bundle{ref: ref, version: version, blob: blob}, nil
}

// openSlot reads a slot's content and the manifest beside it and re-checks the
// content against the manifest: what every read checks before it asks the reader's
// admission, and what a write that lost the race to place a slot checks of the
// slot it takes for its result. A slot that is not there — or not all there —
// answers as a cache miss, and content the manifest does not describe halts with
// ErrFetchIntegrity.
func openSlot(slot, ref, version string) (blob, manifest []byte, err error) {
	blob, err = os.ReadFile(filepath.Join(slot, blobFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("docscache: no cached bundle for %s@%s: %w", ref, version, ErrFetchNotFound())
		}
		return nil, nil, fmt.Errorf("docscache: read cached bundle %s@%s: %w", ref, version, pathless(err))
	}
	manifest, err = readManifest(slot, ref, version)
	if err != nil {
		return nil, nil, err
	}
	if err := oci.VerifyContentAgainstManifest(manifest, blob); err != nil {
		return nil, nil, fmt.Errorf("docscache: cached bundle %s@%s: %w", ref, version, err)
	}
	return blob, manifest, nil
}

// Page returns the bytes and MIME type of one page inside the bundle. page is
// the bundle-internal path of the requested entry (e.g.
// "language-reference/entities/component.md"). A page absent from the bundle
// returns ErrPageNotFound.
func (b *Bundle) Page(page string) ([]byte, string, error) {
	data, ok, err := readTarEntry(b.blob, page)
	if err != nil {
		return nil, "", fmt.Errorf("docscache: read page %q from %s@%s: %w", page, b.ref, b.version, err)
	}
	if !ok {
		return nil, "", fmt.Errorf("docscache: page %q in %s@%s: %w", page, b.ref, b.version, ErrPageNotFound())
	}
	return data, mimeForPage(page), nil
}

// ReadPage returns the bytes and MIME type of a single page inside the cached
// documentation bundle for (ref, version) under cacheRoot, for a reader whose
// check is admit. It is [Open] followed by [Bundle.Page], for a reader that wants
// one page.
func ReadPage(cacheRoot, ref, version, page string, admit AdmitAtRead) ([]byte, string, error) {
	b, err := Open(cacheRoot, ref, version, admit)
	if err != nil {
		return nil, "", err
	}
	return b.Page(page)
}

// readTarEntry gunzips and walks the tar archive in bundleBytes, returning the
// bytes of the regular-file entry whose cleaned path equals the cleaned target.
// The second return reports whether the entry was found. Path-traversal entries
// (a name escaping the archive root via `..` or a leading `/`) are rejected with
// a wrapped ErrFetchIntegrity, so a hostile bundle cannot smuggle an escaping
// entry past the lookup. It is deliberately separate from the disk-extracting
// walker the source-artifact cache uses: this sink is an in-memory page lookup,
// that one materialises a directory tree — different outputs, so unifying them
// would force one to carry the other's concern.
func readTarEntry(bundleBytes []byte, target string) ([]byte, bool, error) {
	want := path.Clean(target)
	gz, err := gzip.NewReader(strings.NewReader(string(bundleBytes)))
	if err != nil {
		return nil, false, fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("tar next: %w", err)
		}
		if !pathscope.IsLocalSlash(hdr.Name) {
			return nil, false, fmt.Errorf("path traversal entry %q: %w", hdr.Name, oci.ErrFetchIntegrity())
		}
		clean := path.Clean(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || clean != want {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, false, fmt.Errorf("read entry %q: %w", hdr.Name, err)
		}
		return data, true, nil
	}
}

// mimeForPage returns the MIME type for a bundle page from its extension.
// Markdown pages — the documentation payload — report text/markdown; a small
// set of other text formats are mapped, and anything else falls back to plain
// text so a reader always receives a concrete type.
func mimeForPage(page string) string {
	switch strings.ToLower(filepath.Ext(page)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".html", ".htm":
		return "text/html"
	default:
		return "text/plain"
	}
}
