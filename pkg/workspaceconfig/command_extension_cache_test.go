// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestACommandExtensionIsCachedForTheMachineWhereNoWorkspaceWasFound asks every
// way a resolution settles its root where it keeps a command extension's bytes:
// its workspace's own artifact cache wherever a workspace was found or named, and
// the machine-wide artifact cache only where none was found and none named — so a
// folder nobody chose never gains a cache. A machine whose environment names no
// cache location refuses, naming what is missing, rather than falling back to the
// folder.
//
// 🔴 PLANTED DEFECTS: return the resolution's own root and artifact cache directory
// from CommandExtensionCache whatever it found, and the "nothing anchored and
// nothing named" row fails naming the folder; return the machine-wide cache
// whenever one resolves, and the workspace rows fail.
func TestACommandExtensionIsCachedForTheMachineWhereNoWorkspaceWasFound(t *testing.T) {
	t.Parallel()
	empty := noWorkspaceFolder(t)
	named := noWorkspaceFolder(t)
	configured := t.TempDir()
	write(t, filepath.Join(configured, workspaceconfig.ConfigFilename), "workspace: {}\nartifact_cache_dir: .cache-here\n")

	machine := t.TempDir()
	withCache := func(name string) string {
		switch name {
		case "HOME":
			return filepath.Join(machine, "home")
		case "XDG_CACHE_HOME":
			return filepath.Join(machine, "cache")
		case "LOCALAPPDATA":
			return filepath.Join(machine, "localappdata")
		}
		return ""
	}
	noCache := func(string) string { return "" }

	for _, tc := range []struct {
		name            string
		start, flagRoot string
		lookup          func(string) string
		wantMachine     bool
		wantErr         string
	}{
		{name: "nothing anchored and nothing named", start: empty, lookup: withCache, wantMachine: true},
		{name: "nothing anchored, nothing named and no cache location", start: empty, lookup: noCache,
			wantErr: "no machine-wide cache location resolves"},
		{name: "a named root", start: empty, flagRoot: named, lookup: withCache},
		{name: "a workspace configuration anchors the root", start: configured, lookup: withCache},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw, err := workspaceconfig.Resolve(tc.start, tc.flagRoot, tc.lookup)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			root, dir, err := rw.CommandExtensionCache()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("CommandExtensionCache() = %s, %s, %v; want a refusal containing %q", root, dir, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CommandExtensionCache(): %v", err)
			}
			wantRoot, wantDir := rw.Root(), rw.Config().ResolvedArtifactCacheDir()
			if tc.wantMachine {
				var merr error
				wantRoot, wantDir, merr = rw.MachineArtifactCache()
				if merr != nil {
					t.Fatalf("MachineArtifactCache(): %v", merr)
				}
				if !strings.HasPrefix(wantRoot, machine) {
					t.Fatalf("precondition: the machine-wide cache %s is not under the scratch machine %s", wantRoot, machine)
				}
			}
			if root != wantRoot || dir != wantDir {
				t.Errorf("CommandExtensionCache() = %s, %s; want %s, %s", root, dir, wantRoot, wantDir)
			}
		})
	}
}
