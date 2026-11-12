// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// firstRunLookup answers only from vars, so a resolution in these tests reads a
// fixed environment: no global config path, no home directory to find one under,
// and only the overrides a case asks for.
func firstRunLookup(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// setFileTerminal points the terminal-detection seam at a fixed answer for the
// duration of one test, and restores it afterwards.
func setFileTerminal(t *testing.T, tty bool) {
	t.Helper()
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(io.Writer) bool { return tty }
}

// TestFirstRunConfigNotice walks the grid the notice is gated on: whether a
// configuration file contributed, and whether the error stream is a terminal.
// Both cells of both axes are present, plus the environment-only case, which is
// the one the two axes alone do not settle.
func TestFirstRunConfigNotice(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	cases := []struct {
		name string
		// treeConfig, when non-empty, is written as the workspace config file.
		treeConfig string
		env        map[string]string
		terminal   bool
		wantNotice bool
	}{
		{
			name:       "no configuration file and a terminal",
			terminal:   true,
			wantNotice: true,
		},
		{
			name:       "no configuration file and a redirected error stream",
			terminal:   false,
			wantNotice: false,
		},
		{
			name:       "a configuration file and a terminal",
			treeConfig: "default_registry: tree.example\n",
			terminal:   true,
			wantNotice: false,
		},
		{
			name:       "a configuration file and a redirected error stream",
			treeConfig: "default_registry: tree.example\n",
			terminal:   false,
			wantNotice: false,
		},
		{
			name:       "an environment override but still no configuration file",
			env:        map[string]string{"AGGREGA_OFFLINE": "true"},
			terminal:   true,
			wantNotice: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setFileTerminal(t, tc.terminal)

			root := t.TempDir()
			if tc.treeConfig != "" {
				if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(tc.treeConfig), 0o644); err != nil {
					t.Fatalf("write workspace config: %v", err)
				}
			}
			rw, err := workspaceconfig.Resolve(root, "", firstRunLookup(tc.env))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			var buf bytes.Buffer
			firstRunConfigNotice(&buf, rw)

			got := buf.String()
			switch {
			case tc.wantNotice && !strings.Contains(got, configInitInvocation):
				t.Errorf("notice: got %q; want a line naming %q", got, configInitInvocation)
			case tc.wantNotice && strings.Count(got, "\n") != 1:
				t.Errorf("notice: got %q; want exactly one line", got)
			case !tc.wantNotice && got != "":
				t.Errorf("notice: got %q; want nothing written", got)
			}
		})
	}
}

// TestFirstRunConfigNoticeOnANilResolution: a caller with no resolution to speak
// about writes nothing rather than crashing, so a failure earlier in a command's
// wiring cannot turn into a panic on this path.
func TestFirstRunConfigNoticeOnANilResolution(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	setFileTerminal(t, true)

	var buf bytes.Buffer
	firstRunConfigNotice(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("notice on a nil resolution: got %q; want nothing written", buf.String())
	}
}

