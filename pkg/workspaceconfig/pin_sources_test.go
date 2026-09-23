// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTheInvocationOverridesEachPinKeyInBothDirections is the whole grid of the
// precedence both operations share: two settings crossed with three invocation
// answers, for each of the two keys.
//
// The grid is asserted cell by cell rather than by its margins, because the rule
// is about the interaction: a precedence that let the flag only turn pinning ON
// would pass every "flag set" cell on a workspace configured off and fail only the
// cell where a workspace configured on is asked, for one run, to halt — the cell
// that stops the setting being a trapdoor.
func TestTheInvocationOverridesEachPinKeyInBothDirections(t *testing.T) {
	yes, no := true, false
	cells := []struct {
		name      string
		setting   bool
		requested *bool
		want      bool
	}{
		{"setting off, invocation silent: the setting decides", false, nil, false},
		{"setting on, invocation silent: the setting decides", true, nil, true},
		{"setting off, invocation asks to pin: the invocation wins", false, &yes, true},
		{"setting on, invocation asks to pin: the invocation wins", true, &yes, true},
		{"setting off, invocation asks not to: the invocation wins", false, &no, false},
		{"setting on, invocation asks not to: the invocation wins", true, &no, false},
	}
	for _, c := range cells {
		t.Run("pull/"+c.name, func(t *testing.T) {
			cfg := Config{PinSourcesAtPull: c.setting, PinSourcesAtPush: !c.setting}
			if got := cfg.PullPinsSources(c.requested); got != c.want {
				t.Errorf("PullPinsSources = %v; want %v", got, c.want)
			}
		})
		t.Run("push/"+c.name, func(t *testing.T) {
			cfg := Config{PinSourcesAtPush: c.setting, PinSourcesAtPull: !c.setting}
			if got := cfg.PushPinsSources(c.requested); got != c.want {
				t.Errorf("PushPinsSources = %v; want %v", got, c.want)
			}
		})
	}
}

// TestThePinKeysLoadFromTheWorkspaceFileAndDefaultOff drives the two keys through
// the one resolution path every command reads, so a key spelled in the schema and
// not in the type — or the reverse — is refused here rather than silently read as
// false.
func TestThePinKeysLoadFromTheWorkspaceFileAndDefaultOff(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		pull, psh bool
	}{
		{"absent keys default off", "workspace: {}\n", false, false},
		{"each key sets its own operation", "workspace: {}\npin_sources_at_pull: true\n", true, false},
		{"the other key sets only its own", "workspace: {}\npin_sources_at_push: true\n", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, ConfigFilename), []byte(tc.body), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			rw, err := Resolve(root, root, func(string) string { return "" })
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			cfg := rw.Config()
			if cfg.PinSourcesAtPull != tc.pull || cfg.PinSourcesAtPush != tc.psh {
				t.Errorf("pin_sources_at_pull=%v pin_sources_at_push=%v; want %v, %v",
					cfg.PinSourcesAtPull, cfg.PinSourcesAtPush, tc.pull, tc.psh)
			}
		})
	}
}
