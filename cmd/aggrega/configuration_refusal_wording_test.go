// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAConfigurationValueTheWorkspaceCannotUseIsRefusedNamingItsLayerOnce drives
// "aggrega config show" through the real command tree over a value the schema
// admits and the workspace cannot use, carried by each layer that can carry it:
// the environment variable that overrides the setting, and the configuration file.
//
// The refusal names the layer that carried the value once, in front of what is
// wrong with it, and no Go package's name. The environment rows are the ones a
// layer named twice would show — once by the overlay that read the variable and
// again by the check that refused its value — so each counts the variable's name
// in what the reader is shown.
func TestAConfigurationValueTheWorkspaceCannotUseIsRefusedNamingItsLayerOnce(t *testing.T) {
	// Not parallel: each row sets environment variables.
	absolute := filepath.Join(t.TempDir(), "cache")
	for _, row := range []struct {
		name string
		// variable and value are the environment layer's; file is the
		// configuration file's body beneath its root marker. A row sets one.
		variable, value string
		file            string
		// opening is how the refusal's text begins once the layer is named, with
		// the configuration file's path written as <file>.
		opening string
	}{
		{
			name:     "an absolute artifact cache directory from the environment",
			variable: "AGGREGA_ARTIFACT_CACHE_DIR",
			value:    absolute,
			opening:  "the AGGREGA_ARTIFACT_CACHE_DIR environment variable: artifact_cache_dir " + strconv.Quote(absolute) + " must be a workspace-relative path",
		},
		{
			name:     "the workspace root as the artifact cache directory from the environment",
			variable: "AGGREGA_ARTIFACT_CACHE_DIR",
			value:    ".",
			opening:  `the AGGREGA_ARTIFACT_CACHE_DIR environment variable: artifact_cache_dir "." names the workspace root itself`,
		},
		{
			name:     "a signing kind that is no artifact kind from the environment",
			variable: "AGGREGA_SIGNING_KINDS",
			value:    "Bogus",
			opening:  `the AGGREGA_SIGNING_KINDS environment variable: signing.kinds entry "Bogus" is not a signable artifact kind`,
		},
		{
			name:    "an absolute artifact cache directory in the configuration file",
			file:    "artifact_cache_dir: " + strconv.Quote(absolute) + "\n",
			opening: "<file>: artifact_cache_dir " + strconv.Quote(absolute) + " must be a workspace-relative path",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			empty := t.TempDir()
			t.Setenv("HOME", empty)
			t.Setenv("XDG_CONFIG_HOME", empty)
			t.Setenv("APPDATA", empty)
			for _, v := range []string{"AGGREGA_ARTIFACT_CACHE_DIR", "AGGREGA_SIGNING_KINDS", "AGGREGA_CONFIG"} {
				t.Setenv(v, "")
			}
			if row.variable != "" {
				t.Setenv(row.variable, row.value)
			}
			ws := t.TempDir()
			configFile := filepath.Join(ws, workspaceconfig.ConfigFilename)
			writeFixture(t, configFile, "workspace: {}\n"+row.file)

			root := newRootCmd()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs([]string{"--workspace-root", ws, "config", "show"})
			err := root.Execute()
			if err == nil {
				t.Fatalf("config show accepted a value the workspace cannot use")
			}
			if !errors.Is(err, workspaceconfig.ErrConfigInvalid()) {
				t.Errorf("the refusal is not the configuration's invalid-value condition: %v", err)
			}

			printed := formatCompilerError(err)
			if opening := strings.ReplaceAll(row.opening, "<file>", configFile); !strings.Contains(printed, opening) {
				t.Errorf("the refusal does not open as %q:\n%s", opening, printed)
			}
			if strings.Contains(printed, "workspaceconfig") {
				t.Errorf("the refusal names a Go package the reader cannot act on:\n%s", printed)
			}
			layer := row.variable
			if layer == "" {
				layer = configFile
			}
			if n := strings.Count(printed, layer); n != 1 {
				t.Errorf("the refusal names %s %d times; want once:\n%s", layer, n, printed)
			}
		})
	}
}
