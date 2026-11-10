// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
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

// withCLIRegistrations points the registration source at a fixture set of
// commands for the duration of the test and restores it afterward, so no test
// touches the real config cascade or describe cache to build the tree.
func withCLIRegistrations(t *testing.T, regs []cliCommandReg) {
	t.Helper()
	prev := cliPluginRegistrations
	cliPluginRegistrations = func() []cliCommandReg { return regs }
	t.Cleanup(func() { cliPluginRegistrations = prev })
}

// withCLIDigest swaps the run-gate digest seam for the test so the drift check
// runs against a chosen digest without resolving a real guest.
func withCLIDigest(t *testing.T, digest string, err error) {
	t.Helper()
	prev := resolveCLIPluginDigest
	resolveCLIPluginDigest = func(*workspaceconfig.ResolvedWorkspace, string) (string, error) { return digest, err }
	t.Cleanup(func() { resolveCLIPluginDigest = prev })
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

// withUnknownCommandWorkspace points the unknown-command hint's workspace seam at
// rw for the duration of the test, so the hint reads that workspace's index rather
// than none.
func withUnknownCommandWorkspace(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) {
	t.Helper()
	prev := unknownCommandWorkspace
	unknownCommandWorkspace = func() *workspaceconfig.ResolvedWorkspace { return rw }
	t.Cleanup(func() { unknownCommandWorkspace = prev })
}

// refusingRegistryWorkspace writes a workspace whose default registry, and only
// configured registry, is a server on this machine that FAILS the test on any
// request it receives, and returns the workspace root and the server's host. A
// hint or a search run against it proves it read the local index alone: a lookup
// reaching for the registry is a failure, not a slow pass.
func refusingRegistryWorkspace(t *testing.T) (string, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the registry was contacted, and nothing here may contact it: %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename),
		"workspace: {}\ndefault_registry: \""+host+"\"\nregistries:\n  - host: \""+host+"\"\n")
	return root, host
}

// writeCLIPluginIndex writes host's local index through the writer the index build
// uses, holding one record per repository. Each record carries the fields the build
// derives from a published plugin's annotations — its URI and name from the
// repository, the Plugin kind and the plugin type — so the hint and the search
// read the shape a real index holds.
func writeCLIPluginIndex(t *testing.T, root, host string, pluginTypes map[string]string, repos ...string) {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	var records []registryindex.Record
	for _, repo := range repos {
		records = append(records, registryindex.Record{
			URI:        "oci://" + host + "/" + repo,
			Name:       repo,
			Version:    "1.0.0",
			Versions:   []string{"1.0.0"},
			Kind:       "Plugin",
			PluginType: pluginTypes[repo],
		})
	}
	if err := registryops.WriteHostIndex(rw.Root(), rw.Config().ResolvedArtifactCacheDir(),
		registryops.HostIndex{Host: host, Records: records}, searchFrozen()); err != nil {
		t.Fatalf("write the local index: %v", err)
	}
}

// unknownCommandHint drives an unrecognized command through the real command tree
// and returns the diagnostic the exit boundary prints for it.
func unknownCommandHint(t *testing.T, name string) string {
	t.Helper()
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{name})
	err := root.Execute()
	if err == nil {
		t.Fatalf("unknown command %q returned no error", name)
	}
	return unknownCommandSuggestion(t.Context(), formatCompilerError(err), err)
}

