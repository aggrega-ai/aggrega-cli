// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// strayPositional is the token every argument-validation test hands a command
// that should not accept it. One spelling is used everywhere so the choice is not
// remade per test. It cannot collide with a present or plausible future
// subcommand name, it names itself in failure output, it sorts last so it never
// reads as a real completion candidate, and it carries no shell metacharacter.
const strayPositional = "zz-stray-positional"

// strayProbeCeiling bounds the search for how many positional arguments a command
// accepts. Real validators on this surface top out at two, so a command that still
// accepts arguments at the ceiling is treated as accepting them without an upper
// bound.
const strayProbeCeiling = 6

// commandArgv reconstructs the argument vector that reaches cmd, walking up to the
// root rather than re-parsing a display path, so a command name that ever contains
// a space cannot silently split.
func commandArgv(cmd *cobra.Command) []string {
	var argv []string
	for c := cmd; c.HasParent(); c = c.Parent() {
		argv = append([]string{c.Name()}, argv...)
	}
	return argv
}

func strayTokens(n int) []string {
	tokens := make([]string, n)
	for i := range tokens {
		tokens[i] = strayPositional
	}
	return tokens
}

// strayArgsFor returns the positional arguments to hand cmd so that it is being
// given one more than it accepts: one for a command that takes none, and N+1 for a
// command that takes N. Handing a single token to a command that legitimately
// takes a path would be handing it valid input and would prove nothing.
//
// The count is derived from the command's own validator rather than from a table
// of command names, so there is nothing to keep in step as the surface grows. A
// command that declares no validator, or one that accepts arguments without an
// upper bound, is handed one token: no count refuses it, so its refusal — if it
// has one — comes from elsewhere, and the assertion below cares only that it
// refuses. Evaluating a validator is safe because every validator on this surface
// is a pure function of the argument count and the parsed flags.
func strayArgsFor(cmd *cobra.Command) []string {
	if cmd.Args == nil {
		return strayTokens(1)
	}
	maxAccepted := -1
	for n := 0; n <= strayProbeCeiling; n++ {
		if cmd.Args(cmd, strayTokens(n)) == nil {
			maxAccepted = n
		}
	}
	if maxAccepted < 0 || maxAccepted >= strayProbeCeiling {
		return strayTokens(1)
	}
	return strayTokens(maxAccepted + 1)
}

// TestEveryCommandRefusesAStrayPositional is the choke point that a command cannot
// join the tree while silently swallowing positional arguments it has no use for.
//
// It asserts the behaviour and never the mechanism. Three ways of refusing a stray
// positional coexist on this surface deliberately — cobra's own count validators,
// a conditional validator that refuses only in the mode where a positional is
// meaningless, and a guard at the top of a command's run body for a command whose
// positionals are conditionally meaningful — and a fourth case is answered
// centrally for a command that only groups subcommands and so never reaches a
// validator at all. Asserting a non-zero exit code covers all four with no
// per-command exception list, and it is the only form that can see the central
// one: a command that groups subcommands reports a nil error however it is
// invoked, so reading a command's validator field would report success while that
// whole class went unchecked.
//
// The exit code is read through executeRoot, which is where a stray positional on
// a grouping command is turned into a failure. Driving cobra directly would step
// over exactly the half this guards.
//
// Scope: the invocations carry no explicit help request, and the refusal this
// asserts does not hold when one is present. A command that resolves arguments at
// its own validator answers help instead of refusing, so "every command refuses a
// stray positional" is true of this population and not of every invocation.
// TestStrayPositionalBesideAnExplicitHelpRequest pins that interaction.
//
// 🔴 The invocations are real, so the ordering that keeps them harmless is
// load-bearing and a later edit would break it silently. A command that refuses at
// its validator never reaches its run body: that is what keeps build and acquire
// from starting a build here, and cache clear and cache prune from touching a
// cache. pull is the exception that proves the rule — its guard lives in its run
// body, and it returns before anything resolves a workspace. So the harness must
// never assume the property it is asserting: if a validator is ever dropped, this
// test's job is to go red, not to do the command's work on the way. Every
// invocation therefore runs with its own throwaway working directory, cache home
// and home directory, and with an empty stdin so the two commands that read a
// stream to end-of-input cannot block on the real one.
func TestEveryCommandRefusesAStrayPositional(t *testing.T) {
	// The population is the statically registered tree, which is the tree as
	// shipped. A configured plugin contributes a command whose whole contract is
	// to forward its argument vector to the plugin verbatim, so a stray positional
	// there is not stray — it belongs to the plugin, which is the sole parser of
	// its own arguments. Pinning the seam to none keeps that command out of the
	// population structurally, rather than by naming it in a skip list.
	withCLIRegistrations(t, nil)

	type invocation struct {
		path  string
		argv  []string
		stray []string
	}
	var cases []invocation
	for _, cmd := range reachableCommands(newRootCmd()) {
		cases = append(cases, invocation{
			path:  cmd.CommandPath(),
			argv:  commandArgv(cmd),
			stray: strayArgsFor(cmd),
		})
	}

	// A guard that passes over an empty walk certifies nothing, so the walk's own
	// yield is asserted before any of its results are trusted.
	if len(cases) == 0 {
		t.Fatal("the command walk yielded no commands, so this guard proved nothing; the walk itself is broken")
	}
	for _, want := range []string{
		"aggrega",             // the root
		"aggrega build",       // a command one level down
		"aggrega cache prune", // a command two levels down
	} {
		found := false
		for _, tc := range cases {
			if tc.path == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("the walk did not reach %q, so it is not covering the tree it claims to", want)
		}
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir())

			root := newRootCmd()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetIn(strings.NewReader(""))
			root.SetArgs(append(append([]string{}, tc.argv...), tc.stray...))

			if code := executeRoot(context.Background(), root); code == 0 {
				t.Errorf("%q accepted %d stray positional argument(s) and reported success; "+
					"give it an argument validator, or a guard at the top of its run body where its "+
					"positionals are conditionally meaningful\noutput:\n%s",
					tc.path, len(tc.stray), buf.String())
			}
		})
	}
}

