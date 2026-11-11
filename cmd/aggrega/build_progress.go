// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// buildDiagOptions carries a command's stderr-diagnostics choices. Every field
// is opt-in from CLI flags; the zero value is the auto default that the
// test-facing dispatchBuild wrapper passes so the diagnostic surface never
// perturbs a build the tests drive.
//
// quiet silences the progress surface entirely. progress forces the surface on
// even when the sink is not an interactive terminal (auto shows it only on a
// TTY). debug turns on a human-readable verbose diagnostic layer (per-phase
// wall-clock timing plus the entity and closure counts cheaply available at each
// boundary).
//
// Precedence when more than one is set: debug wins over everything — a caller
// who asked for verbose output asked for output — then quiet, then progress.
// So --quiet takes precedence over --progress: an explicit request for silence
// is honoured even when progress is also requested.
//
// noColor is the process-global color-off decision (--no-color / --plain /
// NO_COLOR); when set, the checklist draws its elapsed field with no dim ANSI.
// plain is the accessible mode: it forces plain static phase lines even on a
// terminal — no spinner animation, no cursor repaint — and implies no color.
type buildDiagOptions struct {
	quiet    bool
	debug    bool
	progress bool
	noColor  bool
	plain    bool
}

// progressMode is the resolved shape of the progress surface, decided once at
// reporter construction from the diagnostic options and whether the sink is an
// interactive terminal.
type progressMode int

const (
	// progressSilent draws nothing. Chosen for --quiet, and for the auto default
	// when the sink is not an interactive terminal.
	progressSilent progressMode = iota
	// progressStatic is the plain surface: one line per step as the step
	// completes — its name and its elapsed time — with no escape sequence, no
	// spinner and no cursor movement, so nothing is ever redrawn. Chosen for a
	// terminal whose console does not interpret escape sequences, for --plain on
	// any terminal, and when the surface is forced on (--progress) but the sink is
	// not a terminal (redirected / piped / log).
	progressStatic
	// progressChecklist draws an in-place multi-line phase checklist that ticks
	// each phase as it completes and spins the active phase. Chosen when the sink
	// is an interactive terminal that interprets escape sequences.
	progressChecklist
	// progressDebug draws the verbose per-phase timing layer. Chosen for --debug,
	// independent of sink shape.
	progressDebug
)

// progressSpine describes the ordered phases a command's progress surface walks.
// labels is the phase list in execution order: fixed for build and pull, and
// declared once the run knows it for a command whose steps depend on what it
// finds (setSteps). renderIdx is the index of the render phase (the one that
// reports per-node progress and may be preceded by a plugin-compilation step); it
// is -1 for a command with no render phase. coldProfile is the up-front cold/warm
// guess for this build, used only to pick the global bar's weight profile. It
// does not gate the "compiling plugins" step: that step is revealed from whether
// a guest is observed to actually compile during the build, not from this guess.
//
// stepBar draws the global progress line for a spine with no render phase, from
// the counts its steps report (stepCount), each step one equal share of the bar.
// Build's bar is weighted by buildBarWeights instead, and pull draws none.
//
// Every fixed label is a lowercase phrase naming what its step does, short enough
// that its row, marker included, ends before rowBodyColumn, so every elapsed time
// stays in one column. A label naming something the user configured — a
// registry's host — is as long as that name, and its time follows it after one
// space.
type progressSpine struct {
	labels      []string
	renderIdx   int
	coldProfile bool
	stepBar     bool
}

// Phase indices into the build spine's labels. buildPhaseStep maps the shared
// build layer's stage vocabulary onto these as the run crosses each boundary;
// keeping them adjacent to the label slice keeps index and label single-sourced.
const (
	phaseLoadCatalog = iota
	phaseLockfile
	phaseClosure
	phaseValidate
	phaseInheritance
	phaseRender
)

// buildProgressSpine returns the ordered build phase spine. cold selects the
// bar's cold weight profile; the "compiling plugins" step's appearance is
// driven separately by an observed guest compilation, not by this flag.
func buildProgressSpine(cold bool) progressSpine {
	return progressSpine{
		labels: []string{
			phaseLoadCatalog: "loading catalog",
			phaseLockfile:    "resolving lockfile",
			phaseClosure:     "resolving dependencies",
			phaseValidate:    "validating",
			phaseInheritance: "resolving inheritance",
			phaseRender:      "rendering",
		},
		renderIdx:   phaseRender,
		coldProfile: cold,
	}
}

// compilePhaseLabel is the label of the cold-profile plugin-compilation step
// shown between the pre-render phases and the render phase.
const compilePhaseLabel = "compiling plugins"

// buildBarWeights assigns each build phase a fixed share of the global progress
// bar. The compiler cannot know a phase's true duration ahead of time, so the
// bar approximates progress from a typical build's phase-time profile rather
// than from measured work. Two profiles apply:
//
//   - Warm (compile cache already populated): the warm profile now carries its
//     own compile share for the stale-cache recompile case — a build whose cache
//     directory existed but lacked the exact guest this build needs. When no
//     guest actually recompiles, that share is credited as settled work once
//     rendering begins. Lockfile dominates the pre-render span and render
//     dominates the rest.
//   - Cold (a one-time provider-guest compilation is due): the plugin-
//     compilation step dominates, since compiling a large guest is the single
//     longest phase of a first build; every other phase is a thin sliver.
//
// The shares within a profile sum to slightly LESS than 1.0. The small remainder
// is credited only when the whole build completes (artifacts written), so the
// bar reaches 100% at finish rather than the instant the render loop ends. The
// bar value at any moment is the sum of completed phases' shares plus the active
// phase's share scaled by its internal fraction (render: nodes done / total;
// plugin-compilation: guests compiled plus a bounded time ramp within the guest
// currently compiling; every other phase has no sub-progress and credits its
// full share only on completion).
type buildBarWeights struct {
	loadCatalog float64
	lockfile    float64
	closure     float64
	validate    float64
	inheritance float64
	// compile is the plugin-compilation step's share. In the warm profile it
	// covers a stale-cache recompile and, when no guest actually compiles, is
	// credited as settled work once rendering begins.
	compile float64
	render  float64
}

// buildBarWeightsFor returns the phase-share table for the cold or warm profile.
// Cold: after the lockfile the bar sits near 4%, the compilation step fills it to
// roughly 90%, render carries it to about 93%, and finish completes it to 100%.
// Warm: after the lockfile the bar sits near 22%; render entry credits the
// compile share to ~54% when no guest recompiled; render carries it to ~94%; and
// finish completes it to 100%.
func buildBarWeightsFor(cold bool) buildBarWeights {
	if cold {
		return buildBarWeights{
			loadCatalog: 0.01,
			lockfile:    0.03,
			closure:     0.005,
			validate:    0.003,
			inheritance: 0.002,
			compile:     0.85,
			render:      0.03,
		}
	}
	return buildBarWeights{
		loadCatalog: 0.02,
		lockfile:    0.20,
		closure:     0.01,
		validate:    0.005,
		inheritance: 0.005,
		compile:     0.30,
		render:      0.40,
	}
}

