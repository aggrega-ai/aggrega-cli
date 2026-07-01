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

// TestProgress_RenderNode_TTY_Counter pins the terminal render-progress shape:
// each completed node redraws the render line in place with an X/Y counter, and
// finish clears it so nothing lingers.
func TestProgress_RenderNode_TTY_Counter(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return true }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{})
	p.phase(phaseRender)
	total := 3
	for i := 1; i <= total; i++ {
		p.renderNode(i, total, "node-"+string(rune('a'+i-1)))
	}
	p.finish()

	got := buf.String()
	if !strings.Contains(got, "\r") {
		t.Errorf("TTY render progress must redraw in place; got %q", got)
	}
	for _, want := range []string{"rendering 1/3", "rendering 2/3", "rendering 3/3"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected in-place counter %q; got %q", want, got)
		}
	}
	if !strings.HasSuffix(got, "\r"+ansiClearLine) {
		t.Errorf("finish must clear the in-place counter line; got %q", got)
	}
}

// TestProgress_RenderNode_NonTTY_Throttled pins the piped shape: a large graph
// emits periodic lines (roughly one per tenth of the total) plus a guaranteed
// final count — never one line per node — and no carriage returns.
func TestProgress_RenderNode_NonTTY_Throttled(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return false }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{})
	total := 50
	for i := 1; i <= total; i++ {
		p.renderNode(i, total, "node")
	}
	p.finish()

	got := buf.String()
	if strings.ContainsRune(got, '\r') {
		t.Errorf("non-TTY render progress must not contain carriage returns; got %q", got)
	}
	lines := 0
	for _, ln := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if strings.Contains(ln, "rendering ") {
			lines++
		}
	}
	// Throttled to ~one line per tenth (5 for 50 nodes) — far fewer than 50, and
	// more than one so progress is visible.
	if lines < 2 || lines > 12 {
		t.Errorf("expected a throttled handful of interim lines, got %d; output %q", lines, got)
	}
	if !strings.Contains(got, "rendering 50/50...") {
		t.Errorf("expected a guaranteed final count line; got %q", got)
	}
}

// TestProgress_RenderNode_SmallGraph_FinalCount confirms a graph smaller than
// the throttle stride still logs its final count on a plain sink.
func TestProgress_RenderNode_SmallGraph_FinalCount(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return false }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{})
	total := 3
	for i := 1; i <= total; i++ {
		p.renderNode(i, total, "node")
	}
	p.finish()

	if !strings.Contains(buf.String(), "rendering 3/3...") {
		t.Errorf("small graph must still log its final count; got %q", buf.String())
	}
}

// TestProgress_RenderNode_Quiet_Silent confirms --quiet suppresses the per-node
// render surface entirely.
func TestProgress_RenderNode_Quiet_Silent(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return true }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{quiet: true})
	for i := 1; i <= 5; i++ {
		p.renderNode(i, 5, "node")
	}
	p.finish()

	if buf.Len() != 0 {
		t.Errorf("--quiet must produce no render progress; got %q", buf.String())
	}
}

// TestProgress_RenderNode_Debug_NamesNode confirms the debug layer emits a
// throttled per-node line that names the node just finished.
func TestProgress_RenderNode_Debug_NamesNode(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return true }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{debug: true})
	p.renderNode(1, 1, "svc-web")

	got := buf.String()
	if strings.ContainsRune(got, '\r') {
		t.Errorf("debug render progress must not redraw in place; got %q", got)
	}
	if !strings.Contains(got, "[debug]") || !strings.Contains(got, "rendering 1/1") || !strings.Contains(got, "svc-web") {
		t.Errorf("debug render progress must name the node with a count; got %q", got)
	}
}

// TestProgress_RenderNode_ZeroTotal_NoOp confirms a zero total (no nodes) draws
// no render counter at all.
func TestProgress_RenderNode_ZeroTotal_NoOp(t *testing.T) {
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return false }

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{})
	p.renderNode(0, 0, "")

	if buf.Len() != 0 {
		t.Errorf("zero total must produce no render counter; got %q", buf.String())
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
