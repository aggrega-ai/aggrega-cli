// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{quiet: true}, buildProgressSpine(true))
	p.phase(phaseLoadCatalog)
	p.pluginCompiling()
	p.renderNode(1, 1, "n")
	p.finish()

	if buf.Len() != 0 {
		t.Errorf("--quiet must produce no progress output; got %q", buf.String())
	}
}

// TestProgress_QuietBeatsProgress pins the precedence: when both --quiet and
// --progress are set, quiet wins and the surface stays silent.
func TestProgress_QuietBeatsProgress(t *testing.T) {
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

// TestProgress_ColdProfile_ShowsCompileStep pins the cold profile: when a guest
// is observed compiling during the render step, the "compiling plugins" step
// joins the checklist between the pre-render phases and the render phase.
func TestProgress_ColdProfile_ShowsCompileStep(t *testing.T) {
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(true))
	p.phase(phaseLoadCatalog)
	p.phase(phaseInheritance)
	p.phase(phaseRender)
	p.pluginCompiling() // a guest compiles
	p.renderNode(1, 1, "svc")
	p.finish()

	if !strings.Contains(buf.String(), compilePhaseLabel) {
		t.Errorf("cold profile must show the %q step once a guest compiles; got %q", compilePhaseLabel, buf.String())
	}
}

// TestProgress_WarmProfile_OmitsCompileStep pins the warm profile: even if a
// guest loads (a warm-cache cache-hit still drives the observer), the compile
// step never appears.
func TestProgress_WarmProfile_OmitsCompileStep(t *testing.T) {
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseRender)
	p.pluginCompiling() // observed, but warm profile ignores it
	p.renderNode(1, 1, "svc")
	p.finish()

	if strings.Contains(buf.String(), compilePhaseLabel) {
		t.Errorf("warm profile must omit the %q step; got %q", compilePhaseLabel, buf.String())
	}
}