// pre returns the share of a pre-render phase by its spine index.
func (w buildBarWeights) pre(i int) float64 {
	switch i {
	case phaseLoadCatalog:
		return w.loadCatalog
	case phaseLockfile:
		return w.lockfile
	case phaseClosure:
		return w.closure
	case phaseValidate:
		return w.validate
	case phaseInheritance:
		return w.inheritance
	}
	return 0
}

const (
	// compileRampEstimate shapes the time-based sub-progress the global bar shows
	// while a single provider guest is compiling. It is a display estimate only —
	// the phase self-times and snaps to its full share the instant the guest
	// actually finishes — chosen near a typical cold guest-compilation wall-clock
	// so the ramp reads as steady advancement rather than a stall or a jump.
	compileRampEstimate = 20 * time.Second
	// compileRampCap bounds the time ramp below 1 so the bar never claims a guest
	// finished compiling before its completion callback fires.
	compileRampCap = 0.95
)

// progressReporter renders a command's per-phase progress to a diagnostic sink
// as a phase checklist. Its shape is resolved once at construction (see
// progressMode). Every write lands on the diagnostic sink; the reporter never
// touches stdout, so the compiler's byte-deterministic rendered output is
// untouched by the presence or shape of progress.
//
// The checklist ticks each phase as it completes and spins the currently-active
// phase. A "compiling plugins" step appears before the render phase when the
// eager pass observes a guest actually compiling; a build served entirely from
// the cache omits it. The render phase carries an X/Y node counter. A separate
// global progress line below the rows shows a profile-weighted whole-build
// completion bar. The plain surface walks the same steps and writes each one's
// line once, as it completes.
//
// A command with no render phase walks plain steps the same way, and a step may
// count its own items (stepCount): its row then carries the same X/Y counter the
// render phase does, and a spine that asks for it (stepBar) draws the global
// progress line from those counts. Commentary written while the checklist is
// live goes through beside, so it lands above the block rather than under a
// redraw.
type progressReporter struct {
	// mu guards every state mutation and every write to out, so the background
	// spinner ticker (checklist mode only) and the main build thread never race
	// on reporter state or interleave writes on the diagnostic sink.
	mu sync.Mutex

	out   io.Writer
	mode  progressMode
	spine progressSpine

	// weights is the profile-selected phase-share table backing the global
	// progress bar. Fixed at construction from the spine's profile (cold or
	// warm), so the bar's calibration is decided up front.
	weights buildBarWeights

	// colorOff suppresses the dim ANSI on the elapsed field when the
	// process-global color-off decision is set, and always on the plain surface,
	// which writes no escape sequence at all. The elapsed value and its column
	// alignment are preserved; only the dim escape sequences are dropped.
	colorOff bool

	// Spinner ticker lifecycle, live only in checklist mode. The ticker starts
	// lazily on the first draw and drives periodic redraws so the active phase's
	// spinner animates even when no phase-transition event occurs. tickerDone
	// stops the goroutine; tickerWG lets finish wait for it to exit before the
	// final draw so the ticker never redraws after finish.
	tickerStarted bool
	tickerDone    chan struct{}
	tickerWG      sync.WaitGroup

	// started is the whole-run wall clock, set at the first phase and used for
	// the debug total. phaseStart marks the current phase for per-phase timing.
	started    time.Time
	phaseStart time.Time

	// Per-phase timing for the elapsed column the checklist and the plain surface
	// both write. phaseClock records when each row's phase began, keyed by phase
	// index (0..renderIdx for the spine phases, compileTimerKey for the
	// plugin-compilation step). phaseFrozen captures a phase's final elapsed once
	// it completes, so a done row shows a stable time while the active row's time
	// still advances on each redraw.
	phaseClock  map[int]time.Time
	phaseFrozen map[int]time.Duration

	// active is the index into spine.labels of the phase currently in flight, or
	// -1 before the first phase. Phases at a lower index are done; the active one
	// spins; higher indices are pending.
	active int
	// inRenderStage is true once the render step has begun (the first node
	// completed or the first guest began compiling), which marks every pre-render
	// phase done.
	inRenderStage bool

	// compileShown is true once a real guest compilation has been observed this
	// build, so the "compiling plugins" step joins the checklist.
	// compileActive spins it; compileDone ticks it once every guest has compiled.
	compileShown  bool
	compileActive bool
	compileDone   bool

	// Plugin-compilation phase progress. guestsTotal is the number of distinct
	// provider guests the eager compile pass will compile, guestsCompiled the
	// running completed count, and compileStart the phase's start instant — the
	// anchor for the time-based sub-progress the bar shows while a single guest
	// is compiling. All three are cold-profile checklist/static/debug state; they
	// stay zero on a silent or warm reporter.
	guestsTotal    int
	guestsCompiled int
	compileStart   time.Time

	// renderTotal is the node count the render phase walks, captured on the first
	// renderNode call. renderDone is the running completed count. renderNext is
	// the next debug throttle threshold so a large graph does not emit one debug
	// line per node.
	renderTotal int
	renderDone  int
	renderNext  int

	// counts holds, keyed by spine index, how far a step that counts its own work
	// has got: how many of how many items it has finished. A step is present once it
	// knows its total, so a step still finding out what it has to do shows no count
	// and only its spinner.
	counts map[int]stepProgress

	// stepEnded is true once the step in flight was ended by the operation that
	// began it (endStep), before any other began: the run is then waiting on
	// nothing, and the step's row shows it done rather than spinning.
	stepEnded bool
	// stepFailed holds, keyed by spine index, the steps their operation ended
	// with a failure, whose rows keep the pending marker.
	stepFailed map[int]bool
	// held is true while the drawn block is erased for a person (hold) and not yet
	// drawn again.
	held bool
	// rowsFrom is the index of the first spine step the checklist draws. A block
	// left standing once its last step ended (endStep) has scrolled into the
	// output for good, so the next step draws a block of its own beneath whatever
	// was written since, beginning at that step.
	rowsFrom int

	spin int
	// linesDrawn is the number of lines in the last checklist block, so the next
	// redraw can move the cursor up over them.
	linesDrawn int
	// plainWritten records which steps the plain surface has written the line
	// for, keyed as phaseClock is, so every step's line is written exactly once.
	plainWritten map[int]bool
	// finished guards finish against a double call: the caller invokes it
	// explicitly before the stdout summary and again via defer on any return.
	finished bool

	// waitNote is what the step in flight is waiting on — a registry that asked
	// to slow down, say — shown beside that step's row while waitsOpen is above
	// zero (beginWait).
	waitNote  string
	waitsOpen int
}