// TestStrayPositionalBesideAnExplicitHelpRequest pins how an explicit help request
// and an unusable positional resolve when both appear on one invocation. A scripted
// caller can hit the combination, and the two answers differ in exit code, so the
// order is a contract rather than an implementation detail.
//
// The resolved behaviour, and it turns on WHERE the positional is refused:
//
//   - A command that resolves its own arguments answers the help request and exits
//     zero, leaving the positional unreported. The help request is resolved before
//     the argument check runs, so the check never sees the positional. This holds
//     whichever side of the positional the flag is written on, and for the short
//     form, because flag parsing does not depend on token order.
//   - A command that only groups subcommands, and the root, still exit non-zero.
//     Their positional is refused centrally rather than at a validator, and that
//     answer is reached whether or not help was asked for — so help text is printed
//     AND the exit status reports the unusable argument.
//
// The consequence worth writing down: an exit status alone does not tell a caller
// whether its help request was honoured, and a zero here does not mean the
// positional was accepted as meaningful — it means it was never examined.
func TestStrayPositionalBesideAnExplicitHelpRequest(t *testing.T) {
	withCLIRegistrations(t, nil)

	const stray = "stray-positional"
	cases := []struct {
		name     string
		argv     []string
		wantZero bool
		why      string
	}{
		// Controls, so each expectation below is attributable to the interaction
		// rather than to one of its halves.
		{"control: help alone is answered", []string{"build", "--help"}, true,
			"a help request on its own exits zero"},
		{"control: a stray positional alone is refused", []string{"build", stray}, false,
			"the refusal this pins the exception to must itself fire"},

		{"own validator, flag first", []string{"build", "--help", stray}, true,
			"help resolves before the argument check, so the positional is never examined"},
		{"own validator, flag last", []string{"build", stray, "--help"}, true,
			"flag parsing does not depend on token order"},
		{"own validator, short form", []string{"build", "-h", stray}, true,
			"the short form is the same help request"},
		{"run-body guard", []string{"pull", "--help", stray}, true,
			"a guard in the run body is not reached when help is answered"},

		{"grouping command", []string{"cache", "--help", stray}, false,
			"a grouping command's positional is refused centrally, not at a validator"},
		{"the root", []string{"--help", stray}, false,
			"the root refuses centrally for the same reason"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("HOME", t.TempDir())

			root := newRootCmd()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetIn(strings.NewReader(""))
			root.SetArgs(tc.argv)

			code := executeRoot(context.Background(), root)
			if tc.wantZero && code != 0 {
				t.Errorf("%v exited %d, want 0 — %s\noutput:\n%s",
					tc.argv, code, tc.why, buf.String())
			}
			if !tc.wantZero && code == 0 {
				t.Errorf("%v exited 0, want non-zero — %s\noutput:\n%s",
					tc.argv, tc.why, buf.String())
			}
		})
	}
}
