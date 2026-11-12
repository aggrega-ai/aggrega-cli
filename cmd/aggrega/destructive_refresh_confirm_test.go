// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestConfirmDestructiveRefresh exercises the shared gate that fronts a bare
// dependency refresh: consent given in advance bypasses it without reading, a
// non-interactive stdin without that consent refuses rather than blocking, and an
// interactive answer decides.
//
// The empty-reply row is the one that matters most: a bare newline must decline,
// so the destructive path needs a deliberate answer and an accidental return
// reaches the safe outcome.
func TestConfirmDestructiveRefresh(t *testing.T) {
	// Not parallel: mutates the process-global isTerminal seam other tests read.
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })

	for _, tc := range []struct {
		name       string
		terminal   bool
		assumeYes  bool
		reply      string
		wantErr    bool
		wantPrompt bool
	}{
		{name: "consent in advance bypasses without a terminal", terminal: false, assumeYes: true},
		{name: "consent in advance bypasses on a terminal too", terminal: true, assumeYes: true},
		{name: "no terminal and no consent refuses", terminal: false, wantErr: true},
		{name: "interactive y consents", terminal: true, reply: "y\n", wantPrompt: true},
		{name: "interactive yes consents", terminal: true, reply: "yes\n", wantPrompt: true},
		{name: "interactive n declines", terminal: true, reply: "n\n", wantErr: true, wantPrompt: true},
		{name: "interactive empty reply declines", terminal: true, reply: "\n", wantErr: true, wantPrompt: true},
		{name: "interactive unrecognised reply declines", terminal: true, reply: "sure\n", wantErr: true, wantPrompt: true},
		{name: "closed stdin on a terminal declines rather than hanging", terminal: true, reply: "", wantErr: true, wantPrompt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isTerminal = func() bool { return tc.terminal }
			var out bytes.Buffer
			err := confirmDestructiveRefresh(&out, strings.NewReader(tc.reply), "aggrega pull", tc.assumeYes)
			if tc.wantErr && err == nil {
				t.Fatalf("expected a refusal, got nil (reply %q)", tc.reply)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected consent, got: %v", err)
			}
			if got := strings.Contains(out.String(), "[y/N]"); got != tc.wantPrompt {
				t.Errorf("prompt printed = %v, want %v (output %q)", got, tc.wantPrompt, out.String())
			}
		})
	}
}

// TestDestructiveRefreshRefusalNamesTheBypass pins that the refusal a caller with
// no terminal receives names the flag that is its only remedy. A refusal that
// merely says no leaves a non-interactive caller with nothing to do.
func TestDestructiveRefreshRefusalNamesTheBypass(t *testing.T) {
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })
	isTerminal = func() bool { return false }

	err := confirmDestructiveRefresh(io.Discard, strings.NewReader(""), "aggrega pull", false)
	if err == nil {
		t.Fatal("a non-interactive refresh without consent should refuse")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the refusal does not name the bypass a non-interactive caller needs: %v", err)
	}
}