// draws reports whether the reporter shows anything at all: false for a silent
// reporter, and for a nil one.
func (p *progressReporter) draws() bool {
	return p != nil && p.mode != progressSilent
}

// beginWait shows label as what the step in flight is waiting on, until the
// returned function is called. It is how an operation's own report of a wait
// (opprogress) reaches a command that draws its steps itself (stepWaitObserver):
// the wait is part of the step in flight rather than a step of its own, so it
// neither ends that step nor takes its count.
//
// On the checklist the label is drawn beside the row of the step in flight, the
// latest one shown where several waits are open at once; the plain surface writes
// it as one line when the wait begins, and the verbose debug layer as a debug
// line. A silent reporter shows nothing.
func (p *progressReporter) beginWait(label string) (end func()) {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.mode == progressSilent || p.finished:
		return func() {}
	case p.mode == progressDebug:
		fmt.Fprintf(p.out, "  [debug]   %s\n", label)
		return func() {}
	case p.mode == progressStatic:
		fmt.Fprintln(p.out, label)
		return func() {}
	}
	p.waitNote = label
	p.waitsOpen++
	p.draw()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.waitsOpen--
			if p.waitsOpen == 0 {
				p.waitNote = ""
			}
			if !p.finished && p.linesDrawn > 0 {
				p.draw()
			}
		})
	}
}

// newProgressReporter resolves the reporter shape from the diagnostic options,
// the command's phase spine, and what the sink is. It reads the same terminal
// decision the color surface reads, so the two cannot disagree about a stream.
//
//   - debug                      → the verbose timing layer, regardless of sink.
//   - quiet                      → silent.
//   - a drawing terminal         → the in-place checklist; plain lines under --plain.
//   - a terminal that cannot draw → plain lines.
//   - not a terminal, --progress → plain lines.
//   - not a terminal, auto       → silent.
func newProgressReporter(out io.Writer, opts buildDiagOptions, spine progressSpine) *progressReporter {
	mode := resolveProgressMode(out, opts)
	return &progressReporter{
		out:          out,
		mode:         mode,
		spine:        spine,
		weights:      buildBarWeightsFor(spine.coldProfile),
		colorOff:     opts.noColor || opts.plain || mode == progressStatic,
		active:       -1,
		plainWritten: map[int]bool{},
		phaseClock:   map[int]time.Time{},
		phaseFrozen:  map[int]time.Duration{},
		counts:       map[int]stepProgress{},
		stepFailed:   map[int]bool{},
	}
}

// stepProgress is how many of a step's items are finished, out of how many the
// step has to do.
type stepProgress struct {
	done, total int
}

// countedLabel is a counted step's name as every surface writes it: the step's
// label followed by its done/total counter. The render step's per-node counter and
// a step that counts its own items share it, so the two read alike.
func countedLabel(label string, done, total int) string {
	return fmt.Sprintf("%s %d/%d", label, done, total)
}

// setSteps declares the steps of a run whose steps are known only once it has
// started — one per registry an index walk visits, say — before the first of
// them begins. It is inert once a step has begun, so the rows already drawn never
// change their names.
func (p *progressReporter) setSteps(labels []string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active >= 0 {
		return
	}
	p.spine.labels = append([]string(nil), labels...)
}

// beginStep adds a step named label to the run's steps and makes it the step in
// flight, for a run whose steps are known only as each begins: an operation that
// reports the steps it waits on as it reaches them (operationObserver). The step
// in flight before it, if any, is over. It returns the new step's index, which
// endStep takes, or -1 where nothing is drawn.
func (p *progressReporter) beginStep(label string) int {
	if p == nil {
		return -1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == progressSilent || p.finished {
		return -1
	}
	p.spine.labels = append(p.spine.labels, label)
	i := len(p.spine.labels) - 1
	now := time.Now()
	if p.started.IsZero() {
		p.started = now
	}
	if p.mode == progressDebug {
		p.debugCloseAndOpen(i, now)
		p.stepEnded = false
		return i
	}
	if !p.stepEnded {
		p.completeStep(p.active)
	}
	p.active = i
	p.stepEnded = false
	p.startTimer(i)
	if p.mode == progressChecklist {
		p.draw()
	}
	return i
}

// endStep marks the step at index i over, where it is still the step in flight:
// its time stops and, on the plain surface, its line is written. The run then
// waits on nothing, so the checklist draws its block a last time and leaves it
// standing — whatever the command writes next lands beneath it rather than under
// a redraw — and a step begun later draws a block of its own below that.
// A step that failed keeps the pending marker rather than the tick, so its row
// never claims a completion the run did not reach; its time still stops.
// Ending a step that another has followed, or one already ended, does nothing.
func (p *progressReporter) endStep(i int, failed bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == progressSilent || p.finished || i < 0 || i != p.active || p.stepEnded {
		return
	}
	p.stepEnded = true
	if failed {
		p.stepFailed[i] = true
	}
	if p.mode == progressDebug {
		if !p.phaseStart.IsZero() {
			fmt.Fprintf(p.out, "  [debug] %-22s done in %s\n", p.spine.labels[i], time.Since(p.phaseStart).Round(time.Microsecond))
		}
		p.phaseStart = time.Time{}
		return
	}
	p.completeStep(i)
	if p.mode == progressChecklist {
		// A block held out of a person's way (hold) is drawn again here, finished,
		// beneath whatever the exchange left on the screen.
		if p.linesDrawn > 0 || p.held {
			p.draw()
		}
		p.linesDrawn = 0
		p.rowsFrom = len(p.spine.labels)
	}
}

// hold takes the checklist out of the way of a person about to read or type at
// the terminal — a question the command asks, a sign-in it waits on — by erasing
// the drawn block. Nothing is drawn again until the next step begins, or the
// step in flight ends, by which time the exchange is over. Where nothing is
// drawn in place it does nothing.
func (p *progressReporter) hold() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode != progressChecklist || p.finished || p.linesDrawn == 0 {
		return
	}
	p.rewind()
	fmt.Fprint(p.out, ansiClearBelow)
	p.linesDrawn = 0
	p.held = true
}

