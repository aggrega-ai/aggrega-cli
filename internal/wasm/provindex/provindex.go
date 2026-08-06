// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package provindex is the CLI-owned provenance index: a small on-disk record
// mapping a plugin guest's content digest to the identities — the {URI, version}
// pairs — the resolver loaded those exact bytes for, plus when the digest was
// first seen and the guest's byte size. It exists so cache-inspection can report
// the real identity of a compiled module instead of only the runtime's opaque
// internal hash: distinct URIs (or versions) that resolve to byte-identical
// guests collapse to one digest whose identity set names every source.
//
// The index is re-derivable cache metadata, not a source of truth: it is written
// best-effort as a side effect of resolution and a write failure never breaks a
// load. The digest key is stored normalized (prefix-free lowercase hex) so it
// joins the same way every other digest-addressed cache does. On-disk ordering is
// deterministic — entries sorted by digest, identities sorted within each entry —
// so the file is byte-stable across writes of identical state. Persistence is an
// atomic write-temp-rename under a cross-process file lock, since parallel CLI
// invocations share one user cache directory.
package provindex

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// indexFileName is the provenance index's filename under the user cache root,
// a sibling of the compile-cache and plugin-describe directories.
const indexFileName = "provenance-index.json"

// lockFileName is the cross-process lock guarding the atomic index write. It
// sits beside the index file in the same directory.
const lockFileName = ".provenance-index.lock"

// Identity is one source a set of guest bytes was resolved for: the plugin URI
// and the lockfile-resolved version (empty when the resolution carried no pin).
type Identity struct {
	URI     string `json:"uri"`
	Version string `json:"version"`
}

// Entry is the record for one content digest: when the digest was first
// recorded, the guest's byte size, and the deduplicated, sorted set of
// identities the resolver loaded those bytes for. A set size above one means
// distinct URIs or versions resolve to byte-identical guests.
type Entry struct {
	// Digest is the normalized (prefix-free lowercase hex) content digest key.
	Digest string `json:"digest"`
	// FirstSeen is the time the digest was first recorded. It is set once and
	// left unchanged across later upserts of the same digest.
	FirstSeen time.Time `json:"firstSeen"`
	// SizeGuestBytes is the guest's byte size.
	SizeGuestBytes int64 `json:"sizeGuestBytes"`
	// Identities is the sorted, deduplicated set of sources for these bytes.
	Identities []Identity `json:"identities"`
}

// Index is an in-memory view of the provenance index, keyed by normalized
// digest. Construct it with Load; enumerate it in deterministic order with
// Entries.
type Index struct {
	byDigest map[string]Entry
}

// fileFormat is the on-disk shape: a single sorted slice of entries. A struct
// wrapper (rather than a bare map) gives encoding/json a fixed field order and
// lets the slice carry the deterministic digest ordering.
type fileFormat struct {
	Entries []Entry `json:"entries"`
}

// DefaultIndexPath returns the provenance index's path under the cache root and
// whether one could be resolved. The file is a sibling of the compile-cache and
// plugin-describe directories, reusing the single exported root spelling rather
// than re-deriving it — so wherever that root resolves, this index follows it. A
// false return means no cache root could be resolved.
func DefaultIndexPath() (string, bool) {
	root, ok := wasm.AggregaCacheRoot()
	if !ok {
		return "", false
	}
	return filepath.Join(root, indexFileName), true
}

// Load reads the provenance index at path. A missing file is not an error — it
// yields an empty index — so a first-ever run starts clean. A present but
// unparsable file returns the error alongside an empty index, letting the caller
// decide whether to rebuild (the best-effort write path) or report (inspection).
func Load(path string) (Index, error) {
	ix := Index{byDigest: map[string]Entry{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ix, nil
		}
		return ix, err
	}
	var f fileFormat
	if err := json.Unmarshal(b, &f); err != nil {
		return ix, err
	}
	for _, e := range f.Entries {
		ix.byDigest[e.Digest] = e
	}
	return ix, nil
}

// Entries returns every recorded entry in deterministic order: sorted by digest,
// with each entry's identities already sorted. The returned slice is a copy; the
// index is not mutated.
func (ix Index) Entries() []Entry {
	out := make([]Entry, 0, len(ix.byDigest))
	for _, e := range ix.byDigest {
		e.Identities = sortedIdentities(e.Identities)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Digest < out[j].Digest })
	return out
}

// Upsert records that guest bytes with content digest rawDigest were resolved
// for id, with the given byte size, at time now. It is idempotent and set-union:
// a repeated upsert of the same {digest, identity} leaves the entry unchanged
// (FirstSeen preserved), and a new identity for an existing digest joins that
// digest's identity set. rawDigest is normalized internally, so callers pass the
// digest in its native "sha256:..." spelling.
//
// The whole operation runs under a cross-process file lock and finishes with an
// atomic write-temp-rename, so concurrent CLI invocations sharing the user cache
// directory cannot tear the file. An unparsable existing file is rebuilt rather
// than propagated, since the index is re-derivable metadata. An empty normalized
// digest is a no-op.
func Upsert(path, rawDigest string, id Identity, sizeGuestBytes int64, now time.Time) error {
	key := digest.BareHex(rawDigest)
	if key == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	release := flockIndex(dir)
	defer release()

	ix, err := Load(path)
	if err != nil {
		ix = Index{byDigest: map[string]Entry{}}
	}
	e, ok := ix.byDigest[key]
	if !ok {
		e = Entry{Digest: key, FirstSeen: now.UTC(), SizeGuestBytes: sizeGuestBytes}
	} else {
		e.SizeGuestBytes = sizeGuestBytes
	}
	e.Identities = unionIdentity(e.Identities, id)
	ix.byDigest[key] = e

	return writeAtomic(path, ix)
}

// unionIdentity adds id to ids if absent, returning the sorted, deduplicated
// set. An identity already present leaves the set unchanged (idempotent).
func unionIdentity(ids []Identity, id Identity) []Identity {
	for _, x := range ids {
		if x == id {
			return sortedIdentities(ids)
		}
	}
	return sortedIdentities(append(ids, id))
}

// sortedIdentities returns ids ordered by URI then version, the single identity
// ordering the on-disk file relies on for byte-stability.
func sortedIdentities(ids []Identity) []Identity {
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].URI != ids[j].URI {
			return ids[i].URI < ids[j].URI
		}
		return ids[i].Version < ids[j].Version
	})
	return ids
}

// writeAtomic serializes the index deterministically and installs it with a
// write-temp-rename in the target directory, so a reader never observes a
// partial file. The temp file is removed on any failure before the rename.
func writeAtomic(path string, ix Index) error {
	f := fileFormat{Entries: ix.Entries()}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".provindex-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
