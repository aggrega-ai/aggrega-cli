// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestConfigFileContributed pins the predicate a surface reads to answer
// "does this machine have a configuration file at all?".
//
// The discriminating row is the environment-only one: the contributing list is
// NOT empty there, because the environment overlay contributes a pseudo-entry of
// its own, so an implementation that answered from that list's length would
// report a configuration file where none exists.
func TestConfigFileContributed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// treeConfig, globalConfig and env shape the resolution; each is applied
		// only when non-empty.
		treeConfig   string
		globalConfig string
		env          map[string]string

		want             bool
		wantContributing int
	}{
		{
			name:             "nothing but the built-in defaults",
			want:             false,
			wantContributing: 0,
		},
		{
			name:             "an environment override and no file at all",
			env:              map[string]string{"AGGREGA_OFFLINE": "true"},
			want:             false,
			wantContributing: 1,
		},
		{
			name:             "a config file in the workspace tree",
			treeConfig:       "default_registry: tree.example\n",
			want:             true,
			wantContributing: 1,
		},
		{
			name:             "a global config file only",
			globalConfig:     "default_registry: global.example\n",
			want:             true,
			wantContributing: 1,
		},
		{
			name:             "a file and an environment override together",
			treeConfig:       "default_registry: tree.example\n",
			env:              map[string]string{"AGGREGA_OFFLINE": "true"},
			want:             true,
			wantContributing: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if tc.treeConfig != "" {
				write(t, filepath.Join(root, workspaceconfig.ConfigFilename), tc.treeConfig)
			}
			env := map[string]string{}
			for k, v := range tc.env {
				env[k] = v
			}
			if tc.globalConfig != "" {
				home := t.TempDir()
				write(t, filepath.Join(home, ".config", "aggrega", "config.yaml"), tc.globalConfig)
				env["HOME"] = home
			}

			rw, err := workspaceconfig.Resolve(root, "", frozenLookup(env))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got := rw.ConfigFileContributed(); got != tc.want {
				t.Errorf("ConfigFileContributed: got %v; want %v", got, tc.want)
			}
			if got := len(rw.ContributingFiles()); got != tc.wantContributing {
				t.Errorf("ContributingFiles: got %d entries %q; want %d",
					got, rw.ContributingFiles(), tc.wantContributing)
			}
		})
	}
}

// TestConfigFileContributedOnANilResolution: the predicate answers false rather
// than panicking, so a caller holding a resolution that never happened reports
// no configuration file instead of crashing on the way to saying so.
func TestConfigFileContributedOnANilResolution(t *testing.T) {
	t.Parallel()

	var rw *workspaceconfig.ResolvedWorkspace
	if rw.ConfigFileContributed() {
		t.Error("ConfigFileContributed on a nil resolution: got true; want false")
	}
}
