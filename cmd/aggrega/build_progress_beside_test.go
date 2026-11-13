// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
)

// terminalSink is one stream standing for a terminal both of a command's output
// streams reach, as a person running it sees them: what the command writes to
// standard output and what its checklist draws on standard error interleave in
// the order they were written.
type terminalSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *terminalSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *terminalSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// replayTerminal plays raw output through a minimal screen and returns the rows
// a person would see once it has all been written, trailing blanks trimmed. It
// knows what the progress checklist writes: carriage return, line feed (which,
// as a terminal shows output, also returns to the first column), cursor up,
// erase to the end of the line, and erase below. Colour and every other escape
// sequence move nothing and are skipped. Rows never wrap, which is the screen a
// sink with no width gives: the checklist clamps nothing on it either.
func replayTerminal(raw string) []string {
	var screen [][]rune
	row, col := 0, 0
	ensure := func() {
		for len(screen) <= row {
			screen = append(screen, nil)
		}
	}
	runes := []rune(raw)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '\r':
			col = 0
			continue
		case '\n':
			row++
			col = 0
			continue
		case '\x1b':
			if i+1 >= len(runes) || runes[i+1] != '[' {
				continue
			}
			j := i + 2
			for j < len(runes) && (runes[j] < '@' || runes[j] > '~') {
				j++
			}
			if j >= len(runes) {
				i = j
				continue
			}
			param := string(runes[i+2 : j])
			switch runes[j] {
			case 'A':
				n, err := strconv.Atoi(param)
				if err != nil || n < 1 {
					n = 1
				}
				row -= n
				if row < 0 {
					row = 0
				}
			case 'K':
				ensure()
				if col < len(screen[row]) {
					screen[row] = screen[row][:col]
				}
			case 'J':
				ensure()
				if col < len(screen[row]) {
					screen[row] = screen[row][:col]
				}
				screen = screen[:row+1]
			}
			i = j
			continue
		}
		ensure()
		for len(screen[row]) <= col {
			screen[row] = append(screen[row], ' ')
		}
		screen[row][col] = r
		col++
	}
	rows := make([]string, 0, len(screen))
	for _, line := range screen {
		rows = append(rows, strings.TrimRight(string(line), " "))
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

// checklistRowLabel returns the step a checklist row names — the row with its
// marker taken off — and whether the row is a checklist row at all.
func checklistRowLabel(row string) (string, bool) {
	trimmed := strings.TrimLeft(row, " ")
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	end := strings.Index(trimmed, "] ")
	if end < 0 || end > len("[✓]") {
		return "", false
	}
	return trimmed[end+2:], true
}

// rowsNaming counts the checklist rows naming step label, and the index of the
// first of them.
func rowsNaming(rows []string, label string) (count, first int) {
	first = -1
	for i, row := range rows {
		named, ok := checklistRowLabel(row)
		if !ok || !strings.HasPrefix(named, label) {
			continue
		}
		if first < 0 {
			first = i
		}
		count++
	}
	return count, first
}

// rowsStartingWith counts the rows beginning with prefix, and the index of the
// first of them.
func rowsStartingWith(rows []string, prefix string) (count, first int) {
	first = -1
	for i, row := range rows {
		if !strings.HasPrefix(row, prefix) {
			continue
		}
		if first < 0 {
			first = i
		}
		count++
	}
	return count, first
}

// TestLinesWrittenBesideTheChecklistLandAboveIt drives a build and a pull on a
// terminal both of their streams reach, and reads the screen a person is left
// with: every step's row shows once, and each line the command writes while its
// checklist is drawn — the dependency fill's completion line, the per-entity
// resolution report — shows whole, above the block.
//
// A line written beneath a drawn checklist without lifting the block first is
// climbed over by the next redraw, which starts one row too low: the line is
// overwritten and the block's top rows are left standing above it, so a step
// shows twice and the line not at all. Each row below fails on that.
//
// Not parallel: it mutates the process-global fileTerminal and spinnerTickInterval
// seams other tests read.
func TestLinesWrittenBesideTheChecklistLandAboveIt(t *testing.T) {
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour
	t.Cleanup(func() { spinnerTickInterval = orig })
	withTTY(t, true)

	for _, tc := range []struct {
		name string
		// run drives the command against a fresh fixture at root, both streams
		// written to sink.
		run func(t *testing.T, root string, sink *terminalSink) error
		// steps are the checklist rows the command draws, each once; the first is
		// the block's top row.
		steps []string
		// beside is the line the command writes while its block is drawn, which
		// must show once, above the block.
		beside string
		// report, when set, is the header of a report whose rows follow it and must
		// each show: the number of rows is the count the header names.
		report string
	}{
		{
			name: "a build that fills its lockfile",
			run: func(t *testing.T, root string, sink *terminalSink) error {
				return dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					filepath.Join(root, "release.yaml"), []string{filepath.Join(root, "catalog")}, false,
					buildDiagOptions{}, buildops.RunOptions{AutoPull: true}, sink, sink)
			},
			steps:  []string{"loading catalog", "resolving lockfile", "resolving dependencies", "validating", "resolving inheritance", "rendering"},
			beside: "Pull complete: ",
		},
		{
			name: "a build that fills its lockfile and reports what the fill did",
			run: func(t *testing.T, root string, sink *terminalSink) error {
				return dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					filepath.Join(root, "release.yaml"), []string{filepath.Join(root, "catalog")}, false,
					buildDiagOptions{}, buildops.RunOptions{AutoPull: true, Detail: true}, sink, sink)
			},
			steps:  []string{"loading catalog", "resolving lockfile", "resolving dependencies", "validating", "resolving inheritance", "rendering"},
			beside: "Pull complete: ",
			report: "resolved ",
		},
		{
			name: "a pull reporting what it did",
			run: func(t *testing.T, root string, sink *terminalSink) error {
				return pullRun(context.Background(), pullConfig{
					ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					ReleasePath: filepath.Join(root, "release.yaml"),
					CatalogDirs: []string{filepath.Join(root, "catalog")},
					Detail:      true,
					DiagOut:     sink,
					Out:         sink,
				})
			},
			steps:  []string{"loading catalog", "resolving aggregate", "resolving dependencies", "validating", "writing lockfile"},
			report: "resolved ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			writeProgressBuildFixture(t, root)

			var sink terminalSink
			if err := tc.run(t, root, &sink); err != nil {
				t.Fatalf("the run failed: %v\noutput:\n%q", err, sink.String())
			}
			rows := replayTerminal(sink.String())
			screen := strings.Join(rows, "\n")

			// The control: the block was drawn in place, so the screen is a redrawn
			// checklist rather than a log of plain lines that could not go wrong.
			if !strings.Contains(sink.String(), "\x1b[") {
				t.Fatalf("nothing was drawn in place, so this run cannot show a line drawn over:\n%q", sink.String())
			}

			_, top := rowsNaming(rows, tc.steps[0])
			for _, step := range tc.steps {
				if n, _ := rowsNaming(rows, step); n != 1 {
					t.Errorf("the step %q shows on %d rows, want 1; the screen:\n%s", step, n, screen)
				}
			}
			if tc.beside != "" {
				n, at := rowsStartingWith(rows, tc.beside)
				if n != 1 {
					t.Errorf("the line beginning %q shows on %d rows, want 1; the screen:\n%s", tc.beside, n, screen)
				} else if top >= 0 && at > top {
					t.Errorf("the line beginning %q shows below the checklist (row %d, the block from row %d); the screen:\n%s", tc.beside, at, top, screen)
				}
			}
			if tc.report != "" {
				n, at := rowsStartingWith(rows, tc.report)
				if n != 1 {
					t.Fatalf("the report header %q shows on %d rows, want 1; the screen:\n%s", tc.report, n, screen)
				}
				fields := strings.Fields(rows[at])
				if len(fields) < 2 {
					t.Fatalf("the report header %q names no count; the screen:\n%s", rows[at], screen)
				}
				want, err := strconv.Atoi(fields[1])
				if err != nil || want < 1 {
					t.Fatalf("the report header %q names no count of rows; the screen:\n%s", rows[at], screen)
				}
				for i := 1; i <= want; i++ {
					if at+i >= len(rows) {
						t.Fatalf("the report names %d rows and the screen ends after %d of them:\n%s", want, i-1, screen)
					}
					row := rows[at+i]
					if _, isStep := checklistRowLabel(row); isStep || !strings.HasPrefix(row, "  ") {
						t.Errorf("row %d of the report reads %q, not a report row; the screen:\n%s", i, row, screen)
					}
				}
				if top >= 0 && at > top {
					t.Errorf("the report shows below the checklist (row %d, the block from row %d); the screen:\n%s", at, top, screen)
				}
			}
		})
	}
}

