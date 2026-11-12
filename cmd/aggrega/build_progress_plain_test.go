// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
)

// driveBuildSteps walks a reporter through a whole build in the order the build
// reports it: every pre-render phase, the render phase opening, an observed
// plugin compilation, two rendered nodes, and the finish.
func driveBuildSteps(p *progressReporter) {
	p.phase(phaseLoadCatalog)
	p.phase(phaseLockfile)
	p.phase(phaseClosure)
	p.phase(phaseValidate)
	p.phase(phaseInheritance)
	p.phase(phaseRender)
	p.pluginCompileBegin(1)
	p.pluginCompileRunning()
	p.pluginCompileGuestDone(1)
	p.renderNode(1, 2, "api")
	p.renderNode(2, 2, "worker")
	p.finish()
}

// buildStepNames is every step driveBuildSteps completes, as each one's line names
// it, in the order the lines are written.
var buildStepNames = []string{
	"loading catalog",
	"resolving lockfile",
	"resolving dependencies",
	"validating",
	"resolving inheritance",
	"compiling plugins",
	"rendering 2/2",
}

// assertPlainSteps holds plain output to its contract: no escape byte and no
// carriage return anywhere, and exactly one line per completed step, in order,
// each the step's name followed by its elapsed time with the time ending in the
// column the checklist's times end in.
func assertPlainSteps(t *testing.T, got string, want []string) {
	t.Helper()
	if i := strings.IndexByte(got, 0x1b); i >= 0 {
		t.Errorf("plain output carries an escape byte at offset %d; got %q", i, got)
	}
	if strings.ContainsRune(got, '\r') {
		t.Errorf("plain output must never redraw (no carriage return); got %q", got)
	}
	if strings.ContainsRune(got, '[') {
		t.Errorf("plain output draws no checklist or spinner marker; got %q", got)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("plain output has %d lines, want one per completed step (%d); got %q", len(lines), len(want), got)
	}
	for i, name := range want {
		line := lines[i]
		if !regexp.MustCompile(`^` + regexp.QuoteMeta(name) + ` +\d+\.\ds$`).MatchString(line) {
			t.Errorf("line %d = %q, want the step %q followed by its elapsed time", i+1, line, name)
		}
		if w := utf8.RuneCountInString(line); w != rowBodyColumn+elapsedFieldWidth {
			t.Errorf("line %d = %q is %d columns wide, want its time to end in column %d", i+1, line, w, rowBodyColumn+elapsedFieldWidth)
		}
	}
}

// TestProgress_ThreeSinks_DrawingTerminalUnchanged is the drawing case of the
// three: on a terminal that interprets escape sequences the checklist still
// redraws in place, and its finished block is exactly the layout the checklist
// has always drawn — every row ticked, each elapsed time dim and right-aligned
// so it ends in column 36, and the whole-build bar at 100% — with the steps under
// their own names. Elapsed values are masked to T.Ts, the one byte range that
// legitimately varies between runs.
func TestProgress_ThreeSinks_DrawingTerminalUnchanged(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour // keep the async ticker from redrawing mid-test
	t.Cleanup(func() { spinnerTickInterval = orig })

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
	if p.mode != progressChecklist {
		t.Fatalf("a drawing terminal must resolve to the checklist; got mode %d", p.mode)
	}
	driveBuildSteps(p)

	got := regexp.MustCompile(`\d+\.\ds`).ReplaceAllString(buf.String(), "T.Ts")
	finalBlock := got[strings.LastIndex(got, "\x1b[8A"):]

	// The layout is written out literally rather than composed with the
	// production helpers, so a change to it fails here instead of being followed.
	row := func(body string) string {
		return body + strings.Repeat(" ", 30-utf8.RuneCountInString(body)) + "\x1b[2m  T.Ts\x1b[0m" + "\x1b[K\n"
	}
	want := "\x1b[8A\r" +
		row("[✓] loading catalog") +
		row("[✓] resolving lockfile") +
		row("[✓] resolving dependencies") +
		row("[✓] validating") +
		row("[✓] resolving inheritance") +
		row("[✓] compiling plugins") +
		row("[✓] rendering 2/2") +
		"Progress ██████████████████ 100%\x1b[K\n"
	if finalBlock != want {
		t.Errorf("finished checklist block differs from the drawn layout:\n got  %q\n want %q", finalBlock, want)
	}
	if strings.Count(got, "\x1b[") <= strings.Count(finalBlock, "\x1b[") {
		t.Errorf("the checklist must have redrawn in place before finishing; got %q", got)
	}
}

