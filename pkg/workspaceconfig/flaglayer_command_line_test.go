// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A process that resolves a workspace on behalf of a command line it was handed
// rather than one it parsed — a server started from a command line — has to read
// that command line the way the command line itself is read: the global file
// --config names, before the merge, and the settings it carries, over it. And a
// process that runs a command of its own in a child has to hand that command the
// same command line. The proofs here hold the three pieces that make that so:
// the rendering, the reader, and the one resolution call.

// TestFlagOverlayArgsRenderTheCommandLineThatReproducesIt pins the flags an
// overlay renders as, row by row, and holds the setting flags among them to the
// table that applies them, so a flag that table gains is rendered by construction
// rather than by a second list.
//
// The two `--offline` rows are the reason the rendering carries a value at all:
// a flag typed false turns a configured posture off, and rendering it as a bare
// `--offline` would hand the child the opposite of what the reader typed.
func TestFlagOverlayArgsRenderTheCommandLineThatReproducesIt(t *testing.T) {
	t.Parallel()
	on, off := true, false
	for _, tc := range []struct {
		name string
		f    FlagOverlay
		want []string
	}{
		{name: "nothing typed renders nothing", f: FlagOverlay{}, want: nil},
		{name: "--offline", f: FlagOverlay{Offline: &on}, want: []string{"--offline=true"}},
		{name: "--offline=false keeps its direction", f: FlagOverlay{Offline: &off}, want: []string{"--offline=false"}},
		{
			name: "--config",
			f:    FlagOverlay{ConfigPath: "/etc/aggrega/global.yaml"},
			want: []string{"--config=/etc/aggrega/global.yaml"},
		},
		{
			name: "a path holding a space stays one argument",
			f:    FlagOverlay{ConfigPath: "/a dir/global.yaml"},
			want: []string{"--config=/a dir/global.yaml"},
		},
		{
			name: "both, the global file first",
			f:    FlagOverlay{ConfigPath: "/g.yaml", Offline: &on},
			want: []string{"--config=/g.yaml", "--offline=true"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.f.Args()
			if !slices.Equal(got, tc.want) {
				t.Errorf("Args() = %q; want %q", got, tc.want)
			}

			var scratch Config
			applied := applyFlagOverlay(&scratch, tc.f)
			var rendered []string
			for _, arg := range got {
				name, _, _ := strings.Cut(arg, "=")
				if name != flagLayerPrefix+ConfigFlagName {
					rendered = append(rendered, name)
				}
			}
			if !slices.Equal(rendered, applied) {
				t.Errorf("Args() renders the setting flags %q, but the layer applies %q: a child handed these "+
					"would not be configured as this process is", rendered, applied)
			}
		})
	}
}

// TestFlagOverlayLookupAnswersTheGlobalFileWithTheFlagFirst pins the reader a
// resolution for an invocation reads through: --config wins over
// $AGGREGA_CONFIG, an absent --config leaves the environment deciding, and every
// other variable is the environment's either way.
func TestFlagOverlayLookupAnswersTheGlobalFileWithTheFlagFirst(t *testing.T) {
	t.Parallel()
	base := func(name string) string {
		switch name {
		case EnvConfigOverride:
			return "/from/the/environment.yaml"
		case "AGGREGA_OFFLINE":
			return "true"
		}
		return ""
	}
	for _, tc := range []struct {
		name       string
		configPath string
		wantGlobal string
	}{
		{name: "no --config: the environment decides", configPath: "", wantGlobal: "/from/the/environment.yaml"},
		{name: "--config wins over the environment", configPath: "/from/the/flag.yaml", wantGlobal: "/from/the/flag.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			look := FlagOverlay{ConfigPath: tc.configPath}.Lookup(base)
			if got := look(EnvConfigOverride); got != tc.wantGlobal {
				t.Errorf("the global file is read from %q; want %q", got, tc.wantGlobal)
			}
			if got := look("AGGREGA_OFFLINE"); got != "true" {
				t.Errorf("another variable reads %q through the reader; want the environment's %q", got, "true")
			}
		})
	}
}

// TestFlagOverlayLookupWithNoBaseReadsTheProcessEnvironment pins the nil base:
// it is the process environment, which is what a server resolving with no
// reader of its own reads.
func TestFlagOverlayLookupWithNoBaseReadsTheProcessEnvironment(t *testing.T) {
	// Not parallel: it sets the process environment.
	t.Setenv(EnvConfigOverride, "/from/the/process.yaml")
	if got := (FlagOverlay{}).Lookup(nil)(EnvConfigOverride); got != "/from/the/process.yaml" {
		t.Errorf("with no --config and no base, the global file is read from %q; want the process environment's", got)
	}
	if got := (FlagOverlay{ConfigPath: "/flag.yaml"}).Lookup(nil)(EnvConfigOverride); got != "/flag.yaml" {
		t.Errorf("with --config and no base, the global file is read from %q; want the flag's", got)
	}
}

