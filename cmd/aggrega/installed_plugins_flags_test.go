// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/spf13/cobra"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// greetURI and greetDigest identify the fixture plugin the flag tests register.
const (
	greetURI    = "oci://example/greet:1.0.0"
	greetDigest = "sha256:greet-pin"
)

// skipWarningPrefix is the opening every flag-declaration skip warning shares
// for the fixture plugin, so each row asserts only the part that names its fault.
const skipWarningPrefix = `warning: CLI plugin "` + greetURI + `" was not added as a command: `

// buildRootNoPanic builds the command tree and fails the test, rather than the
// test binary, if construction panics. testing.T does not fail on a recovered
// panic by itself, and a panic here is exactly the defect under test.
func buildRootNoPanic(t *testing.T) (root *cobra.Command) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("building the command tree panicked: %v", r)
		}
	}()
	return newRootCmd()
}

// hasCommand reports whether root carries a top-level command named name.
func hasCommand(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

// TestDeclaredFlags_GuardSkipsThePlugin drives one configured CLI plugin through
// the real command tree for each way a flag declaration can fail the host's
// check, one fault per row, plus positive controls. A failing declaration must
// not panic while the tree is built; it must leave the plugin's command out of
// the tree and say why in one warning naming the plugin, the command, the flag
// and the fault. A passing declaration must render in help as the kind its type
// names, with the default the flag library parsed.
func TestDeclaredFlags_GuardSkipsThePlugin(t *testing.T) {
	// Not parallel: mutates process-global cliPluginRegistrations and the warning writer.
	for _, tc := range []struct {
		name  string
		flags []wasm.PluginCommandFlag
		// fault is the warning text after the shared prefix; empty means the
		// declarations are accepted and the command registers.
		fault string
		// help lists what 'aggrega help greet' must show for an accepted row.
		help []string
	}{
		{
			name: "every type and a shorthand accepted",
			flags: []wasm.PluginCommandFlag{
				{Name: "verbose", Shorthand: "v", Description: "talk more", Type: "bool", Default: "true"},
				{Name: "times", Shorthand: "7", Description: "how many times", Type: "int", Default: "3"},
				{Name: "who", Shorthand: "W", Description: "who to greet", Type: "string", Default: "world"},
				{Name: "untyped", Description: "a flag of no declared type"},
			},
			help: []string{
				"-v, --verbose", "talk more (default true)",
				"-7, --times int", "how many times (default 3)",
				"-W, --who string", `who to greet (default "world")`,
				"--untyped string", "a flag of no declared type",
			},
		},
		{
			name:  "a flag with no name",
			flags: []wasm.PluginCommandFlag{{Name: ""}},
			fault: `its command "greet" declares a flag with no name`,
		},
		{
			name:  "a name declared twice",
			flags: []wasm.PluginCommandFlag{{Name: "same"}, {Name: "same"}},
			fault: `its command "greet" declares the flag "--same" twice`,
		},
		{
			name:  "a name every command carries as a global flag",
			flags: []wasm.PluginCommandFlag{{Name: "offline", Type: "bool"}},
			fault: `its command "greet" declares the flag "--offline", which every aggrega command already has`,
		},
		{
			name:  "the help flag's name",
			flags: []wasm.PluginCommandFlag{{Name: "help"}},
			fault: `its command "greet" declares the flag "--help", which every aggrega command already has`,
		},
		{
			name:  "a shorthand of two characters",
			flags: []wasm.PluginCommandFlag{{Name: "verbose", Shorthand: "vv"}},
			fault: `its command "greet" declares the flag "--verbose" with the shorthand "vv"; a shorthand is a single ASCII letter or digit`,
		},
		{
			name:  "a single non-ASCII shorthand",
			flags: []wasm.PluginCommandFlag{{Name: "accent", Shorthand: "é"}},
			fault: `its command "greet" declares the flag "--accent" with the shorthand "é"; a shorthand is a single ASCII letter or digit`,
		},
		{
			name:  "a hyphen as the shorthand",
			flags: []wasm.PluginCommandFlag{{Name: "dash", Shorthand: "-"}},
			fault: `its command "greet" declares the flag "--dash" with the shorthand "-"; a shorthand is a single ASCII letter or digit`,
		},
		{
			name:  "a shorthand declared for two flags",
			flags: []wasm.PluginCommandFlag{{Name: "x1", Shorthand: "x"}, {Name: "x2", Shorthand: "x"}},
			fault: `its command "greet" declares the shorthand "x" for both "--x1" and "--x2"`,
		},
		{
			name:  "the help flag's shorthand",
			flags: []wasm.PluginCommandFlag{{Name: "hush", Shorthand: "h", Type: "bool"}},
			fault: `its command "greet" declares the shorthand "h" for "--hush", which every aggrega command already uses for "--help"`,
		},
		{
			name:  "a type the host does not render",
			flags: []wasm.PluginCommandFlag{{Name: "loud", Type: "boolean"}},
			fault: `its command "greet" declares the flag "--loud" with the type "boolean"; a flag's type is string, bool or int`,
		},
		{
			name:  "a bool default that does not parse",
			flags: []wasm.PluginCommandFlag{{Name: "loud", Type: "bool", Default: "maybe"}},
			fault: `its command "greet" declares the flag "--loud" with the default "maybe", which is not a valid bool value`,
		},
		{
			name:  "an int default that does not parse",
			flags: []wasm.PluginCommandFlag{{Name: "times", Type: "int", Default: "three"}},
			fault: `its command "greet" declares the flag "--times" with the default "three", which is not a valid int value`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warn := withRegistrationWarnings(t)
			withCLIRegistrations(t, []cliCommandReg{
				{name: "greet", short: "Greet", flags: tc.flags, uri: greetURI, digest: greetDigest},
			})
			withDispatcher(t, &fakeDispatcher{})

			root := buildRootNoPanic(t)

			if tc.fault != "" {
				if got, want := warn.String(), skipWarningPrefix+tc.fault+"\n"; got != want {
					t.Fatalf("registration warnings:\n got %q\nwant %q", got, want)
				}
				if hasCommand(root, "greet") {
					t.Fatalf("a plugin whose flag declaration fails must not register its command")
				}
				return
			}

			if warn.Len() != 0 {
				t.Fatalf("accepted declarations drew a warning:\n%s", warn.String())
			}
			if !hasCommand(root, "greet") {
				t.Fatalf("accepted declarations did not register the command")
			}
			out := &bytes.Buffer{}
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs([]string{"help", "greet"})
			if err := root.Execute(); err != nil {
				t.Fatalf("help greet: %v", err)
			}
			for _, want := range tc.help {
				if !strings.Contains(out.String(), want) {
					t.Errorf("help greet lacks %q:\n%s", want, out.String())
				}
			}
		})
	}
}

