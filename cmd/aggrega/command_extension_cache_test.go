// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestACLIPluginCachedForTheMachineRunsInsideAndOutsideAWorkspace resolves an
// installed CLI plugin whose bytes sit in the machine-wide artifact cache — where
// an addition from a folder that is no workspace caches them — through the
// resolver its command runs with, from such a folder and from inside a workspace.
// Both find the bytes, held to the pin the global configuration records.
//
// The workspace row is then resolved again with the machine-wide cache withheld
// from its inputs, and fails as not on this machine: so the bytes it found came
// from that cache, read after its own workspace's cache missed, and not from
// somewhere the row did not mean.
//
// 🔴 PLANTED DEFECTS: drop the machine-wide read from resolvePluginURI and the
// workspace row fails; build the resolution inputs from the resolution's own root
// and artifact cache directory whatever it found, and the row outside a workspace
// fails.
func TestACLIPluginCachedForTheMachineRunsInsideAndOutsideAWorkspace(t *testing.T) {
	machine := t.TempDir()
	global := filepath.Join(machine, "global.yaml")
	const uri = "oci://registry.invalid/plugin/cli/greet:1.0.0"
	published := []byte("the cached plugin's bytes")
	pin := digest.ContentDigest(published)
	writeFixture(t, global, "plugins:\n  cli:\n    - uri: "+uri+"\n      digest: "+pin+"\n")
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

	outsideFolder := folderNoWorkspaceResolvesFrom(t)
	outside, err := workspaceconfig.Resolve(outsideFolder, "", lookup)
	if err != nil || !outside.FoundNoWorkspace() {
		t.Fatalf("precondition: %s must resolve as a folder that is no workspace (err %v)", outsideFolder, err)
	}
	machineRoot, machineDir, err := outside.MachineArtifactCache()
	if err != nil {
		t.Fatalf("the machine-wide cache does not resolve: %v", err)
	}
	if err := oci.WriteCached(machineRoot, machineDir, oci.PluginArtifactKind(), uri, pin, published); err != nil {
		t.Fatalf("cache the plugin for the machine: %v", err)
	}

	workspaceFolder := t.TempDir()
	writeWorkspaceConfig(t, workspaceFolder, "workspace: {}\n")
	inside, err := workspaceconfig.Resolve(workspaceFolder, "", lookup)
	if err != nil || inside.FoundNoWorkspace() {
		t.Fatalf("precondition: %s must resolve as a workspace (err %v)", workspaceFolder, err)
	}

	for _, tc := range []struct {
		name string
		rw   *workspaceconfig.ResolvedWorkspace
	}{
		{"from a folder that is no workspace", outside},
		{"from inside a workspace", inside},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := workspacePluginResolver(tc.rw)(uri)
			if err != nil {
				t.Fatalf("resolve the installed plugin: %v", err)
			}
			if !bytes.Equal(resolved.Bytes, published) {
				t.Errorf("resolved %q; want the bytes cached for the machine", resolved.Bytes)
			}
		})
	}

	withheld := workspacePluginResolutionInputs(inside)
	withheld.machineRoot, withheld.machineCacheDir = "", ""
	if _, err := resolvePluginURI(uri, withheld); !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
		t.Fatalf("with the machine-wide cache withheld the workspace resolved %v; want not on this machine, "+
			"so the row above is not shown to have read that cache", err)
	}
}