// TestResolveWithFlagsReadsTheGlobalFileTheCommandLineNamed drives the one call
// a server resolves through, over a workspace that sets nothing, with a global
// file that turns the posture on (G) and one exported in $AGGREGA_CONFIG that
// turns it off (E).
//
// Three things are read per row, because each fails differently: the effective
// value, which is what every operation reads; the layer it is attributed to and
// the contributing layers, which are what a reader is shown as where a value came
// from; and the file a configuration write lands in, which must be the file the
// configuration was read from. The environment row is the control that the route
// beneath --config still works, and the row naming a missing file holds the flag
// to the same loud refusal the variable has.
func TestResolveWithFlagsReadsTheGlobalFileTheCommandLineNamed(t *testing.T) {
	// Not parallel: it sets the process environment.
	off := false
	for _, tc := range []struct {
		name       string
		named      bool // --config names G
		missing    bool // --config names a file that does not exist
		env        bool // $AGGREGA_CONFIG names E
		offline    *bool
		wantValue  bool
		wantSrc    string   // "G", "E", "--offline" or the built-in-defaults label
		wantLayers []string // "G", "E", "ws" or a flag
		wantWrite  string   // "G", "E", or "" for the default location
	}{
		{name: "nothing named", wantValue: false, wantSrc: originDefaultsLabel, wantLayers: []string{"ws"}},
		{
			name: "--config names a file that turns the posture on", named: true,
			wantValue: true, wantSrc: "G", wantLayers: []string{"G", "ws"}, wantWrite: "G",
		},
		{
			name: "--config wins over $AGGREGA_CONFIG", named: true, env: true,
			wantValue: true, wantSrc: "G", wantLayers: []string{"G", "ws"}, wantWrite: "G",
		},
		{
			name: "$AGGREGA_CONFIG alone (the control)", env: true,
			wantValue: false, wantSrc: "E", wantLayers: []string{"E", "ws"}, wantWrite: "E",
		},
		{
			name: "--offline=false laid over the file --config names", named: true, offline: &off,
			wantValue: false, wantSrc: "--offline", wantLayers: []string{"G", "ws", "--offline"}, wantWrite: "G",
		},
		{name: "--config naming a file that does not exist is refused", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			t.Setenv("AGGREGA_OFFLINE", "")

			dir := t.TempDir()
			g := filepath.Join(dir, "global.yaml")
			e := filepath.Join(dir, "exported.yaml")
			ws := filepath.Join(dir, "ws")
			for path, body := range map[string]string{
				g:                                 "offline: true\n",
				e:                                 "offline: false\n",
				filepath.Join(ws, ConfigFilename): "workspace: {}\n",
			} {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			symbol := map[string]string{"G": g, "E": e, "ws": filepath.Join(ws, ConfigFilename)}
			spell := func(s string) string {
				if p, ok := symbol[s]; ok {
					return p
				}
				return s
			}

			exported := ""
			if tc.env {
				exported = e
			}
			t.Setenv(EnvConfigOverride, exported)
			f := FlagOverlay{Offline: tc.offline}
			if tc.named {
				f.ConfigPath = g
			}
			if tc.missing {
				f.ConfigPath = filepath.Join(dir, "absent.yaml")
			}

			rw, err := ResolveWithFlags(ws, ws, f)
			if tc.missing {
				if err == nil || !strings.Contains(err.Error(), f.ConfigPath) {
					t.Fatalf("a --config naming a missing file resolved (err = %v); want a refusal naming it", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveWithFlags: %v", err)
			}

			if got := rw.Config().Offline; got != tc.wantValue {
				t.Errorf("offline = %v; want %v", got, tc.wantValue)
			}
			origins, err := rw.ValueOrigins()
			if err != nil {
				t.Fatalf("ValueOrigins: %v", err)
			}
			src := ""
			for _, o := range origins {
				if o.Field == OfflineSettingKey {
					src = o.Source
				}
			}
			if want := spell(tc.wantSrc); src != want {
				t.Errorf("offline is attributed to %q; want %q", src, want)
			}
			var wantLayers []string
			for _, l := range tc.wantLayers {
				wantLayers = append(wantLayers, spell(l))
			}
			if got := rw.ContributingLayers(); !slices.Equal(got, wantLayers) {
				t.Errorf("contributing layers = %q; want %q", got, wantLayers)
			}

			dest, err := rw.WriteDestination(OpRegistry, false, false)
			if err != nil {
				t.Fatalf("WriteDestination: %v", err)
			}
			wantDest := filepath.Join(home, "aggrega", "config.yaml")
			if tc.wantWrite != "" {
				wantDest = spell(tc.wantWrite)
			}
			if dest != wantDest {
				t.Errorf("a registry write lands in %q; want %q, the global file this resolution read", dest, wantDest)
			}
		})
	}
}
