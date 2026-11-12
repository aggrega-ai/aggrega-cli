// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestConfigShowOriginNamesTheLayerThatSetsASetting drives the attribution
// rule through the command a reader runs, row by row: `aggrega config show
// --origin` names the highest-precedence layer that sets a setting, whatever
// value that layer sets.
//
// It runs the real command tree rather than calling the report, because the
// report is only half of what a reader sees: the flag reaches it through the
// root's own pre-run, and the global file through the --config flag, and a test
// composing either itself would prove the report and say nothing about whether
// the command hands it the layers a reader typed.
//
// The environment is emptied of every variable the configuration reads, and the
// home directories point at empty ones, so a row reads its own layers and never
// the configuration of the machine running it.
func TestConfigShowOriginNamesTheLayerThatSetsASetting(t *testing.T) {
	clauseOne := "default_registry: hub.aggrega.ai\nauto_pull: false\noffline: true\n"
	for _, tc := range []struct {
		name    string
		global  string
		project string
		ci      string
		env     map[string]string
		args    []string
		field   string
		// want is "global", "project" or "ci" for a file, or the label as the
		// command prints it.
		want string
	}{
		{name: "a project file writing the default registry", project: clauseOne, field: "default_registry", want: "project"},
		{name: "a project file writing auto_pull as the default has it", project: clauseOne, field: "auto_pull", want: "project"},
		{
			name:    "a project file restating the value the global file sets",
			global:  "default_registry: registry.example.test\n",
			project: "default_registry: registry.example.test\n",
			field:   "default_registry", want: "project",
		},
		{
			name:  "a variable exported with the default's value",
			env:   map[string]string{"AGGREGA_DEFAULT_REGISTRY": "hub.aggrega.ai"},
			field: "default_registry", want: "env:AGGREGA_DEFAULT_REGISTRY",
		},
		{
			name:    "--offline=false where no file carries the posture",
			project: "default_registry: registry.example.test\n",
			args:    []string{"--offline=false"},
			field:   workspaceconfig.OfflineSettingKey, want: "--" + workspaceconfig.OfflineSettingKey,
		},
		{name: "an authored empty catalog-directory list", project: "workspace:\n  catalog_dirs: []\n", field: "workspace.catalog_dirs", want: "project"},
		{name: "the control: a setting no layer mentions", project: clauseOne, field: "parse_cache.max_entries", want: "(built-in defaults)"},
		{
			name:    "the control: a layer writing a value different from the one beneath",
			global:  "default_registry: global.example.test\n",
			project: "default_registry: project.example.test\n",
			field:   "default_registry", want: "project",
		},
		{
			name:    "the CI-owned overlay carrying a key it is not read for",
			project: "mcp:\n  allowed_origins: [\"https://base.example.test\"]\n",
			ci:      "mcp:\n  allowed_origins: [\"https://ci.example.test\"]\n",
			field:   "mcp.allowed_origins", want: "project",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			empty := t.TempDir()
			t.Setenv("HOME", empty)
			t.Setenv("XDG_CONFIG_HOME", empty)
			t.Setenv(workspaceconfig.EnvConfigOverride, "")
			t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
			for _, v := range workspaceconfig.ConfigBackedEnvVars() {
				t.Setenv(v, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			dir := t.TempDir()
			root := filepath.Join(dir, "workspace")
			files := map[string]string{
				"global":  filepath.Join(dir, "global", "config.yaml"),
				"project": filepath.Join(root, workspaceconfig.ConfigFilename),
				"ci":      filepath.Join(root, workspaceconfig.AppendCIConfigFilename),
			}
			args := []string{"config", "show", "--origin", "--workspace-root", root}
			if tc.global != "" {
				writeFixture(t, files["global"], tc.global)
				args = append(args, "--config", files["global"])
			}
			if tc.project != "" {
				writeFixture(t, files["project"], tc.project)
			}
			if tc.ci != "" {
				writeFixture(t, files["ci"], tc.ci)
			}
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", root, err)
			}

			var out, errOut bytes.Buffer
			cmd := newRootCmd()
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(append(args, tc.args...))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("config show --origin: %v\nstderr:\n%s", err, errOut.String())
			}

			want := tc.want
			if path, isFile := files[tc.want]; isFile {
				want = path
			}
			if got := originColumnFor(t, out.String(), tc.field); got != want {
				t.Errorf("%s is shown as coming from %q; want %s (%q)\noutput:\n%s", tc.field, got, tc.want, want, out.String())
			}
		})
	}
}

// originColumnFor reads the SOURCE column of field's row out of the table
// `config show --origin` prints. The table is aligned, so the source starts at
// the header's SOURCE offset on every row; it is read from there to the end of
// the line rather than as the row's last word, because the built-in defaults
// label carries a space.
func originColumnFor(t *testing.T, table, field string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "SETTING") {
		t.Fatalf("the output does not open on the table header:\n%s", table)
	}
	at := strings.Index(lines[0], "SOURCE")
	if at < 0 {
		t.Fatalf("the table header names no SOURCE column:\n%s", table)
	}
	for _, line := range lines[1:] {
		cells := strings.Fields(line)
		if len(cells) >= 2 && cells[0] == field && len(line) > at {
			return strings.TrimSpace(line[at:])
		}
	}
	t.Fatalf("the table carries no %q row:\n%s", field, table)
	return ""
}