// TestCacheStatusListsTheCLIPluginStoreOutsideAWorkspace runs `aggrega cache
// status` through the real command tree from a folder that is no workspace, with
// the process environment pointed at a machine of the test's own whose cli-plugin
// store holds one plugin's bytes — where `aggrega plugin add cli` caches them
// outside a workspace. The listing names the store as a machine-wide class,
// carries the slot, and names the class among the machine-wide ones.
//
// Not parallel: it moves the working directory and sets the process environment.
//
// 🔴 PLANTED DEFECT: drop WithCLIPluginsOf from resolveCacheLocations and the
// listing carries no cli-plugin class.
func TestCacheStatusListsTheCLIPluginStoreOutsideAWorkspace(t *testing.T) {
	hometest.Isolate(t)
	machine := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(machine, "cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(machine, "localappdata"))
	global := filepath.Join(machine, "global.yaml")
	if err := os.WriteFile(global, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(workspaceconfig.EnvConfigOverride, global)
	t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
	t.Setenv("AGGREGA_OFFLINE", "true")
	folder := folderNoWorkspaceResolvesFrom(t)
	rw, err := workspaceconfig.Resolve(folder, "")
	if err != nil || !rw.FoundNoWorkspace() {
		t.Fatalf("precondition: %s must resolve as a folder that is no workspace (err %v)", folder, err)
	}
	root, dir, err := rw.MachineArtifactCache()
	if err != nil {
		t.Fatalf("the machine-wide store does not resolve: %v", err)
	}
	const uri = "oci://registry.invalid/plugin/cli/moo:2.0.0"
	data := []byte("the plugin's bytes")
	if err := oci.WriteCached(root, dir, oci.PluginArtifactKind(), uri, digest.ContentDigest(data), data); err != nil {
		t.Fatalf("seed the store: %v", err)
	}
	t.Chdir(folder)

	out, err := runAggrega(t, "cache", "status")
	if err != nil {
		t.Fatalf("aggrega cache status: %v\n%s", err, out)
	}
	for _, want := range []string{
		"cli-plugin cache [machine]:\n  registry.invalid/plugin/cli/moo:2.0.0@" + digest.ContentDigest(data) + " (Plugin)",
		"cli-plugin cache — content there is shared with every project on this machine",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, out)
		}
	}
}

// TestACLIPluginWhoseBytesAreGoneIsToldToBeAddedAgain registers the cli plugins of
// a configuration pinning one whose bytes no cache on the machine holds — the
// state a `cache clear` leaves — and reads the warning the registration writes. It
// names the command that adds the plugin again, at the version its configuration
// pins, and not the pull a plugin a Release uses is cached by. The restated refusal
// still matches the not-found condition.
//
// Not parallel: it captures the process-wide registration warning writer.
//
// 🔴 PLANTED DEFECT: hand the registration warning derr as it is, rather than
// through missingCLIPluginRemedy, and the warning names the pull and not the
// addition.
func TestACLIPluginWhoseBytesAreGoneIsToldToBeAddedAgain(t *testing.T) {
	warnings := withRegistrationWarnings(t)
	machine := t.TempDir()
	global := filepath.Join(machine, "global.yaml")
	const uri = "oci://registry.invalid/plugin/cli/moo:2.0.0"
	pin := digest.ContentDigest([]byte("bytes no cache on this machine holds"))
	writeFixture(t, global, "plugins:\n  cli:\n    - uri: "+uri+"\n      digest: "+pin+"\n")
	rw, err := workspaceconfig.Resolve(folderNoWorkspaceResolvesFrom(t), "", func(name string) string {
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
		t.Fatalf("resolve: %v", err)
	}

	if regs := cliRegistrationsFromWorkspace(t.Context(), rw); len(regs) != 0 {
		t.Fatalf("a plugin whose bytes are gone registered %d command(s)", len(regs))
	}
	got := warnings.String()
	if want := "add it again with: aggrega plugin add cli moo:2.0.0"; !strings.Contains(got, want) {
		t.Errorf("the warning does not name the command that adds the plugin again (%q):\n%s", want, got)
	}
	if strings.Contains(got, "aggrega pull") {
		t.Errorf("the warning names a pull, which does not cache a cli plugin's bytes:\n%s", got)
	}

	restated := missingCLIPluginRemedy(uri, pluginpath.ErrPluginArtifactNotFound())
	if !errors.Is(restated, pluginpath.ErrPluginArtifactNotFound()) {
		t.Errorf("the restated refusal %v no longer matches the not-found condition", restated)
	}
	other := errors.New("some other fault")
	if missingCLIPluginRemedy(uri, other) != other {
		t.Error("a refusal other than not-found was restated")
	}
}
