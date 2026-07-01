// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"time"
)

// buildDiagOptions carries the build's stderr-diagnostics choices. Both fields
// are opt-in from CLI flags; the zero value is the silent default that the
// test-facing dispatchBuild wrapper passes so the diagnostic surface never
// perturbs a build the tests drive.
//
// quiet silences the phase-progress surface. debug turns on a human-readable
// verbose diagnostic layer (per-phase wall-clock timing plus the entity and
// closure counts cheaply available at each boundary). When both are set, debug
// wins — its diagnostics always emit, because a caller who asked for verbose
// output asked for output.
type buildDiagOptions struct {
	quiet bool
	debug bool
}

// buildPhase enumerates the pipeline boundaries the progress surface names as it
// advances. The order matches dispatchBuild's execution order; the labels are
// concise so the redrawn line stays terminal-width-friendly.
type buildPhase struct {
	label string
}

// Phase indices into buildPhases. dispatchBuild passes these to
// progressReporter.phase as it crosses each boundary; keeping them adjacent to
// the label slice keeps index and label single-sourced.
const (
	phaseLoadCatalog = iota
	phaseLockfile
	phaseClosure
	phaseValidate
	phaseInheritance
	phaseRender
)

// buildPhases is the ordered phase list the progress reporter walks. It mirrors
// the boundaries dispatchBuild crosses so the surfaced label always names the
// step in flight.
var buildPhases = []buildPhase{
	phaseLoadCatalog: {"loading catalog"},
	phaseLockfile:    {"resolving lockfile"},
	phaseClosure:     {"computing closure"},
	phaseValidate:    {"validating"},
	phaseInheritance: {"resolving inheritance"},
	phaseRender:      {"rendering"},
}

// progressReporter renders the per-phase build progress to a diagnostic sink. It
// has three shapes chosen at construction: a no-op (quiet, and neither debug nor
// a progress surface requested), a plain one-line-per-phase logger (sink is not
// a terminal, or debug is on), and an in-place single-line redraw (sink is an
// interactive terminal). Every write lands on the diagnostic sink; the reporter
// never touches stdout, so the compiler's byte-deterministic rendered output is
// untouched by the presence or shape of progress.
type progressReporter struct {
	out   io.Writer
	tty   bool
	quiet bool
	debug bool

	// started is the whole-build wall clock, set at the first phase and used for
	// the debug total. phaseStart marks the current phase for per-phase timing.
	started    time.Time
	phaseStart time.Time
	current    int
	// dirty is true when an in-place line is on the terminal and must be cleared
	// before any other write so it never garbles later output.
	dirty bool
	// finished guards finish against a double call: the build calls it explicitly
	// before the stdout summary (so the in-place line clears first) and again via
	// defer on any early return.
	finished bool

	// renderTotal is the node count the render phase walks, captured before the
	// per-node loop. renderNext is the next non-TTY throttle threshold: plain
	// sinks log a line only when the completed count crosses it, so a large graph
	// does not emit one line per node. Zero renderTotal disables the counter and
	// the render phase shows its plain static label.
	renderTotal int
	renderNext  int
}

// newProgressReporter resolves the reporter shape from the diagnostic options
// and whether the sink is an interactive terminal. It reuses the shared
// terminal detection so the decision matches the rest of the CLI. A quiet build
// with no debug produces a no-op reporter (tty=false, quiet=true) whose methods
// return immediately.
func newProgressReporter(out io.Writer, opts buildDiagOptions) *progressReporter {
	// debug wins over quiet: verbose diagnostics always emit. Otherwise quiet
	// suppresses the surface entirely.
	silent := opts.quiet && !opts.debug
	// In-place redraw only when the sink is a real terminal and debug is off;
	// debug's multi-line timing report is incompatible with a single redrawn
	// line, so debug always uses the plain logger.
	tty := !silent && !opts.debug && fileTerminal(out)
	return &progressReporter{
		out:   out,
		tty:   tty,
		quiet: silent,
		debug: opts.debug,
	}
}

const (
	// ansiClearLine erases from the cursor to the end of the line so a shorter
	// redraw never leaves stale characters from a longer prior label.
	ansiClearLine = "\x1b[K"
)

var spinnerFrames = []rune{'|', '/', '-', '\\'}

