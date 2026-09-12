// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
)

// ttyReporter builds a checklist reporter over a forced-terminal sink.
func withTTY(t *testing.T, tty bool) {
	t.Helper()
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return tty }
}

// TestProgress_Auto_NonTTY_Silent pins the default (auto) shape: with no
// --progress and a non-terminal sink, the surface is silent — progress shows
// only on a TTY.
func TestProgress_Auto_NonTTY_Silent(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.phase(phaseRender)
	p.renderNode(1, 1, "n")
	p.finish()

	if buf.Len() != 0 {
		t.Errorf("auto mode on a non-TTY must be silent; got %q", buf.String())
	}
}

// TestProgress_ForcedStatic_NonTTY pins the forced shape: --progress on a
// non-terminal sink prints plain static phase lines — no carriage returns, no
// spinner characters, no cursor movement.
func TestProgress_ForcedStatic_NonTTY(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{progress: true}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.phase(phaseClosure)
	p.finish()

	got := buf.String()
	if strings.ContainsRune(got, '\r') {
		t.Errorf("forced static output must not contain carriage returns; got %q", got)
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("forced static output must not contain ANSI escapes; got %q", got)
	}
	for _, spin := range spinnerFrames {
		if strings.ContainsRune(got, spin) {
			t.Errorf("forced static output must not contain spinner chars; got %q", got)
		}
	}
	if !strings.Contains(got, "loading catalog...") || !strings.Contains(got, "resolving dependencies...") {
		t.Errorf("expected plain per-phase lines; got %q", got)
	}
}

// TestProgress_TTY_Checklist pins the interactive shape: a TTY sink redraws a
// multi-line checklist in place, ticking completed phases with a done mark and
// spinning the active one.
func TestProgress_TTY_Checklist(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.phase(phaseLockfile)
	p.phase(phaseClosure)
	p.phase(phaseValidate)
	p.phase(phaseInheritance)
	p.phase(phaseRender)
	p.renderNode(1, 1, "svc")
	p.finish()

	got := buf.String()
	if !strings.Contains(got, "\r") {
		t.Errorf("TTY checklist must redraw in place with carriage returns; got %q", got)
	}
	if !strings.Contains(got, markDone) {
		t.Errorf("TTY checklist must tick completed phases with a done mark; got %q", got)
	}
	for _, label := range []string{"loading catalog", "resolving dependencies", "rendering"} {
		if !strings.Contains(got, label) {
			t.Errorf("expected phase label %q in the checklist; got %q", label, got)
		}
	}
	// After finish every listed phase carries a done mark; no pending marker
	// lingers on the final redraw.
	lastBlock := got[strings.LastIndex(got, "loading catalog"):]
	if strings.Contains(lastBlock, markPending) {
		t.Errorf("finished checklist must carry no pending markers; got final block %q", lastBlock)
	}
}

// TestProgress_Quiet_Silent pins --quiet: no surface at all, on a TTY or a pipe.
func TestProgress_Quiet_Silent(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{quiet: true}, buildProgressSpine(true))
	p.phase(phaseLoadCatalog)
	p.pluginCompileBegin(1)
	p.pluginCompileGuestDone(1)
	p.renderNode(1, 1, "n")
	p.finish()

	if buf.Len() != 0 {
		t.Errorf("--quiet must produce no progress output; got %q", buf.String())
	}
}

// TestProgress_QuietBeatsProgress pins the precedence: when both --quiet and
// --progress are set, quiet wins and the surface stays silent.
func TestProgress_QuietBeatsProgress(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{quiet: true, progress: true}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.finish()

	if buf.Len() != 0 {
		t.Errorf("--quiet must take precedence over --progress; got %q", buf.String())
	}
}

// TestProgress_DebugWinsOverQuiet pins the top of the precedence ladder: with
// both --debug and --quiet, debug wins — verbose diagnostics still emit with the
// per-phase timing and the cheap counts.
func TestProgress_DebugWinsOverQuiet(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	// A TTY sink to prove debug never redraws in place even on a terminal.
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{quiet: true, debug: true}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.note("closure entities: %d", 7)
	p.phase(phaseClosure)
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
	if !strings.Contains(got, "run total") {
		t.Errorf("debug finish must print the whole-run total; got %q", got)
	}
}

// TestProgress_RealCompile_ShowsCompileStep pins the reveal path: when the eager
// pass observes a guest actually compiling, the "Plugin compilation" step joins
// the checklist between the pre-render phases and the render phase.
func TestProgress_RealCompile_ShowsCompileStep(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(true))
	p.phase(phaseLoadCatalog)
	p.phase(phaseInheritance)
	p.phase(phaseRender)
	p.pluginCompileBegin(1)
	p.pluginCompileRunning() // a guest is observed compiling
	p.pluginCompileGuestDone(1)
	p.renderNode(1, 1, "svc")
	p.finish()

	if !strings.Contains(buf.String(), compilePhaseLabel) {
		t.Errorf("an observed compile must show the %q step; got %q", compilePhaseLabel, buf.String())
	}
}

// TestProgress_WarmProfileRealCompile_ShowsCompileStep pins the stale-cache case:
// the up-front guess is warm (bar profile only), but a guest is observed to
// actually compile this build, so the step must still appear. This is the case a
// directory-presence gate got wrong — a populated cache directory that lacked the
// exact guest this build needed.
func TestProgress_WarmProfileRealCompile_ShowsCompileStep(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseRender)
	p.pluginCompileBegin(1)
	p.pluginCompileRunning() // a guest is observed compiling despite the warm guess
	p.pluginCompileGuestDone(1)
	p.renderNode(1, 1, "svc")
	p.finish()

	if !strings.Contains(buf.String(), compilePhaseLabel) {
		t.Errorf("an observed compile must show the %q step even under the warm bar profile; got %q", compilePhaseLabel, buf.String())
	}
}

