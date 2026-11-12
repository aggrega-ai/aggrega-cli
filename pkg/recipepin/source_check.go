// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package recipepin

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// CheckSourcePin compares the source pin an entity's lock records for one source
// with the lockfile's record of the same source, and returns the halt a
// disagreement raises. It returns nil where the two agree, and nil where either
// record holds nothing for this source.
//
// It is the one home of that comparison and of the diagnostic it produces, and
// every command that makes the comparison calls it: a build, before it reads the
// cached archive, and a verify, over the locks beside a Release's own entities. A
// second copy of either half would be free to disagree with this one about which
// pins are compared or about what a reader is told, and a reader comparing the two
// commands' output could not tell a deliberate difference from a drifted one.
//
// # The coordinates are the ones the entity authors
//
// kind, url, chart, moduleSubdir, version and ref are the source as the entity
// writes it, with any template in it already evaluated. The pin is looked up under
// the authored version, and the lockfile row through the join
// [lockfile.Lockfile.FindSourceArtifact] states — the authored reference where
// there is one, the authored version otherwise.
//
// So a pin recorded under a version the entity does not author is not reached.
// That is the case for a source written at a git reference: the pull records the
// pin, and the lockfile row, under the commit the reference resolved to, while the
// entity names the reference. Nothing is compared for such a pin here. The row
// itself is still reached through the reference, and the build's cache read
// verifies the archive against it.
//
// # What the diagnostic names
//
// It is positioned at lockPath, because both remedies act on the lock: restoring
// the committed file, or the pin step rewriting it from a fresh resolution. It
// names both values of whichever digest disagrees — the content
// digest, or the manifest digest where the content digests agree — because either
// record may be the one that moved, and two equal values would tell a reader
// nothing.
func CheckSourcePin(pins []types.SourceDigest, lf *lockfile.Lockfile, lockPath, kind, url, chart, moduleSubdir, version, ref string) error {
	pin, pinned := MatchSourcePin(pins, kind, url, chart, moduleSubdir, version)
	if !pinned {
		return nil
	}
	row, recorded := lf.FindSourceArtifact(kind, url, chart, moduleSubdir, version, ref)
	if !recorded {
		return nil
	}
	if pin.Digest == row.Digest && pin.ManifestDigest == row.ManifestDigest {
		return nil
	}
	which, pinnedValue, recordedValue := "digest", pin.Digest, row.Digest
	if pin.Digest == row.Digest {
		which, pinnedValue, recordedValue = "manifest digest", pin.ManifestDigest, row.ManifestDigest
	}
	// Name the source by whichever coordinate its author wrote. A source declared
	// only at a reference has no version to print, and an "@" with nothing after it
	// reads as a missing value rather than as a source identified another way.
	at := version
	if at == "" {
		at = ref
	}
	condition := oci.ErrFetchIntegrity()
	return cerrors.Wrap(condition, cerrors.FetchError,
		cerrors.SourceLocation{File: lockPath},
		cerrors.ComposeMessage(condition, []string{
			fmt.Sprintf("source-artifact %s@%s", url, at),
			fmt.Sprintf("the source pin recorded in %s has %s %s", LockFileName, which, pinnedValue),
			fmt.Sprintf("the lockfile records %s", recordedValue),
		}, sourcePinRemediation))
}

// sourcePinRemediation is what a source-pin disagreement tells its reader to do.
//
// It opens with the word the fetch family's remediations open with, and that is
// load-bearing rather than stylistic: a build wraps every fetch failure and adds
// the family's generic remediation unless the chain already carries one, and the
// generic one for an integrity fault is a full upgrade — a heavier act than this
// fault needs, since restoring the lock or pinning the entity again rewrites
// exactly the file that disagrees.
const sourcePinRemediation = "remediation: restore " + LockFileName +
	", or run `aggrega pin` on the entity to rewrite it"
