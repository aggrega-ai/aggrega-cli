// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// frozenLookup returns a func(string) string that answers only from vars,
// yielding "" for every other key. It replaces os.Getenv in Resolve so the
// global-layer resolution (both the $AGGREGA_CONFIG read and the XDG home
// resolution) and the environment overlay read a fixed environment,
// keeping the test deterministic and parallel-safe.
func frozenLookup(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// TestGlobalLayerXDGAutoLoad: with $AGGREGA_CONFIG unset, an existing
// $XDG_CONFIG_HOME/aggrega/config.yaml is auto-loaded as the global layer and
// contributes to the cascade; the Source reflects the global layer.
func TestGlobalLayerXDGAutoLoad(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir()
	write(t, filepath.Join(xdg, "aggrega", "config.yaml"), "default_registry: xdg.io\n")
	root := t.TempDir()

	rw, err := workspaceconfig.Resolve(root, "", frozenLookup(map[string]string{
		"XDG_CONFIG_HOME": xdg,
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rw.Config().DefaultRegistry; got != "xdg.io" {
		t.Errorf("DefaultRegistry: got %q; want xdg.io (XDG global auto-loaded when $AGGREGA_CONFIG unset)", got)
	}
	if rw.Source() != workspaceconfig.SourceGlobal {
		t.Errorf("Source: got %q; want global (only the global layer loaded)", rw.Source())
	}
}

// TestGlobalLayerXDGHomeFallback: with $AGGREGA_CONFIG and $XDG_CONFIG_HOME
// both unset, the global layer falls back to $HOME/.config/aggrega/config.yaml.
func TestGlobalLayerXDGHomeFallback(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	write(t, filepath.Join(home, ".config", "aggrega", "config.yaml"), "default_registry: home.io\n")
	root := t.TempDir()

	rw, err := workspaceconfig.Resolve(root, "", frozenLookup(map[string]string{
		"HOME": home,
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rw.Config().DefaultRegistry; got != "home.io" {
		t.Errorf("DefaultRegistry: got %q; want home.io (HOME fallback when $XDG_CONFIG_HOME empty)", got)
	}
	if rw.Source() != workspaceconfig.SourceGlobal {
		t.Errorf("Source: got %q; want global", rw.Source())
	}
}

// TestGlobalLayerExplicitOverridesXDG: a set $AGGREGA_CONFIG names the explicit
// path and wins over the XDG default.
func TestGlobalLayerExplicitOverridesXDG(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir()
	write(t, filepath.Join(xdg, "aggrega", "config.yaml"), "default_registry: xdg.io\n")
	explicit := filepath.Join(t.TempDir(), "explicit.yaml")
	write(t, explicit, "default_registry: explicit.io\n")
	root := t.TempDir()

	rw, err := workspaceconfig.Resolve(root, "", frozenLookup(map[string]string{
		"AGGREGA_CONFIG":  explicit,
		"XDG_CONFIG_HOME": xdg,
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rw.Config().DefaultRegistry; got != "explicit.io" {
		t.Errorf("DefaultRegistry: got %q; want explicit.io ($AGGREGA_CONFIG path overrides XDG)", got)
	}
}

// TestGlobalLayerExplicitMissingErrors: a set $AGGREGA_CONFIG that points at a
// missing file is an error, never a silent fallback to the XDG default.
func TestGlobalLayerExplicitMissingErrors(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir()
	// A valid XDG default exists; it must NOT rescue the missing explicit path.
	write(t, filepath.Join(xdg, "aggrega", "config.yaml"), "default_registry: xdg.io\n")
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	root := t.TempDir()

	_, err := workspaceconfig.Resolve(root, "", frozenLookup(map[string]string{
		"AGGREGA_CONFIG":  missing,
		"XDG_CONFIG_HOME": xdg,
	}))
	if err == nil {
		t.Fatalf("Resolve: want error for missing $AGGREGA_CONFIG file, got nil")
	}
}

// TestGlobalLayerNoneWhenAbsent: with $AGGREGA_CONFIG unset and no XDG default
// file present, no global layer loads — the effective config is Default() and
// the Source reports defaults.
func TestGlobalLayerNoneWhenAbsent(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir() // empty: no aggrega/config.yaml
	root := t.TempDir()

	rw, err := workspaceconfig.Resolve(root, "", frozenLookup(map[string]string{
		"XDG_CONFIG_HOME": xdg,
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rw.Config().DefaultRegistry; got != "hub.aggrega.ai" {
		t.Errorf("DefaultRegistry: got %q; want hub.aggrega.ai (Default(), no global layer)", got)
	}
	if rw.Source() != workspaceconfig.SourceDefaults {
		t.Errorf("Source: got %q; want defaults (neither global nor workspace file loaded)", rw.Source())
	}
}

// TestGlobalLayerXDGUnderTree: a tree config file overrides the auto-loaded XDG
// global layer per the unchanged cascade precedence (workspace > global).
func TestGlobalLayerXDGUnderTree(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir()
	write(t, filepath.Join(xdg, "aggrega", "config.yaml"), "default_registry: xdg.io\n")
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\ndefault_registry: tree.io\n")

	rw, err := workspaceconfig.Resolve(root, "", frozenLookup(map[string]string{
		"XDG_CONFIG_HOME": xdg,
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rw.Config().DefaultRegistry; got != "tree.io" {
		t.Errorf("DefaultRegistry: got %q; want tree.io (tree file overrides the global layer)", got)
	}
	if rw.Source() != workspaceconfig.SourceWorkspace {
		t.Errorf("Source: got %q; want workspace (a workspace file was loaded)", rw.Source())
	}
}
