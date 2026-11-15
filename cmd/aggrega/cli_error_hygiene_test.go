// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
)

// These tests isolate the home directory and move the working directory, so
// they must not run under t.Parallel. Each case builds a fresh command tree
// via newRootCmd so SilenceUsage set on one case's commands cannot bleed into
// the next.

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

// isolateCLIEnvironment points one case at a scratch home and a scratch working
// directory, and pins the interactivity seam closed.
//
// All three halves are load-bearing once the table reaches the configuration and
// cache verbs. A case that resolves configuration would otherwise read the
// developer's own global config — so its failure would depend on what that
// developer happens to have configured — and a case that writes could reach it.
// A scratch working directory keeps root discovery, which walks upward from
// wherever the process stands, inside the case's own tree rather than reaching
// the checkout the suite runs in. Pinning the interactivity seam makes a
// confirmation prompt's refusal a property of the fixture rather than of
// whatever the test binary's stdin happens to be attached to.
//
// The home policy itself is not spelled out here: which variables carry a home,
// and what each is pointed at, is decided in one place so a case-level scratch
// and the binary-wide redirect cannot drift apart.
func isolateCLIEnvironment(t *testing.T) {
	t.Helper()
	hometest.Isolate(t)
	t.Chdir(t.TempDir())
	prev := isTerminal
	isTerminal = func() bool { return false }
	t.Cleanup(func() { isTerminal = prev })
}

