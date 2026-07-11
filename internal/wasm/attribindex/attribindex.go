// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package attribindex is the CLI-owned compile-attribution index: a small
// on-disk record mapping each leaf of the machine-global compiled-module cache
// to the guest identities that depend on it. Where the compiled-module cache is
// keyed by the runtime's own internal hashing — opaque to the compiler — this
// index records, by observing what a cold compile emits, which guest content
// digests a given compiled leaf was produced for. It is the metadata a
// reference-based reclamation reads to decide whether a compiled leaf is still
// depended upon.
//
// A leaf key is the leaf file's path relative to the compile directory,
// forward-slashed. A leaf ABSENT from the index is "unattributed": absence is
// the representation, so a leaf whose attribution could not be determined is
// simply not recorded and is therefore retained by any reference-based cleanup.
// One leaf is the shared runtime kernel every guest depends on; it carries the
// Kernel flag and accumulates a referrer per distinct guest. Every other
// recorded leaf is a single guest's compiled module and carries that guest's
// content digest as its sole referrer.
//
// The index is re-derivable cache metadata, not a source of truth: it is
// written best-effort as a side effect of a cold compile and a write failure
// never breaks a load. Referrers are stored normalized (prefix-free lowercase
// hex). On-disk ordering is deterministic — entries sorted by leaf key,
// referrers sorted within each entry — so the file is byte-stable across writes
// of identical state. Persistence is an atomic write-temp-rename under a
// cross-process file lock, since parallel CLI invocations share one user cache
// directory.
//
// The package deliberately imports no host-runtime package (in particular not
// the wasm loader package that calls into it): every path is taken as a
// parameter, so the loader → attribindex dependency stays acyclic.
package attribindex

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// indexFileName is the compile-attribution index's filename under the user
// cache root, a sibling of the compiled-module cache directory and the
// provenance index.
const indexFileName = "compile-attribution-index.json"

// lockFileName is the cross-process lock guarding the atomic index write. It
// sits beside the index file in the same directory.
const lockFileName = ".compile-attribution-index.lock"

// Entry is the record for one compiled-module cache leaf: its key (the leaf
// file's path relative to the compile directory, forward-slashed), its on-disk
// byte size, whether it is the shared runtime kernel, and the sorted,
// deduplicated set of normalized guest content digests that depend on it.
type Entry struct {
	// Leaf is the leaf key: the leaf file's path relative to the compile
	// directory, forward-slashed.
	Leaf string `json:"leaf"`
	// Size is the leaf file's on-disk byte size.
	Size int64 `json:"size"`
	// Kernel marks the single shared runtime kernel leaf every guest depends on.
	// A non-kernel leaf is one guest's compiled module.
	Kernel bool `json:"kernel"`
	// Referrers is the sorted, deduplicated set of normalized (prefix-free
	// lowercase hex) guest content digests that depend on this leaf. A guest
	// leaf carries exactly one; the kernel leaf accumulates one per distinct
	// guest.
	Referrers []string `json:"referrers"`
}

// Index is an in-memory view of the compile-attribution index, keyed by leaf
// key. Construct it with Load; enumerate it in deterministic order with
// Entries; look one leaf up with Entry.
type Index struct {
	byLeaf map[string]Entry
}

// fileFormat is the on-disk shape: a single sorted slice of entries. A struct
// wrapper (rather than a bare map) gives encoding/json a fixed field order and
// lets the slice carry the deterministic leaf-key ordering.
type fileFormat struct {
	Entries []Entry `json:"entries"`
}

// normalize reduces a content digest to its referrer form: the lowercase hex
// with any "sha256:" algorithm prefix stripped.
//
// This MUST match the provenance index's Normalize spelling exactly, so a
// referrer digest recorded here joins the provenance index's identity records
// by the same key. It is copied rather than imported because the provenance
// index depends on the wasm loader package, which in turn calls into this
// package; importing it here would cycle. If the provenance index's spelling
// ever changes, change this in lockstep.
func normalize(d string) string {
	return strings.ToLower(strings.TrimPrefix(d, "sha256:"))
}

// DefaultIndexPath returns the compile-attribution index's path under the given
// user cache root — a sibling of the compiled-module cache directory and the
// provenance index. Under a test binary it returns a fixed location under the
// system temp directory, so a suite never writes to the developer's real cache.
// The caller supplies the cache root (rather than this package resolving it) so
// the package depends on no host-runtime package.
func DefaultIndexPath(cacheRoot string) string {
	if testing.Testing() {
		return filepath.Join(os.TempDir(), "aggrega-compile-attribution-index-test", indexFileName)
	}
	return filepath.Join(cacheRoot, indexFileName)
}

// PathForCompileDir returns the index path that sits beside the compiled-module
// cache directory: the index lives in the compile directory's parent, alongside
// the provenance index. It is the write-side spelling the loader uses when it
// knows only the compile directory. In the shipped layout the compile directory
// is a child of the user cache root, so this resolves to the same file
// DefaultIndexPath(root) returns.
func PathForCompileDir(compileDir string) string {
	return filepath.Join(filepath.Dir(compileDir), indexFileName)
}