// TestProgress_ThreeSinks_ConsoleThatCannotDraw is the Windows case of the three,
// driven through the console seam: a console that refuses to interpret escape
// sequences is still a terminal a person reads, so with no flag at all it gets
// plain lines — one per completed step, no escape byte, no redraw — and no color
// either, because color reads the same decision.
func TestProgress_ThreeSinks_ConsoleThatCannotDraw(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal, platformConsole and
	// preparedConsoles seams other tests read.
	console := consoleFile(t)
	withTerminals(t, console)
	withConsole(t, (&fakeConsole{modes: map[uintptr]uint32{}, setErr: errors.New("an older console refuses the mode")}).control())
	prepareTerminals(console)

	p := newProgressReporter(console, buildDiagOptions{}, buildProgressSpine(false))
	if p.mode != progressStatic {
		t.Fatalf("a console that cannot draw must resolve to plain lines; got mode %d", p.mode)
	}
	driveBuildSteps(p)

	written, err := os.ReadFile(console.Name())
	if err != nil {
		t.Fatalf("read what the console received: %v", err)
	}
	assertPlainSteps(t, string(written), buildStepNames)

	if c := newColorizer(colorAuto, console, false); c.header("X") != "X" {
		t.Errorf("a console that cannot draw must receive no color either; got %q", c.header("X"))
	}
}

// TestProgress_ThreeSinks_NotATerminal is the redirected case of the three: a
// stream nobody reads at a terminal, with the surface forced on, gets the same
// plain lines, and with the surface left on auto gets nothing at all.
func TestProgress_ThreeSinks_NotATerminal(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var forced bytes.Buffer
	driveBuildSteps(newProgressReporter(&forced, buildDiagOptions{progress: true}, buildProgressSpine(false)))
	assertPlainSteps(t, forced.String(), buildStepNames)

	var auto bytes.Buffer
	driveBuildSteps(newProgressReporter(&auto, buildDiagOptions{}, buildProgressSpine(false)))
	if auto.Len() != 0 {
		t.Errorf("auto on a stream nobody reads at a terminal must stay silent; got %q", auto.String())
	}
}

// TestProgress_Plain_PullStepsOnce pins the plain pull surface: every one of the
// pull's steps writes its line once, when it completes, the last of them when
// the run finishes — and no two lines share a name.
func TestProgress_Plain_PullStepsOnce(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{progress: true}, pullProgressSpine())
	for i := range pullProgressSpine().labels {
		p.phase(i)
	}
	p.finish()

	assertPlainSteps(t, buf.String(), pullProgressSpine().labels)
	seen := map[string]bool{}
	for _, label := range pullProgressSpine().labels {
		if seen[label] {
			t.Errorf("two pull steps share the name %q", label)
		}
		seen[label] = true
	}
}

// TestProgress_Plain_StepsNeverReachedWriteNoLine pins what a run that stops
// partway writes: a line for each step it reached — the one in flight when it
// stopped included, which the checklist's finished block ticks too — and nothing
// for a step it never reached.
func TestProgress_Plain_StepsNeverReachedWriteNoLine(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	withTTY(t, false)

	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{progress: true}, buildProgressSpine(false))
	p.phase(phaseLoadCatalog)
	p.phase(phaseLockfile)
	p.finish() // the run ends during the lockfile step

	assertPlainSteps(t, buf.String(), []string{"loading catalog", "resolving lockfile"})
}

