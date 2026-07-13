// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// fakeDispatcher records the URI, invoked command name, and argv of each Run
// call and returns a preconfigured exit code and error, so a test can assert
// command and argument forwarding and exit-code propagation without a real guest.
type fakeDispatcher struct {
	gotURI     string
	gotCommand string
	gotArgv    []string
	code       int
	digest     string
	err        error
	calls      int
}

func (f *fakeDispatcher) Run(_ context.Context, uri, command string, argv []string) (int, string, error) {
	f.calls++
	f.gotURI = uri
	f.gotCommand = command
	f.gotArgv = argv
	return f.code, f.digest, f.err
}

// withInstalledIndex points the index source at a fixture for the duration of the
// test and restores it afterward, so no test touches the real cache root.
func withInstalledIndex(t *testing.T, idx installedPluginIndex) {
	t.Helper()
	prev := installedPluginIndexSource
	installedPluginIndexSource = func() (installedPluginIndex, error) { return idx, nil }
	t.Cleanup(func() { installedPluginIndexSource = prev })
}

// withDispatcher swaps in a fake plugin dispatcher for the test, regardless of
// the workspace the runtime path would otherwise derive its resolver from.
func withDispatcher(t *testing.T, d cliPluginDispatcher) {
	t.Helper()
	prev := installedPluginRunnerFor
	installedPluginRunnerFor = func(*workspaceconfig.ResolvedWorkspace) cliPluginDispatcher { return d }
	t.Cleanup(func() { installedPluginRunnerFor = prev })
}

// withRegistrationWarnings captures registration diagnostics for the test.
func withRegistrationWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := pluginRegistrationWarnWriter
	pluginRegistrationWarnWriter = &buf
	t.Cleanup(func() { pluginRegistrationWarnWriter = prev })
	return &buf
}

// withDiscoverable swaps the discoverability seam for the test.
func withDiscoverable(t *testing.T, fn func(string) bool) {
	t.Helper()
	prev := pluginDiscoverable
	pluginDiscoverable = fn
	t.Cleanup(func() { pluginDiscoverable = prev })
}

// TestInstalledPlugin_ListedAndDispatched proves an installed plugin appears in
// the command tree with its cached usage and, when invoked, forwards its argv to
// the runner and returns exit 0 (no error).
func TestInstalledPlugin_ListedAndDispatched(t *testing.T) {
	withRegistrationWarnings(t)
	withInstalledIndex(t, installedPluginIndex{Plugins: map[string]installedPluginEntry{
		"moo": {URI: "oci://example/moo:v1", Usage: "make the cow moo"},
	}})
	d := &fakeDispatcher{code: 0}
	withDispatcher(t, d)

	root := newRootCmd()

	// The command is present in the tree with its cached usage as the short help.
	found := &bytes.Buffer{}
	root.SetOut(found)
	root.SetErr(found)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	if !strings.Contains(found.String(), "moo") || !strings.Contains(found.String(), "make the cow moo") {
		t.Fatalf("--help output does not list the installed plugin:\n%s", found.String())
	}

	// Dispatching the command forwards argv to the runner and returns exit 0.
	root2 := newRootCmd()
	root2.SetOut(&bytes.Buffer{})
	root2.SetErr(&bytes.Buffer{})
	root2.SetArgs([]string{"moo", "--loud", "twice"})
	code := executeRoot(root2)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if d.calls != 1 {
		t.Fatalf("runner called %d time(s); want 1", d.calls)
	}
	if d.gotURI != "oci://example/moo:v1" {
		t.Fatalf("runner URI = %q; want the index URI", d.gotURI)
	}
	if d.gotCommand != "moo" {
		t.Fatalf("forwarded command = %q; want the invoked command name %q", d.gotCommand, "moo")
	}
	if got, want := strings.Join(d.gotArgv, " "), "--loud twice"; got != want {
		t.Fatalf("forwarded argv = %q; want %q", got, want)
	}
}