// Load reads the compile-attribution index at path. A missing file is not an
// error — it yields an empty index — so a first-ever run starts clean. A present
// but unparsable file returns the error alongside an empty index, letting the
// caller decide whether to rebuild (the best-effort write path) or report
// (inspection). An empty path yields an empty index.
func Load(path string) (Index, error) {
	ix := Index{byLeaf: map[string]Entry{}}
	if path == "" {
		return ix, nil
	}
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
		ix.byLeaf[e.Leaf] = e
	}
	return ix, nil
}

// Entries returns every recorded entry in deterministic order: sorted by leaf
// key, with each entry's referrers sorted. The returned slice is a copy; the
// index is not mutated.
func (ix Index) Entries() []Entry {
	out := make([]Entry, 0, len(ix.byLeaf))
	for _, e := range ix.byLeaf {
		e.Referrers = sortedUnique(e.Referrers)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Leaf < out[j].Leaf })
	return out
}

// Entry returns the record for leafKey and whether one exists. The returned
// entry's referrers are sorted. A leaf absent from the index (ok false) is
// "unattributed".
func (ix Index) Entry(leafKey string) (Entry, bool) {
	e, ok := ix.byLeaf[leafKey]
	if !ok {
		return Entry{}, false
	}
	e.Referrers = sortedUnique(e.Referrers)
	return e, true
}

// RecordGuestLeaf upserts a non-kernel leaf entry for leafKey, unioning the
// normalized guestDigest into its referrer set and refreshing its size. It is
// idempotent: a repeated record of the same {leaf, guest} leaves the entry
// unchanged. The whole operation runs under the cross-process file lock and
// finishes with an atomic write-temp-rename.
func RecordGuestLeaf(path, leafKey string, size int64, guestDigest string) error {
	return rmw(path, func(ix *Index) {
		e := ix.byLeaf[leafKey]
		e.Leaf = leafKey
		e.Size = size
		e.Referrers = union(e.Referrers, normalize(guestDigest))
		ix.byLeaf[leafKey] = e
	})
}

// RecordKernelLeaf upserts the shared runtime kernel leaf entry for leafKey,
// marking it Kernel and seeding it with the first depending guest's normalized
// digest. Later guests join the kernel's referrer set via AddKernelReferrer.
func RecordKernelLeaf(path, leafKey string, size int64, firstGuestDigest string) error {
	return rmw(path, func(ix *Index) {
		e := ix.byLeaf[leafKey]
		e.Leaf = leafKey
		e.Size = size
		e.Kernel = true
		e.Referrers = union(e.Referrers, normalize(firstGuestDigest))
		ix.byLeaf[leafKey] = e
	})
}

// AddKernelReferrer unions the normalized guestDigest into the referrer set of
// the (single) Kernel-flagged entry, if one is recorded. Every guest depends on
// the shared kernel, so a cold guest compile records this after recording its
// own leaf. It is a no-op when no kernel entry exists yet — the kernel then
// stays unattributed until its own two-leaf first compile records it.
func AddKernelReferrer(path, guestDigest string) error {
	return rmw(path, func(ix *Index) {
		for _, key := range sortedKeys(ix.byLeaf) {
			e := ix.byLeaf[key]
			if e.Kernel {
				e.Referrers = union(e.Referrers, normalize(guestDigest))
				ix.byLeaf[key] = e
				return
			}
		}
	})
}

// KernelRecorded reports whether a Kernel-flagged entry exists in the index at
// path. A cold compile that sees two new leaves consults this to distinguish a
// genuine first-ever compile (no kernel yet) from a confounded concurrent
// compile (kernel already recorded).
func KernelRecorded(path string) (bool, error) {
	ix, err := Load(path)
	if err != nil {
		return false, err
	}
	for _, e := range ix.byLeaf {
		if e.Kernel {
			return true, nil
		}
	}
	return false, nil
}

// rmw runs a read-modify-write of the index at path under the cross-process
// file lock, finishing with an atomic write-temp-rename. An unparsable existing
// file is rebuilt rather than propagated, since the index is re-derivable
// metadata.
func rmw(path string, mutate func(*Index)) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	release := flockIndex(dir)
	defer release()

	ix, err := Load(path)
	if err != nil {
		ix = Index{byLeaf: map[string]Entry{}}
	}
	mutate(&ix)
	return writeAtomic(path, ix)
}

// union adds v to vs if absent, returning the sorted, deduplicated set.
func union(vs []string, v string) []string {
	for _, x := range vs {
		if x == v {
			return sortedUnique(vs)
		}
	}
	return sortedUnique(append(vs, v))
}

// sortedUnique returns vs sorted ascending with duplicates removed, the single
// referrer ordering the on-disk file relies on for byte-stability.
func sortedUnique(vs []string) []string {
	if len(vs) == 0 {
		return vs
	}
	cp := append([]string(nil), vs...)
	sort.Strings(cp)
	out := cp[:0]
	for i, x := range cp {
		if i == 0 || x != cp[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// sortedKeys returns the map's keys in ascending order, so the single-kernel
// scan in AddKernelReferrer visits entries deterministically.
func sortedKeys(m map[string]Entry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

	tmp, err := os.CreateTemp(filepath.Dir(path), ".attribindex-*.tmp")
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
