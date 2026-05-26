// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestSourceStringRoundTrip pins the lowercase wire tokens the Source
// enum emits via String(). The daemon's aggrega/initialize response
// uses this mapping verbatim.
func TestSourceStringRoundTrip(t *testing.T) {
	cases := []struct {
		src  Source
		want string
	}{
		{SourceDefaults, "defaults"},
		{SourceGlobal, "global"},
		{SourceWorkspace, "workspace"},
	}
	for _, tc := range cases {
		if got := tc.src.String(); got != tc.want {
			t.Errorf("Source(%d).String(): got %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestGlobalConfigPathRespectsEnvPrecedence pins the env-variable
// precedence rule: $AGGREGA_CONFIG wins over $XDG_CONFIG_HOME, which
// wins over the default ~/.config path. Empty $AGGREGA_CONFIG is
// treated as unset.
func TestGlobalConfigPathRespectsEnvPrecedence(t *testing.T) {
	tcases := []struct {
		name         string
		envOverride  string
		xdgHome      string
		wantPrefix   string
		wantExplicit bool
	}{
		{"AGGREGA_CONFIG wins", "/explicit/path/global.yaml", "/xdg/home", "/explicit/path/global.yaml", true},
		{"XDG_CONFIG_HOME used when AGGREGA_CONFIG unset", "", "/xdg/home", "/xdg/home/aggrega/config.yaml", false},
		{"empty AGGREGA_CONFIG is treated as unset", "", "/xdg/home", "/xdg/home/aggrega/config.yaml", false},
	}
	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envVarConfigOverride, tc.envOverride)
			t.Setenv(envVarXDGConfigHome, tc.xdgHome)
			path, explicit := GlobalConfigPath()
			if path != tc.wantPrefix {
				t.Errorf("path: got %q, want %q", path, tc.wantPrefix)
			}
			if explicit != tc.wantExplicit {
				t.Errorf("explicit: got %v, want %v", explicit, tc.wantExplicit)
			}
		})
	}
}

// TestLoadGlobalMissingFileDispositionByExplicit pins the
// missing-file disposition for explicit overrides: a non-empty
// $AGGREGA_CONFIG that points at a non-existent file is an error.
// The companion XDG-default behavior is in
// TestLoadGlobalMissingFileDispositionByXDG.
func TestLoadGlobalMissingFileDispositionByExplicit(t *testing.T) {
	// Explicit override pointing at a non-existent file.
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	_, loaded, err := LoadGlobal(missing)
	if err == nil {
		t.Errorf("expected error for explicit missing-file override, got nil")
	}
	if loaded {
		t.Error("loaded: expected false on error")
	}
}

// TestLoadGlobalMissingFileDispositionByXDG pins the silent
// missing-file behavior when no explicit override is in play. With
// AGGREGA_CONFIG cleared and XDG_CONFIG_HOME pointing at an empty
// tmp directory, LoadGlobal returns Default()+loaded=false+nil err.
func TestLoadGlobalMissingFileDispositionByXDG(t *testing.T) {
	t.Setenv(envVarConfigOverride, "")
	t.Setenv(envVarXDGConfigHome, t.TempDir())
	cfg, loaded, err := LoadGlobal("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded {
		t.Error("loaded: expected false for XDG-default missing file")
	}
	if cfg.Workspace.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("returned Config is not Default(): got DefaultRegistry=%q", cfg.Workspace.DefaultRegistry)
	}
}

