// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// displayState is the process-global accessibility decision, resolved once from
// the global --no-color / --plain flags and the NO_COLOR environment variable in
// the root command's PersistentPreRunE, then read by every command's color and
// progress surfaces so a single decision governs the whole invocation.
//
// colorOff is the single-sourced "suppress ANSI color" signal: it is true when
// any of --no-color, --plain, or NO_COLOR is set. It is the only place the
// NO_COLOR environment variable is read, so color-off cannot drift between the
// inspect colorizer and the build-progress surface.
//
// plain is the accessible mode: it additionally forces the progress surface to
// plain static lines with no spinner animation and no cursor repaint, even on an
// interactive terminal, so screen readers receive stable line-oriented output.
type displayState struct {
	colorOff bool
	plain    bool
}

// globalDisplay holds the resolved process-global display decision. It is set
// once in the root PersistentPreRunE before any command's RunE runs, and read
// (never written) thereafter.
var globalDisplay displayState

// resolveGlobalDisplay computes the process-global display decision from the two
// global flags and the environment lookup. Color is off when any of --no-color,
// --plain, or the NO_COLOR environment variable (set to any value, per the
// no-color.org convention) is present. Plain (accessible) mode is on when
// --plain is set.
//
// lookupEnv is os.LookupEnv in production; tests pass a stub so the decision is
// exercised without mutating the process environment.
func resolveGlobalDisplay(noColorFlag, plainFlag bool, lookupEnv func(string) (string, bool)) displayState {
	_, noColorEnv := lookupEnv("NO_COLOR")
	return displayState{
		colorOff: noColorFlag || plainFlag || noColorEnv,
		plain:    plainFlag,
	}
}
