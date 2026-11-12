// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The global --config flag names the file the cascade's global layer is read
// from, and wins over $AGGREGA_CONFIG. The daemon resolves the workspace it was
// launched over and then each workspace a request names, so the flag reaches its
// requests only if the command line it was launched with is handed to it and
// every one of those resolutions reads the global layer through it. The proofs
// here hold both halves: the daemon's one resolution, and the command line handed
// in through the exported entry point, read off the answers and off the network.

// daemonGlobalFile writes a global configuration file holding body and returns
// its path.
func daemonGlobalFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "global.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write the global file: %v", err)
	}
	return path
}

// TestEveryWorkspaceTheDaemonResolvesReadsTheGlobalFileTheCommandLineNamed
// drives the daemon's one resolution with a global file that turns the posture on
// (named) and one exported in $AGGREGA_CONFIG that turns it off (exported).
//
// Three things are read per row: the effective value every request reads; the
// layer it is attributed to and the contributing layers, which are what the
// effective-configuration answer reports; and the file a configuration write
// lands in, which must be the file the configuration was read from. The
// environment row is the control that the route beneath --config still works.
func TestEveryWorkspaceTheDaemonResolvesReadsTheGlobalFileTheCommandLineNamed(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name      string
		named     bool
		exported  bool
		offline   *bool
		want      bool
		wantFrom  string // "named", "exported" or "--offline"
		wantLayer string // "named" or "exported": the global file among the contributing layers
	}{
		{name: "--config names the global file", named: true, want: true, wantFrom: "named", wantLayer: "named"},
		{
			name: "--config wins over $AGGREGA_CONFIG", named: true, exported: true,
			want: true, wantFrom: "named", wantLayer: "named",
		},
		{
			name: "$AGGREGA_CONFIG alone (the control)", exported: true,
			want: false, wantFrom: "exported", wantLayer: "exported",
		},
		{
			name: "--offline=false laid over the file --config names", named: true, offline: &off,
			want: false, wantFrom: "--offline", wantLayer: "named",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinDaemonOfflineEnvironment(t, "")
			named := daemonGlobalFile(t, "offline: true\n")
			exported := daemonGlobalFile(t, "offline: false\n")
			t.Setenv(workspaceconfig.EnvConfigOverride, "")
			if tc.exported {
				t.Setenv(workspaceconfig.EnvConfigOverride, exported)
			}
			flags := workspaceconfig.FlagOverlay{Offline: tc.offline}
			if tc.named {
				flags.ConfigPath = named
			}
			file := map[string]string{"named": named, "exported": exported, "--offline": "--offline"}
			root := daemonOfflineWorkspace(t, "")

			rw, held, err := resolveWorkspace(root, flags)
			if err != nil || held != nil {
				t.Fatalf("resolve: %v (held %v)", err, held)
			}
			if got := rw.Config().Offline; got != tc.want {
				t.Errorf("offline = %v; want %v", got, tc.want)
			}
			origins, err := rw.ValueOrigins()
			if err != nil {
				t.Fatalf("read the provenance: %v", err)
			}
			src := ""
			for _, o := range origins {
				if o.Field == workspaceconfig.OfflineSettingKey {
					src = o.Source
				}
			}
			if src != file[tc.wantFrom] {
				t.Errorf("the posture is attributed to %q; want %q", src, file[tc.wantFrom])
			}
			if layers := rw.ContributingLayers(); !slices.Contains(layers, file[tc.wantLayer]) {
				t.Errorf("the contributing layers %q do not carry the global file %q", layers, file[tc.wantLayer])
			}
			dest, err := rw.WriteDestination(workspaceconfig.OpRegistry, false, false)
			if err != nil {
				t.Fatalf("WriteDestination: %v", err)
			}
			if dest != file[tc.wantLayer] {
				t.Errorf("a registry write lands in %q; want %q, the global file this resolution read", dest, file[tc.wantLayer])
			}
		})
	}
}