// TestProgress_NoCompileObserved_OmitsCompileStep pins the genuinely-warm case:
// the eager pass reports guests (a cache-hit warm still fires begin/done) but no
// compile is observed, so the step never appears.
func TestProgress_NoCompileObserved_OmitsCompileStep(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseRender)
	p.pluginCompileBegin(1) // reported, but no compile observed
	p.pluginCompileGuestDone(1)
	p.renderNode(1, 1, "svc")
	p.finish()

	if strings.Contains(buf.String(), compilePhaseLabel) {
		t.Errorf("a cache-hit build must omit the %q step; got %q", compilePhaseLabel, buf.String())
	}
}

// TestProgress_RenderNode_TTY_Counter pins the layout-A render row on a checklist
// sink: the render row carries the done/total counter (no inline bar), while the
// bar and whole-number percent live on the global progress line — redrawn in
// place at each node.
func TestProgress_RenderNode_TTY_Counter(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseRender)
	total := 3
	for i := 1; i <= total; i++ {
		p.renderNode(i, total, "node")
	}
	p.finish()

	got := buf.String()
	if !strings.Contains(got, "\r") {
		t.Errorf("TTY render progress must redraw in place; got %q", got)
	}
	// The render row carries the plain done/total counter for each step.
	for _, want := range []string{"rendering 1/3", "rendering 2/3", "rendering 3/3"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected render-row counter %q; got %q", want, got)
		}
	}
	// The global progress line carries the caption, the bar, and the profile-
	// weighted whole-build percent for each step. Warm profile: pre-render (0.24)
	// plus the compile share (0.30) credited when render begins = 0.54, then
	// render (share 0.40) carries the bar to 67% / 80% / 94% across the three
	// nodes, and finish snaps it to 100%.
	for _, want := range []string{"Progress ", "67%", "80%", "94%", "100%"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected progress-line detail %q; got %q", want, got)
		}
	}
	if !strings.ContainsRune(got, '█') || !strings.ContainsRune(got, '░') {
		t.Errorf("expected a filled/empty percentage bar on the progress line; got %q", got)
	}
}

// findRow returns the first composed row whose visible text contains sub, or ""
// if none matches. Test helper for locating a row by content now that the render
// row is no longer positionally last (the global progress line and finish block
// follow it).
func findRow(rows []string, sub string) string {
	for _, r := range rows {
		if strings.Contains(r, sub) {
			return r
		}
	}
	return ""
}

// TestProgress_LayoutA_RenderRowAndProgressLine asserts the layout-A split: the
// render row carries the done/total counter and a dim, right-aligned elapsed
// field but NO inline bar or percentage; the separate global progress line begins
// with the "Progress " caption and carries the filled/empty bar plus the NN%
// percentage but NO elapsed.
func TestProgress_LayoutA_RenderRowAndProgressLine(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseRender)
	p.renderNode(23, 37, "svc")

	rows := p.rows()

	// The render row: counter, dim elapsed, and no bar/percent.
	renderRow := findRow(rows, "rendering ")
	if renderRow == "" {
		t.Fatalf("no render row found; rows=%q", rows)
	}
	if strings.ContainsRune(renderRow, '█') || strings.ContainsRune(renderRow, '░') {
		t.Errorf("layout-A render row must NOT contain an inline bar; got %q", renderRow)
	}
	if strings.Contains(renderRow, "%") {
		t.Errorf("layout-A render row must NOT contain a percentage; got %q", renderRow)
	}
	if !strings.Contains(renderRow, "23/37") {
		t.Errorf("render row must contain the done/total counter; got %q", renderRow)
	}
	dimIdx := strings.Index(renderRow, ansiDim)
	if dimIdx < 0 {
		t.Fatalf("render row must render the elapsed field dim; got %q", renderRow)
	}
	field := renderRow[dimIdx:]
	if !strings.HasSuffix(field, ansiReset) {
		t.Errorf("dim elapsed field must be closed with a reset; got %q", field)
	}
	if !strings.Contains(field, "s") {
		t.Errorf("dim elapsed field must carry a seconds value; got %q", field)
	}

	// The global progress line: caption + bar + percent, no elapsed.
	progRow := findRow(rows, progressCaption)
	if progRow == "" {
		t.Fatalf("no global progress line found; rows=%q", rows)
	}
	if !strings.HasPrefix(progRow, progressCaption) {
		t.Errorf("global progress line must begin with %q; got %q", progressCaption, progRow)
	}
	if !strings.ContainsRune(progRow, '█') || !strings.ContainsRune(progRow, '░') {
		t.Errorf("global progress line must contain the filled/empty bar; got %q", progRow)
	}
	// Warm profile: pre-render (0.24) plus the compile share (0.30) credited when
	// render begins = 0.54, plus render share 0.40 × 23/37 → 0.789 → floored to
	// 78%.
	if !strings.Contains(progRow, "78%") {
		t.Errorf("global progress line must contain the percentage; got %q", progRow)
	}
	if strings.Contains(progRow, ansiDim) {
		t.Errorf("global progress line must carry no dim elapsed field; got %q", progRow)
	}
}

