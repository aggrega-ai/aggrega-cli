// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

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
