// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What the command line that started this server says about configuration — the
// global file --config names, and settings such as --offline — has to reach
// every place the server reads configuration: each call's resolution, the
// listener's startup reads, and the command a generated tool runs in a child
// process. The proofs here hold each of those, read off what each one actually
// did: the configuration a call resolved, what reached a registry, what a
// listener said at startup, and what a child command parsed.

// parsedGlobals is what the stand-in commands below read off their argument
// vector: the global flags the parsing one parsed, and everything the
// forwarding one was handed.
type parsedGlobals struct {
	offline      bool
	offlineTyped bool
	config       string
	forwarded    string
}

// globalFlagTree builds a command tree carrying the global flags under the names
// the real root declares them by, a runnable leaf that records what it parsed
// into got, and a leaf that parses nothing and passes every argument on, as a
// command an installed extension contributes does. A fresh tree is built for
// every run, as a fresh process would be, so one run's parse cannot leak into the
// next.
func globalFlagTree(got *parsedGlobals) *cobra.Command {
	root := &cobra.Command{Use: "aggrega", Short: "the compiler", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String(workspaceconfig.ConfigFlagName, "", "path to the global config file")
	root.PersistentFlags().Bool(workspaceconfig.OfflineSettingKey, false, "refuse every network reach")
	root.PersistentFlags().String("workspace-root", "", "workspace root override")
	root.AddCommand(&cobra.Command{
		Use:   "work",
		Short: "Do the work",
		RunE: func(cmd *cobra.Command, _ []string) error {
			got.offline, _ = cmd.Flags().GetBool(workspaceconfig.OfflineSettingKey)
			got.offlineTyped = cmd.Flags().Changed(workspaceconfig.OfflineSettingKey)
			got.config, _ = cmd.Flags().GetString(workspaceconfig.ConfigFlagName)
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use:                "relay [args...]",
		Short:              "Pass every argument on",
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			got.forwarded = strings.Join(args, " ")
			return nil
		},
	})
	return root
}

// TestACommandToolRunsItsCommandUnderTheServersCommandLine drives a generated
// tool through the tools/call path on a server handed a command line, and runs
// the argument vector the tool built through a real command parser — so what is
// asserted is what the command would have read, not only where an argument sits.
//
// The rows hold five properties. A server started with no global flag runs a
// command exactly as before, flag for flag. A server started with --offline or
// --config hands that flag on, in the direction it was typed. A call sending the
// same flag for itself is answered with its own value. A command that parses no
// flag of its own and passes its arguments on is handed none of the server's,
// so what it receives is unchanged. And the command line the tool reports is the
// one that ran, the server's flags included, so a caller can read what
// configuration a result was produced under.
//
// 🔴 PLANTED DEFECT: have invoke pass no server flags to argv and every row
// whose server was handed a flag reads the built-in default in the child.
func TestACommandToolRunsItsCommandUnderTheServersCommandLine(t *testing.T) {
	t.Parallel()
	on, off := true, false
	for _, tc := range []struct {
		name       string
		tool       string // "" is cli-work
		server     workspaceconfig.FlagOverlay
		call       map[string]any
		wantArgv   []string
		wantParsed parsedGlobals
	}{
		{
			name:     "no global flag on the server and none on the call runs the command as before",
			call:     map[string]any{},
			wantArgv: []string{"work"},
		},
		{
			name:       "a call's own flag on a server started with none is unchanged",
			call:       map[string]any{"offline": true},
			wantArgv:   []string{"work", "--offline=true"},
			wantParsed: parsedGlobals{offline: true, offlineTyped: true},
		},
		{
			name:       "--offline on the server reaches the command",
			server:     workspaceconfig.FlagOverlay{Offline: &on},
			call:       map[string]any{},
			wantArgv:   []string{"--offline=true", "work"},
			wantParsed: parsedGlobals{offline: true, offlineTyped: true},
		},
		{
			name:       "--offline=false on the server reaches the command in that direction",
			server:     workspaceconfig.FlagOverlay{Offline: &off},
			call:       map[string]any{},
			wantArgv:   []string{"--offline=false", "work"},
			wantParsed: parsedGlobals{offline: false, offlineTyped: true},
		},
		{
			name:       "--config on the server reaches the command",
			server:     workspaceconfig.FlagOverlay{ConfigPath: "/etc/aggrega/global.yaml"},
			call:       map[string]any{},
			wantArgv:   []string{"--config=/etc/aggrega/global.yaml", "work"},
			wantParsed: parsedGlobals{config: "/etc/aggrega/global.yaml"},
		},
		{
			name:       "both, the global file first",
			server:     workspaceconfig.FlagOverlay{ConfigPath: "/g.yaml", Offline: &on},
			call:       map[string]any{},
			wantArgv:   []string{"--config=/g.yaml", "--offline=true", "work"},
			wantParsed: parsedGlobals{offline: true, offlineTyped: true, config: "/g.yaml"},
		},
		{
			name:       "a call's own offline wins over the server's",
			server:     workspaceconfig.FlagOverlay{Offline: &on},
			call:       map[string]any{"offline": false},
			wantArgv:   []string{"--offline=true", "work", "--offline=false"},
			wantParsed: parsedGlobals{offline: false, offlineTyped: true},
		},
		{
			name:       "a call's own config wins over the server's",
			server:     workspaceconfig.FlagOverlay{ConfigPath: "/g.yaml"},
			call:       map[string]any{"config": "/elsewhere.yaml"},
			wantArgv:   []string{"--config=/g.yaml", "work", "--config=/elsewhere.yaml"},
			wantParsed: parsedGlobals{config: "/elsewhere.yaml"},
		},
		{
			name:       "a command that passes its arguments on is handed none of the server's flags",
			tool:       "cli-relay",
			server:     workspaceconfig.FlagOverlay{ConfigPath: "/g.yaml", Offline: &on},
			call:       map[string]any{"args": []string{"x"}},
			wantArgv:   []string{"relay", "--", "x"},
			wantParsed: parsedGlobals{forwarded: "-- x"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ranArgv []string
			var parsed parsedGlobals
			run := func(_ context.Context, args []string, stdout, stderr io.Writer) error {
				ranArgv = append([]string(nil), args...)
				root := globalFlagTree(&parsed)
				root.SetArgs(args)
				root.SetOut(stdout)
				root.SetErr(stderr)
				return root.Execute()
			}
			s := &server{
				out:          io.Discard,
				logw:         io.Discard,
				startDir:     t.TempDir(),
				flags:        tc.server,
				runCommand:   run,
				commandTools: deriveCommandTools(projectCommandSurface(globalFlagTree(&parsedGlobals{}))),
			}

			tool := tc.tool
			if tool == "" {
				tool = "cli-work"
			}
			res := callTool(t, s, tool, tc.call)
			if res.IsError {
				t.Fatalf("%s failed: %s", tool, res.Content[0].Text)
			}
			if !slices.Equal(ranArgv, tc.wantArgv) {
				t.Errorf("the command ran with %q; want %q", ranArgv, tc.wantArgv)
			}
			if parsed != tc.wantParsed {
				t.Errorf("the command parsed %+v; want %+v", parsed, tc.wantParsed)
			}
			var payload commandToolResultPayload
			if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
				t.Fatalf("the structured payload does not decode: %v", err)
			}
			if want := "aggrega " + strings.Join(tc.wantArgv, " "); payload.Command != want {
				t.Errorf("the tool reports the command %q; want the one that ran, %q", payload.Command, want)
			}
		})
	}
}