// TestProgress_LayoutA_ElapsedColumnAligned asserts every elapsed field — across
// the phase rows and the render row — right-aligns to a single shared column. The
// completion summary is no longer part of this diagnostic block; it lands on
// stdout, so no total-build-time row appears here.
func TestProgress_LayoutA_ElapsedColumnAligned(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)

	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour // keep the async ticker from redrawing mid-test
	t.Cleanup(func() { spinnerTickInterval = orig })

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.phase(phaseLockfile)
	p.phase(phaseRender)
	p.renderNode(23, 37, "svc")
	p.finished = true

	rows := p.rows()

	// Every row that carries a dim elapsed field starts that field at the same
	// visible column. This spans the started phase rows and the render row.
	offset := -1
	fieldsSeen := 0
	for _, r := range rows {
		i := strings.Index(r, ansiDim)
		if i < 0 {
			continue // pending rows and the progress line show none
		}
		fieldsSeen++
		off := visibleRuneWidth(r[:i])
		if offset < 0 {
			offset = off
			continue
		}
		if off != offset {
			t.Errorf("elapsed column misaligned: expected start %d, got %d (%q)", offset, off, r)
		}
	}
	if fieldsSeen < 3 {
		t.Errorf("expected at least 3 elapsed fields (loading catalog + resolving lockfile + rendering); saw %d", fieldsSeen)
	}
}

// visibleRuneWidth counts display columns in s, skipping ANSI SGR escape
// sequences (which carry no width). Test helper for the alignment assertion.
func visibleRuneWidth(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		n++
		i += size
	}
	return n
}

// TestProgress_UncountedPhase_NoBar confirms a phase with no node count (a
// pre-render phase, and the compiling-plugins step) keeps the spinner and shows
// only its elapsed — it never fabricates a percentage bar.
func TestProgress_UncountedPhase_NoBar(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(true))
	p.phase(phaseLoadCatalog)
	p.phase(phaseRender)
	p.pluginCompileBegin(1)
	p.pluginCompileRunning() // a compile is observed: the step is active

	for _, row := range p.rows() {
		if strings.HasPrefix(row, progressCaption) {
			// The global progress line legitimately shows the empty 0% bar before any
			// node completes; it is not a phase row.
			continue
		}
		if strings.ContainsRune(row, '█') || strings.ContainsRune(row, '░') || strings.Contains(row, "%") {
			// No phase row may carry a bar or percent — that detail lives only on the
			// global progress line.
			t.Errorf("uncounted phase row must not fabricate a bar/percent; got row %q", row)
		}
	}
}

// TestProgress_ForcedStatic_RenderNoEscapesNoBar confirms the forced non-TTY
// static path emits plain lines for the render phase too — no ANSI escapes, no
// bar cells, no dim elapsed — so redirected logs stay clean.
func TestProgress_ForcedStatic_RenderNoEscapesNoBar(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{progress: true}, buildProgressSpine(false))
	p.phase(phaseRender)
	total := 20
	for i := 1; i <= total; i++ {
		p.renderNode(i, total, "node")
	}
	p.finish()

	got := buf.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("static render output must contain no ANSI escapes; got %q", got)
	}
	if strings.ContainsRune(got, '█') || strings.ContainsRune(got, '░') {
		t.Errorf("static render output must contain no bar cells; got %q", got)
	}
	if !strings.Contains(got, "rendering 20/20...") {
		t.Errorf("expected the plain static final count; got %q", got)
	}
}

// TestProgressBar_ExactShape pins the pure global-progress-bar composition from a
// completion fraction: fixed-width filled and empty cells plus the floored
// whole-number percent, and no done/total counter (that lives on the render row).
// The fraction is clamped to [0,1]; a below-1.0 fraction floors so it never shows
// a rounded-up 100%.
func TestProgressBar_ExactShape(t *testing.T) {
	cases := []struct {
		frac float64
		want string
	}{
		{0.0, "░░░░░░░░░░░░░░░░░░ 0%"},
		{0.5, "█████████░░░░░░░░░ 50%"},
		{1.0, "██████████████████ 100%"},
		{0.6216, "███████████░░░░░░░ 62%"},
		{0.999, "█████████████████░ 99%"},
		{-0.5, "░░░░░░░░░░░░░░░░░░ 0%"},
		{1.5, "██████████████████ 100%"},
	}
	for _, c := range cases {
		if got := progressBar(c.frac); got != c.want {
			t.Errorf("progressBar(%v) = %q, want %q", c.frac, got, c.want)
		}
	}
}

// TestClampVisibleWidth_NoWrap confirms the redraw clamp caps a row's visible
// width without splitting an ANSI escape, and leaves an escape-free short row
// untouched.
func TestClampVisibleWidth_NoWrap(t *testing.T) {
	// A row wider than the terminal is truncated to the width in visible columns
	// (escapes excluded) and closed with a reset.
	row := "abcdef" + ansiDim + "GHIJ" + ansiReset
	got := clampVisibleWidth(row, 8)
	if w := visibleRuneWidth(got); w != 8 {
		t.Errorf("clamped visible width = %d, want 8; got %q", w, got)
	}
	if !strings.HasSuffix(got, ansiReset) {
		t.Errorf("a truncated row must end with a reset so no attribute dangles; got %q", got)
	}
	// A row already within width is returned unchanged.
	short := "[/] rendering"
	if got := clampVisibleWidth(short, 80); got != short {
		t.Errorf("a within-width row must be unchanged; got %q", got)
	}
	// Width 0 disables clamping.
	if got := clampVisibleWidth(row, 0); got != row {
		t.Errorf("width 0 must disable clamping; got %q", got)
	}
}

