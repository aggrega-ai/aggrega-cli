// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"io"
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
// command from the requested colorMode, the output sink, and the process-global
// color-off decision (the global --no-color / --plain flags and the NO_COLOR
// environment variable), then carried in inspectOptions for every renderer to
// share.
type colorizer struct {
	enabled bool
}

// newColorizer resolves the color decision. noColorSet is the process-global
// color-off signal (the global --no-color / --plain flags or NO_COLOR, any value
// per the no-color.org convention); it disables color UNLESS an explicit
// --color=always was requested — an explicit operator request outranks the
// global default. Otherwise colorAlways enables, colorNever disables, and
// colorAuto enables only when the global signal is unset and the sink is a
// terminal that interprets escape sequences — the same decision the progress
// surface reads, so a console that cannot draw the checklist gets no color
// either.
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
		return &colorizer{enabled: outputTerminal(out) == drawingTerminal}
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