// globalFileWorkspace writes a global configuration file holding globalBody and
// a workspace root holding wsBody beneath the root marker, and returns both
// paths.
func globalFileWorkspace(t *testing.T, globalBody, wsBody string) (global, root string) {
	t.Helper()
	global = filepath.Join(t.TempDir(), "global.yaml")
	if err := os.WriteFile(global, []byte(globalBody), 0o644); err != nil {
		t.Fatalf("write the global file: %v", err)
	}
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\n"+wsBody), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return global, root
}

// TestEveryCallReadsTheGlobalFileTheCommandLineNamed drives the one resolution
// every tool reads through, handed a command line that names a global file with
// --config, over the workspace the process was started in and one a call names
// for itself.
//
// The global file turns the posture on and a file exported in $AGGREGA_CONFIG
// turns it off, so each row separates the two routes. Three things are read per
// row: the effective value; the layer it is attributed to, which must be the file
// the command line named, as `aggrega config show --origin` names it; and the
// file a configuration write lands in, which must be the file the configuration
// was read from.
func TestEveryCallReadsTheGlobalFileTheCommandLineNamed(t *testing.T) {
	// Not parallel: it sets the process environment.
	for _, tc := range []struct {
		name      string
		named     bool
		exported  bool
		want      bool
		wantFrom  string // "named", "exported" or the built-in-defaults label
		wantWrite string // "named" or "exported"
	}{
		{name: "--config names the global file", named: true, want: true, wantFrom: "named", wantWrite: "named"},
		{
			name: "--config wins over $AGGREGA_CONFIG", named: true, exported: true,
			want: true, wantFrom: "named", wantWrite: "named",
		},
		{
			name: "$AGGREGA_CONFIG alone (the control)", exported: true,
			want: false, wantFrom: "exported", wantWrite: "exported",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinOfflineEnvironment(t, "")
			named, served := globalFileWorkspace(t, "offline: true\n", "")
			exported := filepath.Join(t.TempDir(), "exported.yaml")
			if err := os.WriteFile(exported, []byte("offline: false\n"), 0o644); err != nil {
				t.Fatalf("write the exported file: %v", err)
			}
			_, callNamed := globalFileWorkspace(t, "", "")
			t.Setenv(workspaceconfig.EnvConfigOverride, "")
			if tc.exported {
				t.Setenv(workspaceconfig.EnvConfigOverride, exported)
			}
			var flags workspaceconfig.FlagOverlay
			if tc.named {
				flags.ConfigPath = named
			}
			file := map[string]string{"named": named, "exported": exported}
			s := &server{out: io.Discard, logw: io.Discard, startDir: served, flags: flags}

			for _, w := range []struct{ which, rootArg string }{
				{"the workspace the process was started over", ""},
				{"a workspace the call names for itself", callNamed},
			} {
				tw, err := s.resolveToolWorkspace(w.rootArg, nil)
				if err != nil {
					t.Fatalf("%s: resolve: %v", w.which, err)
				}
				got, src := offlineOrigin(t, tw.rw)
				if got != tc.want {
					t.Errorf("%s: offline = %v; want %v", w.which, got, tc.want)
				}
				if want := file[tc.wantFrom]; src != want {
					t.Errorf("%s: the posture is attributed to %q; want %q", w.which, src, want)
				}
				if !slices.Contains(tw.rw.ContributingLayers(), file[tc.wantFrom]) {
					t.Errorf("%s: the contributing layers %q do not carry %q", w.which,
						tw.rw.ContributingLayers(), file[tc.wantFrom])
				}
				dest, err := tw.rw.WriteDestination(workspaceconfig.OpRegistry, false, false)
				if err != nil {
					t.Fatalf("%s: WriteDestination: %v", w.which, err)
				}
				if dest != file[tc.wantWrite] {
					t.Errorf("%s: a registry write lands in %q; want %q, the global file this call read",
						w.which, dest, file[tc.wantWrite])
				}
			}
		})
	}
}