// TestConfiguredCLIPlugin_ListedAndDispatched proves a configured CLI plugin
// appears in the command tree with its cached help and, when invoked with a
// matching pin, forwards its argv to the runner and returns exit 0 (no error).
func TestConfiguredCLIPlugin_ListedAndDispatched(t *testing.T) {
	// Not parallel: mutates process-global cliPluginRegistrations, globalDisplay seams other tests read.
	withRegistrationWarnings(t)
	withCLIRegistrations(t, []cliCommandReg{
		{name: "moo", short: "make the cow moo", uri: "oci://example/moo:v1", digest: "pinned-digest"},
	})
	withCLIDigest(t, "pinned-digest", nil)
	d := &fakeDispatcher{code: 0}
	withDispatcher(t, d)

	root := newRootCmd()

	// The command is present in the tree with its cached help as the short help.
	found := &bytes.Buffer{}
	root.SetOut(found)
	root.SetErr(found)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	if !strings.Contains(found.String(), "moo") || !strings.Contains(found.String(), "make the cow moo") {
		t.Fatalf("--help output does not list the configured plugin:\n%s", found.String())
	}

	// Dispatching the command forwards argv to the runner and returns exit 0.
	root2 := newRootCmd()
	root2.SetOut(&bytes.Buffer{})
	root2.SetErr(&bytes.Buffer{})
	root2.SetArgs([]string{"moo", "--loud", "twice"})
	code := executeRoot(context.Background(), root2)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if d.calls != 1 {
		t.Fatalf("runner called %d time(s); want 1", d.calls)
	}
	if d.gotURI != "oci://example/moo:v1" {
		t.Fatalf("runner URI = %q; want the config URI", d.gotURI)
	}
	if d.gotCommand != "moo" {
		t.Fatalf("forwarded command = %q; want the invoked command name %q", d.gotCommand, "moo")
	}
	if got, want := strings.Join(d.gotArgv, " "), "--loud twice"; got != want {
		t.Fatalf("forwarded argv = %q; want %q", got, want)
	}
}

// TestConfiguredCLIPlugin_DeclaredFlagsRenderInHelp proves a configured CLI
// plugin's declared flags render in the command's Flags section like a core
// command's flags — long name, shorthand, and usage all shown — driven through
// 'aggrega help <command>'. The declaration is help-only: flag parsing stays
// disabled, so the command still exposes the flags for help without cobra ever
// parsing them.
func TestConfiguredCLIPlugin_DeclaredFlagsRenderInHelp(t *testing.T) {
	// Not parallel: mutates process-global cliPluginRegistrations, globalDisplay seams other tests read.
	withRegistrationWarnings(t)
	withCLIRegistrations(t, []cliCommandReg{
		{
			name:  "moo",
			short: "make the cow moo",
			flags: []wasm.PluginCommandFlag{
				{Name: "alien", Shorthand: "a", Description: "moo like an alien", Type: "bool"},
				{Name: "accent", Description: "regional accent to moo in", Type: "string", Default: "midwest"},
			},
			uri:    "oci://example/moo:v1",
			digest: "pinned-digest",
		},
	})
	withDispatcher(t, &fakeDispatcher{})

	root := newRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"help", "moo"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help moo returned error: %v", err)
	}
	help := out.String()

	if !strings.Contains(help, "Flags:") {
		t.Fatalf("help output has no Flags section:\n%s", help)
	}
	// The bool flag renders its long name, shorthand, and usage.
	for _, want := range []string{"--alien", "-a,", "moo like an alien"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help output missing %q for the declared bool flag:\n%s", want, help)
		}
	}
	// The string flag renders its long name, kind, usage, and default.
	for _, want := range []string{"--accent", "string", "regional accent to moo in", `"midwest"`} {
		if !strings.Contains(help, want) {
			t.Fatalf("help output missing %q for the declared string flag:\n%s", want, help)
		}
	}
	// The command that owns these flags kept parsing disabled — the declaration is
	// help-only and does not turn cobra into the parser.
	var moo *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "moo" {
			moo = c
		}
	}
	if moo == nil {
		t.Fatalf("moo command missing from tree")
	}
	if !moo.DisableFlagParsing {
		t.Fatalf("declaring flags must not enable cobra flag parsing; DisableFlagParsing is false")
	}
}

// TestConfiguredCLIPlugin_DeclaredFlagsForwardRawArgv proves declaring flags on
// the command does not turn cobra into the parser: the raw argument vector,
// declared flag tokens included, still reaches the guest verbatim.
func TestConfiguredCLIPlugin_DeclaredFlagsForwardRawArgv(t *testing.T) {
	// Not parallel: mutates process-global cliPluginRegistrations, globalDisplay seams other tests read.
	withRegistrationWarnings(t)
	withCLIRegistrations(t, []cliCommandReg{
		{
			name:  "moo",
			short: "make the cow moo",
			flags: []wasm.PluginCommandFlag{
				{Name: "alien", Shorthand: "a", Description: "moo like an alien", Type: "bool"},
			},
			uri:    "oci://example/moo:v1",
			digest: "pinned-digest",
		},
	})
	withCLIDigest(t, "pinned-digest", nil)
	d := &fakeDispatcher{code: 0}
	withDispatcher(t, d)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"moo", "--alien", "extra", "--unknown-to-cobra"})
	if code := executeRoot(context.Background(), root); code != 0 {
		t.Fatalf("exit code = %d; want 0", code)
	}
	if d.calls != 1 {
		t.Fatalf("runner called %d time(s); want 1", d.calls)
	}
	if got, want := strings.Join(d.gotArgv, " "), "--alien extra --unknown-to-cobra"; got != want {
		t.Fatalf("forwarded argv = %q; want the raw vector %q", got, want)
	}
}

