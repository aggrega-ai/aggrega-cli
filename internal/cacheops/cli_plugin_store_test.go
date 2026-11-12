// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cacheops

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cliPluginFixture is a workspace holding a lockfile, a global configuration
// pinning one cli plugin, and the machine-wide store holding that plugin's bytes
// and an earlier version's no configuration pins — the store an addition outside
// a workspace fills — all inside the test's own scratch tree.
type cliPluginFixture struct {
	rw                   *workspaceconfig.ResolvedWorkspace
	pinnedSlot, oldSlot  string
	pinnedRef, unpinned  string
	workspaceEntitySlot  string
	workspaceUnreferSlot string
}

func newCLIPluginFixture(t *testing.T) cliPluginFixture {
	t.Helper()
	machine := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.WriteLockfile(filepath.Join(ws, "release.lock"), &lockfile.Lockfile{
		Version: 1, Release: "release.yaml", GeneratedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	const pinnedRef = "oci://registry.invalid/plugin/cli/moo:2.0.0"
	const unpinnedRef = "oci://registry.invalid/plugin/cli/moo:1.0.0"
	pinnedBytes, oldBytes := []byte("the pinned plugin's bytes"), []byte("an earlier version's bytes")
	pin, oldPin := digest.ContentDigest(pinnedBytes), digest.ContentDigest(oldBytes)

	global := filepath.Join(machine, "global.yaml")
	if err := os.WriteFile(global, []byte("plugins:\n  cli:\n    - uri: "+pinnedRef+"\n      digest: "+pin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lookup := func(name string) string {
		switch name {
		case "AGGREGA_CONFIG":
			return global
		case "HOME":
			return filepath.Join(machine, "home")
		case "XDG_CACHE_HOME":
			return filepath.Join(machine, "cache")
		case "LOCALAPPDATA":
			return filepath.Join(machine, "localappdata")
		}
		return ""
	}
	rw, err := workspaceconfig.Resolve(ws, "", lookup)
	if err != nil {
		t.Fatalf("resolve %s: %v", ws, err)
	}
	root, dir, err := rw.MachineArtifactCache()
	if err != nil {
		t.Fatalf("the machine-wide store does not resolve: %v", err)
	}
	kind := oci.PluginArtifactKind()
	write := func(root, dir, ref, version string, data []byte) string {
		t.Helper()
		if err := oci.WriteCached(root, dir, kind, ref, version, data); err != nil {
			t.Fatalf("write %s: %v", ref, err)
		}
		slot, err := oci.CachePath(root, dir, kind, ref, version)
		if err != nil {
			t.Fatal(err)
		}
		return slot
	}
	return cliPluginFixture{
		rw:         rw,
		pinnedSlot: write(root, dir, pinnedRef, pin, pinnedBytes),
		oldSlot:    write(root, dir, unpinnedRef, oldPin, oldBytes),
		pinnedRef:  pinnedRef,
		unpinned:   unpinnedRef,
		// The same pinned plugin installed inside the workspace keeps its bytes in the
		// workspace's own entity cache; a prune keeps those by the same pin.
		workspaceEntitySlot:  write(ws, rw.Config().ResolvedArtifactCacheDir(), pinnedRef, pin, pinnedBytes),
		workspaceUnreferSlot: write(ws, rw.Config().ResolvedArtifactCacheDir(), unpinnedRef, oldPin, oldBytes),
	}
}

// TestTheCLIPluginStoreIsListedKeptWhilePinnedAndCleared drives the inventory, a
// prune and a clear over the machine-wide store of cli plugins' bytes, with the
// roots filled from the resolution that pins one of them
// (Locations.WithCLIPluginsOf).
//
// The status lists the store as a machine-wide class holding both slots. The prune
// removes the slot no pin names and keeps the pinned one — in the store and in the
// workspace's own entity cache alike, since the pin is what keeps it. The clear
// removes the store and names it among the machine-wide classes it reached.
//
// 🔴 PLANTED DEFECTS: drop the cli-plugin class from Collect and the status and
// clear rows fail; drop the CLIPluginPins loop from Prune and the pinned slots are
// removed (both rows of the prune fail); leave WithCLIPluginsOf unchained and the
// status row fails on the missing class.
func TestTheCLIPluginStoreIsListedKeptWhilePinnedAndCleared(t *testing.T) {
	t.Parallel()
	fx := newCLIPluginFixture(t)
	loc := Locations{WorkspaceRoot: fx.rw.Root(), CacheDir: fx.rw.Config().ResolvedArtifactCacheDir()}.WithCLIPluginsOf(fx.rw)

	report, err := Status(t.Context(), loc)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	var listed *Class
	for i := range report.Classes {
		if report.Classes[i].Name == CLIPluginClassName {
			listed = &report.Classes[i]
		}
	}
	if listed == nil {
		t.Fatalf("the inventory lists no %q class: %+v", CLIPluginClassName, report.Classes)
	}
	if listed.Reach != ReachMachine || !listed.Prunable {
		t.Errorf("the %s class is reach %q, prunable %v; want machine, prunable", CLIPluginClassName, listed.Reach, listed.Prunable)
	}
	if len(listed.Elements) != 2 {
		t.Fatalf("the %s class lists %+v; want the two slots laid down", CLIPluginClassName, listed.Elements)
	}
	if !slices.Contains(MachineWideClasses(report.Classes), CLIPluginClassName) {
		t.Errorf("the machine-wide classes %v do not name the %s", MachineWideClasses(report.Classes), CLIPluginClassName)
	}

	res, err := Prune(t.Context(), loc)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	for _, kept := range []string{fx.pinnedSlot, fx.workspaceEntitySlot} {
		if _, serr := os.Stat(filepath.Join(kept, "blob")); serr != nil {
			t.Errorf("the prune removed %s, which the configuration pins: %v", kept, serr)
		}
	}
	for _, gone := range []string{fx.oldSlot, fx.workspaceUnreferSlot} {
		if _, serr := os.Stat(gone); !os.IsNotExist(serr) {
			t.Errorf("the prune kept %s, which nothing pins (stat: %v)", gone, serr)
		}
	}
	var removedFromStore bool
	for _, r := range res.Removed {
		if r.Class == CLIPluginClassName {
			removedFromStore = true
			if r.Reach != ReachMachine || !strings.Contains(r.Label, strings.TrimPrefix(fx.unpinned, "oci://")) {
				t.Errorf("the removal from the store reads %+v; want the unpinned slot, machine reach", r)
			}
		}
	}
	if !removedFromStore {
		t.Errorf("the prune names no removal from the %s: %+v", CLIPluginClassName, res.Removed)
	}

	cleared, err := Clear(t.Context(), loc)
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	var clearedStore bool
	for _, c := range cleared.Cleared {
		clearedStore = clearedStore || c.Class == CLIPluginClassName
	}
	if !clearedStore || !slices.Contains(cleared.MachineWide, CLIPluginClassName) {
		t.Errorf("the clear reports %+v (machine-wide %v); want the %s cleared and named", cleared.Cleared, cleared.MachineWide, CLIPluginClassName)
	}
	if _, serr := os.Stat(fx.pinnedSlot); !os.IsNotExist(serr) {
		t.Errorf("the clear left the pinned slot %s (stat: %v)", fx.pinnedSlot, serr)
	}
}

// TestRootsFilledFromNoResolutionCarryNoCLIPluginStore pins the other half of
// WithCLIPluginsOf: with no resolution the roots name no store and no pin, so an
// inventory taken without one lists no such class rather than guessing at a store
// some resolution would have filled.
func TestRootsFilledFromNoResolutionCarryNoCLIPluginStore(t *testing.T) {
	t.Parallel()
	loc := Locations{WorkspaceRoot: "w", CacheDir: "c"}.WithCLIPluginsOf(nil)
	if loc.CLIPluginsDir != "" || len(loc.CLIPluginPins) != 0 {
		t.Errorf("roots filled from no resolution carry a store %q and pins %v", loc.CLIPluginsDir, loc.CLIPluginPins)
	}
}