// TestFirstRunConfigNoticeNamesACommandTheTreeCarries pins the sentence's command
// to the tree: the notice tells a reader to run something, and a spelling no
// command answers to would be advice that fails when taken.
func TestFirstRunConfigNoticeNamesACommandTheTreeCarries(t *testing.T) {
	t.Parallel()

	var paths []string
	var found bool
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() {
			paths = append(paths, c.CommandPath())
			if c.CommandPath() == configInitInvocation {
				found = true
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd())

	if !found {
		t.Errorf("configInitInvocation %q names no runnable command; the tree carries %q",
			configInitInvocation, paths)
	}
}

// firstRunNoticeOpening is how the first-run line opens, written out rather than
// composed, for counting it in a captured error stream.
const firstRunNoticeOpening = "No configuration file found; run '"

// TestTheFirstRunNoticeIsWrittenOnlyByAWorkspaceScopedCommand runs commands
// through the real command tree on a machine with no configuration file, at what
// the notice takes for a terminal, from a folder no workspace resolves from, and
// counts the first-run line on the process's error stream: once for each command
// whose result depends on the workspace, never for one whose result does not —
// though each of those reads the configuration too, which is the state the line
// is written from — and never for a command that resolves nothing.
//
// 🔴 PLANTED DEFECT: drop the withholdFirstRunNotice call from the root command,
// and every machine-scoped row that reads its configuration fails.
//
// Not parallel: it moves the process working directory, sets the process
// environment, mutates the process-global terminal seam, and the line is written
// to the process's error stream.
func TestTheFirstRunNoticeIsWrittenOnlyByAWorkspaceScopedCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		says bool
	}{
		{args: []string{"validate"}, says: true},
		{args: []string{"cache", "status"}, says: true},
		{args: []string{"search", "moo"}, says: true},
		{args: []string{"config", "show"}},
		{args: []string{"plugin", "list"}},
		{args: []string{"registry", "list"}},
		{args: []string{"explain", "E0007"}},
		{args: []string{"version"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			setFileTerminal(t, true)
			hometest.Isolate(t)
			t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
			t.Setenv("AGGREGA_OFFLINE", "true")
			t.Chdir(folderNoWorkspaceResolvesFrom(t))

			var out string
			stderr := captureStderr(t, func() { out, _ = runAggrega(t, tc.args...) })
			count := strings.Count(stderr, firstRunNoticeOpening)
			switch {
			case tc.says && count != 1:
				t.Errorf("aggrega %s wrote the first-run line %d times, want once\nstderr:\n%s\noutput:\n%s",
					strings.Join(tc.args, " "), count, stderr, out)
			case !tc.says && count != 0:
				t.Errorf("aggrega %s, whose result does not depend on the workspace, wrote the first-run line:\n%s",
					strings.Join(tc.args, " "), stderr)
			}
		})
	}
}

// TestTheCommandTheFirstRunNoticeNamesNeverShowsIt holds the one fact the root
// command's rule relies on for the seeding command: it is not workspace-scoped,
// so its reader is never told to run what they are already running. Declaring it
// workspace-scoped would fail here rather than in front of that reader.
func TestTheCommandTheFirstRunNoticeNamesNeverShowsIt(t *testing.T) {
	t.Parallel()
	if workspaceScopedCommand(configInitInvocation) {
		t.Errorf("%q is declared workspace-scoped, so the first-run line would tell its reader to run it", configInitInvocation)
	}
}

// TestFirstRunNoticeIsWrittenOncePerResolver drives the resolution chokepoint
// itself: a command body that reads its configuration several times sees the
// notice once, because the resolver writes it inside the same once that caches
// the resolution.
func TestFirstRunNoticeIsWrittenOncePerResolver(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	setFileTerminal(t, true)

	var buf bytes.Buffer
	l := &lazyWorkspace{
		flagRoot: t.TempDir(),
		lookup:   firstRunLookup(nil),
		errOut:   &buf,
	}
	for i := 0; i < 3; i++ {
		if _, err := l.get(); err != nil {
			t.Fatalf("get (read %d): %v", i+1, err)
		}
	}

	if n := strings.Count(buf.String(), configInitInvocation); n != 1 {
		t.Errorf("three configuration reads wrote the notice %d times; want 1: %q", n, buf.String())
	}
}

// TestFirstRunNoticeIsWithheldWhenMarked: the invocation the notice names shows
// no notice, so a reader running the seeding command is not told to run it.
func TestFirstRunNoticeIsWithheldWhenMarked(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal seam other tests read.
	setFileTerminal(t, true)

	ctx := withWorkspaceResolver(context.Background(), t.TempDir(), firstRunLookup(nil))
	withholdFirstRunNotice(ctx)

	l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace)
	if !ok {
		t.Fatal("withWorkspaceResolver attached no resolver")
	}
	var buf bytes.Buffer
	l.errOut = &buf

	if _, err := l.get(); err != nil {
		t.Fatalf("get: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a withheld invocation wrote %q; want nothing", buf.String())
	}
}
