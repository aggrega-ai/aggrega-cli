// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestProgress_NonTTY_PlainLines pins the piped/redirected shape: when the sink
// is not a terminal, each phase emits a plain one-line-per-phase log with no
// carriage returns and no spinner characters, so redirected output is clean.
func TestProgress_NonTTY_PlainLines(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return false }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{})
	p.phase(phaseLoadCatalog)
	p.phase(phaseRender)
	p.finish()

	got := buf.String()
	if strings.ContainsRune(got, '\r') {
		t.Errorf("non-TTY output must not contain carriage returns; got %q", got)
	}
	for _, spin := range []rune{'|', '/', '-', '\\'} {
		if strings.ContainsRune(got, spin) {
			t.Errorf("non-TTY output must not contain spinner chars; got %q", got)
		}
	}
	if !strings.Contains(got, "loading catalog...") || !strings.Contains(got, "rendering...") {
		t.Errorf("expected per-phase log lines; got %q", got)
	}
}

// TestProgress_TTY_SingleLine pins the terminal shape: a TTY sink redraws in
// place with carriage returns and clears cleanly on finish, so the line never
// garbles later output.
func TestProgress_TTY_SingleLine(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return true }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{})
	p.phase(phaseLoadCatalog)
	p.phase(phaseClosure)
	p.finish()

	got := buf.String()
	if !strings.Contains(got, "\r") {
		t.Errorf("TTY output must redraw in place with carriage returns; got %q", got)
	}
	if !strings.Contains(got, "loading catalog") || !strings.Contains(got, "computing closure") {
		t.Errorf("expected phase labels on the redrawn line; got %q", got)
	}
	// finish clears the line (writes a trailing clear sequence).
	if !strings.HasSuffix(got, "\r"+ansiClearLine) {
		t.Errorf("finish must clear the in-place line; got %q", got)
	}
	// The last visible content is a clear, so no phase label lingers after it.
	if strings.Count(got, ansiClearLine) < 3 {
		t.Errorf("expected each redraw plus the final clear to erase to EOL; got %q", got)
	}
}

// TestProgress_Quiet_Silent pins --quiet: no phase surface at all when debug is
// off, on either a TTY or a pipe.
func TestProgress_Quiet_Silent(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return true }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{quiet: true})
	p.phase(phaseLoadCatalog)
	p.note("closure entities: %d", 7)
	p.phase(phaseRender)
	p.finish()

	if buf.Len() != 0 {
		t.Errorf("--quiet must produce no progress output; got %q", buf.String())
	}
}

// TestProgress_DebugWinsOverQuiet pins the precedence: with both --debug and
// --quiet, debug wins — verbose diagnostics still emit, and they carry the
// per-phase timing and the cheap counts.
func TestProgress_DebugWinsOverQuiet(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	// A TTY sink to prove debug never redraws in place even on a terminal.
	fileTerminal = func(io.Writer) bool { return true }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{quiet: true, debug: true})
	p.phase(phaseLoadCatalog)
	p.note("closure entities: %d", 7)
	p.phase(phaseRender)
	p.finish()

	got := buf.String()
	if strings.ContainsRune(got, '\r') {
		t.Errorf("debug must not redraw in place; got %q", got)
	}
	if !strings.Contains(got, "[debug]") {
		t.Errorf("debug must emit its verbose layer even with --quiet; got %q", got)
	}
	if !strings.Contains(got, "closure entities: 7") {
		t.Errorf("debug note must surface the cheap count; got %q", got)
	}
	if !strings.Contains(got, "start") || !strings.Contains(got, "done in") {
		t.Errorf("debug must report per-phase timing; got %q", got)
	}
	if !strings.Contains(got, "build total") {
		t.Errorf("debug finish must print the whole-build total; got %q", got)
	}
}

// TestProgress_DebugOnly_NonTTY confirms the debug layer emits identically when
// the sink is not a terminal — debug is sink-shape-independent.
func TestProgress_DebugOnly_NonTTY(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return false }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{debug: true})
	p.phase(phaseLoadCatalog)
	p.finish()

	got := buf.String()
	if !strings.Contains(got, "[debug]") || !strings.Contains(got, "loading catalog") {
		t.Errorf("debug (non-TTY) must emit the verbose layer; got %q", got)
	}
}
