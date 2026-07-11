// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// These tests share the package-level flag globals, so they must not run
// under t.Parallel. Each case builds a fresh command tree via newRootCmd
// (which resets the globals on construction) to avoid SilenceUsage / flag
// bleed between cases.

// TestSilenceUsageOnRun_WrapsEntireTree proves the centralized wrap reaches
// every RunE in the subtree — including nested commands — and leaves
// commands without a RunE untouched. A command never passed through the
// wrap keeps SilenceUsage false, so the test is non-vacuous.
func TestSilenceUsageOnRun_WrapsEntireTree(t *testing.T) {
	sentinel := errors.New("boom")
	runE := func(c *cobra.Command, _ []string) error { return sentinel }

	leaf := &cobra.Command{Use: "leaf", RunE: runE}
	mid := &cobra.Command{Use: "mid", RunE: runE}
	mid.AddCommand(leaf)
	parent := &cobra.Command{Use: "parent"} // no RunE — nothing to wrap
	parent.AddCommand(mid)

	// Control: structurally identical command NOT passed through the wrap.
	control := &cobra.Command{Use: "control", RunE: runE}

	silenceUsageOnRun(parent)

	for _, c := range []*cobra.Command{leaf, mid} {
		if c.SilenceUsage {
			t.Fatalf("%q started with SilenceUsage already set", c.Use)
		}
		if got := c.RunE(c, nil); !errors.Is(got, sentinel) {
			t.Fatalf("%q wrapper did not delegate to the original RunE; got %v", c.Use, got)
		}
		if !c.SilenceUsage {
			t.Errorf("%q RunE wrapper did not set SilenceUsage", c.Use)
		}
	}

	_ = control.RunE(control, nil)
	if control.SilenceUsage {
		t.Errorf("control command (unwrapped) unexpectedly had SilenceUsage set")
	}
}

// runtimeErrorCommands lists, per error-producing subcommand, an invocation
// that reaches the command's RunE and then fails in the command's own work
// (a non-existent path), without network or heavy side effects.
var runtimeErrorCommands = []struct {
	name string
	args []string
}{
	{"build", []string{"build", "--release", "/nonexistent-aggrega-xyz/release.yaml", "--catalog", "/nonexistent-aggrega-xyz/catalog"}},
	// acquire runs the build dispatch, so the same non-existent-path
	// invocation reaches its RunE and fails locally.
	{"acquire", []string{"acquire", "--release", "/nonexistent-aggrega-xyz/release.yaml", "--catalog", "/nonexistent-aggrega-xyz/catalog"}},
	{"pull", []string{"pull", "--release", "/nonexistent-aggrega-xyz/release.yaml", "--catalog", "/nonexistent-aggrega-xyz/catalog"}},
	{"inspect", []string{"inspect", "/nonexistent-aggrega-xyz/release.yaml"}},
	// get-schema reaches its RunE with a well-formed single argument, then
	// fails in its own work: no schema matches the unknown kind.
	{"get-schema", []string{"get-schema", "bogus"}},
	{"serve", []string{"serve", "--workspace-root", "/nonexistent-aggrega-xyz/ws"}},
	{"lint", []string{"lint", "/nonexistent-aggrega-xyz/entity.yaml"}},
	{"push", []string{"push", "/nonexistent-aggrega-xyz/entity.yaml"}},
	// push-artifact reaches RunE with valid flags, then fails reading the
	// non-existent bundle file locally, before any network call.
	{"push-artifact", []string{"push-artifact", "/nonexistent-aggrega-xyz/bundle.tar.gz", "--name", "aggrega-docs", "--tag", "1.0.0", "--registry", "example.com"}},
	// sign fails parsing an invalid reference, locally, before any network call.
	{"sign", []string{"sign", "not a valid ref!!!"}},
	// deprecate reaches RunE with valid flags, then fails parsing an invalid
	// reference locally, before any network call.
	{"deprecate", []string{"deprecate", "not a valid ref!!!", "--severity", "warning", "--message", "gone"}},
	// login fails in its own work before any stdin read or network call:
	// --password-stdin requires --username.
	{"login", []string{"login", "registry.invalid.example", "--password-stdin"}},
}

// TestCLIErrorHygiene_RuntimeErrorSuppressesUsageAndError drives each
// error-producing command through the real tree and asserts that a failure
// in the command's own work prints neither the Usage block nor cobra's own
// "Error:" line — Execute renders the single formatted diagnostic instead.
func TestCLIErrorHygiene_RuntimeErrorSuppressesUsageAndError(t *testing.T) {
	for _, tc := range runtimeErrorCommands {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			root := newRootCmd()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetArgs(tc.args)

			err := root.Execute()
			if err == nil {
				t.Fatalf("expected a runtime error from %q, got nil", tc.name)
			}
			out := buf.String()
			if strings.Contains(out, "Usage:") {
				t.Errorf("%q runtime error leaked the Usage block:\n%s", tc.name, out)
			}
			if strings.Contains(out, "Error:") {
				t.Errorf("%q runtime error printed cobra's Error line (double-print):\n%s", tc.name, out)
			}
		})
	}
}

// TestCLIErrorHygiene_UsageErrorsShowUsage asserts that genuine misuse —
// detected before the command's RunE runs — still surfaces the Usage block,
// because the wrap silences usage only once RunE begins.
func TestCLIErrorHygiene_UsageErrorsShowUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown-flag", []string{"build", "--bogus-flag"}},
		{"missing-required-flag", []string{"build"}},
		{"bad-arg-count", []string{"inspect"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCmd()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetArgs(tc.args)

			err := root.Execute()
			if err == nil {
				t.Fatalf("expected a usage error for %v, got nil", tc.args)
			}
			if !strings.Contains(buf.String(), "Usage:") {
				t.Errorf("usage error %v did not show the Usage block:\n%s", tc.args, buf.String())
			}
		})
	}
}

// TestCLIErrorHygiene_AllRunECommandsCovered is the drift net: every command
// in the tree that owns a RunE must be exercised by the hygiene tests above
// (the runtime-suppression table, or the no-runtime-error set). A future
// command added without coverage fails here, so the wrap is never silently
// left unproven for a command.
func TestCLIErrorHygiene_AllRunECommandsCovered(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range runtimeErrorCommands {
		covered[tc.name] = true
	}
	// version succeeds and has no error path / Usage to leak; it carries
	// the wrap harmlessly and is exempt from the runtime-error table.
	covered["version"] = true
	// logout of an absent credential is a no-op success (like version), so it
	// has no hermetic runtime-error invocation to drive here; the centralized
	// wrap it carries is the same one proven against the login error path above.
	covered["logout"] = true
	// mcp reads its stdin to EOF and returns on a clean close — a no-error
	// success path (like version) with no Usage to leak, so it carries the
	// centralized wrap harmlessly and is exempt from the runtime-error table.
	covered["mcp"] = true

	for _, c := range newRootCmd().Commands() {
		if c.RunE == nil {
			continue
		}
		name := c.Name()
		if name == "completion" || name == "help" || strings.HasPrefix(name, "__complete") {
			continue // cobra built-ins
		}
		if !covered[name] {
			t.Errorf("command %q has a RunE but is not covered by the CLI error-hygiene tests; add it to runtimeErrorCommands and confirm the wrap holds", name)
		}
	}
}