// stepCount records how far the step in flight has got: done of its total items
// finished. The step's row carries the count once a total is known — before that,
// only its spinner — and on the plain surface the step's one line carries the
// count it ended on. A step whose total is zero has nothing to count and shows
// none. The caller reports counts in the order its items are finished in the
// output, so a count never goes back. A silent reporter, the verbose debug layer,
// and a reporter with no step in flight ignore it.
func (p *progressReporter) stepCount(done, total int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == progressSilent || p.mode == progressDebug || p.active < 0 || total < 0 {
		return
	}
	p.counts[p.active] = stepProgress{done: done, total: total}
	if p.mode == progressChecklist {
		p.draw()
	}
}

// phaseLabel is the name of the spine step at index i as its row and its plain
// line write it: its label, followed by its counter once the step knows a total.
func (p *progressReporter) phaseLabel(i int) string {
	label := p.spine.labels[i]
	if c, ok := p.counts[i]; ok && c.total > 0 {
		return countedLabel(label, c.done, c.total)
	}
	return label
}

// stepFraction is the run's completion for a spine whose bar gives every step one
// equal share: each step before the one in flight is over, and the step in flight
// is as far along as its count says — over once it knows it has nothing to count.
// Unlike the build's bar it is not filled at finish: a run that stops part way
// leaves its bar where its count stopped, so the bar never claims work the run did
// not do.
func (p *progressReporter) stepFraction() float64 {
	n := len(p.spine.labels)
	if n == 0 || p.active < 0 {
		return 0
	}
	frac := float64(p.active)
	if c, ok := p.counts[p.active]; ok {
		if c.total > 0 {
			frac += float64(c.done) / float64(c.total)
		} else {
			frac++
		}
	}
	frac /= float64(n)
	if frac > 1 {
		frac = 1
	}
	return frac
}

// beside returns a writer for running commentary that goes to w while the
// reporter's surface is live. Each write — expected to be whole lines — lands
// above the checklist rather than through it: the drawn block is erased, the
// lines are written, and the block is drawn again beneath them. Without that, the
// next redraw's cursor rewind would climb over the commentary and overwrite it.
// Where nothing is drawn in place — a silent or plain surface, or a checklist not
// yet drawn or already finished — the write goes straight to w.
//
// w may be a different stream from the reporter's own: commentary on standard
// output beside a checklist on standard error reads in order on a terminal both
// reach, and a redirected standard output simply receives the lines.
func (p *progressReporter) beside(w io.Writer) io.Writer {
	return besideWriter{p: p, w: w}
}

// besideWriter is the writer beside returns.
type besideWriter struct {
	p *progressReporter
	w io.Writer
}

func (b besideWriter) Write(data []byte) (int, error) {
	p := b.p
	if p == nil || len(data) == 0 {
		return b.w.Write(data)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode != progressChecklist || p.finished || p.linesDrawn == 0 {
		return b.w.Write(data)
	}
	p.rewind()
	fmt.Fprint(p.out, ansiClearBelow)
	p.linesDrawn = 0
	n, err := b.w.Write(data)
	p.draw()
	return n, err
}

// resolveProgressMode applies the precedence ladder (debug > quiet > plain on a
// drawing terminal under --plain > checklist on a drawing terminal > plain on a
// terminal that cannot draw > forced plain > auto-silent) to pick the reporter's
// mode, from what outputTerminal says the sink is.
//
// The plain (accessible) mode downgrades what would be the interactive checklist
// on a terminal to plain lines — no spinner ticker, no cursor repaint — so
// screen readers receive stable line-oriented output. It does not turn the
// surface on where it would otherwise be silent (a non-terminal sink with no
// --progress stays silent); plain only removes animation where a terminal would
// have animated.
//
// A terminal whose console does not interpret escape sequences gets the same
// plain lines without being asked: a person is reading it, and the checklist's
// redraws would reach them as raw escape bytes and a reprinted block per tick.
func resolveProgressMode(out io.Writer, opts buildDiagOptions) progressMode {
	if opts.debug {
		return progressDebug
	}
	if opts.quiet {
		return progressSilent
	}
	switch outputTerminal(out) {
	case drawingTerminal:
		if opts.plain {
			return progressStatic
		}
		return progressChecklist
	case plainTerminal:
		return progressStatic
	}
	if opts.progress {
		return progressStatic
	}
	return progressSilent
}

const (
	// ansiClearLine erases from the cursor to the end of the line so a shorter
	// redraw never leaves stale characters from a longer prior label.
	ansiClearLine = "\x1b[K"
	// ansiClearBelow erases from the cursor to the end of the screen, so a block
	// lifted out of the way of commentary leaves none of its rows behind.
	ansiClearBelow = "\x1b[J"
	// ansiDim opens the SGR dim (faint) attribute; it is closed with ansiReset.
	// The per-phase elapsed field is drawn dim so it reads as secondary to the
	// phase label and bar.
	ansiDim = "\x1b[2m"
)

const (
	// progressBarWidth is the fixed cell count of the global progress bar: filled
	// cells drawn with '█', the remainder with '░'. Fixed so the bar occupies a
	// stable column across redraws regardless of the percentage.
	progressBarWidth = 18
	// rowBodyColumn is the column the dim elapsed field is padded out to, so the
	// elapsed times line up in a single right-hand column across every row that
	// carries one — the phase rows and the total-build-time line. It is wide
	// enough to clear the longest fixed body (the longest fixed-label phase row of
	// any spine, marker included); the render row carries no inline bar, so no row
	// needs room for one. A row longer than that keeps one space before its time.
	rowBodyColumn = 30
	// elapsedFieldWidth right-aligns the elapsed value within its own column so
	// single- and multi-digit second counts still align on their trailing 's'.
	elapsedFieldWidth = 6
)

// progressCaption prefixes the global progress line. Verbatim and load-bearing:
// the layout is asserted against this exact string.
const progressCaption = "Progress "

// terminalWidth reports the column count of w when it is an interactive
// terminal, or 0 when the width is unknown (w is not an *os.File, or the query
// fails). It is a package-level seam so the width-clamp branch is unit-testable
// without a real TTY; a 0 return disables clamping, which is the correct default
// for the buffer sinks tests drive.
var terminalWidth = func(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	cols, _, err := term.GetSize(int(f.Fd()))
	if err != nil || cols < 0 {
		return 0
	}
	return cols
}

var spinnerFrames = []rune{'|', '/', '-', '\\'}

// spinnerTickInterval is the redraw cadence of the checklist spinner ticker:
// often enough to read as smooth animation, seldom enough to stay off the busy
// path. It is a package var rather than a const so tests can shorten it to make
// the time-driven animation observable without a long wait.
var spinnerTickInterval = 100 * time.Millisecond

// markDone, markActive, and markPending are the checklist row prefixes.
const (
	markDone    = "[✓] " // the check mark is U+2713
	markPending = "[ ] "
)

func markActive(frame rune) string { return fmt.Sprintf("[%c] ", frame) }

// phase advances the reporter to the pre-render phase at index i and renders the
// surface. The caller passes phase indices in ascending order; phase closes the
// previous phase's timing (debug) and opens the next. A silent reporter returns
// immediately. Indices at or past the render phase are driven by renderNode and
// the plugin-compile callbacks instead, not by phase.
func (p *progressReporter) phase(i int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == progressSilent {
		return
	}
	now := time.Now()
	if p.started.IsZero() {
		p.started = now
	}
	if p.mode == progressDebug {
		p.debugCloseAndOpen(i, now)
		return
	}
	if i < 0 || i >= len(p.spine.labels) {
		return
	}
	// The render phase's detail (plugin-compilation step, per-node counter) is
	// driven by the plugin-compile / renderNode callbacks; phase only marks the
	// pre-render phases done and opens the render row.
	if p.spine.renderIdx >= 0 && i == p.spine.renderIdx {
		p.beginRenderStage()
		if p.mode == progressChecklist {
			p.draw()
		}
		return
	}
	p.completeStep(p.active)
	p.active = i
	p.startTimer(i)
	if p.mode == progressChecklist {
		p.draw()
	}
}

// pluginCompileBegin records that the eager plugin-compilation pass is about to
// compile total distinct provider guests, seeding the compilation step's guest
// count and self-timer. It does not reveal the step: whether the step becomes
// visible is decided later by pluginCompileRunning, from whether a guest is
// observed to actually compile this build — a populated cache directory does not
// mean the specific guest this build needs is already cached. A silent reporter,
// a spine with no render phase, or a zero total makes it a no-op. Called once,
// before the first guest is compiled.
func (p *progressReporter) pluginCompileBegin(total int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == progressSilent || p.spine.renderIdx < 0 || total <= 0 {
		return
	}
	p.guestsTotal = total
	p.compileStart = time.Now()
	// Seed the phase timer now so a later reveal reports the elapsed since the
	// compile pass actually began, not since the reveal.
	p.startTimer(compileTimerKey)
}

// pluginCompileRunning reveals the "compiling plugins" step because a provider
// guest is being compiled this build rather than served from the cache. The
// eager pass calls it once a guest's warm runs past the near-instant window a
// cache hit completes in, so the step appears only for a build that truly
// recompiles a guest — including a build whose cache directory already existed
// but lacked the exact guest bytes this build needs. It surfaces the step as the
// active checklist row, so the row spins and its self-timer advances for the rest
// of the compile. Idempotent across guests and inert on a reporter that never
// began the pass (silent, no render phase, or zero guests) or already ticked the
// step done. May be called from a timer goroutine.
func (p *progressReporter) pluginCompileRunning() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == progressSilent || p.spine.renderIdx < 0 || p.guestsTotal <= 0 {
		return
	}
	if p.compileShown || p.compileDone {
		return
	}
	p.beginRenderStage()
	p.compileShown = true
	p.compileActive = true
	if p.compileStart.IsZero() {
		p.compileStart = time.Now()
	}
	// Seed the phase timer before the mode branches so every surface — including
	// debug, whose completion line reads the frozen elapsed — has a start instant.
	p.startTimer(compileTimerKey)
	if p.mode == progressDebug {
		fmt.Fprintf(p.out, "  [debug] %-22s start\n", compilePhaseLabel)
		return
	}
	if p.mode == progressStatic {
		// The plain surface writes a step's line when the step completes.
		return
	}
	p.draw()
}

