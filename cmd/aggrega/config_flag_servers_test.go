// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The global --config flag names the file the cascade's global layer is read
// from. The two long-running commands resolve a workspace afresh for each call or
// request rather than reading the one this invocation resolved, so --config
// reaches what they answer only if each command hands its server the value the
// root command composed. The servers' own packages prove what they do with it
// once they hold it; these drive the real command tree and prove that each command
// hands it over, against the command line's own reading of the same flag.

// serverConfigFlagRow is one row of the proofs below: whether --config names the
// file that turns the posture on, whether $AGGREGA_CONFIG names the file that
// turns it off, and which of the two the server must then report the posture
// from.
type serverConfigFlagRow struct {
	name      string
	named     bool
	exported  bool
	wantValue string
	wantFrom  string // "named", "exported" or the built-in-defaults label
}

var serverConfigFlagRows = []serverConfigFlagRow{
	{name: "no --config (the control)", wantValue: "false", wantFrom: "(built-in defaults)"},
	{name: "--config naming a file that turns the posture on", named: true, wantValue: "true", wantFrom: "named"},
	{
		name: "--config over $AGGREGA_CONFIG naming a file that turns it off", named: true, exported: true,
		wantValue: "true", wantFrom: "named",
	},
	{name: "$AGGREGA_CONFIG alone", exported: true, wantValue: "false", wantFrom: "exported"},
}

// configFlagFixture prepares one row: an empty machine-wide configuration, the
// two global files, the environment the row names, a workspace whose registry is
// on this machine, and the arguments that go before the subcommand. It returns
// the workspace root, those arguments, the registry's hit count, and the source
// spelling the row expects.
func configFlagFixture(t *testing.T, row serverConfigFlagRow) (root string, args []string, reached func() int64, wantSrc string) {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("XDG_CONFIG_HOME", empty)
	t.Setenv("AGGREGA_OFFLINE", "")
	files := t.TempDir()
	named := filepath.Join(files, "named.yaml")
	exported := filepath.Join(files, "exported.yaml")
	for path, body := range map[string]string{named: "offline: true\n", exported: "offline: false\n"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	t.Setenv(workspaceconfig.EnvConfigOverride, "")
	if row.exported {
		t.Setenv(workspaceconfig.EnvConfigOverride, exported)
	}
	if row.named {
		args = []string{"--" + workspaceconfig.ConfigFlagName, named}
	}
	root, reached = loopbackRegistryWorkspace(t, "")
	wantSrc = map[string]string{"named": named, "exported": exported}[row.wantFrom]
	if wantSrc == "" {
		wantSrc = row.wantFrom
	}
	return root, args, reached, wantSrc
}

// TestTheMCPCommandHandsItsServerTheConfigFlag runs `aggrega [--config file]
// mcp` from the real command tree over its own streams and asks the
// configuration reading what is in force and from where, beside `aggrega config
// show --origin` run with the same arguments: the server has to name the layer
// the command line names.
//
// 🔴 PLANTED DEFECT: stop flagOverlayFromCobra recording --config and the --config
// rows report the built-in default — and, being online, reach the loopback
// registry for the documentation bundle, which the zero-reach check also
// catches — while the command line's own reading still names the file.
func TestTheMCPCommandHandsItsServerTheConfigFlag(t *testing.T) {
	// Not parallel: it sets the process environment and drives the command tree,
	// whose pre-run writes the process-global display decision.
	for _, row := range serverConfigFlagRows {
		t.Run(row.name, func(t *testing.T) {
			root, args, reached, wantSrc := configFlagFixture(t, row)

			var stdin, stdout, stderr bytes.Buffer
			stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
			stdin.WriteString(rpcLine(2, "tools/call", `{"name":"config-show","arguments":{}}`))
			cmd := newRootCmd()
			cmd.SetIn(&stdin)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(append(append([]string{}, args...), "mcp", "--workspace-root", root))
			if code := executeRoot(context.Background(), cmd); code != 0 {
				t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
			}

			answers := answersByID(t, stdout.String(), 1, 2)
			if answers[2].Error != nil {
				t.Fatalf("config-show was refused: %s", answers[2].Error.Message)
			}
			var call struct {
				StructuredContent struct {
					Settings []struct{ Field, Value, Source string } `json:"settings"`
				} `json:"structuredContent"`
				IsError bool `json:"isError"`
			}
			if err := json.Unmarshal(answers[2].Result, &call); err != nil || call.IsError {
				t.Fatalf("config-show did not answer with its reading (isError=%v): %v\n%s",
					call.IsError, err, answers[2].Result)
			}
			value, src := offlineSettingIn(t, call.StructuredContent.Settings)
			if value != row.wantValue || src != wantSrc {
				t.Errorf("config-show reports offline = %q from %q; want %q from %q", value, src, row.wantValue, wantSrc)
			}
			if row.wantValue == "true" && reached() != 0 {
				t.Errorf("a server started with %v reached the registry %d time(s)", args, reached())
			}
			assertTheCommandLineAgrees(t, root, args, row.wantValue, wantSrc)
		})
	}
}

// TestTheServeCommandHandsItsDaemonTheConfigFlag runs `aggrega [--config file]
// serve` from the real command tree and asks the daemon for the effective
// configuration of the workspace it was launched over, beside `aggrega config
// show --origin` run with the same arguments.
//
// 🔴 PLANTED DEFECT: stop flagOverlayFromCobra recording --config and the --config
// rows report the built-in default while the command line's own reading still
// names the file.
func TestTheServeCommandHandsItsDaemonTheConfigFlag(t *testing.T) {
	// Not parallel: it sets the process environment and drives the command tree,
	// whose pre-run writes the process-global display decision.
	for _, row := range serverConfigFlagRows {
		t.Run(row.name, func(t *testing.T) {
			root, args, _, wantSrc := configFlagFixture(t, row)

			body, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "aggrega/effectiveConfiguration",
				"params": map[string]any{"workspaceRoot": root},
			})
			if err != nil {
				t.Fatalf("marshal the request: %v", err)
			}
			var stdin, stdout, stderr bytes.Buffer
			fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
			stdin.Write(body)
			cmd := newRootCmd()
			cmd.SetIn(&stdin)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(append(append([]string{}, args...), "serve", "--workspace-root", root))
			if code := executeRoot(context.Background(), cmd); code != 0 {
				t.Fatalf("aggrega serve exited %d\nstderr:\n%s", code, stderr.String())
			}

			frame := daemonFrameAnswering(t, stdout.Bytes(), 1)
			if raw, refused := frame["error"]; refused {
				t.Fatalf("aggrega/effectiveConfiguration was refused: %s", raw)
			}
			var result struct {
				Settings []struct{ Field, Value, Source string } `json:"settings"`
			}
			if err := json.Unmarshal(frame["result"], &result); err != nil {
				t.Fatalf("the answer is not the effective-configuration shape: %v\n%s", err, frame["result"])
			}
			value, src := offlineSettingIn(t, result.Settings)
			if value != row.wantValue || src != wantSrc {
				t.Errorf("aggrega/effectiveConfiguration reports offline = %q from %q; want %q from %q",
					value, src, row.wantValue, wantSrc)
			}
			assertTheCommandLineAgrees(t, root, args, row.wantValue, wantSrc)
		})
	}
}

