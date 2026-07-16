// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestMain isolates the configuration home for the whole test binary so no test
// reads from or writes to the developer's real global configuration. The
// redirect runs before any test resolves a workspace, so a populated global
// file in the ambient environment cannot leak into the suite.
func TestMain(m *testing.M) {
	os.Exit(hometest.RunHermetic(m))
}

// TestConfigHomeIsIsolated is the regression floor for the TestMain redirect. It
// asserts the process environment the whole binary runs under is hermetic:
// $XDG_CONFIG_HOME points inside a temporary directory rather than the
// developer's real home, $AGGREGA_CONFIG is empty, and the global config path
// the production resolver would consult sits under the redirected home. If the
// redirect is ever dropped, these assertions fail loudly rather than letting the
// developer's real global configuration silently merge beneath every test's
// fixtures.
func TestConfigHomeIsIsolated(t *testing.T) {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		t.Fatal("XDG_CONFIG_HOME is empty; the TestMain redirect did not run")
	}

	realHome, err := os.UserHomeDir()
	if err == nil && realHome != "" {
		realXDG := filepath.Join(realHome, ".config")
		if xdg == realHome || xdg == realXDG {
			t.Fatalf("XDG_CONFIG_HOME %q points at the developer's real home; the redirect is not isolating", xdg)
		}
	}

	tmp := os.TempDir()
	if !strings.HasPrefix(xdg, tmp) {
		t.Fatalf("XDG_CONFIG_HOME %q is not under the temp root %q; the redirect is not isolating", xdg, tmp)
	}

	if cfg := os.Getenv("AGGREGA_CONFIG"); cfg != "" {
		t.Fatalf("AGGREGA_CONFIG is set to %q; the redirect must clear it", cfg)
	}

	globalPath, ok := workspaceconfig.GlobalConfigPath(nil)
	if !ok {
		t.Fatal("GlobalConfigPath reported no resolvable path under the redirected home")
	}
	if !strings.HasPrefix(globalPath, xdg) {
		t.Fatalf("global config path %q does not sit under the redirected home %q", globalPath, xdg)
	}
}