// TestInstalledPlugin_BuiltinAndReservedRefused proves a plugin named after a
// built-in and one on the reserved denylist are both refused registration with an
// observable diagnostic, and the built-in keeps its own command.
func TestInstalledPlugin_BuiltinAndReservedRefused(t *testing.T) {
	warn := withRegistrationWarnings(t)
	const sentinel = "SHADOW ATTEMPT — should never win"
	withInstalledIndex(t, installedPluginIndex{Plugins: map[string]installedPluginEntry{
		"build":  {URI: "oci://example/evil-build:v1", Usage: sentinel},
		"deploy": {URI: "oci://example/evil-deploy:v1", Usage: sentinel},
	}})
	withDispatcher(t, &fakeDispatcher{})

	root := newRootCmd()

	// The built-in "build" survives: its command is present and is NOT the plugin
	// (its short help is not the fixture sentinel).
	var build *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "build" {
			build = c
		}
	}
	if build == nil {
		t.Fatalf("built-in build command missing from tree")
	}
	if build.Short == sentinel {
		t.Fatalf("installed plugin shadowed the built-in build command")
	}

	// The reserved "deploy" name registered nothing.
	for _, c := range root.Commands() {
		if c.Name() == "deploy" {
			t.Fatalf("reserved name deploy was registered as a command")
		}
	}

	out := warn.String()
	if !strings.Contains(out, "build") || !strings.Contains(out, "built-in") {
		t.Fatalf("no observable diagnostic for the built-in collision:\n%s", out)
	}
	if !strings.Contains(out, "deploy") || !strings.Contains(out, "reserved") {
		t.Fatalf("no observable diagnostic for the reserved-name collision:\n%s", out)
	}
}

// TestInstalledPlugin_ExitCodePropagation proves a plugin exit code threads
// through executeRoot: a non-zero code becomes the process exit code, and zero
// exits zero.
func TestInstalledPlugin_ExitCodePropagation(t *testing.T) {
	withRegistrationWarnings(t)
	withInstalledIndex(t, installedPluginIndex{Plugins: map[string]installedPluginEntry{
		"moo": {URI: "oci://example/moo:v1", Usage: "moo"},
	}})

	for _, tc := range []struct {
		name string
		code int
	}{
		{"nonzero", 3},
		{"zero", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDispatcher(t, &fakeDispatcher{code: tc.code})
			root := newRootCmd()
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs([]string{"moo"})
			if got := executeRoot(root); got != tc.code {
				t.Fatalf("executeRoot exit = %d; want %d", got, tc.code)
			}
		})
	}
}

// TestUnknownCommand_InstallSuggestion proves the unknown-command boundary appends
// the install hint only when the name is discoverable, and otherwise leaves
// cobra's normal error unchanged.
func TestUnknownCommand_InstallSuggestion(t *testing.T) {
	withRegistrationWarnings(t)
	withInstalledIndex(t, installedPluginIndex{})
	withDispatcher(t, &fakeDispatcher{})

	run := func() string {
		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"definitely-not-a-command"})
		err := root.Execute()
		if err == nil {
			t.Fatalf("unknown command returned no error")
		}
		return unknownCommandSuggestion(formatCompilerError(err), err)
	}

	withDiscoverable(t, func(string) bool { return true })
	withHint := run()
	if !strings.Contains(withHint, "aggrega plugin install definitely-not-a-command") {
		t.Fatalf("discoverable unknown command lacks the install hint:\n%s", withHint)
	}

	withDiscoverable(t, func(string) bool { return false })
	noHint := run()
	if strings.Contains(noHint, "plugin install") {
		t.Fatalf("non-discoverable unknown command should keep cobra's normal error:\n%s", noHint)
	}
	if !strings.Contains(noHint, "unknown command") {
		t.Fatalf("non-discoverable unknown command lost cobra's normal error:\n%s", noHint)
	}
}

// TestReconcileInstalledPluginDigest_PinThenDrift proves trust-on-first-use: the
// first reconciliation pins the resolved digest into the entry, a matching re-run
// is a clean no-op, and a re-run whose bytes hash differently warns without
// overwriting the pin.
func TestReconcileInstalledPluginDigest_PinThenDrift(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := writeInstalledPluginIndex(installedPluginIndex{Plugins: map[string]installedPluginEntry{
		"moo": {URI: "/abs/moo.wasm", Usage: "greet"},
	}}); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	var warn bytes.Buffer
	reconcileInstalledPluginDigest("moo", "digest-A", &warn)
	if warn.Len() != 0 {
		t.Fatalf("first run must not warn: %q", warn.String())
	}
	if idx, _ := readInstalledPluginIndex(); idx.Plugins["moo"].Digest != "digest-A" {
		t.Fatalf("first run should pin the digest; got %q", idx.Plugins["moo"].Digest)
	}

	warn.Reset()
	reconcileInstalledPluginDigest("moo", "digest-A", &warn)
	if warn.Len() != 0 {
		t.Fatalf("matching re-run must not warn: %q", warn.String())
	}

	warn.Reset()
	reconcileInstalledPluginDigest("moo", "digest-B", &warn)
	if !strings.Contains(warn.String(), "changed since they were pinned") {
		t.Fatalf("drift must warn; got %q", warn.String())
	}
	if idx, _ := readInstalledPluginIndex(); idx.Plugins["moo"].Digest != "digest-A" {
		t.Fatalf("drift must not overwrite the pin; got %q", idx.Plugins["moo"].Digest)
	}
}