// runtimeErrorCommands lists, per error-producing command anywhere in the tree,
// an invocation that reaches that command's RunE and then fails in the command's
// own work, without network access or heavy side effects.
//
// Cases are keyed by full command path rather than by bare name, and that is not
// cosmetic: the drift net below walks the whole tree, where a bare name is
// ambiguous — "list", "add" and "remove" each name two different commands under
// two different parents. Keying by name would let one command's case silently
// stand in as coverage for its namesake.
//
// setup runs after the working directory has been switched, for the cases whose
// failure has to be arranged on disk rather than expressed in arguments.
var runtimeErrorCommands = []struct {
	path  string
	args  []string
	setup func(t *testing.T)
}{
	{path: "aggrega build", args: []string{"build", "--release", "/nonexistent-aggrega-xyz/release.yaml", "--catalog", "/nonexistent-aggrega-xyz/catalog"}},
	{path: "aggrega pull", args: []string{"pull", "--release", "/nonexistent-aggrega-xyz/release.yaml", "--catalog", "/nonexistent-aggrega-xyz/catalog"}},
	{path: "aggrega inspect", args: []string{"inspect", "/nonexistent-aggrega-xyz/release.yaml"}},
	// import reaches its RunE with a well-formed single argument, then fails in
	// its own work reading the non-existent source file, before format detection
	// or workspace resolution.
	{path: "aggrega import", args: []string{"import", "/nonexistent-aggrega-xyz/source.json"}},
	// edit-document reaches its RunE with a well-formed edit-and-document pair —
	// both positionals present, so cobra's own argument check passes and the
	// failure is not a usage one — then fails in its own work: the document it was
	// asked to edit is not there to read. The refusal comes before anything is
	// composed, so nothing is written and the case needs no arrangement and leaves
	// no file behind.
	{path: "aggrega edit-document", args: []string{"edit-document", "add-component", "no-such-document.aggregate.yaml", "--node", "spec", "--node", "components", "--name", "web", "--value", "core/api-server:1.0.0"}},
	// get-schema reaches its RunE with a well-formed single argument, then
	// fails in its own work: no schema matches the unknown kind.
	{path: "aggrega get-schema", args: []string{"get-schema", "bogus"}},
	// draft is the same shape: a well-formed single argument reaches its
	// RunE, and the failure is the shared drafting layer's own refusal of a kind
	// token no schema answers to. Nothing is read from disk and nothing is
	// written, so the case needs no arrangement.
	{path: "aggrega draft", args: []string{"draft", "bogus"}},
	// The hidden scaffold spelling runs the draft body behind its notice, so the
	// same refusal must reach the reader without a Usage block.
	{path: "aggrega scaffold", args: []string{"scaffold", "bogus"}},
	// explain is the same shape again: a well-formed single argument reaches its
	// RunE, and the failure is the refusal of a number that named a condition once
	// and names none now. A retired number is answered from the record the binary
	// carries, before any workspace is resolved and before the published
	// documentation is looked for, so the case reaches no registry.
	{path: "aggrega explain", args: []string{"explain", "E0157"}},
	{path: "aggrega serve", args: []string{"serve", "--workspace-root", "/nonexistent-aggrega-xyz/ws"}},
	{path: "aggrega lint", args: []string{"lint", "/nonexistent-aggrega-xyz/entity.yaml"}},
	// validate reaches its RunE with a well-formed single argument, then fails
	// in its own work reading the non-existent entity file, before any catalog
	// or render work.
	{path: "aggrega validate", args: []string{"validate", "/nonexistent-aggrega-xyz/entity.yaml"}},
	{path: "aggrega push", args: []string{"push", "/nonexistent-aggrega-xyz/entity.yaml"}},
	// pin reaches its RunE with a well-formed single argument, then fails in its
	// own work reading the non-existent entity document, before any catalog view
	// is built, any source is resolved or any lock is written.
	{path: "aggrega pin", args: []string{"pin", "/nonexistent-aggrega-xyz/recipe.yaml"}},
	// push-artifact reaches RunE with valid flags, then fails reading the
	// non-existent bundle file locally, before any network call.
	{path: "aggrega push-artifact", args: []string{"push-artifact", "/nonexistent-aggrega-xyz/bundle.tar.gz", "--name", "aggrega-docs", "--tag", "1.0.0", "--registry", "example.com"}},
	// sign fails parsing an invalid reference, locally, before any network call.
	{path: "aggrega sign", args: []string{"sign", "not a valid ref!!!"}},
	// deprecate reaches RunE with valid flags, then fails parsing an invalid
	// reference locally, before any network call.
	{path: "aggrega deprecate", args: []string{"deprecate", "not a valid ref!!!", "--severity", "warning", "--message", "gone"}},
	// login fails in its own work before any stdin read or network call:
	// --password-stdin requires --username.
	{path: "aggrega login", args: []string{"login", "registry.invalid.example", "--password-stdin"}},
	// search validates its output format inside its own work, so an unknown
	// --output value reaches RunE and fails locally, before any registry read
	// or network call.
	{path: "aggrega search", args: []string{"search", "--output", "bogus"}},

	// auth token is absent by design and its exemption is recorded with the
	// other exemptions in the drift net below.

	// cache clear refuses to wipe anything on a non-interactive stdin without
	// --yes, so the refusal is reached inside RunE and nothing is removed. The
	// interactivity seam is pinned closed by the fixture, so the refusal is a
	// property of the case rather than of the ambient stdin.
	{path: "aggrega cache clear", args: []string{"cache", "clear"}},
	// cache evict requires --max-size, and checks it inside its own RunE rather
	// than as a cobra-declared requirement, so the bare invocation reaches the
	// wrap before failing.
	{path: "aggrega cache evict", args: []string{"cache", "evict"}},
	// cache prune needs a pulled workspace and halts when no lockfile is
	// present, which a scratch working directory guarantees.
	{path: "aggrega cache prune", args: []string{"cache", "prune"}},
	// config capture fails resolving the workspace configuration it is about to
	// rewrite when the config path is not a readable file. A directory standing
	// where the file belongs is deterministic and needs no assumption about
	// which paths the test user may write to.
	{path: "aggrega config capture", args: []string{"config", "capture"}, setup: func(t *testing.T) {
		t.Helper()
		if err := os.Mkdir(".aggrega-config.yaml", 0o755); err != nil {
			t.Fatalf("arranging an unreadable workspace config: %v", err)
		}
	}},
	// config init rejects the two write-target flags together inside its own
	// work, before it resolves a target file or reaches for a trust policy — so
	// nothing is written and no network call is attempted.
	{path: "aggrega config init", args: []string{"config", "init", "--global", "--workspace"}},
	// config show rejects the same pair for the same reason, on the read side.
	{path: "aggrega config show", args: []string{"config", "show", "--global", "--workspace"}},
	// plugin add validates the socket type inside its own work, so an unknown
	// type fails before the registry search.
	{path: "aggrega plugin add", args: []string{"plugin", "add", "bogus-socket", "anything"}},
	// plugin gen-catalog requires one of its two selection modes and says so
	// from inside RunE.
	{path: "aggrega plugin gen-catalog", args: []string{"plugin", "gen-catalog"}},
	// plugin list validates its output format inside its own work, like search.
	{path: "aggrega plugin list", args: []string{"plugin", "list", "-o", "bogus"}},
	// plugin remove validates the socket type inside its own work, like add.
	{path: "aggrega plugin remove", args: []string{"plugin", "remove", "bogus-socket", "anything"}},
	// plugin run-local fails reading the non-existent guest locally, before any
	// guest is instantiated.
	{path: "aggrega plugin run-local", args: []string{"plugin", "run-local", "--plugin", "/nonexistent-aggrega-xyz/guest.wasm", "--export", "describe"}},
	// plugin sync requires --plugin and checks it inside its own RunE, so the
	// entity path alone reaches the wrap before failing.
	{path: "aggrega plugin sync", args: []string{"plugin", "sync", "/nonexistent-aggrega-xyz/plugin.yaml"}},
	// registry add rejects the two write-target flags together inside its own
	// work, before the trust-policy fetch, so no network call is attempted.
	{path: "aggrega registry add", args: []string{"registry", "add", "--global", "--workspace", "registry.invalid.example"}},
	// registry list validates its output format inside its own work, like search.
	{path: "aggrega registry list", args: []string{"registry", "list", "-o", "bogus"}},
	// registry remove halts on a token that matches no configured registry, and
	// leaves the config untouched.
	{path: "aggrega registry remove", args: []string{"registry", "remove", "no-such-registry-alias"}},
	// registry update resolves its argument to a host before contacting
	// anything, so a token that cannot be a registry reference fails locally.
	{path: "aggrega registry update", args: []string{"registry", "update", "not a registry!!!"}},
	// skills install refuses an assistant it knows no skill folder for inside its
	// own work, before any target is checked, so nothing is written.
	{path: "aggrega skills install", args: []string{"skills", "install", "--agent", "no-such-assistant"}},
}

