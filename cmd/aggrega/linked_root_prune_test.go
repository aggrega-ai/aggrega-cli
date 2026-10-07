// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestACachePruneOfAWorkspaceReachedThroughALinkKeepsWhatItsLockfilePins drives
// `aggrega cache prune` through the real command tree over a pulled workspace — a
// lockfile pinning one cached entity, and a second cached entity nothing pins —
// reached three ways: typed as itself (the control), typed through a link to it,
// and as the workspace discovered from a working directory reached through that
// link. Each prune finds the lockfile, keeps the pinned entity and removes the
// other, as it does for the workspace named directly; none refuses the workspace
// as one never pulled.
func TestACachePruneOfAWorkspaceReachedThroughALinkKeepsWhatItsLockfilePins(t *testing.T) {
	for _, row := range []struct {
		name string
		// args are the command line, given the workspace and the link to it; dir is
		// the working directory to run in, "" for none.
		args func(realDir, via string) []string
		dir  func(realDir, via string) string
	}{
		{"control: the workspace typed as itself",
			func(realDir, _ string) []string { return []string{"--workspace-root", realDir, "cache", "prune"} },
			func(string, string) string { return "" }},
		{"the workspace typed through a link to it",
			func(_, via string) []string { return []string{"--workspace-root", via, "cache", "prune"} },
			func(string, string) string { return "" }},
		{"run from a directory reached through a link to the workspace",
			func(string, string) []string { return []string{"cache", "prune"} },
			func(_, via string) string { return via }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			base := t.TempDir()
			realDir := filepath.Join(base, "real")
			via := filepath.Join(base, "via")
			if err := os.MkdirAll(realDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(realDir, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			pinned, pinnedDigest := writeEntitySlot(t, realDir, "oci://hub.aggrega.ai/component/core/api", "1.0.0", []byte("pinned-entity-bytes"))
			orphan, _ := writeEntitySlot(t, realDir, "oci://hub.aggrega.ai/component/core/orphan", "9.9.9", []byte("orphan-entity-bytes"))
			if err := lockfile.WriteLockfile(lockfile.PathFor(filepath.Join(realDir, "release.yaml")), &lockfile.Lockfile{
				Version: 1, Release: "release.yaml", GeneratedAt: "2026-01-01T00:00:00Z",
				Entities: []lockfile.Entity{{URI: "oci://hub.aggrega.ai/component/core/api", ResolvedVersion: "1.0.0",
					Digest: pinnedDigest, ReferencedFrom: []string{"release.yaml"}}},
			}); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(realDir, via); err != nil {
				t.Skipf("symbolic links are not available here: %v", err)
			}
			if dir := row.dir(realDir, via); dir != "" {
				t.Chdir(dir)
			}

			root := newRootCmd()
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetIn(strings.NewReader(""))
			root.SetArgs(row.args(realDir, via))
			if code := executeRoot(context.Background(), root); code != 0 {
				t.Fatalf("`aggrega %s` exited %d; want 0 — a pulled workspace refused\nstdout:\n%s\nstderr:\n%s",
					strings.Join(row.args(realDir, via), " "), code, out.String(), errOut.String())
			}
			if !exists(pinned) {
				t.Errorf("the prune removed the entity its lockfile pins: %s\n%s", pinned, out.String())
			}
			if exists(orphan) {
				t.Errorf("the prune kept the entity nothing pins: %s\n%s", orphan, out.String())
			}
		})
	}
}
