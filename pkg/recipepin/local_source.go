// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LocalSourcePins digests each of files, read beside the entity in dir, into the
// pin the lock records for it, and returns the pins in path order.
//
// files is the set a local source contributes, as the entity's declared set names
// it: slash-separated paths relative to dir. This function reads what it is
// given and decides nothing about which files belong, so the set a publication
// carries for the source and the set the lock pins cannot come apart here; the
// one reading of that set is the declared set's. A file is read through any link
// in its path, as a publication reads it, so what is pinned is what would be
// published.
func LocalSourcePins(dir string, files []string) ([]types.LocalSourceDigest, error) {
	pins := make([]types.LocalSourceDigest, 0, len(files))
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return nil, fmt.Errorf("read the local source file %s: %w", f, err)
		}
		pins = append(pins, types.LocalSourceDigest{Path: f, Digest: digest.ContentDigest(data)})
	}
	sortLocalSourcePins(pins)
	return pins, nil
}

// MatchLocalSourcePin returns the recorded pin for the local source file at path,
// and whether one is present.
func MatchLocalSourcePin(pins []types.LocalSourceDigest, path string) (types.LocalSourceDigest, bool) {
	for _, p := range pins {
		if p.Path == path {
			return p, true
		}
	}
	return types.LocalSourceDigest{}, false
}

func sortLocalSourcePins(p []types.LocalSourceDigest) {
	sort.Slice(p, func(i, j int) bool { return p[i].Path < p[j].Path })
}

// localSourceDifference is one file on which a local source as it stands and the
// pins recorded for it disagree. current is empty for a file the lock records and
// the source no longer holds, and recorded is empty for a file the source holds
// and the lock records nothing for; both are set for a file whose content moved.
type localSourceDifference struct {
	path     string
	current  string
	recorded string
}

// localSourceDifferences is how a local source's recorded pins and its files as
// they stand now differ, and it is the one statement of that: a pull holding the
// lock to the pin step and a build refusing a drifted source both read it, so the
// files one reports and the files the other refuses are the same files.
//
// The pins of a source are read as one set, because the source is one input: a
// file added, a file whose content moved and a file removed each change what the
// entity renders from. The differences come in current's order — every file the
// source holds that the lock does not record at its digest — followed by every
// file the lock records that the source no longer holds, in recorded's order.
func localSourceDifferences(recorded, current []types.LocalSourceDigest) []localSourceDifference {
	var out []localSourceDifference
	for _, now := range current {
		pin, pinned := MatchLocalSourcePin(recorded, now.Path)
		if pinned && pin.Digest == now.Digest {
			continue
		}
		out = append(out, localSourceDifference{path: now.Path, current: now.Digest, recorded: pin.Digest})
	}
	for _, pin := range recorded {
		if _, held := MatchLocalSourcePin(current, pin.Path); !held {
			out = append(out, localSourceDifference{path: pin.Path, recorded: pin.Digest})
		}
	}
	return out
}

// ErrLocalSourceContentMismatch is the Loud Guardrail halt raised when the files
// of an entity's local source no longer match the pins recorded for them in the
// entity.lock beside it: a file whose content digests to something other than
// its pin, a file the source holds and the lock records no pin for, or a file the
// lock records that the source no longer holds.
//
// The pin step records one pin per file the local source contributes to the
// entity's declared set, and once the lock records any, a build compares the
// source as it stands with them before any provider reads it, so a chart or a
// module edited after it was pinned never renders as though it had been checked.
// A verifying pull reports the same disagreement under the same condition. A lock
// recording no local source pin at all leaves nothing to compare: that is an
// entity nobody pinned, which neither a build nor a pull refuses.
//
// The diagnostic is positioned at the entity document and names every file that
// differs, with the digest its content has now and the digest recorded for it;
// the remedy is to pin the entity again if the change was intended, or to
// restore the source. Category: CATALOG_ERROR. Match with
// errors.Is(err, ErrLocalSourceContentMismatch()).
func ErrLocalSourceContentMismatch() error { return errLocalSourceContentMismatch }

var errLocalSourceContentMismatch = cerrors.Register("local-source-content-mismatch", cerrors.Meta{
	ShortCode: "E0305",
	Message:   "the files of the entity's local source do not match the pins recorded for them in entity.lock",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// localSourceMismatchAdvice is what a local-source pin disagreement tells its
// reader to do. It offers the two remedies the imported-file disagreement offers,
// over a source rather than a file: the change was meant, so the pins are taken
// again, or it was not, so the source is restored.
const localSourceMismatchAdvice = "If the source was changed on purpose, run `aggrega pin` on the entity to record " +
	"its files again; otherwise restore the files"

// CheckLocalSourcePins compares the pins an entity's lock records for its local
// source with the pins the source's files digest to now, and returns the halt a
// disagreement raises. It returns nil where the two agree, and nil where the lock
// records no local source pin.
//
// It is the one home of that comparison and of the diagnostic it produces, and
// every command that makes the comparison calls it: a build, immediately before a
// provider reads the source, and a verify, over the locks beside a Release's own
// entities. documentPath is the entity document, which positions the diagnostic;
// current is what the pin step would record for the source now.
func CheckLocalSourcePins(documentPath string, recorded, current []types.LocalSourceDigest) error {
	if len(recorded) == 0 {
		return nil
	}
	diffs := localSourceDifferences(recorded, current)
	if len(diffs) == 0 {
		return nil
	}
	values := make([]string, 0, len(diffs))
	for _, d := range diffs {
		switch {
		case d.recorded == "":
			values = append(values, fmt.Sprintf("file %s digests to %s and no pin is recorded for it", d.path, d.current))
		case d.current == "":
			values = append(values, fmt.Sprintf("file %s is no longer in the source and its recorded pin is %s", d.path, d.recorded))
		default:
			values = append(values, fmt.Sprintf("file %s digests to %s and its recorded pin is %s", d.path, d.current, d.recorded))
		}
	}
	condition := ErrLocalSourceContentMismatch()
	return cerrors.Wrap(condition, cerrors.CatalogError,
		cerrors.SourceLocation{File: documentPath},
		cerrors.ComposeMessage(condition, values, localSourceMismatchAdvice))
}