// phase advances the reporter to the named-by-index phase and renders it. The
// caller passes phase indices in ascending order; phase closes the previous
// phase's timing (debug) and opens the next. A no-op reporter returns
// immediately.
func (p *progressReporter) phase(i int) {
	if p == nil || p.quiet {
		return
	}
	now := time.Now()
	if p.started.IsZero() {
		p.started = now
	}
	if p.debug {
		if !p.phaseStart.IsZero() && p.current >= 0 && p.current < len(buildPhases) {
			fmt.Fprintf(p.out, "  [debug] %-22s done in %s\n", buildPhases[p.current].label, now.Sub(p.phaseStart).Round(time.Microsecond))
		}
		p.phaseStart = now
		p.current = i
		if i >= 0 && i < len(buildPhases) {
			fmt.Fprintf(p.out, "  [debug] %-22s start\n", buildPhases[i].label)
		}
		return
	}
	p.current = i
	if i < 0 || i >= len(buildPhases) {
		return
	}
	if p.tty {
		frame := spinnerFrames[i%len(spinnerFrames)]
		fmt.Fprintf(p.out, "\r%c %s%s", frame, buildPhases[i].label, ansiClearLine)
		p.dirty = true
		return
	}
	fmt.Fprintf(p.out, "%s...\n", buildPhases[i].label)
}

// note emits a supplementary debug line for the current phase — a count or a
// tier observation that is cheap at the boundary. It is a no-op unless debug is
// on. In debug mode the plain logger is active, so the note is a simple line.
func (p *progressReporter) note(format string, args ...any) {
	if p == nil || !p.debug {
		return
	}
	fmt.Fprintf(p.out, "  [debug]   %s\n", fmt.Sprintf(format, args...))
}

// renderThrottleFraction bounds how often a non-TTY (piped/redirected) or debug
// sink logs a render-progress line: at most once per this fraction of the total
// node count, so a large graph emits a handful of periodic lines rather than one
// per node. A TTY sink is unaffected — it redraws a single in-place line every
// node.
const renderThrottleFraction = 10

// renderThrottleStep is the non-TTY logging stride for a total node count: one
// tenth of the total, floored at one so a small graph still logs at least one
// interim line before the guaranteed final count.
func renderThrottleStep(total int) int {
	step := total / renderThrottleFraction
	if step < 1 {
		step = 1
	}
	return step
}

// renderNode advances the render phase's per-node progress, called once per node
// as the render loop completes it. done is the 1-based count of nodes rendered
// so far, total is the graph's node count, and nodeID names the node just
// finished. The total is captured on the first call (the render loop knows it
// only once the IR graph exists), so no separate start call is needed.
//
// On a TTY it redraws the render line in place with an X/Y counter every node.
// On a plain sink it logs a line only when the count crosses the throttle
// threshold, and always for the final node, so redirected output stays a handful
// of lines rather than one per node. In debug mode it logs a throttled per-node
// line naming the node alongside the verbose layer. A no-op reporter, or a
// zero/negative total, returns immediately.
func (p *progressReporter) renderNode(done, total int, nodeID string) {
	if p == nil || p.quiet || total <= 0 {
		return
	}
	if p.renderTotal == 0 {
		p.renderTotal = total
		p.renderNext = renderThrottleStep(total)
	}
	label := buildPhases[phaseRender].label
	if p.tty {
		frame := spinnerFrames[done%len(spinnerFrames)]
		fmt.Fprintf(p.out, "\r%c %s %d/%d%s", frame, label, done, p.renderTotal, ansiClearLine)
		p.dirty = true
		return
	}
	// Non-TTY (plain logger, includes debug): throttle to periodic lines plus a
	// guaranteed final count, so a large graph does not spam one line per node.
	final := done >= p.renderTotal
	if done < p.renderNext && !final {
		return
	}
	for p.renderNext <= done {
		p.renderNext += renderThrottleStep(p.renderTotal)
	}
	if p.debug {
		fmt.Fprintf(p.out, "  [debug]   %s %d/%d (%s)\n", label, done, p.renderTotal, nodeID)
		return
	}
	fmt.Fprintf(p.out, "%s %d/%d...\n", label, done, p.renderTotal)
}

// finish clears any in-place line and, in debug mode, closes the final phase
// timing and prints the whole-build total. It is called once when the build
// completes or errors, so the progress surface never lingers as noise before
// later output (the success summary on stdout, or an error on stderr).
func (p *progressReporter) finish() {
	if p == nil || p.quiet || p.finished {
		return
	}
	p.finished = true
	now := time.Now()
	if p.debug {
		if !p.phaseStart.IsZero() && p.current >= 0 && p.current < len(buildPhases) {
			fmt.Fprintf(p.out, "  [debug] %-22s done in %s\n", buildPhases[p.current].label, now.Sub(p.phaseStart).Round(time.Microsecond))
		}
		if !p.started.IsZero() {
			fmt.Fprintf(p.out, "  [debug] %-22s %s\n", "build total", now.Sub(p.started).Round(time.Microsecond))
		}
		return
	}
	if p.tty && p.dirty {
		// Erase the redrawn line so it does not garble the success summary or an
		// error that follows.
		fmt.Fprintf(p.out, "\r%s", ansiClearLine)
		p.dirty = false
	}
}
