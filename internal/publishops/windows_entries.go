// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"fmt"
	"io"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// ErrWindowsUnusableEntry is the advisory a publication reports, on every
// platform, for each entry of the archive it publishes that holds a path segment
// Windows cannot hold as written: a name Windows reserves for a device — CON, PRN,
// AUX, NUL, COM1 and the rest, in any letter case and whether or not an extension
// follows — or a segment other than "." and ".." ending in a full stop or a space.
// The layout that unpacks a published archive refuses such an entry where it runs
// on Windows, so a Windows workspace cannot use the version.
//
// It ADVISES and never refuses: the publication goes ahead, or stops, exactly as it
// would without it. The entity document's own file name is judged with the files it
// carries, because the archive holds it under that name; an entity carrying nothing
// publishes as its document alone, laid out under a name the product chooses, and is
// never reported.
//
// No error value carries it. A publication reports it as one line per entry on its
// advisory output, each opening with this condition's sentence, and as the entry
// names its result carries; the accessor is how the sentence and the two codes are
// read, through cerrors.MessageOf, cerrors.CodeOf and cerrors.ShortCodeOf.
func ErrWindowsUnusableEntry() error { return errWindowsUnusableEntry }

var errWindowsUnusableEntry = cerrors.Register("windows-unusable-entry", cerrors.Meta{
	ShortCode: "E0344",
	Message:   "an entry this publication carries cannot be used on Windows",
	RulePlace: cerrors.RuleNoStageReaches("The rule is evaluated when a publication composes the names of the " +
		"entries its archive will carry — the entity document's own file name and each file its declarations " +
		"name — before the archive is built, a dry run included. A publication is not a compilation, and what " +
		"the rule reads is those names as the archive will hold them."),
})

// windowsUnusableAdvice is what the advisory tells its reader, in terms of what
// they can change. The reason is the predicate's own wording. It says the warning
// stops nothing rather than that the publication goes ahead, because on Windows
// the bundle still refuses a carried path naming a device after this is written.
const windowsUnusableAdvice = pathsegment.WindowsCannotHoldReason + ", so a workspace on Windows refuses to " +
	"unpack the entry. This warning does not stop the publication; rename that segment to publish a version " +
	"Windows can use"

// windowsUnusableEntry is one entry name Windows cannot hold as written, and the
// first segment of it that decides so.
type windowsUnusableEntry struct {
	name    string
	segment string
}

// windowsUnusableEntries returns every name in names holding a segment Windows
// cannot hold as written, in the order given, each with the segment that decides
// it. names are an archive's entry names, slash-separated, as
// oci.ArchiveEntryNames composes them; nil yields nil.
//
// The judgement is pathsegment.WindowsCannotHold, the one statement of what Windows
// cannot hold, which the layout unpacking the archive on Windows also reads — so the
// entries reported here are the ones that layout refuses there.
func windowsUnusableEntries(names []string) []windowsUnusableEntry {
	var out []windowsUnusableEntry
	for _, name := range names {
		if segment, held := pathsegment.WindowsCannotHold(name); held {
			out = append(out, windowsUnusableEntry{name: name, segment: segment})
		}
	}
	return out
}

// namesOf returns the names of entries, in order, and nil where there are none,
// which is the shape a result carries them in: absent rather than empty.
func namesOf(entries []windowsUnusableEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	return names
}

// warnWindowsUnusable writes the advisory for each of entries to w, one line each,
// in order. Each line opens with the condition's registered sentence and names the
// entry and the segment that decides it.
func warnWindowsUnusable(w io.Writer, entries []windowsUnusableEntry) {
	for _, e := range entries {
		values := []string{fmt.Sprintf("the entry %q", e.name), fmt.Sprintf("its segment %q", e.segment)}
		fmt.Fprintf(w, "warning: %s\n", cerrors.ComposeMessage(errWindowsUnusableEntry, values, windowsUnusableAdvice))
	}
}