// TestProgress_RenderNode_ForcedStatic_Throttled pins the forced-static render
// shape: a large graph emits periodic lines (roughly one per tenth) plus a
// guaranteed final count — never one line per node — and no carriage returns.
func TestProgress_RenderNode_ForcedStatic_Throttled(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{progress: true}, buildProgressSpine(false))
	p.phase(phaseRender)
	total := 50
	for i := 1; i <= total; i++ {
		p.renderNode(i, total, "node")
	}
	p.finish()

	got := buf.String()
	if strings.ContainsRune(got, '\r') {
		t.Errorf("forced static render progress must not contain carriage returns; got %q", got)
	}
	lines := 0
	for _, ln := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if strings.Contains(ln, "rendering ") {
			lines++
		}
	}
	if lines < 2 || lines > 12 {
		t.Errorf("expected a throttled handful of interim lines, got %d; output %q", lines, got)
	}
	if !strings.Contains(got, "rendering 50/50...") {
		t.Errorf("expected a guaranteed final count line; got %q", got)
	}
}

// TestProgress_TTY_SpinnerTicker_Animates proves the active-phase spinner is
// time-driven, not event-driven: after a single phase() draw, with no further
// phase-transition calls, the background ticker must redraw the checklist several
// more times and advance the spinner frame. The tick interval is shortened so the
// animation is observable without a long wait. finish() stops the ticker and
// waits for its goroutine to exit, so reading the buffer afterward is race-free.
func TestProgress_TTY_SpinnerTicker_Animates(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)

	orig := spinnerTickInterval
	spinnerTickInterval = 2 * time.Millisecond
	t.Cleanup(func() { spinnerTickInterval = orig })

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
	// One phase() draws the initial block and lazily starts the ticker; the active
	// row is the "loading catalog" spinner.
	p.phase(phaseLoadCatalog)

	// No further phase() calls: let the ticker alone drive redraws for a window
	// spanning many intervals.
	time.Sleep(80 * time.Millisecond)
	p.finish()

	got := buf.String()

	// Each full redraw re-emits every row, so the count of the active label is the
	// count of draws. The event-driven baseline is 2 (the phase() draw plus the
	// finish() draw); anything well above that proves the ticker drove redraws.
	if draws := strings.Count(got, "loading catalog"); draws < 4 {
		t.Errorf("spinner ticker did not drive periodic redraws: only %d draws (event-driven baseline is 2); got %q", draws, got)
	}

	// The active-phase spinner frame must advance over time: more than one distinct
	// frame character appears on the active "loading catalog" row across redraws.
	seen := 0
	for _, f := range spinnerFrames {
		if strings.Contains(got, markActive(f)+"loading catalog") {
			seen++
		}
	}
	if seen < 2 {
		t.Errorf("spinner frame did not advance over time: saw %d distinct active frames, want >= 2; got %q", seen, got)
	}
}

// TestProgress_NonChecklist_NoTicker confirms the animation ticker is scoped to
// checklist (interactive TTY) mode only: forced-static, silent, and debug modes
// never start it, so their output is unchanged by the passage of time.
func TestProgress_NonChecklist_NoTicker(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	orig := spinnerTickInterval
	spinnerTickInterval = 2 * time.Millisecond
	t.Cleanup(func() { spinnerTickInterval = orig })

	cases := []struct {
		name string
		tty  bool
		diag buildDiagOptions
	}{
		{name: "forced-static", tty: false, diag: buildDiagOptions{progress: true}},
		{name: "silent", tty: false, diag: buildDiagOptions{}},
		{name: "debug", tty: true, diag: buildDiagOptions{debug: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTTY(t, tc.tty)

			var buf bytes.Buffer
			p := newProgressReporter(&buf, tc.diag, buildProgressSpine(false))
			p.phase(phaseLoadCatalog)
			p.phase(phaseClosure)
			time.Sleep(40 * time.Millisecond)
			if p.tickerStarted {
				t.Errorf("%s must not start the animation ticker", tc.name)
			}
			// Output must not grow during an idle window without events (the scope of
			// this check is the ticker; finish() legitimately appends final lines and
			// is deliberately outside the comparison window).
			before := buf.String()
			time.Sleep(40 * time.Millisecond)
			after := buf.String()
			if before != after {
				t.Errorf("%s output changed over time without events: before=%q after=%q", tc.name, before, after)
			}
			p.finish()
		})
	}
}

// TestProgress_RenderNode_ZeroTotal_NoOp confirms a zero total (no nodes) draws
// no render counter.
func TestProgress_RenderNode_ZeroTotal_NoOp(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
	p.renderNode(0, 0, "")

	if strings.Contains(buf.String(), "rendering 0/0") {
		t.Errorf("zero total must not draw a render counter; got %q", buf.String())
	}
}

// TestProgress_Pull_Checklist_NoCompileStep confirms the pull spine renders its
// own phases as a checklist and never shows a render counter or a compile step
// (pull has no render phase).
func TestProgress_Pull_Checklist_NoCompileStep(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, pullProgressSpine())
	p.phase(pullPhaseLoadCatalog)
	p.phase(pullPhaseClosure)
	p.phase(pullPhaseLockfile)
	// Plugin-compile callbacks are meaningless for pull (no render phase); they
	// must be inert.
	p.pluginCompileBegin(1)
	p.pluginCompileGuestDone(1)
	p.renderNode(1, 1, "x")
	p.finish()

	got := buf.String()
	for _, label := range []string{"loading catalog", "resolving dependencies", "writing lockfile"} {
		if !strings.Contains(got, label) {
			t.Errorf("expected pull phase label %q; got %q", label, got)
		}
	}
	if strings.Contains(got, compilePhaseLabel) {
		t.Errorf("pull must never show the compile step; got %q", got)
	}
	if strings.Contains(got, "rendering") {
		t.Errorf("pull has no render phase; got %q", got)
	}
	// Pull has no render fraction, so it shows no global progress line; its
	// completion is the stdout "Pull complete" summary.
	if strings.Contains(got, progressCaption) {
		t.Errorf("pull must not show the global progress line; got %q", got)
	}
	if strings.Contains(got, "Total build time") || strings.Contains(got, "Build completed:") {
		t.Errorf("pull must not show the build completion summary; got %q", got)
	}
}