// TestDeclaredFlags_SkippedPluginLeavesEveryOtherCommandWorking proves the skip is
// whole-plugin and contained: a plugin with two commands, only one of which
// declares a bad flag, registers neither, while another plugin's command and a
// built-in both still run in the same tree.
func TestDeclaredFlags_SkippedPluginLeavesEveryOtherCommandWorking(t *testing.T) {
	// Not parallel: mutates process-global cliPluginRegistrations and the warning writer.
	warn := withRegistrationWarnings(t)
	withCLIRegistrations(t, []cliCommandReg{
		{name: "greet-ok", short: "Greet", flags: []wasm.PluginCommandFlag{{Name: "ok", Shorthand: "o", Type: "bool"}}, uri: greetURI, digest: greetDigest},
		{name: "greet-bad", short: "Greet", flags: []wasm.PluginCommandFlag{{Name: "verbose", Shorthand: "vv"}}, uri: greetURI, digest: greetDigest},
		{name: "hello", short: "Say hello", flags: []wasm.PluginCommandFlag{{Name: "name", Shorthand: "n", Default: "world"}}, uri: "oci://example/hello:1.0.0", digest: "sha256:hello-pin"},
	})
	withCLIDigest(t, "sha256:hello-pin", nil)
	d := &fakeDispatcher{code: 0}
	withDispatcher(t, d)

	root := buildRootNoPanic(t)

	want := skipWarningPrefix + `its command "greet-bad" declares the flag "--verbose" with the shorthand "vv"; a shorthand is a single ASCII letter or digit` + "\n"
	if got := warn.String(); got != want {
		t.Fatalf("registration warnings:\n got %q\nwant %q", got, want)
	}
	for _, name := range []string{"greet-ok", "greet-bad"} {
		if hasCommand(root, name) {
			t.Errorf("command %q of the skipped plugin was registered; the whole plugin must be skipped", name)
		}
	}
	if !hasCommand(root, "hello") {
		t.Fatalf("the well-formed plugin's command was not registered")
	}

	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"hello", "--name", "moon"})
	if code := executeRoot(context.Background(), root); code != 0 {
		t.Fatalf("hello exit = %d; want 0", code)
	}
	if d.calls != 1 || d.gotCommand != "hello" {
		t.Fatalf("hello did not dispatch to its plugin: calls=%d command=%q", d.calls, d.gotCommand)
	}

	version := buildRootNoPanic(t)
	version.SetOut(&bytes.Buffer{})
	version.SetErr(&bytes.Buffer{})
	version.SetArgs([]string{"version"})
	if code := executeRoot(context.Background(), version); code != 0 {
		t.Fatalf("version exit = %d; want 0 beside a skipped plugin", code)
	}
}