// TestTheLaunchGlobalFileReachesEveryWorkspaceTheDaemonServes is the proof read
// off the daemon's answers and off the network. Two workspaces name, as their
// registry, an address on this machine that counts what reaches it, and set no
// posture of their own; the global file the command line names turns the posture
// on. The daemon is launched over the first, handed that command line exactly as
// the command layer hands it, and asked for the effective configuration of the
// workspace it was launched over and of the second, which a request names for the
// first time, and asked to refresh the second's registry index.
//
// Handed --config, both workspaces report the posture on, attributed to that
// file, with the file among their layers; the refresh is refused naming the file;
// and the registry is never reached. The control is the same launch with no
// --config, and it is what makes the zero evidence.
func TestTheLaunchGlobalFileReachesEveryWorkspaceTheDaemonServes(t *testing.T) {
	for _, launch := range []struct {
		name  string
		named bool
	}{
		{"handed --config naming a file that turns the posture on", true},
		{"handed no --config (the control)", false},
	} {
		t.Run(launch.name, func(t *testing.T) {
			pinDaemonOfflineEnvironment(t, "")
			t.Setenv(workspaceconfig.EnvConfigOverride, "")
			global := daemonGlobalFile(t, "offline: true\n")
			host, reached := daemonCountingRegistry(t)
			body := "default_registry: \"" + host + "\"\nregistries:\n  - host: \"" + host + "\"\n"
			launched := daemonOfflineWorkspace(t, body)
			named := daemonOfflineWorkspace(t, body)
			var flags workspaceconfig.FlagOverlay
			if launch.named {
				flags.ConfigPath = global
			}

			answers := daemonExchange(t, launched, flags, [][]byte{
				framedRequestAt(t, launched, 1, "aggrega/effectiveConfiguration", map[string]any{}),
				framedRequestAt(t, named, 2, "aggrega/effectiveConfiguration", map[string]any{}),
				framedRequestAt(t, named, 3, "aggrega/registryUpdate", map[string]any{"registry": host}),
			})

			wantValue, wantSrc := "false", "(built-in defaults)"
			if launch.named {
				wantValue, wantSrc = "true", global
			}
			for i, which := range []string{"the workspace the daemon was launched over", "a workspace a request named"} {
				if answers[i].Error != nil {
					t.Fatalf("%s: aggrega/effectiveConfiguration refused: %s", which, answers[i].Error.Message)
				}
				var result effectiveConfigurationResult
				if err := json.Unmarshal(answers[i].Result, &result); err != nil {
					t.Fatalf("%s: the answer is not the effective-configuration shape: %v", which, err)
				}
				found := false
				for _, s := range result.Settings {
					if s.Field != workspaceconfig.OfflineSettingKey {
						continue
					}
					found = true
					if s.Value != wantValue || s.Source != wantSrc {
						t.Errorf("%s: offline = %q from %q; want %q from %q", which, s.Value, s.Source, wantValue, wantSrc)
					}
				}
				if !found {
					t.Errorf("%s: no %s setting is reported", which, workspaceconfig.OfflineSettingKey)
				}
				if launch.named && !slices.Contains(result.Layers, global) {
					t.Errorf("%s: the contributing layers %v do not carry the file --config named", which, result.Layers)
				}
			}

			refresh := answers[2]
			hits := reached()
			if launch.named {
				if refresh.Error == nil || !strings.Contains(refresh.Error.Message, global) {
					t.Errorf("the registry refresh was not refused naming the file that set the posture: %+v", refresh.Error)
				}
				if hits != 0 {
					t.Errorf("the registry was reached %d time(s) by a daemon whose --config file sets the posture", hits)
				}
			} else if hits == 0 {
				t.Fatal("the control reached the registry zero times, so a zero under --config would measure the " +
					"fixture rather than the flag")
			}
		})
	}
}