// TestConfiguredCLIPlugin_BuiltinReservedAndDuplicateRefused proves a command
// named after a built-in, one on the reserved denylist, and a second plugin
// contributing an already-registered name are all refused registration with an
// observable diagnostic, and the built-in keeps its own command.
func TestConfiguredCLIPlugin_BuiltinReservedAndDuplicateRefused(t *testing.T) {
	// Not parallel: mutates process-global cliPluginRegistrations, globalDisplay seams other tests read.
	warn := withRegistrationWarnings(t)
	const sentinel = "SHADOW ATTEMPT — should never win"
	withCLIRegistrations(t, []cliCommandReg{
		{name: "build", short: sentinel, uri: "oci://example/evil-build:v1", digest: "d"},
		{name: "deploy", short: sentinel, uri: "oci://example/evil-deploy:v1", digest: "d"},
		{name: "moo", short: "first moo", uri: "oci://example/moo-a:v1", digest: "d"},
		{name: "moo", short: "second moo", uri: "oci://example/moo-b:v1", digest: "d"},
	})
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
		t.Fatalf("configured plugin shadowed the built-in build command")
	}

	// The reserved "deploy" name registered nothing.
	for _, c := range root.Commands() {
		if c.Name() == "deploy" {
			t.Fatalf("reserved name deploy was registered as a command")
		}
	}

	// The first "moo" registered once; the second is refused as a duplicate.
	var mooCount int
	var moo *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "moo" {
			mooCount++
			moo = c
		}
	}
	if mooCount != 1 {
		t.Fatalf("moo registered %d times; want exactly 1", mooCount)
	}
	if moo.Short != "first moo" {
		t.Fatalf("first contributor should win the name; got short %q", moo.Short)
	}

	out := warn.String()
	if !strings.Contains(out, "build") || !strings.Contains(out, "built-in") {
		t.Fatalf("no observable diagnostic for the built-in collision:\n%s", out)
	}
	if !strings.Contains(out, "deploy") || !strings.Contains(out, "reserved") {
		t.Fatalf("no observable diagnostic for the reserved-name collision:\n%s", out)
	}
	if !strings.Contains(out, "already contributed") {
		t.Fatalf("no observable diagnostic for the duplicate-name collision:\n%s", out)
	}
}

// TestConfiguredCLIPlugin_ExitCodePropagation proves a plugin exit code threads
// through executeRoot: a non-zero code becomes the process exit code, and zero
// exits zero.
func TestConfiguredCLIPlugin_ExitCodePropagation(t *testing.T) {
	// Not parallel: mutates process-global cliPluginRegistrations, globalDisplay seams other tests read.
	withRegistrationWarnings(t)
	withCLIRegistrations(t, []cliCommandReg{
		{name: "moo", short: "moo", uri: "oci://example/moo:v1", digest: "pinned"},
	})
	withCLIDigest(t, "pinned", nil)

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
			if got := executeRoot(context.Background(), root); got != tc.code {
				t.Fatalf("executeRoot exit = %d; want %d", got, tc.code)
			}
		})
	}
}

