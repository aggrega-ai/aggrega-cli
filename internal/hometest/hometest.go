// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package hometest redirects the per-user configuration home for the lifetime
// of a test binary so a suite never reads from or writes to the developer's
// real global configuration.
//
// The workspaceconfig package resolves a global configuration layer beneath
// every workspace tree: it reads $AGGREGA_CONFIG when that is set, otherwise
// $XDG_CONFIG_HOME/aggrega/config.yaml, falling back to
// $HOME/.config/aggrega/config.yaml. That layer loads unconditionally, so a
// test that resolves a workspace would otherwise merge the developer's ambient
// global configuration beneath its temporary fixtures, making results depend on
// the machine the suite runs on. Pointing $XDG_CONFIG_HOME at an empty
// throwaway directory and clearing $AGGREGA_CONFIG makes the XDG-default global
// file absent (so reads stay clean) and confines any default-scoped write to
// the throwaway (so writes stay contained). $HOME is left untouched because the
// XDG variable wins in the resolution rule this mirrors.
package hometest

import (
	"fmt"
	"os"
	"testing"
)

// RunHermetic isolates the configuration home for a whole test binary and runs
// its suite. It points $XDG_CONFIG_HOME at a fresh throwaway directory, clears
// $AGGREGA_CONFIG, runs m.Run, removes the throwaway, and returns the suite's
// exit code so a caller can propagate it. Drive it from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(hometest.RunHermetic(m)) }
func RunHermetic(m *testing.M) int {
	dir, err := os.MkdirTemp("", "aggrega-config-home-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hometest: create throwaway config home: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		fmt.Fprintf(os.Stderr, "hometest: redirect XDG_CONFIG_HOME: %v\n", err)
		return 1
	}
	if err := os.Unsetenv("AGGREGA_CONFIG"); err != nil {
		fmt.Fprintf(os.Stderr, "hometest: clear AGGREGA_CONFIG: %v\n", err)
		return 1
	}

	return m.Run()
}