// TestReconcileInstalledPluginDigest_NoOpCases proves reconciliation is inert
// when there is nothing to pin: an empty digest (no bytes resolved) and a command
// name absent from the index both leave the index and stderr untouched.
func TestReconcileInstalledPluginDigest_NoOpCases(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := writeInstalledPluginIndex(installedPluginIndex{Plugins: map[string]installedPluginEntry{
		"moo": {URI: "/abs/moo.wasm", Usage: "greet", Digest: "kept"},
	}}); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	var warn bytes.Buffer
	reconcileInstalledPluginDigest("moo", "", &warn)
	reconcileInstalledPluginDigest("absent", "whatever", &warn)
	if warn.Len() != 0 {
		t.Fatalf("no-op cases must not warn: %q", warn.String())
	}
	if idx, _ := readInstalledPluginIndex(); idx.Plugins["moo"].Digest != "kept" {
		t.Fatalf("no-op cases must not alter the pin; got %q", idx.Plugins["moo"].Digest)
	}
}

// TestInstalledPlugin_RunPinsAndWarnsOnDrift drives the command through the real
// dispatch path: the first run pins the digest the dispatcher reports for the
// resolved bytes, and a later run whose dispatcher reports a different digest
// emits the drift warning to stderr while leaving the pin in place.
func TestInstalledPlugin_RunPinsAndWarnsOnDrift(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	withRegistrationWarnings(t)
	if err := writeInstalledPluginIndex(installedPluginIndex{Plugins: map[string]installedPluginEntry{
		"moo": {URI: "oci://example/moo:v1", Usage: "greet"},
	}}); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	// First run pins the reported digest; stderr stays clean.
	withDispatcher(t, &fakeDispatcher{code: 0, digest: "run-digest-1"})
	root := newRootCmd()
	var firstErr bytes.Buffer
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&firstErr)
	root.SetArgs([]string{"moo"})
	if code := executeRoot(root); code != 0 {
		t.Fatalf("first run exit = %d; want 0", code)
	}
	if strings.Contains(firstErr.String(), "changed since") {
		t.Fatalf("first run must not warn:\n%s", firstErr.String())
	}
	if idx, _ := readInstalledPluginIndex(); idx.Plugins["moo"].Digest != "run-digest-1" {
		t.Fatalf("first run should pin the reported digest; got %q", idx.Plugins["moo"].Digest)
	}

	// A later run whose resolved bytes hash differently warns and keeps the pin.
	withDispatcher(t, &fakeDispatcher{code: 0, digest: "run-digest-2"})
	root2 := newRootCmd()
	var secondErr bytes.Buffer
	root2.SetOut(&bytes.Buffer{})
	root2.SetErr(&secondErr)
	root2.SetArgs([]string{"moo"})
	if code := executeRoot(root2); code != 0 {
		t.Fatalf("second run exit = %d; want 0", code)
	}
	if !strings.Contains(secondErr.String(), "changed since they were pinned") {
		t.Fatalf("drifted run should warn on stderr; got:\n%s", secondErr.String())
	}
	if idx, _ := readInstalledPluginIndex(); idx.Plugins["moo"].Digest != "run-digest-1" {
		t.Fatalf("drift must not overwrite the pin; got %q", idx.Plugins["moo"].Digest)
	}
}

// TestUnknownCommandName_ParsesOnlyCobraForm proves the extractor matches cobra's
// unknown-command error precisely and ignores unrelated errors.
func TestUnknownCommandName_ParsesOnlyCobraForm(t *testing.T) {
	name, ok := unknownCommandName(errString(`unknown command "moo" for "aggrega"`))
	if !ok || name != "moo" {
		t.Fatalf("unknownCommandName = (%q,%v); want (moo,true)", name, ok)
	}
	if _, ok := unknownCommandName(errString("some other failure")); ok {
		t.Fatalf("unknownCommandName matched an unrelated error")
	}
	if _, ok := unknownCommandName(nil); ok {
		t.Fatalf("unknownCommandName matched nil")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