// TestDestructiveRefreshIsHomogeneousAcrossCommands is the homogeneity assertion:
// `aggrega build --upgrade` and a bare `aggrega pull --upgrade` are the same act,
// so they must ask the same question and refuse a caller that cannot be asked on
// the same terms.
//
// It drives the real command tree rather than the helper, because what is being
// pinned is that both commands REACH the gate — a helper both call proves they
// agree about the sentence and nothing about whether either one runs it. The
// Release path names no file that exists: the gate sits ahead of workspace
// resolution, so reaching the confirmation is itself the evidence, and a run that
// got past it would fail later on the missing Release instead.
func TestDestructiveRefreshIsHomogeneousAcrossCommands(t *testing.T) {
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })

	const missingRelease = "does-not-exist.yaml"

	t.Run("both refuse a caller that cannot be asked, naming only their own command", func(t *testing.T) {
		isTerminal = func() bool { return false }

		refusals := map[string]string{}
		for _, argv := range [][]string{
			{"pull", "--release", missingRelease, "--upgrade"},
			{"build", "--release", missingRelease, "--upgrade"},
		} {
			root := newRootCmd()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(argv)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v: a bare refresh on a non-interactive stdin should refuse", argv)
			}
			refusals[argv[0]] = err.Error()
		}

		for name, want := range map[string]string{
			"pull":  "aggrega pull: refusing to drop every recorded pin on a non-interactive stdin without --yes",
			"build": "aggrega build: refusing to drop every recorded pin on a non-interactive stdin without --yes",
		} {
			if refusals[name] != want {
				t.Errorf("%s refusal:\n got %q\nwant %q", name, refusals[name], want)
			}
		}

		// The two differ by the command label and by nothing else. Comparing the
		// remainder is what makes this a homogeneity assertion rather than two
		// independent wordings that happen to look alike today.
		pullTail := strings.TrimPrefix(refusals["pull"], "aggrega pull:")
		buildTail := strings.TrimPrefix(refusals["build"], "aggrega build:")
		if pullTail != buildTail {
			t.Errorf("the two commands refuse differently:\n pull %q\nbuild %q", pullTail, buildTail)
		}
	})

	t.Run("both ask the same question", func(t *testing.T) {
		isTerminal = func() bool { return true }

		questions := map[string]string{}
		for _, argv := range [][]string{
			{"pull", "--release", missingRelease, "--upgrade"},
			{"build", "--release", missingRelease, "--upgrade"},
		} {
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(io.Discard)
			// Declining keeps the run from reaching the missing Release, so what
			// the buffer holds is the question and nothing else.
			root.SetIn(strings.NewReader("n\n"))
			root.SetArgs(argv)
			if err := root.Execute(); err == nil {
				t.Fatalf("%v: declining should stop the run", argv)
			}
			questions[argv[0]] = out.String()
		}

		if questions["pull"] != questions["build"] {
			t.Errorf("the two commands ask different questions:\n pull %q\nbuild %q", questions["pull"], questions["build"])
		}
		if !strings.Contains(questions["pull"], "drops every recorded pin") {
			t.Errorf("the question does not say what is lost: %q", questions["pull"])
		}
	})
}

// TestAdditiveFormsAskNothing pins the other half of the ruling: the confirmation
// gates the destructive form ONLY. A prompt on a path that holds every pin it did
// not name would teach the habit of dismissing the prompt that guards the path
// where everything is dropped.
//
// Each row reaches the gate's position and must pass through it. The Release names
// no file that exists, so every row is expected to fail LATER — the assertion is
// that the failure is not the confirmation and that nothing was asked.
func TestAdditiveFormsAskNothing(t *testing.T) {
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })
	// A terminal is available, so a gate that fired would print rather than refuse.
	isTerminal = func() bool { return true }

	for _, tc := range []struct {
		name string
		argv []string
	}{
		{name: "pull narrowing an upgrade to one target", argv: []string{"pull", "--release", "nope.yaml", "--upgrade", "core/database"}},
		{name: "pull reconciling", argv: []string{"pull", "--release", "nope.yaml"}},
		{name: "pull verifying", argv: []string{"pull", "--release", "nope.yaml", "--verify"}},
		{name: "build filling the lockfile", argv: []string{"build", "--release", "nope.yaml", "--auto-pull"}},
		{name: "build read-only", argv: []string{"build", "--release", "nope.yaml"}},
		{name: "build refusing dry-run with upgrade", argv: []string{"build", "--release", "nope.yaml", "--upgrade", "--dry-run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(io.Discard)
			// Empty stdin: a gate that fired here would read it, decline, and the
			// error below would name the confirmation.
			root.SetIn(strings.NewReader(""))
			root.SetArgs(tc.argv)
			err := root.Execute()
			if strings.Contains(out.String(), "[y/N]") {
				t.Errorf("an additive form asked for confirmation: %q", out.String())
			}
			if err != nil && strings.Contains(err.Error(), "aborted") {
				t.Errorf("an additive form was gated by the confirmation: %v", err)
			}
		})
	}
}