// TestUnknownCommandHintReadsTheLocalIndex proves the hint an unknown command gets
// is decided by the default registry's local index, and by nothing else: each row
// runs against a registry that fails the test on any request, so a hint that
// reached the registry fails rather than passing slowly.
//
// A planted defect makes a row fail: point the hint at the registry rather than the
// index and every row reports the request; drop the plugin-type narrowing and the
// "without" row prints the install hint for the provider plugin named hep; read a
// missing index as an empty one and the "no index" row prints the search hint
// rather than the command that adds the plugin.
func TestUnknownCommandHintReadsTheLocalIndex(t *testing.T) {
	// Not parallel: mutates the process-global registration, dispatcher and
	// unknown-command workspace seams other tests read.
	withRegistrationWarnings(t)
	withCLIRegistrations(t, nil)
	withDispatcher(t, &fakeDispatcher{})

	cases := []struct {
		name  string
		repos []string // nil writes no index at all
		types map[string]string
		want  string
	}{
		{
			name:  "an index holding the CLI plugin prints the install hint",
			repos: []string{"plugin/cli/hep", "plugin/cli/moo"},
			types: map[string]string{"plugin/cli/hep": "cli", "plugin/cli/moo": "cli"},
			want:  `"hep" is not a command — install the "hep" plugin with 'aggrega plugin add cli hep'`,
		},
		{
			name:  "an index without the CLI plugin prints the search hint",
			repos: []string{"plugin/cli/moo", "plugin/provider/hep"},
			types: map[string]string{"plugin/cli/moo": "cli", "plugin/provider/hep": "provider"},
			want:  `"hep" is not a command — to look for a CLI plugin of that name, run: aggrega search hep --kind plugin --plugin-type cli`,
		},
		{
			name: "no index prints the command that adds the plugin directly",
			want: `"hep" is not a command — to add a CLI plugin of that name, run: aggrega plugin add cli hep`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, host := refusingRegistryWorkspace(t)
			if tc.repos != nil {
				writeCLIPluginIndex(t, root, host, tc.types, tc.repos...)
			}
			rw, err := workspaceconfig.Resolve(root, "")
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			withUnknownCommandWorkspace(t, rw)

			got := unknownCommandHint(t, "hep")
			t.Logf("diagnostic:\n%s", got)
			if !strings.Contains(got, `unknown command "hep" for "aggrega"`) {
				t.Errorf("the hint replaced cobra's own message rather than following it:\n%s", got)
			}
			if !strings.HasSuffix(got, "\n"+tc.want) {
				t.Errorf("diagnostic ends\n%s\nwant it to end with the hint\n%s", got, tc.want)
			}
		})
	}
}

