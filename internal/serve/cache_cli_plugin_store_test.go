// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The daemon addresses the machine-wide store of cli plugins' bytes as every other
// surface does: its cache roots carry the store and the pins of the workspace it
// was opened over, and its cache status lists the store among the machine-wide
// classes.
//
// Not parallel: the wire test points the session's cache roots at its own fixture
// through the package-level seam, and the roots test reads that seam unset.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheDaemonsCacheRootsCarryTheCLIPluginStoreOfItsWorkspace resolves the
// session's cache roots over a workspace whose configuration pins one cli plugin,
// and holds them to that resolution: the store is the machine-wide one the
// resolution names, and the pin is the one its configuration records.
//
// 🔴 PLANTED DEFECT: drop WithCLIPluginsOf from Server.cacheLocations and this
// fails on the missing store and pin.
func TestTheDaemonsCacheRootsCarryTheCLIPluginStoreOfItsWorkspace(t *testing.T) {
	if testCacheLocations != nil {
		t.Fatal("precondition: the cache roots are redirected by another test, so the session's own roots cannot be read")
	}
	machine := t.TempDir()
	pin := digest.ContentDigest([]byte("the pinned plugin's bytes"))
	global := filepath.Join(machine, "global.yaml")
	if err := os.WriteFile(global, []byte("plugins:\n  cli:\n    - uri: oci://registry.invalid/plugin/cli/moo:2.0.0\n      digest: "+pin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rw, err := workspaceconfig.Resolve(ws, "", func(name string) string {
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
	})
	if err != nil {
		t.Fatalf("resolve %s: %v", ws, err)
	}
	root, dir, err := rw.MachineArtifactCache()
	if err != nil {
		t.Fatalf("the machine-wide store does not resolve: %v", err)
	}

	// The session is built as every test here builds one, so what it holds beside
	// the resolution — the documentation-bundle statement among it — is a session's
	// own; the resolution is then the one this test took, with its frozen environment.
	srv := newTestServer(t, ws, nil, nil)
	srv.resolvedWorkspace = rw

	loc := srv.cacheLocations(t.Context())

	if want := oci.EntitiesRoot(root, dir); loc.CLIPluginsDir != want {
		t.Errorf("the daemon's roots name the cli-plugin store %q; want %q", loc.CLIPluginsDir, want)
	}
	if !slices.Equal(loc.CLIPluginPins, []string{pin}) {
		t.Errorf("the daemon's roots carry the pins %v; want [%s]", loc.CLIPluginPins, pin)
	}
}

// TestTheDaemonsCacheStatusListsTheCLIPluginStoreAsMachineWide seeds one cli
// plugin's bytes in a store of the test's own and reads the daemon's cache status
// through the real dispatch: the store is listed with machine reach, carries the
// slot, and is named among the machine-wide classes.
//
// 🔴 PLANTED DEFECT: drop the cli-plugin class from cacheops.Collect and this fails
// on the missing class.
func TestTheDaemonsCacheStatusListsTheCLIPluginStoreAsMachineWide(t *testing.T) {
	machine := t.TempDir()
	const ref = "oci://registry.invalid/plugin/cli/moo:2.0.0"
	data := []byte("the pinned plugin's bytes")
	if err := oci.WriteCached(machine, "artifacts", oci.PluginArtifactKind(), ref, digest.ContentDigest(data), data); err != nil {
		t.Fatalf("seed the store: %v", err)
	}
	wsRoot := t.TempDir()
	addressCacheRootsAt(t, cacheops.Locations{
		WorkspaceRoot: wsRoot,
		CacheDir:      heldOpenCacheDir,
		CLIPluginsDir: oci.EntitiesRoot(machine, "artifacts"),
	})
	srv := newTestServer(t, wsRoot, nil, nil)

	answer := cacheAnswerOnTheWire(t, srv, "aggrega/cacheStatus", map[string]any{})

	var found bool
	for _, class := range arrayOnTheWire(t, answer, "classes") {
		if stringOnTheWire(t, class, "name") != cacheops.CLIPluginClassName {
			continue
		}
		found = true
		if reach := stringOnTheWire(t, class, "reach"); reach != string(cacheops.ReachMachine) {
			t.Errorf("the cli-plugin class reports reach %q, want %q", reach, cacheops.ReachMachine)
		}
		if elements := arrayOnTheWire(t, class, "elements"); len(elements) != 1 {
			t.Errorf("the cli-plugin class lists %v, want the one seeded slot", elements)
		}
	}
	if !found {
		t.Fatalf("the daemon's cache status lists no %q class: %s", cacheops.CLIPluginClassName, answer["classes"])
	}
}