// TestReplayTerminalFollowsTheChecklistsMoves holds the screen model the test
// above reads to the moves it has to follow, including the one the defect makes:
// a line written beneath a drawn block, then a redraw climbing the block's
// height, leaves the block's top row standing and the line overwritten.
func TestReplayTerminalFollowsTheChecklistsMoves(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "a block redrawn in place",
			raw:  "\r[ ] a\x1b[K\n[ ] b\x1b[K\n\x1b[2A\r[✓] a\x1b[K\n[✓] b\x1b[K\n",
			want: []string{"[✓] a", "[✓] b"},
		},
		{
			name: "a line lifted above the block",
			raw:  "\r[ ] a\x1b[K\n[ ] b\x1b[K\n\x1b[2A\r\x1b[Jdone\n\r[✓] a\x1b[K\n[✓] b\x1b[K\n",
			want: []string{"done", "[✓] a", "[✓] b"},
		},
		{
			name: "a line written beneath the block, then a redraw",
			raw:  "\r[ ] a\x1b[K\n[ ] b\x1b[K\ndone\n\x1b[2A\r[✓] a\x1b[K\n[✓] b\x1b[K\n",
			want: []string{"[ ] a", "[✓] a", "[✓] b"},
		},
		{
			name: "colour moves nothing",
			raw:  "\x1b[2m0.1s\x1b[0m x\n",
			want: []string{"0.1s x"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := replayTerminal(tc.raw); strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("replayTerminal(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
