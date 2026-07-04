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
	if !strings.Contains(got, "loading catalog...") || !strings.Contains(got, "computing closure...") {
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
	for _, label := range []string{"loading catalog", "computing closure", "rendering"} {
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

// TestProgress_RenderNode_TTY_Counter pins the render row on a checklist sink:
// the label followed by the percentage bar, the whole-number percent, and the
// done/total counter, redrawn in place at each node.
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
	// Each node's redraw carries the bar cells, the percent, and the done/total
	// counter for that step.
	for _, want := range []string{"rendering ", "33% 1/3", "66% 2/3", "100% 3/3"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected render detail %q; got %q", want, got)
		}
	}
	if !strings.ContainsRune(got, '█') || !strings.ContainsRune(got, '░') {
		t.Errorf("expected a filled/empty percentage bar on the render row; got %q", got)
	}
}

// TestProgress_ActiveRenderRow_HasBarPercentAndDimElapsed asserts the composed
// active render row carries all four locked elements — the filled/empty bar, the
// NN% percentage, the done/total counter, and a dim, right-aligned elapsed field
// — and that the elapsed value is drawn with the ANSI dim attribute and closed
// with a reset.
func TestProgress_ActiveRenderRow_HasBarPercentAndDimElapsed(t *testing.T) {
	withTTY(t, true)

	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{}, buildProgressSpine(false))
	p.phase(phaseRender)
	p.renderNode(23, 37, "svc")

	rows := p.rows()
	row := rows[len(rows)-1] // the render row is last

	if !strings.ContainsRune(row, '█') || !strings.ContainsRune(row, '░') {
		t.Errorf("active render row must contain the filled/empty bar; got %q", row)
	}
	if !strings.Contains(row, "62%") {
		t.Errorf("active render row must contain the percentage; got %q", row)
	}
	if !strings.Contains(row, "23/37") {
		t.Errorf("active render row must contain the done/total counter; got %q", row)
	}
	// The elapsed field is dim (opened with the dim SGR, closed with a reset) and
	// carries a seconds value.
	dimIdx := strings.Index(row, ansiDim)
	if dimIdx < 0 {
		t.Fatalf("active render row must render the elapsed field dim; got %q", row)
	}
	field := row[dimIdx:]
	if !strings.HasSuffix(field, ansiReset) {
		t.Errorf("dim elapsed field must be closed with a reset; got %q", field)
	}
	if !strings.Contains(field, "s") {
		t.Errorf("dim elapsed field must carry a seconds value; got %q", field)
	}
	// Right-alignment: every drawn row shares the same dim-field start column, so
	// the times line up. Compare the render row's dim-column offset (visible
	// width before the dim escape) with a pre-render row's.
	preRows := rows[:len(rows)-1]
	renderOffset := visibleRuneWidth(row[:strings.Index(row, ansiDim)])
	for _, r := range preRows {
		i := strings.Index(r, ansiDim)
		if i < 0 {
			continue // a pending row shows no elapsed
		}
		if off := visibleRuneWidth(r[:i]); off != renderOffset {
			t.Errorf("elapsed column misaligned: render row starts at %d, other row at %d (%q)", renderOffset, off, r)
		}
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
		if strings.ContainsRune(row, '█') || strings.ContainsRune(row, '░') || strings.Contains(row, "%") {
			// Only the render row (last, once a count is known) may carry a bar; no
			// count is known here, so no row may.
			t.Errorf("uncounted phase must not fabricate a bar/percent; got row %q", row)
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

// TestRenderBar_ExactShape pins the pure bar composition: fixed-width filled and
// empty cells, the truncated whole-number percent, and the done/total counter.
func TestRenderBar_ExactShape(t *testing.T) {
	cases := []struct {
		done, total int
		want        string
	}{
		{0, 4, "░░░░░░░░░░░░░░░░░░ 0% 0/4"},
		{2, 4, "█████████░░░░░░░░░ 50% 2/4"},
		{4, 4, "██████████████████ 100% 4/4"},
		{23, 37, "███████████░░░░░░░ 62% 23/37"},
	}
	for _, c := range cases {
		if got := renderBar(c.done, c.total); got != c.want {
			t.Errorf("renderBar(%d,%d) = %q, want %q", c.done, c.total, got, c.want)
		}
	}
	if got := renderBar(1, 0); got != "" {
		t.Errorf("renderBar with non-positive total must be empty; got %q", got)
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
			before := buf.String()
			time.Sleep(40 * time.Millisecond)
			p.finish()
			// Output must not have grown during the idle window (finish appends only
			// in debug mode's run-total, which is deterministic in shape).
			after := buf.String()
			if tc.name != "debug" && before != after {
				t.Errorf("%s output changed over time without events: before=%q after=%q", tc.name, before, after)
			}
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
	for _, label := range []string{"loading catalog", "computing closure", "writing lockfile"} {
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
}
