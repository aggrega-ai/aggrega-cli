// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"
	"strings"
)

// CommandMirrorsEqual reports whether a recorded command mirror equals a
// plugin's described commands under full equality: same length, and each entry
// equal by name and short-help description at the same position. The comparison
// is order-sensitive — the mirror is recorded in describe()-declared order, so a
// reordering is a divergence — and it is the single home of the mirror-equality
// rule both the publish verify and the load gate consult. A nil slice and an
// empty slice compare equal, so a non-CLI plugin that describes no commands is in
// sync with an entity that records an explicit empty list or omits the field.
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
	}
	return true
}

// FormatCommandMirror renders a command mirror as a stable, human-readable list
// of name="short help" pairs for a diagnostic message, so a divergence in either
// the command name or its short help is visible in the rejection. The order is
// the slice's own order (describe()-declared for a described set), so the same
// mirror renders identically across runs.
func FormatCommandMirror(cmds []PluginCLICommand) string {
	if len(cmds) == 0 {
		return "[]"
	}
	parts := make([]string, len(cmds))
	for i, c := range cmds {
		parts[i] = fmt.Sprintf("%s=%q", c.Name, c.Description)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