// withWorkspaceRegistrations points the registration source at the production
// projection of rw's plugins.cli list — the describe cache when it is warm, a
// describe of the guest when it is cold — for the duration of the test.
func withWorkspaceRegistrations(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) {
	t.Helper()
	prev := cliPluginRegistrations
	cliPluginRegistrations = func() []cliCommandReg { return cliRegistrationsFromWorkspace(context.Background(), rw) }
	t.Cleanup(func() { cliPluginRegistrations = prev })
}

// workspacePinning writes a workspace config whose plugins.cli list pins one
// plugin at uri and pin, and returns the resolved workspace.
func workspacePinning(t *testing.T, uri, pin string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	dir := t.TempDir()
	body := "workspace: {}\noffline: true\nplugins:\n  cli:\n    - uri: " + uri + "\n      digest: " + pin + "\n"
	if err := os.WriteFile(filepath.Join(dir, workspaceconfig.ConfigFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.ResolveWithoutGlobal(dir, "", nil)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	return rw
}

// withCustomSection returns module with one WebAssembly custom section appended.
// A custom section may sit anywhere in a module and carries nothing the runtime
// executes, so the result loads exactly as module does while hashing to a
// different content digest.
func withCustomSection(module []byte, name string, payload []byte) []byte {
	uleb := func(n int) []byte {
		var out []byte
		for {
			b := byte(n & 0x7f)
			n >>= 7
			if n == 0 {
				return append(out, b)
			}
			out = append(out, b|0x80)
		}
	}
	body := append(uleb(len(name)), name...)
	body = append(body, payload...)
	out := append([]byte{}, module...)
	out = append(out, 0x00)
	out = append(out, uleb(len(body))...)
	return append(out, body...)
}

// TestDeclaredFlags_BothRegistrationPathsReachTheGuard proves the check runs
// whichever path supplied a plugin's commands. Warm: the only source of the
// commands is a planted describe-cache entry, because the pinned URI names a
// file that does not exist, so a cold describe could not have supplied them.
// Cold: no entry exists for the guest's digest, so registration describes a real
// guest whose self-report declares the bad flag — and caches what it read.
//
// The describe cache is a persistent store shared by every run of the test
// binary, so each subtest keys on bytes made unique to this run: the planted
// entry on a nonce, and the guest on a custom section carrying one. A fixed
// digest would find the entry an earlier run left and never take the cold path.
func TestDeclaredFlags_BothRegistrationPathsReachTheGuard(t *testing.T) {
	// Not parallel: mutates process-global cliPluginRegistrations and the warning writer.
	nonce := fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())

	t.Run("warm describe cache", func(t *testing.T) {
		uri := filepath.Join(t.TempDir(), "absent.wasm")
		pin := digest.ContentDigest([]byte("planted describe entry " + nonce))
		writeDescribeCache(pin, wasm.PluginDescriptor{
			AbiVersion: wasm.ABIVersionV1Alpha1,
			PluginType: "cli",
			Commands: []wasm.PluginCLICommand{{
				Name:  "greet",
				Flags: []wasm.PluginCommandFlag{{Name: "verbose", Shorthand: "vv"}},
			}},
			Exports: []wasm.PluginExport{{Name: "cli_run"}},
		})
		if _, ok := readDescribeCache(pin); !ok {
			t.Fatalf("planted describe entry is not readable; the warm path is not under test")
		}
		warn := withRegistrationWarnings(t)
		withWorkspaceRegistrations(t, workspacePinning(t, uri, pin))

		root := buildRootNoPanic(t)

		want := `warning: CLI plugin "` + uri + `" was not added as a command: its command "greet" declares the flag "--verbose" with the shorthand "vv"; a shorthand is a single ASCII letter or digit` + "\n"
		if got := warn.String(); got != want {
			t.Fatalf("registration warnings:\n got %q\nwant %q", got, want)
		}
		if hasCommand(root, "greet") {
			t.Fatalf("the skipped plugin's command was registered from the warm cache")
		}
	})

	t.Run("cold describe of the guest", func(t *testing.T) {
		guest := withCustomSection(buildInstallGuest(t, "cli-bad-flag-guest"), "aggrega-test-nonce", []byte(nonce))
		uri := filepath.Join(t.TempDir(), "cli-bad-flag-guest.wasm")
		if err := os.WriteFile(uri, guest, 0o644); err != nil {
			t.Fatalf("write guest: %v", err)
		}
		pin := digest.ContentDigest(guest)
		if _, ok := readDescribeCache(pin); ok {
			t.Fatalf("describe cache is warm before the run; the cold path is not under test")
		}
		warn := withRegistrationWarnings(t)
		withWorkspaceRegistrations(t, workspacePinning(t, uri, pin))

		root := buildRootNoPanic(t)

		want := `warning: CLI plugin "` + uri + `" was not added as a command: its command "greet-bad" declares the flag "--verbose" with the shorthand "vv"; a shorthand is a single ASCII letter or digit` + "\n"
		if got := warn.String(); got != want {
			t.Fatalf("registration warnings:\n got %q\nwant %q", got, want)
		}
		for _, name := range []string{"greet-ok", "greet-bad"} {
			if hasCommand(root, name) {
				t.Errorf("command %q of the skipped plugin was registered from a cold describe", name)
			}
		}
		if _, ok := readDescribeCache(pin); !ok {
			t.Fatalf("registration did not describe the guest: no describe-cache entry was written for its digest")
		}
	})
}

// flagAgreementRow is one entry of the corpus both the host's flag check and the
// Plugin entity schema judge. schemaReach marks a row whose fault the schema can
// express; on those rows the two must agree. The remaining rows carry faults no
// JSON Schema over one array item can state — a repeated name or shorthand, a
// flag every command carries, a default that must parse as its type — so the
// schema accepts them and the host check alone refuses them.
type flagAgreementRow struct {
	name        string
	flags       []wasm.PluginCommandFlag
	accepted    bool
	schemaReach bool
}

var flagAgreementCorpus = []flagAgreementRow{
	{"bool with shorthand and default", []wasm.PluginCommandFlag{{Name: "verbose", Shorthand: "v", Type: "bool", Default: "true"}}, true, true},
	{"int with default", []wasm.PluginCommandFlag{{Name: "times", Type: "int", Default: "3"}}, true, true},
	{"string with default", []wasm.PluginCommandFlag{{Name: "who", Type: "string", Default: "world"}}, true, true},
	{"no type", []wasm.PluginCommandFlag{{Name: "untyped"}}, true, true},
	{"digit shorthand", []wasm.PluginCommandFlag{{Name: "seven", Shorthand: "7"}}, true, true},
	{"upper-case shorthand", []wasm.PluginCommandFlag{{Name: "upper", Shorthand: "V"}}, true, true},
	{"no name", []wasm.PluginCommandFlag{{Name: ""}}, false, true},
	{"two-character shorthand", []wasm.PluginCommandFlag{{Name: "verbose", Shorthand: "vv"}}, false, true},
	{"non-ASCII shorthand", []wasm.PluginCommandFlag{{Name: "accent", Shorthand: "é"}}, false, true},
	{"hyphen shorthand", []wasm.PluginCommandFlag{{Name: "dash", Shorthand: "-"}}, false, true},
	{"space shorthand", []wasm.PluginCommandFlag{{Name: "space", Shorthand: " "}}, false, true},
	{"punctuation shorthand", []wasm.PluginCommandFlag{{Name: "query", Shorthand: "?"}}, false, true},
	{"type boolean", []wasm.PluginCommandFlag{{Name: "loud", Type: "boolean"}}, false, true},
	{"type Bool", []wasm.PluginCommandFlag{{Name: "loud", Type: "Bool"}}, false, true},
	{"type float", []wasm.PluginCommandFlag{{Name: "ratio", Type: "float"}}, false, true},
	{"bool default maybe", []wasm.PluginCommandFlag{{Name: "loud", Type: "bool", Default: "maybe"}}, false, false},
	{"int default three", []wasm.PluginCommandFlag{{Name: "times", Type: "int", Default: "three"}}, false, false},
	{"name declared twice", []wasm.PluginCommandFlag{{Name: "same"}, {Name: "same"}}, false, false},
	{"shorthand declared twice", []wasm.PluginCommandFlag{{Name: "x1", Shorthand: "x"}, {Name: "x2", Shorthand: "x"}}, false, false},
	{"global flag name", []wasm.PluginCommandFlag{{Name: "offline"}}, false, false},
	{"help flag name", []wasm.PluginCommandFlag{{Name: "help"}}, false, false},
	{"help flag shorthand", []wasm.PluginCommandFlag{{Name: "hush", Shorthand: "h"}}, false, false},
}

// pluginEntityWithFlags renders a minimal CLI Plugin entity whose one mirrored
// command carries flags, writing each field only when it is set — the shape the
// stamp step writes, since an unset field is omitted rather than written empty.
// The name is always written, so an empty one reaches the schema as a value.
func pluginEntityWithFlags(t *testing.T, flags []wasm.PluginCommandFlag) []byte {
	t.Helper()
	entries := make([]map[string]string, 0, len(flags))
	for _, f := range flags {
		e := map[string]string{"name": f.Name}
		for key, value := range map[string]string{"shorthand": f.Shorthand, "description": f.Description, "type": f.Type, "default": f.Default} {
			if value != "" {
				e[key] = value
			}
		}
		entries = append(entries, e)
	}
	doc, err := json.Marshal(map[string]any{
		"apiVersion": "aggrega.ai/v1beta1",
		"kind":       "Plugin",
		"metadata":   map[string]any{"name": "greet", "version": "1.0.0", "pluginType": "cli"},
		"spec": map[string]any{
			"abiVersion": "v1alpha1",
			"commands":   []any{map[string]any{"name": "greet", "flags": entries}},
		},
	})
	if err != nil {
		t.Fatalf("render entity: %v", err)
	}
	return doc
}

// flagAgreementFailures runs every corpus row through the host's flag check and
// through schemaAccepts, and returns one line per row where they do not stand in
// the relation the row declares: the same verdict on a row within the schema's
// reach, schema-accepts-and-host-refuses on a row outside it. A row whose host
// verdict differs from the one it declares is reported too, so a corpus that has
// drifted from the check cannot pass by agreeing with itself.
func flagAgreementFailures(t *testing.T, schemaAccepts func([]byte) bool) []string {
	t.Helper()
	carried := flagsEveryCommandCarries(newRootCmd())
	var failures []string
	for _, row := range flagAgreementCorpus {
		hostAccepts := declarePluginCommandFlags(&cobra.Command{Use: "greet"}, row.flags, carried) == nil
		entityAccepts := schemaAccepts(pluginEntityWithFlags(t, row.flags))
		switch {
		case hostAccepts != row.accepted:
			failures = append(failures, row.name+": the host check's verdict is not the one the row declares")
		case row.schemaReach && entityAccepts != hostAccepts:
			failures = append(failures, row.name+": the host check and the entity schema disagree")
		case !row.schemaReach && !entityAccepts:
			failures = append(failures, row.name+": the entity schema refuses a row marked outside its reach")
		}
	}
	return failures
}

// envelopeAccepts reports whether the document passes the Plugin entity's
// envelope validation, the gate a catalog load applies.
func envelopeAccepts(doc []byte) bool {
	_, err := envelope.ValidateBytes(envelope.KindPlugin, doc, "plugin.yaml")
	return err == nil
}

// compiledPluginSchemaAccepts compiles schemaText with the engine the envelope
// uses and returns a verdict function over documents, so a test can judge the
// corpus against a schema other than the embedded one.
func compiledPluginSchemaAccepts(t *testing.T, schemaText string) func([]byte) bool {
	t.Helper()
	s := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemaText), s); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	v, err := jsonvalidator.New(s)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return func(doc []byte) bool {
		var instance any
		if err := json.Unmarshal(doc, &instance); err != nil {
			t.Fatalf("decode document: %v", err)
		}
		report, err := v.Validate(envelope.NormaliseYAML(instance), nil)
		if err != nil {
			t.Fatalf("validate document: %v", err)
		}
		return len(report.Findings) == 0
	}
}

