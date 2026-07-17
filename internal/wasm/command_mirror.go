// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"
	"strings"
)

// CommandMirrorsEqual reports whether a recorded command mirror equals a
// plugin's described commands under full equality: same length, and each entry
// equal by name, short-help description, and declared flag list at the same
// position. The comparison is order-sensitive — the mirror is recorded in
// describe()-declared order, so a reordering of commands or of a command's flags
// is a divergence — and it is the single home of the mirror-equality rule both
// the publish verify and the load gate consult. A nil slice and an empty slice
// compare equal, so a non-CLI plugin that describes no commands is in sync with
// an entity that records an explicit empty list or omits the field.
//
// Unlike the authored export and capability claims — curated subsets an author
// may legitimately under-declare — the command mirror is a machine record that
// must reflect the binary exactly, so this is full equality rather than a subset.
func CommandMirrorsEqual(a, b []PluginCLICommand) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Description != b[i].Description {
			return false
		}
		if !commandFlagsEqual(a[i].Flags, b[i].Flags) {
			return false
		}
	}
	return true
}

// commandFlagsEqual reports whether two command flag lists are equal under
// order-sensitive full equality: same length, and each flag equal by every
// field at the same position. A nil slice and an empty slice compare equal, so a
// command that declares no flags is in sync whether the field is omitted or an
// explicit empty list.
func commandFlagsEqual(a, b []PluginCommandFlag) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FormatCommandMirror renders a command mirror as a stable, human-readable list
// of name="short help" pairs, each followed by its flags, for a diagnostic
// message, so a divergence in the command name, its short help, or any flag is
// visible in the rejection. The order is the slice's own order (describe()-
// declared for a described set), so the same mirror renders identically across
// runs.
func FormatCommandMirror(cmds []PluginCLICommand) string {
	if len(cmds) == 0 {
		return "[]"
	}
	parts := make([]string, len(cmds))
	for i, c := range cmds {
		parts[i] = fmt.Sprintf("%s=%q%s", c.Name, c.Description, formatCommandFlags(c.Flags))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// formatCommandFlags renders a command's flags as a stable, human-readable
// suffix so a flag divergence is visible in a command-mirror diagnostic. A
// command with no flags renders no suffix. Each flag renders its long name and
// the fields that carry a value, in field-declaration order.
func formatCommandFlags(flags []PluginCommandFlag) string {
	if len(flags) == 0 {
		return ""
	}
	parts := make([]string, len(flags))
	for i, f := range flags {
		parts[i] = fmt.Sprintf("--%s(shorthand=%q,description=%q,type=%q,default=%q)",
			f.Name, f.Shorthand, f.Description, f.Type, f.Default)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