// TestLoadGlobalLoadsValidFile verifies the happy path: a valid
// global config at the explicit override path loads as a Config with
// the file's fields overlaid on Default().
func TestLoadGlobalLoadsValidFile(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("testdata", "global", "global-only.yaml"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cfg, loaded, err := LoadGlobal(path)
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if !loaded {
		t.Error("loaded: expected true for a real file")
	}
	if cfg.Workspace.DefaultRegistry != "global.example.com" {
		t.Errorf("DefaultRegistry: got %q, want %q", cfg.Workspace.DefaultRegistry, "global.example.com")
	}
	if cfg.Build.HaltOnDeprecated != true {
		t.Errorf("HaltOnDeprecated: got %v, want true", cfg.Build.HaltOnDeprecated)
	}
	// Field NOT set in the file inherits the Default() value.
	if cfg.Build.HaltOnDeprecatedSeverity != "error" {
		t.Errorf("HaltOnDeprecatedSeverity: got %q, want \"error\" (from Default)",
			cfg.Build.HaltOnDeprecatedSeverity)
	}
}

// TestLoadGlobalRejectsMalformedFile verifies the malformed-file
// disposition: a yaml file that fails schema validation is an error
// regardless of source (explicit or XDG).
func TestLoadGlobalRejectsMalformedFile(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("testdata", "global", "malformed.yaml"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	_, _, err = LoadGlobal(path)
	if err == nil {
		t.Fatal("expected error for malformed global config, got nil")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("expected ErrConfigInvalid in error chain, got %v", err)
	}
}

// TestLoadWithGlobalDefaults pins the three-level precedence
// outcome when only Default() applies: no global file, no workspace
// file. Source is SourceDefaults.
func TestLoadWithGlobalDefaults(t *testing.T) {
	t.Setenv(envVarConfigOverride, "")
	t.Setenv(envVarXDGConfigHome, t.TempDir())
	cfg, src, err := LoadWithGlobal("", "")
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if src != SourceDefaults {
		t.Errorf("Source: got %s, want SourceDefaults", src)
	}
	if cfg.Workspace.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("DefaultRegistry: got %q, want hub.aggrega.ai", cfg.Workspace.DefaultRegistry)
	}
}

// TestLoadWithGlobalSourceGlobal verifies the global-only path: a
// global file is loaded and no workspace file is supplied. Source
// reports SourceGlobal; the returned Config carries the global
// file's values.
func TestLoadWithGlobalSourceGlobal(t *testing.T) {
	global := mustAbs(t, "testdata/global/global-only.yaml")
	cfg, src, err := LoadWithGlobal("", global)
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if src != SourceGlobal {
		t.Errorf("Source: got %s, want SourceGlobal", src)
	}
	if cfg.Workspace.DefaultRegistry != "global.example.com" {
		t.Errorf("DefaultRegistry: got %q, want global.example.com", cfg.Workspace.DefaultRegistry)
	}
}

// TestLoadWithGlobalPerFieldOverlay verifies per-field overlay
// semantics across all three layers: workspace fields override
// global fields per-key; global fields override Default per-key.
// Workspace file sets DefaultRegistry only; global file sets
// DefaultRegistry AND cache_dir; defaults supply
// HaltOnDeprecatedSeverity. After merge:
//   - DefaultRegistry from workspace (workspace wins)
//   - cache_dir from global (workspace silent → falls through)
//   - HaltOnDeprecatedSeverity from defaults (neither overlay set it)
func TestLoadWithGlobalPerFieldOverlay(t *testing.T) {
	global := mustAbs(t, "testdata/global/global-only.yaml")
	workspace := mustAbs(t, "testdata/global/workspace-overlay.yaml")
	cfg, src, err := LoadWithGlobal(workspace, global)
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if src != SourceWorkspace {
		t.Errorf("Source: got %s, want SourceWorkspace", src)
	}
	if cfg.Workspace.DefaultRegistry != "workspace.example.com" {
		t.Errorf("DefaultRegistry: got %q, want workspace.example.com (workspace overrides global)",
			cfg.Workspace.DefaultRegistry)
	}
	if cfg.Workspace.CacheDir != ".aggrega/global-cache" {
		t.Errorf("CacheDir: got %q, want .aggrega/global-cache (global, workspace silent)",
			cfg.Workspace.CacheDir)
	}
	if cfg.Build.HaltOnDeprecatedSeverity != "error" {
		t.Errorf("HaltOnDeprecatedSeverity: got %q, want \"error\" (default, neither overlay set it)",
			cfg.Build.HaltOnDeprecatedSeverity)
	}
}

func mustAbs(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}

// Sanity helper to ensure the test fixtures actually exist before any
// case fails for environmental reasons.
func TestGlobalTestdataPresent(t *testing.T) {
	for _, name := range []string{"global-only.yaml", "workspace-overlay.yaml", "malformed.yaml"} {
		path := filepath.Join("testdata", "global", name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("fixture %s not present: %v", path, err)
		}
	}
}
