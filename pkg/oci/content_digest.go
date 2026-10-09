// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// CarriedDigest returns the digest a lockfile's entity row records for the entity
// whose document is at entityPath, given carried: the files its declarations name
// beside the document, as [Bundle] takes them. supplied names bytes to digest in
// place of what is on disk, keyed by carried path — the document a caller already
// read, or a file as it would stand once written.
//
// It is [PublishedDigest] of the publication [Bundle] would build from the same
// files, computed without building it: the same shape rule decides whether the
// entity publishes as its document alone, and the same entries are read, so an
// author holding the files and a consumer holding the published bytes record the
// same digest.
func CarriedDigest(entityPath string, carried []string, supplied map[string][]byte) (string, error) {
	entries, err := collectArchiveEntries(entityPath, carried, supplied)
	if err != nil {
		return "", err
	}
	if publishesAlone(carried) {
		return DocumentDigest(entries[0].data), nil
	}
	return entriesDigest(entries), nil
}

// PublishedDigest returns the digest a lockfile's entity row records for bytes
// published at a registry coordinate: the digest of the entries an archive
// carries, or of a document published alone as a set of one entry.
//
// An archive that does not unpack returns [ErrBundleUnreadable]; a document
// published alone always digests.
func PublishedDigest(published []byte) (string, error) {
	if !IsTarGzBundle(published) {
		return DocumentDigest(published), nil
	}
	entries, err := archiveEntries(published)
	if err != nil {
		return "", err
	}
	return entriesDigest(entries), nil
}

// DocumentDigest returns the digest a lockfile's entity row records for an entity
// document that is the whole of what it publishes: a set of one entry, laid out
// under [SingleDocumentFileName], which is what a document published alone is once
// a consumer lays it out.
func DocumentDigest(document []byte) string {
	return entriesDigest([]archiveEntry{{relPath: SingleDocumentFileName, data: document}})
}

// entriesDigest is the one computation of the digest an entity row records: the
// SHA-256 of the entries sorted by path, each contributing the SHA-256 of its
// bytes and its path, one line per entry. Every line opens with [archiveFileMode],
// the mode a publication records for every file.
//
// Nothing else enters it. The compressed bytes of an archive are not an input, so
// a recompression of the same entries — a different compression level, another
// machine's compressor — leaves it unchanged; nor do a modification time,
// ownership, or any permission bit, the execute bit included. The path is written
// with its length ahead of it, so no path, whatever it holds, can be read as the
// end of one entry and the start of the next.
func entriesDigest(entries []archiveEntry) string {
	sorted := append([]archiveEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].relPath < sorted[j].relPath })
	var listing bytes.Buffer
	for _, e := range sorted {
		fmt.Fprintf(&listing, "%o %s %d:%s\n", archiveFileMode, digest.BareHex(digest.ContentDigest(e.data)), len(e.relPath), e.relPath)
	}
	return digest.ContentDigest(listing.Bytes())
}

// archiveFileMode is the mode a publication records for every file it carries,
// whatever the file's mode on disk. It is the one statement of that mode, read by
// the archive a publication packs and by the digest a lockfile records for it.
//
// No permission bit is carried, the execute bit included, because none is a fact
// of the content: a checkout writes files by its own umask, and a system that
// records no execute bit reports none for a file another system checks out as
// executable. A mode read from disk would make one commit publish, and digest,
// differently depending on where it was checked out.
const archiveFileMode = 0o644
