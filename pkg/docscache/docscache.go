// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package docscache resolves and locally caches a documentation bundle — a
// standalone, non-entity data artifact carrying rendered documentation pages
// — and reads individual pages back out of the cache offline.
//
// A documentation bundle is a gzipped tar published as its own OCI artifact
// under the reserved docs namespace, distinct from any catalog entity. Like a
// source artifact it rides the kind-agnostic OCI transport rather than an
// entity envelope, and it caches in its own sibling subtree of the workspace
// artifact cache. The package is the documentation-payload complement to the
// catalog-entity cache (pkg/oci) and the source-artifact cache
// (pkg/sourcecache): the resolve half pulls, verifies, and stores; the read
// half reads a stored bundle and returns one page's bytes.
//
// The split mirrors the ahead-of-time boundary the rest of the compiler holds:
// ResolveAndCache is the network-touching acquisition step (content integrity
// is always enforced, and signature verification runs at pull when the caller
// supplies a verifier), while ReadPage is fully offline — it takes no puller or
// target and can only ever read what a prior resolve already cached, so a
// cache miss is a loud not-found rather than a fetch. A reader that serves
// documentation pages therefore never reaches the network on the read path.
package docscache

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"oras.land/oras-go/v2"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// ErrFetchNotFound is re-exported from pkg/oci so a caller can errors.Is a
// cache miss against either name. ReadPage returns it (wrapped) when no bundle
// has been resolved for the requested reference and version. It is the
// canonical identity itself, and restates no message: the message is
// registered once, beside the canonical declaration.
func ErrFetchNotFound() error { return oci.ErrFetchNotFound() }

// ErrNotDocsBundle reports that a resolved artifact does not carry the
// documentation-bundle media type. A reference that resolves to some other
// artifact kind (a catalog entity, a .wasm plugin) is rejected rather than
// cached as a documentation bundle, so a mis-pointed reference fails loudly at
// resolve time instead of surfacing as an unreadable bundle later.
func ErrNotDocsBundle() error { return errNotDocsBundle }

var errNotDocsBundle = cerrors.Register("docscache/not-docs-bundle", cerrors.Meta{
	ShortCode: "E0123",
	Message:   "docscache: artifact is not a documentation bundle",
})

// ErrPageNotFound reports that the requested page is absent from an otherwise
// present, integrity-verified bundle. It is distinct from ErrFetchNotFound (no
// bundle cached at all): the bundle resolved and cached, but carries no entry
// at the requested path.
func ErrPageNotFound() error { return errPageNotFound }

var errPageNotFound = cerrors.Register("docscache/page-not-found", cerrors.Meta{
	ShortCode: "E0124",
	Message:   "docscache: page not found in documentation bundle",
})

// VerifyAtPull verifies a resolved documentation bundle's signature at pull
// time, given the OCI manifest digest the signature commits to. It is the
// signature seam the resolve path drives: the caller wires the same pull-time
// verification a catalog entity or plugin uses, so a documentation bundle — a
// non-executable artifact — verifies through the identical path rather than a
// bespoke one. A nil VerifyAtPull skips signature verification; content
// integrity against the manifest is enforced regardless.
type VerifyAtPull func(ctx context.Context, manifestDigest string) error

// DocsBundlesRoot returns the root of the documentation-bundle subtree of the
// workspace's artifact cache:
//
//	<workspaceRoot>/<cacheDir>/docs-bundles/
//
// It is the single site that names the `docs-bundles/` segment; the per-slot
// path composes beneath it and cache-inspection tools enumerate the subtree
// from it, so the segment is spelled once. The subtree sits parallel to the
// `entities/` and `source-artifacts/` siblings, so a by-hand cache inspection
// surfaces one clean subtree per artifact class.
func DocsBundlesRoot(workspaceRoot, cacheDir string) string {
	return filepath.Join(workspaceRoot, cacheDir, "docs-bundles")
}

// slotPath returns the cache slot for a documentation bundle addressed by
// reference and version:
//
//	<DocsBundlesRoot>/<url-encoded ref>/<version>/
//
// The reference is URL-encoded to a single filesystem-safe segment so the slot
// is stable across operating systems. Two slotPath calls with the same inputs
// produce byte-identical paths.
func slotPath(workspaceRoot, cacheDir, ref, version string) string {
	return filepath.Join(DocsBundlesRoot(workspaceRoot, cacheDir), encodeRef(ref), version)
}

// encodeRef maps an OCI reference to a single filesystem-safe path segment via
// standard URL path-segment encoding, stripping any oci:// scheme prefix first
// so a scheme-qualified and a bare reference to the same artifact share a slot.
func encodeRef(ref string) string {
	ref = strings.TrimPrefix(ref, "oci://")
	return url.PathEscape(ref)
}