// TestTheCommandLineGlobalFileReachesEveryCallOnBothTransports is the proof read
// off the network. The served workspace names, as its registry, an address on this
// machine that counts what reaches it, and sets no posture of its own; the global
// file the command line names turns the posture on. Each transport is started
// through its exported constructor handed that command line, and asked to
// describe an entity that registry publishes and to report the configuration in
// force — after the documentation acquisition, which reaches for the same
// registry.
//
// Handed --config, nothing reaches the registry, the description is refused for
// the posture, and the configuration reading reports the posture on, from that
// file — named over standard input and output, and kept back over HTTP, where a
// config file's path is a place on the host. The control is the same launch with
// no --config, and it is what makes the zero evidence.
func TestTheCommandLineGlobalFileReachesEveryCallOnBothTransports(t *testing.T) {
	for _, transport := range []struct {
		name     string
		run      func(*testing.T, string, workspaceconfig.FlagOverlay, []string) []string
		withheld bool
	}{
		{"over standard input and output", offlineCallsOverStdio, false},
		{"over HTTP", offlineCallsOverHTTP, true},
	} {
		for _, launch := range []struct {
			name  string
			named bool
		}{
			{"handed --config naming a file that turns the posture on", true},
			{"handed no --config (the control)", false},
		} {
			t.Run(transport.name+", "+launch.name, func(t *testing.T) {
				pinOfflineEnvironment(t, "")
				t.Setenv(workspaceconfig.EnvConfigOverride, "")
				host, reached := countingRegistry(t)
				global, root := globalFileWorkspace(t, "offline: true\n",
					"default_registry: \""+host+"\"\nregistries:\n  - host: \""+host+"\"\n")
				var flags workspaceconfig.FlagOverlay
				if launch.named {
					flags.ConfigPath = global
				}
				answers := transport.run(t, root, flags, []string{
					modernRequest(t, 1, "tools/call", map[string]any{
						"name":      "describe",
						"arguments": map[string]any{"name": "oci://" + host + "/component/core/api:1.0.0"},
					}),
					modernRequest(t, 2, "tools/call", map[string]any{"name": "config-show", "arguments": map[string]any{}}),
				})

				if hits := reached(); launch.named && hits != 0 {
					t.Errorf("the registry was reached %d time(s) by a server whose --config file sets the posture", hits)
				} else if !launch.named && hits == 0 {
					t.Fatal("the control reached the registry zero times, so a zero under --config would measure the " +
						"fixture rather than the flag")
				}
				if launch.named {
					described := toolAnswer(t, answers[0])
					if !described.IsError || !strings.Contains(described.Content[0].Text, "offline") {
						t.Errorf("describe was not refused for the posture (isError=%v): %s",
							described.IsError, described.Content[0].Text)
					}
				}

				shown := toolAnswer(t, answers[1])
				if shown.IsError {
					t.Fatalf("config-show failed: %+v", shown.Content)
				}
				var payload configShowResult
				if err := json.Unmarshal(shown.StructuredContent, &payload); err != nil {
					t.Fatalf("the configuration reading is not its structured shape: %v", err)
				}
				var setting *configSettingResult
				for i := range payload.Settings {
					if payload.Settings[i].Field == workspaceconfig.OfflineSettingKey {
						setting = &payload.Settings[i]
					}
				}
				if setting == nil {
					t.Fatalf("config-show reports no %s setting", workspaceconfig.OfflineSettingKey)
				}
				wantValue, wantSrc := "false", "(built-in defaults)"
				if launch.named {
					wantValue, wantSrc = "true", global
				}
				if told(setting.Value) != wantValue {
					t.Errorf("config-show reports offline = %q; want %q", told(setting.Value), wantValue)
				}
				switch {
				case launch.named && transport.withheld:
					if setting.Source != nil || !setting.SourceWithheld {
						t.Errorf("over HTTP the source %q is a config file's path and is not kept back", told(setting.Source))
					}
				default:
					if told(setting.Source) != wantSrc {
						t.Errorf("config-show attributes offline to %q; want %q", told(setting.Source), wantSrc)
					}
				}
			})
		}
	}
}