// pluginCompileGuestDone records that done of the total provider guests have
// finished compiling, advancing the plugin-compilation step. Once every guest is
// done the step ticks complete and freezes its elapsed. Inert on a reporter that
// never opened the step (warm, silent, or already done). Called once per guest,
// in ascending order, from the eager compile pass.
func (p *progressReporter) pluginCompileGuestDone(done int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.compileShown || p.compileDone {
		return
	}
	p.guestsCompiled = done
	complete := p.guestsTotal > 0 && done >= p.guestsTotal
	if complete {
		p.compileActive = false
		p.compileDone = true
		p.completeStep(compileTimerKey)
	} else {
		// Restart the ramp anchor so the next guest's time ramp begins at zero
		// rather than continuing from the phase start.
		p.compileStart = time.Now()
	}
	if p.mode == progressDebug {
		if complete {
			fmt.Fprintf(p.out, "  [debug] %-22s done in %s\n", compilePhaseLabel, p.elapsedStr(compileTimerKey))
		}
		return
	}
	if p.mode == progressChecklist {
		p.draw()
	}
}

// globalFraction is the whole-build completion the global progress bar shows, in
// [0,1]. It sums the shares of the phases that have completed and adds the active
// phase's share scaled by its internal fraction. Pre-render phases have no sub-
// progress, so each credits its full share only once the next phase begins; the
// plugin-compilation step and the render phase carry their own fractions. finish
// forces 1.0 so the bar completes only when the whole build does.
func (p *progressReporter) globalFraction() float64 {
	if p.finished {
		return 1
	}
	// Pre-render phases credited as done: those strictly before the active spinner
	// before render begins, and all of them once render has begun.
	doneThrough := p.active
	if p.inRenderStage {
		doneThrough = p.spine.renderIdx
	}
	var frac float64
	for i := 0; i < doneThrough && i < p.spine.renderIdx; i++ {
		frac += p.weights.pre(i)
	}
	if p.compileShown {
		switch {
		case p.compileDone:
			frac += p.weights.compile
		case p.compileActive:
			frac += p.weights.compile * p.compileFraction()
		}
	} else if p.renderTotal > 0 {
		// Render began without any guest observed compiling: the compile step's
		// share is settled work (nothing needed compiling this build), so credit it
		// in full. Gated on render actually starting, not on entering the render
		// stage, so a build that is about to observe a compile (the compile pass
		// runs after the render phase opens) is not credited prematurely.
		frac += p.weights.compile
	}
	if p.renderTotal > 0 {
		frac += p.weights.render * float64(p.renderDone) / float64(p.renderTotal)
	}
	if frac > 1 {
		frac = 1
	}
	if frac < 0 {
		frac = 0
	}
	return frac
}