// parseCursorUp returns the N in the first "\x1b[NA" cursor-up sequence in s, or
// -1 if none is present. Test helper for the redraw-bookkeeping assertion.
func parseCursorUp(t *testing.T, s string) int {
	t.Helper()
	marker := "\x1b["
	i := strings.Index(s, marker)
	if i < 0 {
		return -1
	}
	j := i + len(marker)
	start := j
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j >= len(s) || s[j] != 'A' || j == start {
		return -1
	}
	n := 0
	for _, r := range s[start:j] {
		n = n*10 + int(r-'0')
	}
	return n
}

// TestProgress_Redraw_CursorUpMatchesLineCount is the redraw-bookkeeping proof:
// the in-place redraw rewinds exactly as many lines as the prior block drew, and
// re-emits the same physical-line count — so physical lines == logical rows ==
// linesDrawn stays exact with the global progress line in the block. The final
// finish redraw re-emits the same block in place (the completion summary lives on
// stdout, not in this block), so the line count is unchanged. A desync here would
// reprint the block instead of redrawing it in place.
func TestProgress_Redraw_CursorUpMatchesLineCount(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)

	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour // keep the async ticker from redrawing mid-test
	t.Cleanup(func() { spinnerTickInterval = orig })

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))

	// First block: pre-render phases + render row + global progress line.
	p.phase(phaseRender)
	firstRows := p.linesDrawn
	if firstRows == 0 {
		t.Fatalf("first draw recorded no lines")
	}

	// A redraw with the same block structure must rewind exactly firstRows lines
	// and re-emit exactly firstRows physical lines.
	buf.Reset()
	p.renderNode(1, 4, "n")
	redraw := buf.String()
	if up := parseCursorUp(t, redraw); up != firstRows {
		t.Errorf("redraw cursor-up = %d, want %d (prior line count)", up, firstRows)
	}
	if drawn := strings.Count(redraw, "\n"); drawn != firstRows {
		t.Errorf("redraw emitted %d physical lines, want %d", drawn, firstRows)
	}
	if p.linesDrawn != firstRows {
		t.Errorf("linesDrawn = %d after redraw, want %d", p.linesDrawn, firstRows)
	}

	// finish() redraws the same block in place (no appended finish block): it must
	// rewind over the prior (firstRows) block and re-emit exactly firstRows lines,
	// keeping the count exact.
	buf.Reset()
	p.finish()
	fin := buf.String()
	if up := parseCursorUp(t, fin); up != firstRows {
		t.Errorf("finish redraw cursor-up = %d, want %d (prior line count)", up, firstRows)
	}
	if drawn := strings.Count(fin, "\n"); drawn != firstRows {
		t.Errorf("finish redraw emitted %d physical lines, want %d", drawn, firstRows)
	}
	if p.linesDrawn != firstRows {
		t.Errorf("linesDrawn = %d after finish, want %d", p.linesDrawn, firstRows)
	}
}

// TestProgress_StaticFinish_NoSummaryOnDiagSink confirms the forced non-TTY
// surface does NOT print the completion summary on the diagnostic sink: the single
// "Total build time" / "Build completed:" block lives on stdout in every mode, so
// the stderr diagnostic surface must not duplicate it. The static surface's finish
// is silent (its render lines already printed inline).
func TestProgress_StaticFinish_NoSummaryOnDiagSink(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{progress: true}, buildProgressSpine(false))
	p.phase(phaseRender)
	p.renderNode(1, 1, "n")
	p.finish()

	got := buf.String()
	if strings.Contains(got, "Total build time") {
		t.Errorf("static finish must not print the total-build-time line on the diagnostic sink; got %q", got)
	}
	if strings.Contains(got, "Build completed:") {
		t.Errorf("static finish must not print the completion caption on the diagnostic sink; got %q", got)
	}
	if strings.ContainsRune(got, '\r') || strings.ContainsRune(got, '\x1b') {
		t.Errorf("static surface must carry no cursor movement or ANSI escapes; got %q", got)
	}
}

// TestProgress_NoColor_ChecklistDropsDim pins the color-off accessibility rule
// for the checklist surface: with the process-global color-off decision set
// (--no-color / --plain / NO_COLOR), the elapsed field carries no dim ANSI, yet
// the elapsed value, its column alignment, and the [✓]/[ ] symbol markers all
// survive — status stays symbol+text, never color-alone. NO_COLOR reaches this
// surface as buildDiagOptions.noColor, the same field the CLI populates from the
// process-global decision.
func TestProgress_NoColor_ChecklistDropsDim(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)

	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour // keep the async ticker from redrawing mid-test
	t.Cleanup(func() { spinnerTickInterval = orig })

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{noColor: true}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.phase(phaseRender)
	p.renderNode(1, 1, "svc")
	p.finished = true

	rows := p.rows()
	joined := strings.Join(rows, "\n")

	if strings.Contains(joined, ansiDim) {
		t.Errorf("no-color checklist must emit no dim ANSI; got rows %q", rows)
	}
	if strings.Contains(joined, "\x1b[") {
		t.Errorf("no-color checklist rows must carry no ANSI escapes; got rows %q", rows)
	}
	if !strings.Contains(joined, markDone) {
		t.Errorf("no-color checklist must keep the [✓] done marker; got rows %q", rows)
	}
	// The elapsed value and its right-aligned column survive the loss of dim: the
	// render row still carries a seconds value after its counter.
	renderRow := findRow(rows, "rendering ")
	if renderRow == "" {
		t.Fatalf("no render row found; rows=%q", rows)
	}
	if !strings.Contains(renderRow, "1/1") {
		t.Errorf("render row must keep the done/total counter; got %q", renderRow)
	}
	if !strings.Contains(renderRow, "s") {
		t.Errorf("render row must keep the elapsed seconds value; got %q", renderRow)
	}
}

