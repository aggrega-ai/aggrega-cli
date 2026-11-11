// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cursorUp matches the escape sequence that moves the cursor up over drawn rows.
var cursorUp = regexp.MustCompile(`\x1b\[\d+A`)

// holdSpinner keeps the checklist's ticker from redrawing during a test, so what
// a test reads is what the events it drove drew and nothing else.
func holdSpinner(t *testing.T) {
	t.Helper()
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour
	t.Cleanup(func() { spinnerTickInterval = orig })
}

// TestAnOperationsStepsAreDrawnOnTheReporterBuildDrawsOn drives the steps an
// operation reports through the observer every waiting command installs, on each
// shape the one reporter resolves to, and reads what each shape wrote.
func TestAnOperationsStepsAreDrawnOnTheReporterBuildDrawsOn(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal and spinnerTickInterval
	// seams other tests read.
	holdSpinner(t)
	cases := []struct {
		name string
		tty  bool
		opts buildDiagOptions
		// check reads everything written, and mark the length written once the first
		// step ended.
		check func(t *testing.T, all string, mark int)
	}{
		{
			name: "a terminal draws each step's row, ticks one that finished, and leaves one that failed unticked",
			tty:  true,
			opts: buildDiagOptions{noColor: true},
			check: func(t *testing.T, all string, mark int) {
				first, rest := all[:mark], all[mark:]
				if !strings.Contains(first, "[✓] reading the first thing") {
					t.Errorf("the finished step's row is not ticked; got %q", first)
				}
				if strings.Contains(rest, "reading the first thing") {
					t.Errorf("the step that ended was drawn again under the next one; got %q", rest)
				}
				// The second step's own block may be redrawn in place once drawn; what
				// must not happen is its first drawing climbing over the first step's.
				firstDraw, _, _ := strings.Cut(rest, "\n")
				if cursorUp.MatchString(firstDraw) {
					t.Errorf("the next step climbed back over the block the first one left standing; its first drawing was %q", firstDraw)
				}
				if !strings.Contains(rest, "[ ] reading the second thing") {
					t.Errorf("the failed step's row must keep the pending marker; got %q", rest)
				}
				if strings.Contains(rest, "[✓] reading the second thing") {
					t.Errorf("the failed step's row claims a completion; got %q", rest)
				}
			},
		},
		{
			name: "a sink that cannot redraw gets one plain line per step as it ends",
			tty:  false,
			opts: buildDiagOptions{progress: true},
			check: func(t *testing.T, all string, _ int) {
				if strings.Contains(all, "\x1b") {
					t.Errorf("plain lines carry no escape sequence; got %q", all)
				}
				lines := strings.Split(strings.TrimRight(all, "\n"), "\n")
				if len(lines) != 2 || !strings.HasPrefix(lines[0], "reading the first thing") ||
					!strings.HasPrefix(lines[1], "reading the second thing") {
					t.Errorf("want exactly one line per step, in order; got %q", all)
				}
			},
		},
		{
			name: "a sink that is not a terminal shows nothing",
			tty:  false,
			check: func(t *testing.T, all string, _ int) {
				if all != "" {
					t.Errorf("nothing is shown where standard error is not a terminal; got %q", all)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTTY(t, tc.tty)
			var buf bytes.Buffer
			p := newProgressReporter(&buf, tc.opts, operationProgressSpine())
			obs := operationObserver{p: p}
			obs.Begin("reading the first thing")(nil)
			mark := buf.Len()
			obs.Begin("reading the second thing")(errors.New("the registry did not answer"))
			p.finish()
			tc.check(t, buf.String(), mark)
		})
	}
}

// TestHoldClearsTheBlockForAPerson pins that the observer's hold erases the drawn
// block, so a sign-in address or a question printed next is not drawn over.
func TestHoldClearsTheBlockForAPerson(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal and spinnerTickInterval
	// seams other tests read.
	withTTY(t, true)
	holdSpinner(t)
	var buf bytes.Buffer
	p := newProgressReporter(&buf, buildDiagOptions{noColor: true}, operationProgressSpine())
	obs := operationObserver{p: p}
	end := obs.Begin("signing")
	mark := buf.Len()
	obs.Yield()
	if got := buf.String()[mark:]; got != "\x1b[1A\r"+ansiClearBelow {
		t.Errorf("hold must climb over the one drawn row and clear below it; wrote %q", got)
	}
	mark = buf.Len()
	end(nil)
	p.finish()
	if !strings.Contains(buf.String()[mark:], "[✓] signing") {
		t.Errorf("the step ended after the hold is shown done; got %q", buf.String()[mark:])
	}
}

// TestAWaitingCommandShowsTheStepsItsOperationReports runs plugin add through the
// real command tree at a terminal and reads the steps it drew while it looked a
// plugin up, searched the registry's repositories for it, and listed its versions.
func TestAWaitingCommandShowsTheStepsItsOperationReports(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal, spinnerTickInterval and
	// plugin registry seams other tests read.
	withTTY(t, true)
	holdSpinner(t)
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/governance/rego"}}, nil)
	withTagLister(t, []string{"1.0.0"})
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\ndefault_registry: hub.aggrega.ai\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runAggrega(t, "--no-color", "--workspace-root", root,
		"plugin", "add", "governance", "reg", "--role", "render-verdict", "--dry-run")
	if err != nil {
		t.Fatalf("plugin add: %v\n%s", err, out)
	}
	for _, step := range []string{
		"[✓] looking up governance plugin reg on hub.aggrega.ai",
		`[✓] searching every repository on hub.aggrega.ai for governance plugins matching "reg"`,
		"[✓] listing the versions of hub.aggrega.ai/plugin/governance/rego",
	} {
		if !strings.Contains(out, step) {
			t.Errorf("the command did not show the step %q; got %q", step, out)
		}
	}
}

// TestConfigInitHonoursTheOfflineFlag pins that the seeding command lays the
// command line's settings over the workspace it resolves off the shared path, so
// --offline reaches the trust-policy read as it reaches every other command's.
func TestConfigInitHonoursTheOfflineFlag(t *testing.T) {
	offline := true
	ctx := withWorkspaceResolver(t.Context(), t.TempDir(), nil, workspaceconfig.FlagOverlay{Offline: &offline})
	rw, err := configInitWorkspace(ctx)
	if err != nil {
		t.Fatalf("configInitWorkspace: %v", err)
	}
	if !rw.Config().Offline {
		t.Error("--offline on the command line did not reach the workspace config init seeds from")
	}
}