// TestCLIErrorHygiene_RuntimeErrorSuppressesUsageAndError drives each
// error-producing command through the real tree and asserts that a failure
// in the command's own work prints neither the Usage block nor cobra's own
// "Error:" line — Execute renders the single formatted diagnostic instead.
func TestCLIErrorHygiene_RuntimeErrorSuppressesUsageAndError(t *testing.T) {
	for _, tc := range runtimeErrorCommands {
		t.Run(tc.path, func(t *testing.T) {
			isolateCLIEnvironment(t)
			if tc.setup != nil {
				tc.setup(t)
			}
			root := newRootCmd()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			// An empty stdin, so no case can block on or consume the test
			// binary's own input.
			root.SetIn(strings.NewReader(""))
			root.SetArgs(tc.args)

			err := root.Execute()
			if err == nil {
				t.Fatalf("expected a runtime error from %q, got nil", tc.path)
			}
			out := buf.String()
			if strings.Contains(out, "Usage:") {
				t.Errorf("%q runtime error leaked the Usage block:\n%s", tc.path, out)
			}
			if strings.Contains(out, "Error:") {
				t.Errorf("%q runtime error printed cobra's Error line (double-print):\n%s", tc.path, out)
			}
		})
	}
}

// TestCLIErrorHygiene_UsageErrorsShowUsage asserts that genuine misuse —
// detected before the command's RunE runs — still surfaces the Usage block,
// because the wrap silences usage only once RunE begins.
func TestCLIErrorHygiene_UsageErrorsShowUsage(t *testing.T) {
	// Not parallel: mutates the process-global globalDisplay seam other tests read.
	cases := []struct {
		name string
		args []string
	}{
		{"unknown-flag", []string{"build", "--bogus-flag"}},
		{"missing-required-flag", []string{"build"}},
		{"bad-arg-count", []string{"inspect"}},
		// A nested command's misuse must surface usage the same way a top-level
		// command's does, so the usage half of the contract is checked below the
		// first level too.
		{"nested-unknown-flag", []string{"cache", "status", "--bogus-flag"}},
		{"nested-bad-arg-count", []string{"registry", "remove"}},
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

// TestCLIErrorHygiene_FrameworkBuiltinsAreAbsentAtConstruction records, as a
// live assertion rather than an assumption, why the drift net below needs no
// exclusion for the command framework's own entries: the framework adds its
// completion and help commands while executing, and the net walks a tree that
// was only constructed.
//
// The assertion states the absence rather than skipping the names. An exclusion
// would keep passing if the framework ever moved that injection to construction
// time; this fails there instead, which is the point at which the coverage
// decision would need revisiting.
func TestCLIErrorHygiene_FrameworkBuiltinsAreAbsentAtConstruction(t *testing.T) {
	for _, c := range reachableCommands(newRootCmd()) {
		name := c.Name()
		if name == "completion" || name == "help" || strings.HasPrefix(name, "__complete") {
			t.Errorf("the command framework's %q entry is present in a freshly constructed tree (%s); the coverage rule in the drift net assumes these appear only during execution", name, c.CommandPath())
		}
	}
}

// TestCLIErrorHygiene_AllRunECommandsCovered is the drift net: every command in
// the tree that owns a RunE must be exercised by the hygiene tests above — the
// runtime-suppression table, or the set of commands with no reachable runtime
// error. A future command added without coverage fails here, so the wrap is
// never silently left unproven for a command.
//
// The walk is the whole tree, through the one shared traversal, so a command
// nested under a container command is held to the same contract as a top-level
// one. Coverage is keyed by command path, because a bare name does not identify
// a command once the walk descends.
func TestCLIErrorHygiene_AllRunECommandsCovered(t *testing.T) {
	// Not parallel: mutates the process-global globalDisplay seam other tests read.
	covered := map[string]bool{}
	for _, tc := range runtimeErrorCommands {
		covered[tc.path] = true
	}
	// version succeeds and has no error path / Usage to leak; it carries
	// the wrap harmlessly and is exempt from the runtime-error table.
	covered["aggrega version"] = true
	// logout of an absent credential is a no-op success (like version), so it
	// has no hermetic runtime-error invocation to drive here; the centralized
	// wrap it carries is the same one proven against the login error path above.
	covered["aggrega logout"] = true
	// mcp reads its stdin to EOF and returns on a clean close — a no-error
	// success path (like version) with no Usage to leak, so it carries the
	// centralized wrap harmlessly and is exempt from the runtime-error table.
	covered["aggrega mcp"] = true
	// cache status is a read-only listing that reports an empty cache rather
	// than failing, so it has no runtime error to drive — the same shape as
	// version. Measured, not assumed: driven with no arguments and with a
	// non-existent workspace root, it succeeds both ways.
	covered["aggrega cache status"] = true
	// skills list reports the skills and assistants this binary carries, which
	// are compiled into it, so it has no runtime error to drive — the same shape
	// as version.
	covered["aggrega skills list"] = true
	// auth token's RunE begins the interactive identity acquisition, which
	// cannot be driven here at all: every path through it either opens a browser
	// or contacts the identity provider. It is exempt for the opposite reason to
	// version — not because it has no error path, but because it has no error
	// path reachable without leaving the machine. The wrap it carries is the
	// same one the sign and push credential paths prove.
	covered["aggrega auth token"] = true

	for _, c := range reachableCommands(newRootCmd()) {
		if c.RunE == nil {
			continue
		}
		path := c.CommandPath()
		if !covered[path] {
			t.Errorf("command %q has a RunE but is not covered by the CLI error-hygiene tests; add it to runtimeErrorCommands and confirm the wrap holds", path)
		}
	}
}