// TestProgress_RenderNode_TTY_Counter pins the layout-A render row on a checklist
// sink: the render row carries the done/total counter (no inline bar), while the
// bar and whole-number percent live on the global progress line — redrawn in
// place at each node.
func TestProgress_RenderNode_TTY_Counter(t *testing.T) {
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
	// The global progress line carries the caption, the bar, and the percent for
	// each step.
	for _, want := range []string{"Progress ", "33%", "66%", "100%"} {
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
	if !strings.Contains(progRow, "62%") {
		t.Errorf("global progress line must contain the percentage; got %q", progRow)
	}
	if strings.Contains(progRow, ansiDim) {
		t.Errorf("global progress line must carry no dim elapsed field; got %q", progRow)
	}
}

// TestProgress_LayoutA_ElapsedColumnAligned asserts every elapsed field — across
// all phase rows AND the total-build-time line in the finish block — right-aligns
// to a single shared column. The finish block also carries the total-build-time
// row (with elapsed) and the "Build completed:" caption (without).
func TestProgress_LayoutA_ElapsedColumnAligned(t *testing.T) {
	withTTY(t, true)

	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour // keep the async ticker from redrawing mid-test
	t.Cleanup(func() { spinnerTickInterval = orig })

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.phase(phaseLockfile)
	p.phase(phaseRender)
	p.renderNode(23, 37, "svc")
	// finished=true so footerRows contributes the total-build-time row.
	p.finished = true

	rows := p.rows()

	// The finish block is present with both captions.
	if findRow(rows, totalBuildTimeCaption) == "" {
		t.Errorf("finish block must contain %q; rows=%q", totalBuildTimeCaption, rows)
	}
	if findRow(rows, buildCompletedCaption) == "" {
		t.Errorf("finish block must contain %q; rows=%q", buildCompletedCaption, rows)
	}

	// Every row that carries a dim elapsed field starts that field at the same
	// visible column. This spans phase rows, the render row, and total-build-time.
	offset := -1
	fieldsSeen := 0
	for _, r := range rows {
		i := strings.Index(r, ansiDim)
		if i < 0 {
			continue // pending rows, the progress line, and "Build completed:" show none
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
		t.Errorf("expected at least 3 elapsed fields (>=2 phase rows + total-build-time); saw %d", fieldsSeen)
	}
	// The total-build-time row specifically carries a dim, right-aligned elapsed.
	tot := findRow(rows, totalBuildTimeCaption)
	if !strings.Contains(tot, ansiDim) || !strings.HasSuffix(tot, ansiReset) {
		t.Errorf("total-build-time row must carry a dim elapsed field; got %q", tot)
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
	withTTY(t, true)

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(true))
	p.phase(phaseLoadCatalog)
	p.phase(phaseRender)
	p.pluginCompiling() // cold profile: the compiling-plugins step is active

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

// TestProgressBar_ExactShape pins the pure global-progress-bar composition:
// fixed-width filled and empty cells plus the truncated whole-number percent, and
// no done/total counter (that lives on the render row). A non-positive total
// renders the all-empty 0% bar rather than an empty string.
func TestProgressBar_ExactShape(t *testing.T) {
	cases := []struct {
		done, total int
		want        string
	}{
		{0, 4, "░░░░░░░░░░░░░░░░░░ 0%"},
		{2, 4, "█████████░░░░░░░░░ 50%"},
		{4, 4, "██████████████████ 100%"},
		{23, 37, "███████████░░░░░░░ 62%"},
		{1, 0, "░░░░░░░░░░░░░░░░░░ 0%"},
	}
	for _, c := range cases {
		if got := progressBar(c.done, c.total); got != c.want {
			t.Errorf("progressBar(%d,%d) = %q, want %q", c.done, c.total, got, c.want)
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
	withTTY(t, true)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, pullProgressSpine())
	p.phase(pullPhaseLoadCatalog)
	p.phase(pullPhaseClosure)
	p.phase(pullPhaseLockfile)
	// Guest observations are meaningless for pull; they must be inert.
	p.pluginCompiling()
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
	// Pull has no render fraction, so it shows neither the global progress line nor
	// the build finish block; its completion is the stdout "Pull complete" summary.
	if strings.Contains(got, progressCaption) {
		t.Errorf("pull must not show the global progress line; got %q", got)
	}
	if strings.Contains(got, totalBuildTimeCaption) || strings.Contains(got, buildCompletedCaption) {
		t.Errorf("pull must not show the build finish block; got %q", got)
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
// linesDrawn stays exact after the layout-A lines (the global progress line, and
// later the finish block) join the block. A desync here would reprint the block
// instead of redrawing it in place.
func TestProgress_Redraw_CursorUpMatchesLineCount(t *testing.T) {
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

	// finish() appends the two-line finish block: it must rewind over the prior
	// (firstRows) block and re-emit firstRows+2 lines, keeping the count exact.
	buf.Reset()
	p.finish()
	fin := buf.String()
	if up := parseCursorUp(t, fin); up != firstRows {
		t.Errorf("finish redraw cursor-up = %d, want %d (prior line count)", up, firstRows)
	}
	if drawn := strings.Count(fin, "\n"); drawn != firstRows+2 {
		t.Errorf("finish redraw emitted %d physical lines, want %d (block + finish block)", drawn, firstRows+2)
	}
	if p.linesDrawn != firstRows+2 {
		t.Errorf("linesDrawn = %d after finish, want %d", p.linesDrawn, firstRows+2)
	}
}

// TestProgress_LayoutA_StaticFinishLines confirms the forced non-TTY surface
// prints the plain equivalent of the finish block — a "Total build time" line and
// the "Build completed:" caption — with no cursor movement and no ANSI escapes.
func TestProgress_LayoutA_StaticFinishLines(t *testing.T) {
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{progress: true}, buildProgressSpine(false))
	p.phase(phaseRender)
	p.renderNode(1, 1, "n")
	p.finish()

	got := buf.String()
	if !strings.Contains(got, totalBuildTimeCaption+" ") {
		t.Errorf("static finish must print %q with an elapsed; got %q", totalBuildTimeCaption, got)
	}
	if !strings.Contains(got, buildCompletedCaption) {
		t.Errorf("static finish must print %q; got %q", buildCompletedCaption, got)
	}
	if strings.ContainsRune(got, '\r') || strings.ContainsRune(got, '\x1b') {
		t.Errorf("static finish lines must carry no cursor movement or ANSI escapes; got %q", got)
	}
}