// TestTheListenerReadsTheGlobalFileTheCommandLineNamed holds the listener's
// startup read to the command line it was started with: a signing key named only
// in the global file --config names is a key the calls answered over that
// listener will read, so it is the key the startup line has to be about. The
// control is the same launch with no --config, which names no key and must say
// nothing.
func TestTheListenerReadsTheGlobalFileTheCommandLineNamed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		named bool
		want  bool
	}{
		{"handed --config naming a file that names a signing key", true, true},
		{"handed no --config (the control)", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinOfflineEnvironment(t, "")
			t.Setenv(workspaceconfig.EnvConfigOverride, "")
			t.Setenv(signingKeyEnvVarName, "")
			global, root := globalFileWorkspace(t, "signing:\n  key_path: "+theListenerKeyPath+"\n", "")
			var flags workspaceconfig.FlagOverlay
			if tc.named {
				flags.ConfigPath = global
			}
			var log bytes.Buffer
			noteAnUnusedSigningKey(lifecycleLog{w: &log, root: root}, root, flags, "127.0.0.1:0")
			said := strings.Contains(log.String(), eventSigningKeyIgnored)
			if said != tc.want {
				t.Errorf("the listener said it would not use a held key = %v; want %v\n%s", said, tc.want, log.String())
			}
		})
	}
}