// TestPluginFlagGuardAgreesWithTheEntitySchema holds the host's flag check and
// the Plugin entity schema's command mirror to one shape: one corpus of
// declarations judged by both, where the two sides must agree on every fault the
// schema can express. The plant subtest proves the comparison can fail: the same
// corpus judged against the schema with one type dropped from its enum must
// report the rows that type carries, and the unplanted schema compiled the same
// way must report nothing, so the failure is the plant's and not the harness's.
func TestPluginFlagGuardAgreesWithTheEntitySchema(t *testing.T) {
	t.Run("the envelope gate", func(t *testing.T) {
		if failures := flagAgreementFailures(t, envelopeAccepts); len(failures) != 0 {
			t.Fatalf("the host flag check and the Plugin schema diverge:\n%s", strings.Join(failures, "\n"))
		}
	})

	t.Run("a planted divergence is caught", func(t *testing.T) {
		const anchor = `"enum": ["string", "bool", "int"]`
		if n := strings.Count(schemas.PluginV1beta1, anchor); n != 1 {
			t.Fatalf("plant anchor %s appears %d times in the Plugin schema; want exactly 1", anchor, n)
		}
		if failures := flagAgreementFailures(t, compiledPluginSchemaAccepts(t, schemas.PluginV1beta1)); len(failures) != 0 {
			t.Fatalf("the unplanted schema, compiled directly, diverges from the host check:\n%s", strings.Join(failures, "\n"))
		}
		planted := strings.Replace(schemas.PluginV1beta1, anchor, `"enum": ["string", "bool"]`, 1)
		failures := flagAgreementFailures(t, compiledPluginSchemaAccepts(t, planted))
		want := strings.Join([]string{
			"int with default: the host check and the entity schema disagree",
			"int default three: the entity schema refuses a row marked outside its reach",
		}, "\n")
		if got := strings.Join(failures, "\n"); got != want {
			t.Fatalf("the planted divergence was not reported as expected:\n got %q\nwant %q", got, want)
		}
	})
}