// ResolveAndCache pulls the documentation bundle tagged version from target,
// enforces its content integrity against the OCI manifest, verifies its
// signature at pull when verify is non-nil, and writes the bundle's content
// blob and manifest into the local cache slot for (ref, version). It returns
// the slot directory.
//
// oci.Pull verifies the content against the manifest layer digest inside the
// fetch, so a corrupt or substituted blob fails before it is ever cached. The
// media type is checked against the documentation-bundle type so a reference
// that resolves to a different artifact kind is rejected. Signature
// verification — when wired — runs before the bundle is written, so an
// unverifiable bundle never populates the cache. The manifest is persisted
// beside the blob so a later offline ReadPage can re-derive the manifest
// digest from stored bytes rather than trusting an unchecked recorded value.
func ResolveAndCache(ctx context.Context, target oras.ReadOnlyTarget, ref, version, workspaceRoot, cacheDir string, verify VerifyAtPull) (string, error) {
	pr, err := oci.Pull(ctx, target, version)
	if err != nil {
		return "", fmt.Errorf("docscache: pull %s@%s: %w", ref, version, err)
	}
	if pr.MediaType != oci.MediaTypeDocs {
		return "", fmt.Errorf("docscache: %s@%s has media type %q, want %q: %w",
			ref, version, pr.MediaType, oci.MediaTypeDocs, ErrNotDocsBundle())
	}
	if verify != nil {
		if err := verify(ctx, pr.Digest); err != nil {
			return "", fmt.Errorf("docscache: verify %s@%s: %w", ref, version, err)
		}
	}

	slot := slotPath(workspaceRoot, cacheDir, ref, version)
	if err := os.MkdirAll(slot, 0o755); err != nil {
		return "", fmt.Errorf("docscache: cache mkdir %s: %w", slot, err)
	}
	if err := os.WriteFile(filepath.Join(slot, "blob"), pr.Bytes, 0o644); err != nil {
		return "", fmt.Errorf("docscache: cache write blob for %s@%s: %w", ref, version, err)
	}
	if err := os.WriteFile(filepath.Join(slot, "manifest"), pr.ManifestBytes, 0o644); err != nil {
		return "", fmt.Errorf("docscache: cache write manifest for %s@%s: %w", ref, version, err)
	}
	return slot, nil
}

// ReadPage returns the bytes and MIME type of a single page inside the cached
// documentation bundle for (ref, version). It is fully offline: it takes no
// puller or target and reads only what a prior ResolveAndCache stored, so a
// missing bundle is a wrapped ErrFetchNotFound (loud cache miss) rather than a
// fetch. The cached content is re-verified against its persisted manifest on
// every read, so a bundle tampered with on disk halts with ErrFetchIntegrity
// instead of serving forged bytes.
//
// page is the bundle-internal path of the requested entry (e.g.
// "language-reference/entities/component.md"). A page absent from an otherwise
// valid bundle returns ErrPageNotFound.
func ReadPage(workspaceRoot, cacheDir, ref, version, page string) ([]byte, string, error) {
	slot := slotPath(workspaceRoot, cacheDir, ref, version)
	blob, err := os.ReadFile(filepath.Join(slot, "blob"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("docscache: no cached bundle for %s@%s: %w", ref, version, ErrFetchNotFound())
		}
		return nil, "", fmt.Errorf("docscache: read cached bundle %s@%s: %w", ref, version, err)
	}
	manifest, err := os.ReadFile(filepath.Join(slot, "manifest"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("docscache: cached bundle %s@%s missing its manifest: %w", ref, version, ErrFetchNotFound())
		}
		return nil, "", fmt.Errorf("docscache: read cached manifest %s@%s: %w", ref, version, err)
	}
	if err := oci.VerifyContentAgainstManifest(manifest, blob); err != nil {
		return nil, "", fmt.Errorf("docscache: cached bundle %s@%s: %w", ref, version, err)
	}

	data, ok, err := readTarEntry(blob, page)
	if err != nil {
		return nil, "", fmt.Errorf("docscache: read page %q from %s@%s: %w", page, ref, version, err)
	}
	if !ok {
		return nil, "", fmt.Errorf("docscache: page %q in %s@%s: %w", page, ref, version, ErrPageNotFound())
	}
	return data, mimeForPage(page), nil
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
		clean := path.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
			return nil, false, fmt.Errorf("path traversal entry %q: %w", hdr.Name, oci.ErrFetchIntegrity())
		}
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