// compileFraction estimates how far the active plugin-compilation step has
// progressed, in [0,1). Guests already compiled contribute their full per-guest
// share; within the guest currently compiling a bounded, time-based ramp advances
// the bar so a single long compile (the common case — one dominant provider
// guest) visibly climbs between completion events rather than freezing. The ramp
// is capped below the guest boundary so it never overtakes a real completion;
// actual completion snaps the whole step to its full share via compileDone.
func (p *progressReporter) compileFraction() float64 {
	if p.guestsTotal <= 0 {
		return 0
	}
	base := float64(p.guestsCompiled) / float64(p.guestsTotal)
	share := 1.0 / float64(p.guestsTotal)
	ramp := 0.0
	if !p.compileStart.IsZero() {
		ramp = time.Since(p.compileStart).Seconds() / compileRampEstimate.Seconds()
		if ramp > compileRampCap {
			ramp = compileRampCap
		}
	}
	f := base + ramp*share
	if f > 0.999 {
		f = 0.999
	}
	return f
}

// renderNode advances the render phase's per-node progress, called once per node
// as the render loop completes it. done is the 1-based count of nodes rendered so
// far, total is the graph's node count, and nodeID names the node just finished.
// The total is captured on the first call. The first call also ticks the
// pre-render phases (and the plugin-compilation step, if shown) done and makes the
// render phase active. A silent reporter, or a zero/negative total, returns
// immediately.
func (p *progressReporter) renderNode(done, total int, nodeID string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == progressSilent || total <= 0 || p.spine.renderIdx < 0 {
		return
	}
	first := p.renderTotal == 0
	if first {
		p.renderTotal = total
		p.renderNext = renderThrottleStep(total)
		p.startTimer(p.spine.renderIdx)
	}
	p.beginRenderStage()
	p.compileActive = false
	if p.compileShown {
		p.compileDone = true
		p.completeStep(compileTimerKey)
	}
	p.renderDone = done

	if p.mode == progressDebug {
		p.debugRenderNode(done, nodeID)
		return
	}
	if p.mode == progressChecklist {
		p.draw()
	}
	// The plain surface writes nothing per node: the render step's single line,
	// carrying the final count, is written when the step completes.
}

// beginRenderStage marks the transition out of the pre-render phases: every phase
// before the render phase is done and none of them is the active spinner. It
// completes the phase being left so its row stops advancing.
func (p *progressReporter) beginRenderStage() {
	if p.started.IsZero() {
		p.started = time.Now()
	}
	p.completeStep(p.active)
	p.inRenderStage = true
}

// compileTimerKey is the phaseClock / phaseFrozen key for the plugin-compilation
// step, chosen outside the non-negative spine-index range so it never collides
// with a phase index.
const compileTimerKey = -2

// startTimer records the start instant of a row's phase the first time it
// becomes active, so its elapsed can be reported. A no-op after the first call
// for that key.
func (p *progressReporter) startTimer(key int) {
	if p.phaseClock == nil {
		return
	}
	if _, ok := p.phaseClock[key]; !ok {
		p.phaseClock[key] = time.Now()
	}
}

// freezeTimer captures a phase's final elapsed when it completes, so its row
// shows a stable time thereafter. Idempotent, and a no-op for a phase that never
// started (never became active).
func (p *progressReporter) freezeTimer(key int) {
	if p.phaseClock == nil {
		return
	}
	st, ok := p.phaseClock[key]
	if !ok {
		return
	}
	if _, done := p.phaseFrozen[key]; done {
		return
	}
	p.phaseFrozen[key] = time.Since(st)
}

// elapsedStr returns the dim-column value for a row's phase: the frozen elapsed
// for a completed phase, the live elapsed for the active one, or "" for a phase
// that has not started (no time is shown on a pending row).
func (p *progressReporter) elapsedStr(key int) string {
	if p.phaseClock == nil {
		return ""
	}
	if d, ok := p.phaseFrozen[key]; ok {
		return formatPhaseElapsed(d)
	}
	if st, ok := p.phaseClock[key]; ok {
		return formatPhaseElapsed(time.Since(st))
	}
	return ""
}

// formatPhaseElapsed renders a per-phase wall-clock for the checklist elapsed
// column as seconds to one decimal ("0.4s", "5.3s"). Like the build-summary
// duration it is intentionally nondeterministic; it lands only on the diagnostic
// sink and is never part of the rendered artifacts.
func formatPhaseElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// progressBar composes the global progress line's bar and whole-number
// percentage from the whole-build completion fraction: a fixed-width run of
// filled '█' and empty '░' cells reflecting the fraction, then the percentage,
// e.g. "████████████░░░░░░ 62%". The fraction is clamped to [0,1]; both the fill
// and the percentage floor, so the bar shows 100% only at a true fraction of 1.0
// (finish), never a rounded-up 100% mid-build.
func progressBar(frac float64) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * progressBarWidth)
	if filled > progressBarWidth {
		filled = progressBarWidth
	}
	pct := int(frac * 100)
	return fmt.Sprintf("%s%s %d%%",
		strings.Repeat("█", filled),
		strings.Repeat("░", progressBarWidth-filled),
		pct)
}

// composeRow assembles a checklist row from its visible body (marker, label, and
// any bar) and its elapsed value. The elapsed is padded to a common column so the
// times line up in a single right-hand column across rows; an empty elapsed (a
// not-yet-started phase) yields the bare body. The field is drawn dim unless the
// process-global color-off decision is set, in which case the value and its
// column alignment are preserved but the dim escapes are dropped. The dim escapes
// carry no display width, so the row's visible width is the body plus the padded
// elapsed field either way — which the redraw path uses to keep its line
// bookkeeping exact.
func (p *progressReporter) composeRow(body, elapsed string) string {
	if elapsed == "" {
		return body
	}
	pad := rowBodyColumn - utf8.RuneCountInString(body)
	if pad < 1 {
		pad = 1
	}
	field := fmt.Sprintf("%*s", elapsedFieldWidth, elapsed)
	if !p.colorOff {
		field = ansiDim + field + ansiReset
	}
	return body + strings.Repeat(" ", pad) + field
}

// clampVisibleWidth truncates s so its visible width does not exceed max
// columns, leaving ANSI escape sequences intact (they carry no width) and
// closing with a reset when a truncation occurred, so a cut can never leave a
// dangling attribute. A non-positive max disables clamping. The redraw path uses
// it to guarantee no row wraps past the terminal width: a wrapped row would
// occupy more physical lines than the logical row count the cursor-up rewind is
// computed from, desynchronising the redraw into a reprint.
func clampVisibleWidth(s string, max int) string {
	if max <= 0 {
		return s
	}
	var b strings.Builder
	visible := 0
	truncated := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		if visible >= max {
			truncated = true
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		visible++
		i += size
	}
	if truncated {
		b.WriteString(ansiReset)
	}
	return b.String()
}

