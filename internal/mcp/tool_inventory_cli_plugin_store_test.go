// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheCacheInventoryListsTheCLIPluginStoreOfTheCallsWorkspace calls the
// cache-status tool with the inventory's roots left to the server's own
// resolution, over a workspace whose environment names a machine-wide store of the
// test's own holding one cli plugin's bytes. The store is listed with machine reach,
// carries the slot, and is named among the machine-wide classes — the class every
// other surface reports, filled from the call's own workspace.
//
// Not parallel: the workspace is resolved through the process environment, which
// the test points at its own scratch tree.
//
// 🔴 PLANTED DEFECT: drop WithCLIPluginsOf from the tool's default roots and this
// fails on the missing class.
func TestTheCacheInventoryListsTheCLIPluginStoreOfTheCallsWorkspace(t *testing.T) {
	machine := t.TempDir()
	t.Setenv("HOME", filepath.Join(machine, "home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(machine, "cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(machine, "localappdata"))
	global := filepath.Join(machine, "global.yaml")
	if err := os.WriteFile(global, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(workspaceconfig.EnvConfigOverride, global)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rw, err := workspaceconfig.Resolve(ws, ws)
	if err != nil {
		t.Fatalf("resolve %s: %v", ws, err)
	}
	root, dir, err := rw.MachineArtifactCache()
	if err != nil {
		t.Fatalf("the machine-wide store does not resolve: %v", err)
	}
	const ref = "oci://registry.invalid/plugin/cli/moo:2.0.0"
	data := []byte("the plugin's bytes")
	if err := oci.WriteCached(root, dir, oci.PluginArtifactKind(), ref, digest.ContentDigest(data), data); err != nil {
		t.Fatalf("seed the store: %v", err)
	}
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: ws}

	res := callTool(t, s, "cache-status", map[string]any{"workspace_root": ws})
	if res.IsError {
		t.Fatalf("cache-status reported an error: %s", res.Content[0].Text)
	}
	var payload struct {
		Classes []struct {
			Name     string            `json:"name"`
			Reach    string            `json:"reach"`
			Elements []json.RawMessage `json:"elements"`
		} `json:"classes"`
		MachineWideClasses []string `json:"machine_wide_classes"`
	}
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("decode the payload: %v (%s)", err, res.StructuredContent)
	}
	var found bool
	for _, class := range payload.Classes {
		if class.Name != cacheops.CLIPluginClassName {
			continue
		}
		found = true
		if class.Reach != string(cacheops.ReachMachine) || len(class.Elements) != 1 {
			t.Errorf("the cli-plugin class reads reach %q with %d element(s); want machine with the one seeded slot",
				class.Reach, len(class.Elements))
		}
	}
	if !found {
		t.Fatalf("the cache inventory lists no %q class: %s", cacheops.CLIPluginClassName, res.StructuredContent)
	}
	var named bool
	for _, name := range payload.MachineWideClasses {
		named = named || name == cacheops.CLIPluginClassName
	}
	if !named {
		t.Errorf("machine_wide_classes %v does not name the %s", payload.MachineWideClasses, cacheops.CLIPluginClassName)
	}
}
