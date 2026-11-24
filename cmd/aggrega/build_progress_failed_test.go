// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
)

// failedBlock plays a checklist reporter's output through a screen and returns
// the rows a person is left reading, with elapsed times masked.
func failedBlock(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	rows := replayTerminal(buf.String())
	mask := regexp.MustCompile(` +\d+\.\ds`)
	for i := range rows {
		rows[i] = strings.TrimRight(mask.ReplaceAllString(rows[i], " T.Ts"), " ")
	}
	return rows
}

// TestProgress_FailedRun_BarStopsAndFailedStepIsNamed pins the final screen of a
// build an authoring error stopped: the bar stays at the fraction the run had
// reached rather than filling to 100%, the step the run stopped in carries the
// failure mark, and the steps it never reached stay pending.
func TestProgress_FailedRun_BarStopsAndFailedStepIsNamed(t *testing.T) {
	// Not parallel: mutates process-global fileTerminal, spinnerTickInterval seams other tests read.
	withTTY(t, true)
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour
	t.Cleanup(func() { spinnerTickInterval = orig })

	t.Run("a pre-render step", func(t *testing.T) {
		var buf bytes.Buffer
		p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
		p.phase(phaseLoadCatalog)
		p.phase(phaseLockfile)
		p.finishFailed()

		got := failedBlock(t, &buf)
		var bar string
		for _, r := range got {
			if strings.HasPrefix(r, progressCaption) {
				bar = r
			}
		}
		if strings.Contains(bar, "100%") || bar == "" {
			t.Errorf("a stopped run's bar = %q, want the fraction reached and not 100%%; screen %q", bar, got)
		}
		want := []string{
			"[✓] loading catalog T.Ts",
			"[✗] resolving lockfile T.Ts",
			"[ ] resolving dependencies",
			"[ ] validating",
			"[ ] resolving inheritance",
			"[ ] rendering",
		}
		if len(got) != len(want)+1 {
			t.Fatalf("screen = %q, want the six step rows and the bar", got)
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("row %d = %q, want %q", i, got[i], w)
			}
		}
	})

	t.Run("the render step", func(t *testing.T) {
		var buf bytes.Buffer
		p := newProgressReporter(&buf, buildDiagOptions{}, buildProgressSpine(false))
		for _, ph := range []int{phaseLoadCatalog, phaseLockfile, phaseClosure, phaseValidate, phaseInheritance, phaseRender} {
			p.phase(ph)
		}
		p.renderNode(1, 4, "api")
		p.finishFailed()

		got := failedBlock(t, &buf)
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, "[✗] rendering 1/4") {
			t.Errorf("the render step must carry the failure mark; screen %q", got)
		}
		if strings.Contains(joined, "100%") {
			t.Errorf("a stopped run's bar must not read 100%%; screen %q", got)
		}
	})
}

// TestProgress_FinishFailed_AfterFinishChangesNothing pins that a run already
// finished stays as it was drawn, and that a run that completes still ends at 100%.
func TestProgress_FinishFailed_AfterFinishChangesNothing(t *testing.T) {
	p := newProgressReporter(&bytes.Buffer{}, buildDiagOptions{progress: true}, buildProgressSpine(false))
	driveBuildSteps(p)
	p.finishFailed()
	if f := p.globalFraction(); !approxEq(f, 1) {
		t.Errorf("fraction after a completed run = %v, want 1", f)
	}
}
