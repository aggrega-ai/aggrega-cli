// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AuthoredEntityDigest returns the digest a lockfile's entity row records for the
// entity authored in the document data, found at path: the digest of what a
// publication of it would carry — the document and every file its declarations
// name beside it, read through [DeclaredFilesOf] — computed by [oci.CarriedDigest]
// over those files as they stand.
//
// It is the author's half of the one subject an entity row has. A consumer of the
// same entity, holding the bytes a registry published for it, records
// [oci.PublishedDigest] of them, and the two agree because both read the same
// entries under the same rule. So a chart edited under a Recipe, a file planted
// inside a source it declares, and a changed lock beside it each move the row,
// while a file beside the entity that nothing declares neither travels nor moves
// it.
func AuthoredEntityDigest(data []byte, path string) (string, error) {
	return authoredEntityDigest(data, path, nil)
}

// AuthoredEntityDigestAt is [AuthoredEntityDigest] over the entity document on
// disk at path.
func AuthoredEntityDigestAt(path string) (string, error) {
	return authoredEntityDigestAt(path, nil)
}

// UnwrittenLocks holds, for a run that reports what it would write instead of
// writing it, the lock that run would leave beside each entity document: keyed by
// the document's path as the catalog walk recorded it, the bytes the lock would
// hold, or nil where the run would remove it. A document with no entry keeps the
// lock on disk beside it, because the run would leave that lock as it is.
//
// It exists so that such a run and the build it predicts digest an entity through
// one computation. The lock is one of the files an entity row covers, so a row the
// run records over the lock it would write, and a build of the same Release
// reading the lock on disk, would describe two different entities; a view holding
// the run's unwritten locks ([LazyLookup.SetUnwrittenLocks]) reads each one in
// place of the file on disk, wherever that view takes an entity row, so the two
// readings are one.
//
// Only a Recipe or a FeatureRecipe carries the lock beside it, so only their
// documents have an entry: the pin step that fills this writes a lock for no
// other kind.
type UnwrittenLocks map[string][]byte

// authoredEntityDigestAt is [authoredEntityDigest] over the entity document on
// disk at path.
func authoredEntityDigestAt(path string, unwritten UnwrittenLocks) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return authoredEntityDigest(data, path, unwritten)
}

// authoredEntityDigest is the one computation of an authored entity's row:
// [AuthoredEntityDigest], with the lock unwritten holds for the document at path,
// where it holds one, read in place of the lock on disk — carried with the bytes
// it would hold, or left out where the run would remove it.
//
// The unwritten lock is read for its ignore rules as well as carried, because
// those rules decide which files under the entity's local source directory the
// row covers: a row taken over the lock a run would write is a row over the
// files that lock's rules keep.
func authoredEntityDigest(data []byte, path string, unwritten UnwrittenLocks) (string, error) {
	lock, overlaid := unwritten[path]
	var declared DeclaredFiles
	var err error
	if overlaid {
		var ignore *types.IgnoreRules
		if len(lock) > 0 {
			decoded, derr := recipepin.DecodeLock(lock, recipepin.LockPath(path))
			if derr != nil {
				return "", derr
			}
			ignore = decoded.Ignore
		}
		declared, err = DeclaredFilesUnder(data, path, ignore)
	} else {
		declared, err = DeclaredFilesOf(data, path)
	}
	if err != nil {
		return "", err
	}
	files := declared.Files()
	supplied := map[string][]byte{filepath.Base(path): data}
	if overlaid {
		kept := make([]string, 0, len(files)+1)
		for _, f := range files {
			if f != recipepin.LockFileName {
				kept = append(kept, f)
			}
		}
		if len(lock) > 0 {
			kept = append(kept, recipepin.LockFileName)
			sort.Strings(kept)
			supplied[recipepin.LockFileName] = lock
		}
		files = kept
	}
	return oci.CarriedDigest(path, files, supplied)
}