// TestProgress_Plain_ForcesStaticEvenOnTTY pins the accessible mode: --plain
// downgrades what would be the interactive checklist on a terminal to plain
// static lines. Even with a terminal sink, the surface starts no spinner ticker
// and does no cursor repaint — no carriage returns, no cursor-up sequences, no
// spinner frames, no color — so screen readers receive stable line output.
func TestProgress_Plain_ForcesStaticEvenOnTTY(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, true) // a terminal sink would normally get the animated checklist

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{plain: true}, buildProgressSpine(false))
	if p.mode != progressStatic {
		t.Fatalf("--plain on a terminal must resolve to progressStatic; got mode %d", p.mode)
	}

	p.phase(phaseLoadCatalog)
	p.phase(phaseClosure)
	p.phase(phaseRender)
	p.renderNode(1, 1, "svc")
	p.finish()

	got := buf.String()
	if strings.ContainsRune(got, '\r') {
		t.Errorf("plain mode must not repaint with carriage returns; got %q", got)
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("plain mode must emit no ANSI escapes (no color, no cursor-up); got %q", got)
	}
	// Static lines carry no checklist markers at all — no animated [|]/[/] spinner
	// row and no [✓]/[ ] marker — so the '[' character never appears. (A bare
	// spinner-frame scan would false-match the '/' in the "1/1" counter.)
	if strings.ContainsRune(got, '[') {
		t.Errorf("plain mode must draw no checklist/spinner markers; got %q", got)
	}
	if !strings.Contains(got, "loading catalog...") {
		t.Errorf("plain mode must print plain per-phase lines; got %q", got)
	}
}

// TestProgress_Plain_NonTTY_StaysSilent pins that --plain does not turn the
// surface on where it would otherwise be silent: on a non-terminal sink with no
// --progress, plain removes animation but adds no output. Only --progress forces
// static lines onto a non-terminal sink.
func TestProgress_Plain_NonTTY_StaysSilent(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{plain: true}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.phase(phaseRender)
	p.renderNode(1, 1, "n")
	p.finish()

	if buf.Len() != 0 {
		t.Errorf("plain on a non-TTY with no --progress must stay silent; got %q", buf.String())
	}
}

// approxEq reports whether a and b are within a small epsilon — the global-bar
// weight math is float arithmetic, so checkpoints are compared with tolerance
// rather than for exact equality.
func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

// TestProgress_GlobalBar_WarmCheckpoints pins the warm-profile weighted bar: the
// pre-render phases credit on completion (lockfile brings the bar to ~22% before
// render), the compile share is credited at the first rendered node (bringing the
// bar to ~54% when no guest recompiled), render carries it the rest of the way,
// and finish snaps it to 100%. The bar is monotonic non-decreasing across the
// sequence.
func TestProgress_GlobalBar_WarmCheckpoints(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour // freeze the async ticker for a stable read
	t.Cleanup(func() { spinnerTickInterval = orig })

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(false))

	p.phase(phaseLoadCatalog)
	f0 := p.globalFraction()
	if !approxEq(f0, 0) {
		t.Errorf("warm: fraction at loading-catalog = %v, want 0", f0)
	}
	p.phase(phaseLockfile)
	p.phase(phaseClosure) // lockfile now completed
	fLock := p.globalFraction()
	if !approxEq(fLock, 0.22) {
		t.Errorf("warm: fraction after lockfile = %v, want 0.22", fLock)
	}
	p.phase(phaseRender)
	fRenderStart := p.globalFraction()
	if !approxEq(fRenderStart, 0.24) {
		t.Errorf("warm: fraction entering render = %v, want 0.24", fRenderStart)
	}
	p.renderNode(1, 2, "a")
	fHalf := p.globalFraction()
	if !approxEq(fHalf, 0.74) {
		t.Errorf("warm: fraction at render 1/2 = %v, want 0.74", fHalf)
	}
	p.renderNode(2, 2, "b")
	fRenderDone := p.globalFraction()
	if !approxEq(fRenderDone, 0.94) {
		t.Errorf("warm: fraction after render = %v, want 0.94", fRenderDone)
	}
	p.finish()
	if f := p.globalFraction(); !approxEq(f, 1) {
		t.Errorf("warm: fraction at finish = %v, want 1", f)
	}

	// Monotonic non-decreasing across the observed checkpoints.
	for i, seq := range []float64{f0, fLock, fRenderStart, fHalf, fRenderDone} {
		if i > 0 {
			prev := []float64{f0, fLock, fRenderStart, fHalf, fRenderDone}[i-1]
			if seq < prev {
				t.Errorf("warm: bar went backwards at checkpoint %d: %v < %v", i, seq, prev)
			}
		}
	}
}

