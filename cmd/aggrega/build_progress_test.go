// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
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

// TestProgress_RenderNode_TTY_Counter pins the render row's X/Y counter on a
// checklist sink.
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
	for _, want := range []string{"rendering 1/3", "rendering 2/3", "rendering 3/3"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected render counter %q; got %q", want, got)
		}
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