// TestTheCommandSurfaceMarksTheCommandsThatPassTheirArgumentsOn holds the one
// fact the server reads to decide whether a generated tool's command is handed
// the server's own global flags: whether the command parses its flags at all. A
// command an installed extension contributes passes every argument on to the
// extension verbatim and parses no global flag, at a terminal or anywhere else,
// so putting the server's flags in front of it would hand the extension
// arguments it never declared.
//
// The fact is read off the tree, so every entry is held to its command's own
// answer; the extension registered through the production registration path is
// the row that makes the comparison discriminating, and an ordinary command is
// its control.
//
// 🔴 PLANTED DEFECT: drop ForwardsFlags from the CommandSurface literal
// commandSurface builds and the extension's entry reads false.
func TestTheCommandSurfaceMarksTheCommandsThatPassTheirArgumentsOn(t *testing.T) {
	withCLIRegistrations(t, []cliCommandReg{{name: "relay-ext", short: "An installed extension"}})
	root := executingRootCmd()

	want := map[string]bool{}
	for _, c := range reachableCommands(root) {
		want[c.CommandPath()] = c.DisableFlagParsing
	}
	marked := map[string]bool{}
	for _, cs := range commandSurface(root) {
		marked[cs.Descriptor.Path] = cs.ForwardsFlags
		if w, ok := want[cs.Descriptor.Path]; ok && cs.ForwardsFlags != w {
			t.Errorf("%q is marked as passing its arguments on = %v; the tree says %v", cs.Descriptor.Path, cs.ForwardsFlags, w)
		}
	}
	if forwards, ok := marked["aggrega relay-ext"]; !ok || !forwards {
		t.Errorf("the installed extension's command is on the surface = %v, marked as passing its arguments on = %v; "+
			"want both", ok, forwards)
	}
	if forwards, ok := marked["aggrega config show"]; !ok || forwards {
		t.Errorf("an ordinary command is on the surface = %v, marked as passing its arguments on = %v; want present "+
			"and not marked", ok, forwards)
	}
}

// assertTheCommandLineAgrees runs `aggrega [args] config show --origin` over
// the same workspace and environment and holds its reading of the offline
// setting to the one the server gave. It is the control the two proofs above
// are measured against: the command line has always read --config, so a server
// that agrees with it reads the flag the same way.
func assertTheCommandLineAgrees(t *testing.T, root string, args []string, wantValue, wantSrc string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append(append([]string{}, args...), "config", "show", "--origin", "--workspace-root", root))
	if code := executeRoot(context.Background(), cmd); code != 0 {
		t.Fatalf("aggrega config show --origin exited %d\nstderr:\n%s", code, stderr.String())
	}
	for _, line := range bytes.Split(stdout.Bytes(), []byte("\n")) {
		// A row is the setting, its value, and its source, which may itself hold a
		// space — the built-in-defaults label does.
		fields := bytes.Fields(line)
		if len(fields) >= 3 && string(fields[0]) == workspaceconfig.OfflineSettingKey {
			value, src := string(fields[1]), string(bytes.Join(fields[2:], []byte(" ")))
			if value != wantValue || src != wantSrc {
				t.Errorf("the command line reads offline = %s from %s; the server was held to %q from %q",
					value, src, wantValue, wantSrc)
			}
			return
		}
	}
	t.Fatalf("aggrega config show --origin printed no %s row:\n%s", workspaceconfig.OfflineSettingKey, stdout.String())
}
