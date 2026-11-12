// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import "strings"

// Verbosity tokens select how much of a validation result a front end renders.
// They govern output volume only — never what is validated: every verbosity runs
// the identical checks and reaches the identical verdict and exit code. The
// tokens live here, in the shared compiler-operations layer, so every front end
// (the CLI verb and the MCP tool) reads one canonical home for the literal
// values rather than each spelling them locally.
const (
	// VerbosityDetailed renders every diagnostic plus the exit-code-neutral
	// advisory lines. It is the default: a reader benefits from the fuller
	// result, and the terse form is an explicit opt-in.
	VerbosityDetailed = "detailed"
	// VerbosityConcise renders the verdict, the total diagnostic count, and the
	// first diagnostic only, with no advisory lines.
	VerbosityConcise = "concise"
)

// ParseVerbosity maps a caller-supplied verbosity value to one of the two
// supported tokens, defaulting to the detailed form for any absent or
// unrecognized value. It trims surrounding whitespace and folds case.
func ParseVerbosity(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), VerbosityConcise) {
		return VerbosityConcise
	}
	return VerbosityDetailed
}
