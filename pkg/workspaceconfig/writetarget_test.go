// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestConfigOperationNature proves the single nature classification: registry
// and CLI operations are tool-scoped; provider bindings and config-socket
// attachments are build-scoped.
func TestConfigOperationNature(t *testing.T) {
	t.Parallel()
	cases := []struct {
		op   workspaceconfig.ConfigOperation
		want workspaceconfig.ConfigNature
	}{
		{workspaceconfig.OpRegistry, workspaceconfig.NatureTool},
		{workspaceconfig.OpCLI, workspaceconfig.NatureTool},
		{workspaceconfig.OpProviderBinding, workspaceconfig.NatureBuild},
		{workspaceconfig.OpConfigSocket, workspaceconfig.NatureBuild},
	}
	for _, c := range cases {
		if got := c.op.Nature(); got != c.want {
			t.Errorf("op %d Nature() = %d; want %d", c.op, got, c.want)
		}
	}
}

// TestGlobalConfigPath proves the global-path resolution: $AGGREGA_CONFIG wins
// when set, else the XDG default, else the HOME fallback, else not-resolvable.
func TestGlobalConfigPath(t *testing.T) {
	t.Parallel()
	explicit := filepath.Join(t.TempDir(), "explicit.yaml")
	xdg := t.TempDir()
	home := t.TempDir()

	if got, ok := workspaceconfig.GlobalConfigPath(frozenLookup(map[string]string{
		"AGGREGA_CONFIG":  explicit,
		"XDG_CONFIG_HOME": xdg,
	})); !ok || got != explicit {
		t.Errorf("explicit: got %q ok=%v; want %q true", got, ok, explicit)
	}

	wantXDG := filepath.Join(xdg, "aggrega", "config.yaml")
	if got, ok := workspaceconfig.GlobalConfigPath(frozenLookup(map[string]string{
		"XDG_CONFIG_HOME": xdg,
	})); !ok || got != wantXDG {
		t.Errorf("xdg: got %q ok=%v; want %q true", got, ok, wantXDG)
	}

	wantHome := filepath.Join(home, ".config", "aggrega", "config.yaml")
	if got, ok := workspaceconfig.GlobalConfigPath(frozenLookup(map[string]string{
		"HOME": home,
	})); !ok || got != wantHome {
		t.Errorf("home: got %q ok=%v; want %q true", got, ok, wantHome)
	}

	if got, ok := workspaceconfig.GlobalConfigPath(frozenLookup(map[string]string{})); ok {
		t.Errorf("unresolvable: got %q ok=%v; want ok=false", got, ok)
	}
}

// TestWriteDestinationDefaults proves the nature default: a tool-scoped
// operation lands in the global file, a build-scoped operation in the workspace
// file, with no override flags set.
func TestWriteDestinationDefaults(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	global := filepath.Join(t.TempDir(), "global.yaml")
	lookup := frozenLookup(map[string]string{"AGGREGA_CONFIG": global})

	got, err := workspaceconfig.WriteDestination(workspaceconfig.OpRegistry, false, false, root, lookup)
	if err != nil {
		t.Fatalf("registry default: %v", err)
	}
	if got != global {
		t.Errorf("registry default = %q; want the global file %q", got, global)
	}

	wantWS := filepath.Join(root, workspaceconfig.ConfigFilename)
	for _, op := range []workspaceconfig.ConfigOperation{workspaceconfig.OpProviderBinding, workspaceconfig.OpConfigSocket} {
		got, err := workspaceconfig.WriteDestination(op, false, false, root, lookup)
		if err != nil {
			t.Fatalf("build op %d default: %v", op, err)
		}
		if got != wantWS {
			t.Errorf("build op %d default = %q; want the workspace file %q", op, got, wantWS)
		}
	}
}

// TestWriteDestinationOverrides proves --global and --workspace force the
// destination regardless of the operation's nature.
func TestWriteDestinationOverrides(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	global := filepath.Join(t.TempDir(), "global.yaml")
	lookup := frozenLookup(map[string]string{"AGGREGA_CONFIG": global})
	wantWS := filepath.Join(root, workspaceconfig.ConfigFilename)

	// A build op forced global.
	if got, err := workspaceconfig.WriteDestination(workspaceconfig.OpProviderBinding, true, false, root, lookup); err != nil || got != global {
		t.Errorf("provider --global = %q err=%v; want %q", got, err, global)
	}
	// A tool op forced workspace.
	if got, err := workspaceconfig.WriteDestination(workspaceconfig.OpRegistry, false, true, root, lookup); err != nil || got != wantWS {
		t.Errorf("registry --workspace = %q err=%v; want %q", got, err, wantWS)
	}
}

// TestWriteDestinationBothFlags proves setting both overrides is a usage error.
func TestWriteDestinationBothFlags(t *testing.T) {
	t.Parallel()
	_, err := workspaceconfig.WriteDestination(workspaceconfig.OpRegistry, true, true, t.TempDir(), frozenLookup(nil))
	if err == nil {
		t.Fatal("both --global and --workspace should be a usage error")
	}
	if !strings.Contains(err.Error(), "global") || !strings.Contains(err.Error(), "workspace") {
		t.Errorf("err = %v; want it to name both global and workspace", err)
	}
}

// TestWriteDestinationUnresolvableGlobal proves a global target with no
// resolvable path (neither $AGGREGA_CONFIG nor a home base directory) is an
// error rather than a bogus relative path.
func TestWriteDestinationUnresolvableGlobal(t *testing.T) {
	t.Parallel()
	_, err := workspaceconfig.WriteDestination(workspaceconfig.OpRegistry, false, false, t.TempDir(), frozenLookup(map[string]string{}))
	if err == nil {
		t.Fatal("an unresolvable global path should be an error")
	}
}

// TestUpsertCreatesMissingParentDir proves the config writer creates a missing
// parent directory before writing, so a global write into a not-yet-created home
// config dir succeeds rather than failing at os.WriteFile.
func TestUpsertCreatesMissingParentDir(t *testing.T) {
	t.Parallel()
	// A path two directory levels below a fresh temp dir: neither level exists.
	path := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
	if err := workspaceconfig.UpsertRegistry(path, workspaceconfig.RegistryConfig{Host: "example.com"}); err != nil {
		t.Fatalf("UpsertRegistry into a missing parent dir: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not written at %q: %v", path, err)
	}
}