// TestProgress_GlobalBar_ColdCheckpoints pins the cold-profile weighted bar: the
// lockfile leaves it near 4%, the plugin-compilation step (share 0.85) fills it to
// ~90%, render carries it to ~93%, and finish snaps it to 100%.
func TestProgress_GlobalBar_ColdCheckpoints(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour
	t.Cleanup(func() { spinnerTickInterval = orig })

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(true))

	p.phase(phaseLoadCatalog)
	p.phase(phaseLockfile)
	p.phase(phaseClosure) // lockfile completed
	if f := p.globalFraction(); !approxEq(f, 0.04) {
		t.Errorf("cold: fraction after lockfile = %v, want 0.04", f)
	}
	p.phase(phaseRender)
	if f := p.globalFraction(); !approxEq(f, 0.05) {
		t.Errorf("cold: fraction entering render (pre-compile) = %v, want 0.05", f)
	}
	p.pluginCompileBegin(1)
	p.pluginCompileRunning()
	// Just opened: the time ramp has barely started, so the bar sits near the
	// pre-compile 0.05 (base 0 guests done, negligible elapsed).
	if f := p.globalFraction(); f < 0.05 || f > 0.06 {
		t.Errorf("cold: fraction at compile start = %v, want ~0.05", f)
	}
	p.pluginCompileGuestDone(1)
	if f := p.globalFraction(); !approxEq(f, 0.90) {
		t.Errorf("cold: fraction after compile = %v, want 0.90", f)
	}
	p.renderNode(1, 1, "a")
	if f := p.globalFraction(); !approxEq(f, 0.93) {
		t.Errorf("cold: fraction after render = %v, want 0.93", f)
	}
	p.finish()
	if f := p.globalFraction(); !approxEq(f, 1) {
		t.Errorf("cold: fraction at finish = %v, want 1", f)
	}
}

// TestProgress_EagerCompile_BarAdvancesDuringCompile is the defect-3 gate in unit
// form: while a single provider guest is compiling (before its completion
// callback fires), the global bar must advance with elapsed time rather than
// freeze — successive reads show an increasing fraction — yet stay below the
// share the phase credits only on real completion.
func TestProgress_EagerCompile_BarAdvancesDuringCompile(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour
	t.Cleanup(func() { spinnerTickInterval = orig })

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(true))
	p.phase(phaseRender)
	p.pluginCompileBegin(1)
	p.pluginCompileRunning()

	// Simulate elapsed compile time by rewinding the ramp anchor, mimicking what
	// the async redraw ticker observes as the real compile runs.
	preCompile := p.globalFraction()
	p.compileStart = time.Now().Add(-2 * time.Second)
	early := p.globalFraction()
	p.compileStart = time.Now().Add(-10 * time.Second)
	late := p.globalFraction()

	if !(preCompile < early && early < late) {
		t.Errorf("bar did not advance during compile: pre=%v early=%v late=%v", preCompile, early, late)
	}
	// The time ramp is capped below the completed-phase share, so it never claims
	// the guest finished before the completion callback: cold pre-render 0.05 plus
	// compile share 0.85 = 0.90 at real completion; the ramp must stay under that.
	if late >= 0.90 {
		t.Errorf("time ramp overtook real completion: late=%v must stay below 0.90", late)
	}
	p.pluginCompileGuestDone(1)
	if done := p.globalFraction(); !approxEq(done, 0.90) {
		t.Errorf("fraction after guest-done = %v, want 0.90", done)
	}
}

// TestProgress_GlobalBar_WarmProfileCompile_Advances is the warm-profile freeze
// gate: on a stale cache the up-front guess is warm, but the specific guest this
// build needs is not cached, so a real recompile is observed. The warm profile
// now carries its own compile share, so the bar must ADVANCE with elapsed time
// during that recompile rather than freeze — successive reads climb — and stay
// below the share the step credits only on real completion.
func TestProgress_GlobalBar_WarmProfileCompile_Advances(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour
	t.Cleanup(func() { spinnerTickInterval = orig })

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseRender)
	p.pluginCompileBegin(1)
	p.pluginCompileRunning() // a guest is observed compiling despite the warm guess

	// Simulate elapsed compile time by rewinding the ramp anchor, mimicking what
	// the async redraw ticker observes as the real recompile runs.
	preCompile := p.globalFraction()
	p.compileStart = time.Now().Add(-2 * time.Second)
	early := p.globalFraction()
	p.compileStart = time.Now().Add(-10 * time.Second)
	late := p.globalFraction()

	if !(preCompile < early && early < late) {
		t.Errorf("warm bar did not advance during recompile: pre=%v early=%v late=%v", preCompile, early, late)
	}
	// The time ramp is capped below the completed-phase share, so it never claims
	// the guest finished before the completion callback: warm pre-render 0.24 plus
	// compile share 0.30 = 0.54 at real completion; the ramp must stay under that.
	if late >= 0.54 {
		t.Errorf("time ramp overtook real completion: late=%v must stay below 0.54", late)
	}
	p.pluginCompileGuestDone(1)
	afterGuest := p.globalFraction()
	if !approxEq(afterGuest, 0.54) {
		t.Errorf("warm: fraction after guest-done = %v, want 0.54", afterGuest)
	}
	p.renderNode(1, 1, "a")
	afterRender := p.globalFraction()
	if !approxEq(afterRender, 0.94) {
		t.Errorf("warm: fraction after render = %v, want 0.94", afterRender)
	}
	p.finish()
	fin := p.globalFraction()
	if !approxEq(fin, 1) {
		t.Errorf("warm: fraction at finish = %v, want 1", fin)
	}

	// Monotonic non-decreasing across the full captured sequence.
	seq := []float64{preCompile, early, late, afterGuest, afterRender, fin}
	for i := 1; i < len(seq); i++ {
		if seq[i] < seq[i-1] {
			t.Errorf("warm: bar went backwards at step %d: %v < %v", i, seq[i], seq[i-1])
		}
	}
}

