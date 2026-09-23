// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// LockFileName is the name of the lock file that sits beside an entity and
// carries the pins derived for it.
//
// It is one name for every kind rather than a spelling per kind, and what tells
// two locks apart is the directory each sits in. A reader who learns the name
// once can predict it everywhere, and a kind-specific spelling would have had to
// be learned again for each kind while carrying exactly the same content.
const LockFileName = "entity.lock"

// CurrentLockVersion is the entity-lock schema version this package writes. The
// published schema pins the field to this value, so a document carrying any
// other integer is refused at read rather than decoded into a shape this
// package does not speak.
//
// It is exported so a caller constructing a lock names the same constant the
// writer stamps, rather than spelling the integer a second time where nothing
// would report the two coming apart.
const CurrentLockVersion = 1

// lockHeader opens every written lock. It is addressed at the author who finds
// the file in their own directory and in their own diffs, and it says the two
// things that are actionable from there: nothing here was typed by hand, and the
// command that brings it up to date.
const lockHeader = "# Content pins for this entity's external inputs, written by `aggrega pull`.\n" +
	"# Commit it beside the entity. Re-run the pull to bring it up to date.\n"

// Lock is the decoded content of an entity's lock file: the content pins for the
// inputs the entity reaches for outside its own document.
//
// The two pin classes are the two ways an entity depends on bytes it does not
// itself carry — an acquired remote source, and a file an artifacts entry
// imports from the workspace — and they are kept as separate lists because they
// are keyed differently: a source by its resolved identity, an imported file by
// the artifacts key that consumes it.
//
// The rows are the same types the entity's own pin blocks are built from, which
// is what keeps one pin from reading two ways depending on where it was written.
type Lock struct {
	Version         int                    `json:"version" yaml:"version"`
	SourceDigests   []types.SourceDigest   `json:"sourceDigests,omitempty" yaml:"sourceDigests,omitempty"`
	ArtifactDigests []types.ArtifactDigest `json:"artifactDigests,omitempty" yaml:"artifactDigests,omitempty"`
}

// empty reports whether the lock carries no pin at all, which is the state that
// has no file rather than an empty one: a document holding two empty lists says
// the same thing as no document, and leaving one behind would put a file in the
// author's directory that records nothing.
func (l Lock) empty() bool { return len(l.SourceDigests) == 0 && len(l.ArtifactDigests) == 0 }

// LockPath returns the lock file path for the entity document at entityPath —
// the sibling [LockFileName] in the entity's own directory.
func LockPath(entityPath string) string {
	return filepath.Join(filepath.Dir(entityPath), LockFileName)
}

// ReadLock reads the lock beside the entity document at entityPath and reports
// whether one is present.
//
// An absent lock is not a failure: an entity that reaches for nothing outside
// its own document has no pins to record, and one that has not been resolved yet
// has none recorded. Both answer (zero, false, nil), and a caller distinguishing
// "no pins" from "pins that did not match" reads the flag rather than the
// length.
//
// A lock that IS present is validated against its published schema before it is
// decoded, and a violation is returned rather than tolerated. The reason is the
// one that makes the file worth having: a pin the decode silently dropped —
// because the document spelled a key the Go shape has no field for — would leave
// the build comparing against nothing and reporting that as agreement, which
// reads exactly like a build whose inputs had not drifted.
func ReadLock(entityPath string) (Lock, bool, error) {
	path := LockPath(entityPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Lock{}, false, nil
		}
		return Lock{}, false, lockReadError(path, err)
	}
	if _, verr := envelope.ValidateBytes(envelope.KindEntityLock, data, path); verr != nil {
		return Lock{}, false, lockReadError(path, verr)
	}
	var lock Lock
	if uerr := yaml.Unmarshal(data, &lock); uerr != nil {
		return Lock{}, false, lockReadError(path, uerr)
	}
	return lock, true, nil
}

// lockReadError reports a lock file that could not be read as the catalog-level
// load failure it is, naming the lock file itself rather than the entity beside
// it — the file the reader would open to fix it is the one that would not read.
func lockReadError(path string, cause error) error {
	return cerrors.Wrap(cause, cerrors.CatalogError, cerrors.SourceLocation{File: path},
		fmt.Sprintf("read entity lock %s: %s", path, cause.Error()))
}

// PreviewLock returns the bytes [WriteLock] would leave beside the entity
// document at entityPath for the same pins, and whether writing them would
// change what is on disk.
//
// It is the computing half of the write, and [WriteLock] is it plus one call to
// the filesystem — so a caller reporting what a resolution would do reads the
// same bytes the resolution writes rather than a second rendering of them.
//
// An empty pin set previews as no bytes at all, and rewrites is then true
// exactly when a lock is currently present: what the write performs in that case
// is a removal, and a caller reporting the run needs to be told the file moves
// even though there is nothing to show as its new content.
func PreviewLock(entityPath string, srcPins []types.SourceDigest, artPins []types.ArtifactDigest) ([]byte, bool, error) {
	lock := Lock{Version: CurrentLockVersion}
	lock.SourceDigests = append(lock.SourceDigests, srcPins...)
	lock.ArtifactDigests = append(lock.ArtifactDigests, artPins...)
	sortSourcePins(lock.SourceDigests)
	sortArtifactPins(lock.ArtifactDigests)

	path := LockPath(entityPath)
	current, currentErr := os.ReadFile(path)
	present := currentErr == nil
	if currentErr != nil && !os.IsNotExist(currentErr) {
		return nil, false, lockReadError(path, currentErr)
	}

	if lock.empty() {
		return nil, present, nil
	}

	out, err := encodeLock(lock)
	if err != nil {
		return nil, false, err
	}
	return out, !present || !bytes.Equal(current, out), nil
}

// WriteLock brings the lock beside the entity document at entityPath into
// agreement with srcPins and artPins, and reports whether what is on disk
// changed.
//
// Both pin lists are sorted to a canonical order before emission (sources by
// {kind, url, chart, moduleSubdir, version}, artifacts by name), and the
// document carries no timestamp and no resolution metadata, so re-running a
// resolution over unchanged declarations leaves the file byte-identical. That is
// what lets an author keep the file under version control and read its diff as a
// statement about their inputs rather than about when they last ran a command.
//
// An empty pin set removes the file rather than writing an empty document, so an
// entity that stops reaching outside itself stops carrying a lock.
//
// The entity document is never opened for writing here, and that is the point of
// the file: what an author typed is left exactly as typed.
func WriteLock(entityPath string, srcPins []types.SourceDigest, artPins []types.ArtifactDigest) (bool, error) {
	out, changes, err := PreviewLock(entityPath, srcPins, artPins)
	if err != nil {
		return false, err
	}
	if !changes {
		return false, nil
	}
	path := LockPath(entityPath)
	if len(out) == 0 {
		if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
			return false, fmt.Errorf("remove %s: %w", path, rerr)
		}
		return true, nil
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return false, fmt.Errorf("write %s: %w", path, werr)
	}
	return true, nil
}

// encodeLock renders a lock to its on-disk bytes at the indentation every
// Aggrega-written YAML document uses, behind the header the author reads.
func encodeLock(lock Lock) ([]byte, error) {
	var body bytes.Buffer
	enc := yaml.NewEncoder(&body)
	enc.SetIndent(2)
	if err := enc.Encode(lock); err != nil {
		return nil, fmt.Errorf("encode entity lock: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode entity lock: %w", err)
	}
	return append([]byte(lockHeader), body.Bytes()...), nil
}