// note emits a supplementary debug line for the current phase — a count or a
// tier observation that is cheap at the boundary. It is a no-op unless the
// verbose debug layer is active.
func (p *progressReporter) note(format string, args ...any) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode != progressDebug {
		return
	}
	fmt.Fprintf(p.out, "  [debug]   %s\n", fmt.Sprintf(format, args...))
}

// renderThrottleFraction bounds how often a debug sink logs a render-progress
// line: at most once per this fraction of the total node count, so a large graph
// emits a handful of periodic lines rather than one per node. A checklist sink is
// unaffected — it redraws in place every node — and the plain surface writes the
// render step's one line when the step completes.
const renderThrottleFraction = 10

// renderThrottleStep is the non-redraw logging stride for a total node count: one
// tenth of the total, floored at one so a small graph still logs at least one
// interim line before the guaranteed final count.
func renderThrottleStep(total int) int {
	step := total / renderThrottleFraction
	if step < 1 {
		step = 1
	}
	return step
}

// draw renders the full checklist block in place, moving the cursor up over the
// previously drawn block first so the surface redraws rather than scrolls.
func (p *progressReporter) draw() {
	p.ensureTicker()
	rows := p.rows()
	// Clamp each row to the terminal width so no row wraps onto a second physical
	// line. A wrapped row would occupy more physical lines than the logical row
	// count linesDrawn tracks, so the next redraw's cursor-up rewind would
	// under-shoot the block top and the surface would reprint instead of redraw.
	// Clamping keeps physical lines == logical rows == linesDrawn, so the rewind
	// is always exact. Width 0 (unknown, or a non-terminal sink) disables it.
	width := terminalWidth(p.out)
	p.rewind()
	for _, row := range rows {
		fmt.Fprintf(p.out, "%s%s\n", clampVisibleWidth(row, width), ansiClearLine)
	}
	p.linesDrawn = len(rows)
	p.held = false
	p.spin++
}

// rewind moves the cursor back to the start of the drawn block's first row, so
// what is written next lands where the block began. It is the one place the
// block's height is climbed — by draw before a redraw, and by beside before it
// erases the block — so the climb stays as exact as linesDrawn is.
func (p *progressReporter) rewind() {
	if p.linesDrawn > 0 {
		fmt.Fprintf(p.out, "\x1b[%dA", p.linesDrawn)
	}
	fmt.Fprint(p.out, "\r")
}

// ensureTicker starts the background spinner ticker the first time a checklist
// block is drawn. The ticker redraws the current block on a fixed cadence so the
// active phase's spinner animates between phase-transition events; it advances
// only the spinner frame, never the phase or render state. It is a no-op outside
// checklist mode and after it has already started. Called only from draw, which
// holds the lock, so the started flag is safe to check and set here.
func (p *progressReporter) ensureTicker() {
	if p.mode != progressChecklist || p.tickerStarted {
		return
	}
	p.tickerStarted = true
	p.tickerDone = make(chan struct{})
	interval := spinnerTickInterval
	p.tickerWG.Add(1)
	go func() {
		defer p.tickerWG.Done()
		tk := time.NewTicker(interval)
		defer tk.Stop()
		for {
			select {
			case <-p.tickerDone:
				return
			case <-tk.C:
				p.mu.Lock()
				if !p.finished && p.linesDrawn > 0 {
					p.draw()
				}
				p.mu.Unlock()
			}
		}
	}()
}

// rows builds the checklist lines for the current state: the pre-render phases,
// then the optional plugin-compilation step, then the render phase (with its X/Y
// counter), and — for a spine with a render phase, or one whose steps count their
// own work — the global progress line. Each
// phase row carries a done / active / pending marker; the progress line carries
// none. The completion summary (total build time, files written) lands on stdout
// once the build finishes, not in this diagnostic block, so a completed checklist
// stays as the live surface above that single stdout summary.
//
// The whole block is composed here so linesDrawn (set by draw to len(rows)) is
// always the exact physical-line count of the block, keeping the in-place redraw
// rewind exact as the block grows (the compile step appears).
func (p *progressReporter) rows() []string {
	frame := spinnerFrames[p.spin%len(spinnerFrames)]
	preCount := len(p.spine.labels)
	if p.spine.renderIdx >= 0 {
		preCount = p.spine.renderIdx
	}
	rows := make([]string, 0, len(p.spine.labels)+4)

	for i := p.rowsFrom; i < preCount; i++ {
		label := p.phaseLabel(i)
		if i == p.active && p.waitNote != "" && !p.stepEnded {
			label += " — " + p.waitNote
		}
		rows = append(rows, p.composeRow(p.phaseRow(i, frame)+label, p.elapsedStr(i)))
	}
	if p.spine.renderIdx < 0 {
		// No render phase: every label is an ordinary spine phase, and there is no
		// node fraction to show. The global progress line is drawn only for a spine
		// whose steps count their own work (stepBar); pull's carries none.
		if p.spine.stepBar {
			rows = append(rows, progressCaption+progressBar(p.stepFraction()))
		}
		return rows
	}
	if p.compileShown {
		// The plugin-compilation step has no node count, so it keeps the spinner and
		// shows only its elapsed — never a fabricated percentage bar.
		marker := markDone
		if p.compileActive && !p.compileDone {
			marker = markActive(frame)
		}
		rows = append(rows, p.composeRow(marker+compilePhaseLabel, p.elapsedStr(compileTimerKey)))
	}
	rows = append(rows, p.composeRow(p.renderRowBody(frame), p.elapsedStr(p.spine.renderIdx)))
	// The global progress line: the whole-build completion fraction as a fixed-
	// width bar and whole-number percent on its own line below the phase rows —
	// profile-weighted so it climbs through every phase, not just render.
	rows = append(rows, progressCaption+progressBar(p.globalFraction()))
	return rows
}

// phaseRow returns the marker for a pre-render (or pull) phase at index i.
//
// A step that counts its own items is ticked only once its count reaches its
// total. One the run left short of it — a walk stopped part way — keeps the
// pending marker, as the render row does for a render that never finished, so
// its row never claims a completion its count denies. A step its operation ended
// with a failure (endStep) keeps it for the same reason.
func (p *progressReporter) phaseRow(i int, frame rune) string {
	switch {
	case p.stepFailed[i]:
		return markPending
	case p.finished:
		if (i <= p.active || p.inRenderStage) && !p.countShort(i) {
			return markDone
		}
		return markPending
	case p.inRenderStage:
		return markDone
	case i < p.active:
		if p.countShort(i) {
			return markPending
		}
		return markDone
	case i == p.active && p.stepEnded:
		return markDone
	case i == p.active:
		return markActive(frame)
	default:
		return markPending
	}
}