// TestBuild_SingleFinalBlockOnStdout is the defect-1 gate in unit form: a
// successful build emits exactly ONE clean completion block — the total-build-time
// line and the files-written line — on STDOUT, with no duplicate, empty, or bare
// caption lines, and the diagnostic sink (stderr) carries none of that summary.
func TestBuild_SingleFinalBlockOnStdout(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeProgressBuildFixture(t, root)

	withTTY(t, true) // interactive checklist on the diagnostic sink

	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour
	t.Cleanup(func() { spinnerTickInterval = orig })

	catalogRoot := filepath.Join(root, "catalog")
	releaseYAML := filepath.Join(root, "release.yaml")

	// Build is read-only: prime the lockfile with an explicit pull (output discarded
	// so it cannot perturb the stdout/stderr capture below).
	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releaseYAML, CatalogDirs: []string{catalogRoot}, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releaseYAML, []string{catalogRoot}, false, buildDiagOptions{}, buildops.RunOptions{}, &stderr, &stdout); err != nil {
		t.Fatalf("build failed: %v (stderr=%q)", err, stderr.String())
	}

	// STDOUT: exactly two non-empty lines, in order, no blank/duplicate lines.
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout must be exactly two lines; got %d: %q", len(lines), stdout.String())
	}
	if !strings.HasPrefix(lines[0], "Total build time: ") {
		t.Errorf("first stdout line must be the total-build-time line; got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "Build completed: ") || !strings.Contains(lines[1], "files written to") {
		t.Errorf("second stdout line must be the files-written line; got %q", lines[1])
	}
	// The old duplicated "Build time:" (no "Total") and a bare "Build completed:"
	// caption must not appear anywhere on stdout.
	if strings.Contains(stdout.String(), "Build time: ") && !strings.Contains(stdout.String(), "Total build time: ") {
		t.Errorf("stdout must not carry the old bare 'Build time:' line; got %q", stdout.String())
	}

	// STDERR (diagnostic sink): carries the checklist but NOT the completion
	// summary — the block is single-sourced on stdout.
	if strings.Contains(stderr.String(), "Total build time") {
		t.Errorf("diagnostic sink must not duplicate the total-build-time line; got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "Build completed:") {
		t.Errorf("diagnostic sink must not duplicate the completion caption; got %q", stderr.String())
	}
}

// TestFormatTotalBuildTimeLine_SharesPhaseTimingRightEdge asserts the summary's
// total-build-time value is right-aligned to the same right-hand column the
// per-phase checklist elapsed times occupy, so the value and the phase timings
// share a right edge.
func TestFormatTotalBuildTimeLine_SharesPhaseTimingRightEdge(t *testing.T) {
	// A phase row's visible right edge: composeRow pads the body to rowBodyColumn
	// then appends the elapsed field of elapsedFieldWidth. colorOff drops the dim
	// escapes so the rune count is the exact visible width.
	rp := &progressReporter{colorOff: true}
	phaseRow := rp.composeRow(markDone+"rendering 8/8", "0.7s")
	wantEdge := rowBodyColumn + elapsedFieldWidth
	if got := utf8.RuneCountInString(phaseRow); got != wantEdge {
		t.Fatalf("phase-row right edge = %d, want %d (row=%q)", got, wantEdge, phaseRow)
	}
	for _, value := range []string{"3.1s", "17.8s", "0.4s", "120.5s"} {
		line := formatTotalBuildTimeLine(value)
		if got := utf8.RuneCountInString(line); got != wantEdge {
			t.Errorf("total line for %q has right edge %d, want %d (line=%q)", value, got, wantEdge, line)
		}
		if !strings.HasSuffix(line, value) {
			t.Errorf("total line %q must end with the value %q", line, value)
		}
		if !strings.HasPrefix(line, "Total build time: ") {
			t.Errorf("total line %q must keep the 'Total build time: ' prefix", line)
		}
	}
}

// TestFormatPhaseElapsed pins the elapsed rendering shared by the per-phase
// checklist column and the build-summary total line: seconds to one decimal with
// no space before the unit, sub-second values still in the "N.Ns" form, and a
// negative duration clamped to zero.
func TestFormatPhaseElapsed(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "sub-second", d: 400 * time.Millisecond, want: "0.4s"},
		{name: "at-one-second", d: 1000 * time.Millisecond, want: "1.0s"},
		{name: "multi-second", d: 1400 * time.Millisecond, want: "1.4s"},
		{name: "rounds-to-one-decimal", d: 17750 * time.Millisecond, want: "17.8s"},
		{name: "zero", d: 0, want: "0.0s"},
		{name: "negative-clamped-to-zero", d: -5 * time.Second, want: "0.0s"},
		{name: "large", d: 65 * time.Second, want: "65.0s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatPhaseElapsed(tc.d); got != tc.want {
				t.Errorf("formatPhaseElapsed(%s) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

// TestFormatTotalBuildTimeLine_LongValueFallsBackToSingleSpace verifies a value
// long enough to reach (or exceed) the alignment column separates with a single
// space rather than producing a negative pad.
func TestFormatTotalBuildTimeLine_LongValueFallsBackToSingleSpace(t *testing.T) {
	value := strings.Repeat("9", rowBodyColumn+elapsedFieldWidth)
	line := formatTotalBuildTimeLine(value)
	if want := "Total build time: " + value; line != want {
		t.Errorf("long-value line = %q, want single-space separator %q", line, want)
	}
}