// TestProgressSpines_OneNameOneCase holds every step label of both commands to
// the spine's naming rule: written in lowercase, each name used by one step of
// its command only, and short enough that its row, marker included, ends before
// the elapsed column.
func TestProgressSpines_OneNameOneCase(t *testing.T) {
	t.Parallel()
	spines := map[string][]string{
		"build": append(append([]string{}, buildProgressSpine(false).labels...), compilePhaseLabel),
		"pull":  pullProgressSpine().labels,
	}
	for command, labels := range spines {
		seen := map[string]bool{}
		for _, label := range labels {
			if label != strings.ToLower(label) {
				t.Errorf("%s step %q is not lowercase like every other step", command, label)
			}
			if seen[label] {
				t.Errorf("%s has two steps named %q", command, label)
			}
			seen[label] = true
			if w := utf8.RuneCountInString(markDone + label); w >= rowBodyColumn {
				t.Errorf("%s step %q makes a %d-column row, which reaches the elapsed column at %d", command, label, w, rowBodyColumn)
			}
		}
	}
}

// stepLineCounts counts, for each name, the lines of out that are that step's
// plain line: the name at the start of the line followed by its elapsed time.
func stepLineCounts(out string, names []string) map[string]int {
	counts := map[string]int{}
	for _, name := range names {
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` +\d+\.\ds$`)
		counts[name] = len(re.FindAllString(out, -1))
	}
	return counts
}

// TestBuild_PlainProgress_EveryStepOnce drives a real build with the surface
// forced onto a stream nobody reads at a terminal and holds what it wrote to the
// plain contract: no escape byte, and every step the build walks named on
// exactly one line. The compiling-plugins step appears only when a guest is
// observed compiling, so it may be absent, but never twice.
func TestBuild_PlainProgress_EveryStepOnce(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	root := t.TempDir()
	writeProgressBuildFixture(t, root)
	withTTY(t, false)

	catalogRoot := filepath.Join(root, "catalog")
	releaseYAML := filepath.Join(root, "release.yaml")
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releaseYAML, CatalogDirs: []string{catalogRoot}, DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true}}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releaseYAML, []string{catalogRoot}, false, buildDiagOptions{progress: true}, rootedBuildOptions(t, root, buildops.RunOptions{}), &stderr, &stdout); err != nil {
		t.Fatalf("build failed: %v (stderr=%q)", err, stderr.String())
	}

	got := stderr.String()
	if i := strings.IndexByte(got, 0x1b); i >= 0 {
		t.Errorf("plain build output carries an escape byte at offset %d; got %q", i, got)
	}
	counts := stepLineCounts(got, []string{"loading catalog", "resolving lockfile", "resolving dependencies", "validating", "resolving inheritance", "compiling plugins"})
	for _, name := range []string{"loading catalog", "resolving lockfile", "resolving dependencies", "validating", "resolving inheritance"} {
		if counts[name] != 1 {
			t.Errorf("step %q is named on %d lines, want exactly 1; got %q", name, counts[name], got)
		}
	}
	if counts["compiling plugins"] > 1 {
		t.Errorf("the compiling-plugins step is named on %d lines, want at most 1; got %q", counts["compiling plugins"], got)
	}
	if n := len(regexp.MustCompile(`(?m)^rendering \d+/\d+ +\d+\.\ds$`).FindAllString(got, -1)); n != 1 {
		t.Errorf("the render step is named on %d lines, want exactly 1; got %q", n, got)
	}
	if !strings.Contains(stdout.String(), "Total build time:") || !strings.Contains(stdout.String(), "Build completed:") {
		t.Errorf("the final result must still reach stdout; got %q", stdout.String())
	}
}
