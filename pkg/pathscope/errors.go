// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrNameWindowsCannotHold is the halt [PlaceBeneath] raises, on Windows only,
// for a name holding a path segment Windows cannot hold as written: a name
// Windows reserves for a device — CON, PRN, AUX, NUL, COM1 and the rest, in any
// letter case and whether or not an extension follows — or a segment other than
// "." and ".." ending in a full stop or a space. Windows maps such a segment to a
// device or does not keep it as written, so writing the name would put its bytes
// somewhere other than the file it names.
//
// The name is not written. The refusal names it, the segment Windows cannot hold
// and the directory it was to be placed beneath, so a reader knows which name to
// change; it is raised before the name is placed, and every writer asking
// [PlaceBeneath] asks it for a whole set before writing any of it.
//
// Category: none. The name comes from whatever produced it — an archive's
// publisher, an importer, a template, the path an edit names — rather than from
// one authoring surface the compiler files faults against. Match with
// errors.Is(err, ErrNameWindowsCannotHold()).
func ErrNameWindowsCannotHold() error { return errNameWindowsCannotHold }

var errNameWindowsCannotHold = cerrors.Register("name-windows-cannot-hold", cerrors.Meta{
	ShortCode: "E0340",
	Message:   "a name this operation writes holds a path segment Windows cannot hold as written",
	RulePlace: cerrors.RuleNoStageReaches("The rule is evaluated at the moment the product places a name it did " +
		"not choose beneath a directory on a Windows machine, before writing it — laying out an archive in the " +
		"artifact cache, writing the drafts an import returned, writing a build's rendered artifacts, writing an " +
		"edited document back. None of those moments is a declared stage of a compilation, and what the rule " +
		"reads is the name as the writer is about to place it."),
})

// windowsCannotHoldAdvice is what the refusal tells its reader, in terms of what
// they can change.
const windowsCannotHoldAdvice = "It was not written. Windows reserves device names such as CON, PRN, AUX, NUL, " +
	"COM1 and LPT1, in any letter case and whatever extension follows, and does not hold a segment ending in a " +
	"full stop or a space as written; rename that segment where the name is produced"

// refuseWindowsName composes the halt for name, whose segment Windows cannot hold,
// beneath the directory dir.
func refuseWindowsName(name, segment, dir string) error {
	values := []string{
		fmt.Sprintf("the name %q", name),
		fmt.Sprintf("its segment %q", segment),
		"beneath " + dir,
	}
	return cerrors.Raise(errNameWindowsCannotHold, "", cerrors.SourceLocation{},
		cerrors.ComposeMessage(ErrNameWindowsCannotHold(), values, windowsCannotHoldAdvice), nil)
}