// countShort reports whether the step at index i counts its own items and has
// finished fewer of them than its total. A step that counts nothing is never
// short, so build's and pull's steps tick exactly as they always have.
func (p *progressReporter) countShort(i int) bool {
	c, ok := p.counts[i]
	return ok && c.total > 0 && c.done < c.total
}

// renderRowBody returns the render-phase checklist row body (marker, label, and
// — once a node count is known — the done/total counter). The percentage bar
// lives on the global progress line, not here; the elapsed field is added by
// composeRow, not here.
func (p *progressReporter) renderRowBody(frame rune) string {
	label := p.renderLabel()
	rendered := p.renderDone > 0 || p.inRenderStage
	switch {
	case p.finished && rendered:
		return markDone + label
	case !p.finished && rendered && !p.compileActive:
		return markActive(frame) + label
	default:
		// Not yet reached, or finished before the render step ran (an early error):
		// leave it pending rather than fake a completion.
		return markPending + label
	}
}

// renderLabel is the render step's name as both surfaces write it: the render
// phase's label, followed by the done/total counter once a node count is known.
func (p *progressReporter) renderLabel() string {
	label := p.spine.labels[p.spine.renderIdx]
	if p.renderTotal > 0 {
		label = countedLabel(label, p.renderDone, p.renderTotal)
	}
	return label
}

// stepLabel names the step a timer key stands for, as its row names it: a spine
// phase by phaseLabel, the plugin-compilation step by compilePhaseLabel, and the
// render phase by renderLabel. It is "" for a key that stands for no step, such
// as the -1 of a reporter no phase has reached yet.
func (p *progressReporter) stepLabel(key int) string {
	switch {
	case key == compileTimerKey:
		return compilePhaseLabel
	case key >= 0 && key == p.spine.renderIdx:
		return p.renderLabel()
	case key >= 0 && key < len(p.spine.labels):
		return p.phaseLabel(key)
	}
	return ""
}

// completeStep marks the step under key complete. It freezes the step's elapsed
// so every surface reports one stable time for it, and on the plain surface it
// writes the step's line: the step's name and that elapsed, in the column the
// checklist uses, with no escape sequence, so a redirected or non-drawing stream
// reads one line per completed step and nothing is ever redrawn. A key that
// stands for no step, or a step whose line is already written, writes nothing.
func (p *progressReporter) completeStep(key int) {
	p.freezeTimer(key)
	if p.mode != progressStatic || p.plainWritten[key] {
		return
	}
	label := p.stepLabel(key)
	if label == "" {
		return
	}
	p.plainWritten[key] = true
	fmt.Fprintln(p.out, p.composeRow(label, p.elapsedStr(key)))
}

// debugCloseAndOpen closes the previous phase's timing and opens phase i in the
// verbose debug layer.
func (p *progressReporter) debugCloseAndOpen(i int, now time.Time) {
	if !p.phaseStart.IsZero() && p.active >= 0 && p.active < len(p.spine.labels) {
		fmt.Fprintf(p.out, "  [debug] %-22s done in %s\n", p.spine.labels[p.active], now.Sub(p.phaseStart).Round(time.Microsecond))
	}
	p.phaseStart = now
	p.active = i
	if i >= 0 && i < len(p.spine.labels) {
		fmt.Fprintf(p.out, "  [debug] %-22s start\n", p.spine.labels[i])
	}
}

// debugRenderNode emits a throttled per-node line naming the node in the verbose
// debug layer.
func (p *progressReporter) debugRenderNode(done int, nodeID string) {
	label := p.spine.labels[p.spine.renderIdx]
	final := done >= p.renderTotal
	if done < p.renderNext && !final {
		return
	}
	for p.renderNext <= done {
		p.renderNext += renderThrottleStep(p.renderTotal)
	}
	fmt.Fprintf(p.out, "  [debug]   %s %d/%d (%s)\n", label, done, p.renderTotal, nodeID)
}

// finish finalizes the surface: it ticks the active phase done and redraws the
// completed checklist (leaving it in place so a completed run stays visible above
// the stdout summary), and in the verbose debug layer closes the final phase
// timing and prints the whole-run total. It is called once when the run completes
// or errors, so the progress surface never lingers as an in-flight spinner beside
// later output.
func (p *progressReporter) finish() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.mode == progressSilent || p.finished {
		p.mu.Unlock()
		return
	}
	p.finished = true
	done := p.tickerDone
	p.mu.Unlock()

	// Stop the spinner ticker and wait for its goroutine to exit before the final
	// draw, so it can never redraw after finish. The signal-and-wait happens with
	// the lock released: a tick blocked on the lock drains, observes finished, and
	// exits without drawing, and finish cannot deadlock against it.
	if done != nil {
		close(done)
		p.tickerWG.Wait()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if p.mode == progressDebug {
		if !p.phaseStart.IsZero() && p.active >= 0 && p.active < len(p.spine.labels) {
			fmt.Fprintf(p.out, "  [debug] %-22s done in %s\n", p.spine.labels[p.active], now.Sub(p.phaseStart).Round(time.Microsecond))
		}
		if !p.started.IsZero() {
			fmt.Fprintf(p.out, "  [debug] %-22s %s\n", "run total", now.Sub(p.started).Round(time.Microsecond))
		}
		return
	}
	if p.mode == progressChecklist && p.linesDrawn > 0 {
		// Freeze any still-running timers so the completed checklist shows stable
		// times: the active pre-render phase (an early error before render), the
		// render phase, and the plugin-compilation step. The final redraw leaves the
		// block in place above the stdout completion summary. A build's bar lands at
		// 100% there (globalFraction returns 1 once finished); a bar drawn from its
		// steps' counts (stepFraction) stays where those counts stopped, so a walk
		// stopped part way shows a bar short of full.
		p.freezeTimer(p.active)
		p.freezeTimer(p.spine.renderIdx)
		p.freezeTimer(compileTimerKey)
		p.draw()
		return
	}
	if p.mode == progressStatic {
		// The steps still open complete with the run, in the order the checklist
		// lists them, and each writes its line: the active pre-render phase (the
		// last step of a spine with no render phase), then the plugin-compilation
		// step, then the render phase. Which of them count as complete is the
		// checklist's own rule for its finished block, so the two surfaces report
		// the same steps. A counted step the run left short of its total still
		// writes its line, carrying the count it stopped at, which is what says it
		// did not finish where the checklist leaves its marker unticked.
		p.completeStep(p.active)
		if p.compileShown {
			p.completeStep(compileTimerKey)
		}
		if p.spine.renderIdx >= 0 && (p.renderDone > 0 || p.inRenderStage) {
			p.completeStep(p.spine.renderIdx)
		}
	}
}