// TestUnknownCommandSearchHintRunsAsPrinted runs the search command the hint
// prints, exactly as printed, through the real command tree against an index
// holding a CLI plugin of that name, and proves it finds that plugin — so the
// printed flags are ones the search accepts and the plugin type it names is the
// one a CLI plugin's record carries. The index also holds a provider plugin of the
// same name, which the printed search must leave out.
//
// A planted defect makes this fail: print a plugin type other than the one a CLI
// plugin records and the search finds nothing; drop the type from the printed line
// and the provider plugin is found too.
func TestUnknownCommandSearchHintRunsAsPrinted(t *testing.T) {
	// Not parallel: mutates the process-global registration, dispatcher, search
	// clock and unknown-command workspace seams other tests read.
	withRegistrationWarnings(t)
	withCLIRegistrations(t, nil)
	withDispatcher(t, &fakeDispatcher{})
	withSearchClock(t, searchFrozen().Add(time.Hour))

	root, host := refusingRegistryWorkspace(t)
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	withUnknownCommandWorkspace(t, rw)

	// The hint is taken while the index holds no CLI plugin named hep, which is
	// when the search hint is what the reader is shown.
	writeCLIPluginIndex(t, root, host, map[string]string{"plugin/provider/hep": "provider"}, "plugin/provider/hep")
	hint := unknownCommandHint(t, "hep")
	_, line, ok := strings.Cut(hint, "run: ")
	if !ok {
		t.Fatalf("the diagnostic prints no search command (no %q):\n%s", "run: ", hint)
	}
	if strings.ContainsAny(line, `'"\`) {
		t.Fatalf("the printed command %q carries shell quoting this test does not split; the name was plain", line)
	}
	args := strings.Fields(line)
	if len(args) < 2 || args[0] != "aggrega" || args[1] != "search" {
		t.Fatalf("the printed command %q is not an 'aggrega search' invocation", line)
	}

	// Now the plugin is published: the index holds the CLI plugin as well.
	writeCLIPluginIndex(t, root, host,
		map[string]string{"plugin/cli/hep": "cli", "plugin/provider/hep": "provider"},
		"plugin/cli/hep", "plugin/provider/hep")

	cmd := newRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"--workspace-root", root}, append(args[1:], "-o", "names")...))
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("the printed search command %q failed: %v\nstderr:\n%s", line, err, errOut.String())
	}
	t.Logf("printed command: %s\nstdout:\n%s", line, out.String())
	want := "oci://" + host + "/plugin/cli/hep\n"
	if out.String() != want {
		t.Errorf("the printed search found\n%q\nwant exactly the CLI plugin\n%q", out.String(), want)
	}
}

// TestCLIPluginSearchCommandWritesTheNameAsOneShellWord proves the printed search
// command carries the typed name as a single word a POSIX shell reads back as the
// name, quoting it only where the name holds a character the shell would read.
func TestCLIPluginSearchCommandWritesTheNameAsOneShellWord(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"hep", "aggrega search hep --kind plugin --plugin-type cli"},
		{"my-tool_2.0", "aggrega search my-tool_2.0 --kind plugin --plugin-type cli"},
		{"two words", "aggrega search 'two words' --kind plugin --plugin-type cli"},
		{"it's", `aggrega search 'it'\''s' --kind plugin --plugin-type cli`},
		{"$(x)", "aggrega search '$(x)' --kind plugin --plugin-type cli"},
		{"~home", "aggrega search '~home' --kind plugin --plugin-type cli"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cliPluginSearchCommand(tc.name); got != tc.want {
				t.Errorf("cliPluginSearchCommand(%q) = %q; want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestCLIPluginAddCommandWritesTheNameAsOneShellWord proves the printed command
// that adds a CLI plugin carries the typed name as a single word a POSIX shell
// reads back as the name, quoting it only where the name holds a character the
// shell would read.
func TestCLIPluginAddCommandWritesTheNameAsOneShellWord(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"moo", "aggrega plugin add cli moo"},
		{"two words", "aggrega plugin add cli 'two words'"},
		{"it's", `aggrega plugin add cli 'it'\''s'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cliPluginAddCommand(tc.name); got != tc.want {
				t.Errorf("cliPluginAddCommand(%q) = %q; want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestConfiguredCLIPlugin_DriftHaltsBeforeRun proves the drift guard is a loud
// halt, not an advisory: when the freshly resolved bytes hash differently from
// the pinned digest, the command refuses to run — the dispatcher is never called
// — and the error names the drift and instructs a re-pin. Re-pinning (the config
// digest now matches the resolved bytes) re-enables the command.
func TestConfiguredCLIPlugin_DriftHaltsBeforeRun(t *testing.T) {
	// Not parallel: mutates process-global cliPluginRegistrations, globalDisplay seams other tests read.
	withRegistrationWarnings(t)
	withCLIRegistrations(t, []cliCommandReg{
		{name: "moo", short: "moo", uri: "oci://example/moo:v1", digest: "pinned-digest"},
	})

	// Drift: the resolved bytes hash to something other than the pin.
	withCLIDigest(t, "drifted-digest", nil)
	d := &fakeDispatcher{code: 0}
	withDispatcher(t, d)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"moo"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("drift run should return a host error, not succeed")
	}
	if d.calls != 0 {
		t.Fatalf("dispatcher must not run on drift; got %d call(s)", d.calls)
	}
	// A drift halt is a host error, not a plugin exit status.
	if _, ok := err.(exitCodeError); ok {
		t.Fatalf("drift should be a host error, not an exit-code status; got %v", err)
	}
	got := err.Error()
	if !strings.Contains(got, "no longer match the pinned digest") {
		t.Fatalf("drift error should name the digest mismatch; got:\n%s", got)
	}
	if !strings.Contains(got, "aggrega plugin add cli moo") {
		t.Fatalf("drift error should instruct a re-pin; got:\n%s", got)
	}

	// Re-pin: the resolved bytes now match the (new) pinned digest, so the command
	// runs again.
	withCLIRegistrations(t, []cliCommandReg{
		{name: "moo", short: "moo", uri: "oci://example/moo:v1", digest: "drifted-digest"},
	})
	withCLIDigest(t, "drifted-digest", nil)
	d2 := &fakeDispatcher{code: 0}
	withDispatcher(t, d2)
	root2 := newRootCmd()
	root2.SetOut(&bytes.Buffer{})
	root2.SetErr(&bytes.Buffer{})
	root2.SetArgs([]string{"moo"})
	if code := executeRoot(context.Background(), root2); code != 0 {
		t.Fatalf("re-pinned run exit = %d; want 0", code)
	}
	if d2.calls != 1 {
		t.Fatalf("re-pinned command should run; got %d call(s)", d2.calls)
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