// PinDigest returns the digest a lockfile row records for bytes a registry
// published, as a cache slot holds them: the digest of what the publication
// carries where they carry an entity whose row is an entity row, and the digest of
// the bytes themselves for anything else — a library, which always publishes as
// its document and whose row records its bytes, and a plugin's executable, whose
// row records the executable's.
//
// Which row pins them is read from the entity the bytes carry, through the same
// reading every consumer applies, so a slot is held to the row of the entity it
// holds whatever reader reaches it. An archive that does not unpack returns the
// refusal [oci.EntityYAML] gives it.
func PinDigest(published []byte) (string, error) {
	document, err := oci.EntityYAML(published)
	if err != nil {
		return "", err
	}
	if kind := peekEntityKind(document); oci.IsPublishableKind(kind) && !isLibraryKind(kind) {
		return oci.PublishedDigest(published)
	}
	return contentDigest(published), nil
}

// isLibraryKind reports whether kind is the kind of a library on a declared axis.
// A library's lockfile row records the digest of its bytes rather than of what a
// publication carries: it always publishes as its document alone, so the two
// actors already record one subject for it.
func isLibraryKind(kind string) bool {
	for _, axis := range closure.LibraryAxes() {
		if axis.Kind == kind {
			return true
		}
	}
	return false
}

// entityRowDigest returns the digest the entity row for the entity this catalog
// indexes under kind and indexKey records: over its declared set where the entity
// was authored in a catalog directory — reading any lock this catalog holds as
// unwritten in place of the one on disk — and over the published bytes retained
// for it where it was read from a cache slot.
func (c *Catalog) entityRowDigest(kind, indexKey string) (string, error) {
	key := entityBytesKey(kind, indexKey)
	data := c.entityBytes[key]
	if document, ok := c.authoredDocumentOf(key); ok {
		return authoredEntityDigest(data, document, c.unwrittenLocks)
	}
	return oci.PublishedDigest(data)
}

// SetUnwrittenLocks installs the locks a run that reports its writes would leave,
// so every entity row this view takes for an authored entity reads each one in
// place of the lock on disk: a closure resolved through the view, a catalog
// directory's copy of a pinned entity, and [LazyLookup.EntityRowDigestAt]. A nil
// or empty set reads every lock from disk, which is what a view that writes, or
// writes nothing, holds.
//
// It is for the one owner of a view that reports rather than writes, installed
// once its report is known and before anything reads the rows; a pin change keeps
// it, because what a lock would hold does not depend on the pins, and Reset drops
// it with the rest of what the view read from the workspace.
func (l *LazyLookup) SetUnwrittenLocks(locks UnwrittenLocks) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unwrittenLocks = locks
	if l.recipeCat != nil {
		l.recipeCat.unwrittenLocks = locks
	}
}

// EntityRowDigestAt returns the digest the lockfile's entity row records for the
// entity authored in the document at path, as this view reads it:
// [AuthoredEntityDigestAt], with the lock [LazyLookup.SetUnwrittenLocks]
// installed for the document, where it installed one, read in place of the file
// beside it. It is the reading a closure resolved through this view takes of the
// same entity, so a caller recording it records what that closure holds.
func (l *LazyLookup) EntityRowDigestAt(path string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return authoredEntityDigestAt(path, l.unwrittenLocks)
}

// authoredDocumentOf returns the catalog-directory document the entity indexed
// under key was authored in, and whether it has one. The lazy backing answers from
// its record of where each entity came from; the eager load, which holds no such
// record, answers from the identity it claimed as it indexed each file. An entity
// with neither — read from a cache slot, or placed in a catalog literal — has none.
func (c *Catalog) authoredDocumentOf(key string) (string, bool) {
	if origin, ok := c.origins[key]; ok {
		return origin.authoredDocument()
	}
	if path, ok := c.pathByIdentity[key]; ok && path != "" {
		return path, true
	}
	return "", false
}
