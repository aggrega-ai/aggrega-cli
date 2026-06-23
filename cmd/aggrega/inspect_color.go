// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"io"
	"os"

	"golang.org/x/term"
)

// ANSI SGR sequences. The palette is deliberately tiny — color marks structure
// (headers, provenance brackets, URIs, error text), never body text or tabular
// values, so a hand-rolled set keeps output deterministic and adds no
// dependency.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
)

// colorizer wraps display strings in ANSI sequences when color is enabled, and
// returns them verbatim when disabled. A single colorizer is resolved once per
// command from the requested colorMode, the output sink, and the NO_COLOR
// environment variable, then carried in inspectOptions for every renderer to
// share.
type colorizer struct {
	enabled bool
}

// fileTerminal reports whether w is an interactive terminal. It is a
// package-level seam so the auto-detect branch is unit-testable without a real
// TTY; tests replace it. A writer that is not an *os.File (a *bytes.Buffer in
// tests, a pipe in production) is never a terminal.
var fileTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// newColorizer resolves the color decision. NO_COLOR (any value, per the
// no-color.org convention) disables color UNLESS an explicit --color=always was
// requested — an explicit operator request outranks the environment default.
// Otherwise colorAlways enables, colorNever disables, and colorAuto enables
// only when the sink is an interactive terminal.
//
// look reads the environment; pass os.Getenv in production. The presence test
// uses LookupEnv-style semantics threaded through look returning "" for unset —
// callers supply a lookup that distinguishes set-but-empty from unset.
func newColorizer(mode colorMode, out io.Writer, noColorSet bool) *colorizer {
	switch mode {
	case colorAlways:
		return &colorizer{enabled: true}
	case colorNever:
		return &colorizer{enabled: false}
	default: // colorAuto
		if noColorSet {
			return &colorizer{enabled: false}
		}
		return &colorizer{enabled: fileTerminal(out)}
	}
}

// wrap returns s bracketed by the given SGR code and a reset when color is
// enabled, and s verbatim otherwise.
func (c *colorizer) wrap(code, s string) string {
	if c == nil || !c.enabled {
		return s
	}
	return code + s + ansiReset
}

// header styles a section header (bold).
func (c *colorizer) header(s string) string { return c.wrap(ansiBold, s) }

// uri styles a URI / identifier (cyan).
func (c *colorizer) uri(s string) string { return c.wrap(ansiCyan, s) }

// bracket styles a provenance bracket (yellow).
func (c *colorizer) bracket(s string) string { return c.wrap(ansiYellow, s) }

// errText styles error / warning text (red).
func (c *colorizer) errText(s string) string { return c.wrap(ansiRed, s) }
