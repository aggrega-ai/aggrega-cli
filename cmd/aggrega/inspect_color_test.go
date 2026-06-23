// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// hasANSI reports whether s carries any ANSI SGR escape.
func hasANSI(s string) bool { return strings.Contains(s, "\x1b[") }

func TestColorizer_Never_Verbatim(t *testing.T) {
	t.Parallel()
	c := newColorizer(colorNever, io.Discard, false)
	if got := c.header("X"); got != "X" {
		t.Errorf("never: header should be verbatim; got %q", got)
	}
	if hasANSI(c.uri("Y")) {
		t.Errorf("never: uri should carry no ANSI")
	}
}

func TestColorizer_Always_Wraps(t *testing.T) {
	t.Parallel()
	c := newColorizer(colorAlways, io.Discard, false)
	if !hasANSI(c.header("X")) {
		t.Errorf("always: header should carry ANSI")
	}
	if !hasANSI(c.bracket("[explicit]")) {
		t.Errorf("always: bracket should carry ANSI")
	}
}

// TestColorizer_AlwaysBeatsNoColor pins the precedence ruling: an explicit
// --color=always outranks NO_COLOR (an explicit operator request beats the
// environment default).
func TestColorizer_AlwaysBeatsNoColor(t *testing.T) {
	t.Parallel()
	c := newColorizer(colorAlways, io.Discard, true /* NO_COLOR set */)
	if !c.enabled {
		t.Errorf("--color=always must beat NO_COLOR")
	}
}

// TestColorizer_AutoOffWithNoColor pins NO_COLOR overriding the auto default.
func TestColorizer_AutoOffWithNoColor(t *testing.T) {
	t.Parallel()
	c := newColorizer(colorAuto, io.Discard, true /* NO_COLOR set */)
	if c.enabled {
		t.Errorf("auto + NO_COLOR must disable color")
	}
}

// TestColorizer_AutoOnTTY exercises the auto-detect seam: when the sink reports
// as a terminal, auto enables; when it does not, auto disables. The fileTerminal
// seam is replaced for the duration so no real TTY is needed.
func TestColorizer_AutoOnTTY(t *testing.T) {
	// Not parallel: mutates the package-level fileTerminal seam.
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })

	fileTerminal = func(io.Writer) bool { return true }
	if c := newColorizer(colorAuto, io.Discard, false); !c.enabled {
		t.Errorf("auto + TTY should enable color")
	}

	fileTerminal = func(io.Writer) bool { return false }
	if c := newColorizer(colorAuto, io.Discard, false); c.enabled {
		t.Errorf("auto + non-TTY should disable color")
	}
}

// TestColorizer_AutoOffPipe pins that the default auto-detect against a plain
// buffer (a *bytes.Buffer is never an *os.File terminal) yields no color.
func TestColorizer_AutoOffPipe(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	c := newColorizer(colorAuto, &buf, false)
	if c.enabled {
		t.Errorf("auto over a non-TTY buffer should disable color")
	}
}

// TestColorizer_NilSafe pins that a nil colorizer (uninitialized options) is
// treated as disabled rather than panicking.
func TestColorizer_NilSafe(t *testing.T) {
	t.Parallel()
	var c *colorizer
	if got := c.header("X"); got != "X" {
		t.Errorf("nil colorizer should pass through verbatim; got %q", got)
	}
}
