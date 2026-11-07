// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cacheWorkspaceLayout is one way the workspace a cache command runs over can
// stand: the root named to the command, and the machine it runs as.
type cacheWorkspaceLayout struct {
	root string
	env  map[string]string
}

// cacheCommandRow is one cache command, invoked the way a reader types it.
//
// No row passes --yes to clear or a cap an eviction would act on. A row whose
// stop has gone missing then refuses at the confirmation, or finds the cache
// within its cap, rather than wiping or trimming the machine-wide stores this
// test process shares with every other one.
type cacheCommandRow struct {
	name string
	args []string
}

// cacheCommandRows is every cache command that reads the workspace configuration.
func cacheCommandRows() []cacheCommandRow {
	return []cacheCommandRow{
		{name: "status", args: []string{"cache", "status"}},
		{name: "prune", args: []string{"cache", "prune"}},
		{name: "evict", args: []string{"cache", "evict", "--max-size", "1024GiB"}},
		{name: "clear", args: []string{"cache", "clear"}},
	}
}

// configurationLinkedOut lays a workspace whose configuration file is a link
// leading to a file outside it, which names a cache directory of its own.
func configurationLinkedOut(t *testing.T) cacheWorkspaceLayout {
	t.Helper()
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "kept-elsewhere.yaml")
	if err := os.WriteFile(outside, []byte("workspace: {}\nartifact_cache_dir: cache-elsewhere\n"), 0o644); err != nil {
		t.Fatalf("write the configuration kept outside the workspace: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, workspaceconfig.ConfigFilename)); err != nil {
		t.Skipf("this platform does not let the test create a link: %v", err)
	}
	return cacheWorkspaceLayout{root: ws, env: map[string]string{"HOME": t.TempDir()}}
}

// configurationInPlace lays a workspace whose configuration file is a real file
// naming a cache directory of its own, with one entity cached there, so a listing
// that read the configuration names the entity and one that did not cannot.
func configurationInPlace(t *testing.T) cacheWorkspaceLayout {
	t.Helper()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\nartifact_cache_dir: cache-elsewhere\n"), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	if err := oci.WriteCached(ws, "cache-elsewhere", "Component", "oci://hub.aggrega.ai/component/core/probe", "1.0.0",
		[]byte("cached-entity-bytes")); err != nil {
		t.Fatalf("cache an entity under the configured cache directory: %v", err)
	}
	return cacheWorkspaceLayout{root: ws, env: map[string]string{"HOME": t.TempDir()}}
}

// noWorkspaceResolves names a root that is not an existing folder, so no
// workspace resolves at all.
func noWorkspaceResolves(t *testing.T) cacheWorkspaceLayout {
	t.Helper()
	return cacheWorkspaceLayout{
		root: filepath.Join(t.TempDir(), "no-such-folder"),
		env:  map[string]string{"HOME": t.TempDir()},
	}
}

// classListsElements reports whether the cache status listing in out names at
// least one element under the class headed heading, and whether the class is
// listed at all.
func classListsElements(out, heading string) (listed, populated bool) {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, heading+" [") {
			return true, i+1 < len(lines) && lines[i+1] != "  (empty)"
		}
	}
	return false, false
}

// TestEveryCacheCommandStopsBehindAConfigurationLeadingOutOfTheWorkspace drives
// every cache command through the real command tree over a workspace whose
// configuration file is a link leading out of it, over one whose configuration
// file is a real file, and over a root where no workspace resolves.
//
// Behind the link each command stops with the refusal every other command
// reading the configuration stops with, matched through the condition's accessor
// and printed with its short code at the head. With a real file each runs on the
// workspace the configuration describes: the listing names the entity cached
// under the configured directory. Where no workspace resolves each runs on the
// machine-wide caches, as it did before the stop existed.
//
// 🔴 PLANTED DEFECT: drop the refusal arm from resolveCacheLocations, and every
// row behind the link fails — status and evict answer on the machine-wide caches,
// prune reports that no workspace resolved, and clear refuses at its
// confirmation; none of them names the link.
func TestEveryCacheCommandStopsBehindAConfigurationLeadingOutOfTheWorkspace(t *testing.T) {
	// Not parallel: runAsMachine swaps the command-extension registration, and
	// clear reads the process-wide terminal seam.
	orig := isTerminal
	isTerminal = func() bool { return false }
	t.Cleanup(func() { isTerminal = orig })

	code, ok := cerrors.ShortCodeOf(readscope.ErrReadLeadsOutOfWorkspace())
	if !ok {
		t.Fatal("the condition a configuration leading out draws carries no short code, so no head can name it")
	}

	for _, row := range cacheCommandRows() {
		t.Run("behind a link leading out/"+row.name, func(t *testing.T) {
			layout := configurationLinkedOut(t)
			out, errOut, err := runAsMachine(t, machinedirs.Host(), layout.env, layout.root, row.args...)
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("aggrega %s returned %v, want the refusal of a configuration leading out of the workspace\nstdout:\n%s\nstderr:\n%s",
					strings.Join(row.args, " "), err, out, errOut)
			}
			printed := formatCompilerError(err)
			if !strings.HasPrefix(printed, "["+code+"]") {
				t.Errorf("the printed refusal does not open with [%s]:\n%s", code, printed)
			}
			if !strings.Contains(printed, workspaceconfig.ConfigFilename) {
				t.Errorf("the printed refusal does not name the configuration file:\n%s", printed)
			}
			if out != "" {
				t.Errorf("a refused cache command still answered on standard output:\n%s", out)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		layout func(*testing.T) cacheWorkspaceLayout
		// workspaceClasses is whether the listing carries the workspace's own
		// classes, and populated whether the configured directory's entity is in
		// them.
		workspaceClasses bool
		populated        bool
		// pruneRefusal is a fragment of the refusal a prune draws with no lockfile
		// to keep entries by.
		pruneRefusal string
	}{
		{
			name: "with a real configuration file", layout: configurationInPlace,
			workspaceClasses: true, populated: true,
			pruneRefusal: "needs a pulled workspace",
		},
		{
			name: "where no workspace resolves", layout: noWorkspaceResolves,
			pruneRefusal: "needs a workspace",
		},
	} {
		for _, row := range cacheCommandRows() {
			t.Run(tc.name+"/"+row.name, func(t *testing.T) {
				layout := tc.layout(t)
				out, errOut, err := runAsMachine(t, machinedirs.Host(), layout.env, layout.root, row.args...)
				if errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
					t.Fatalf("aggrega %s stopped on a link it was never shown: %v", strings.Join(row.args, " "), err)
				}
				switch row.name {
				case "status":
					if err != nil {
						t.Fatalf("cache status failed: %v\nstderr:\n%s", err, errOut)
					}
					listed, populated := classListsElements(out, "workspace entity cache")
					if listed != tc.workspaceClasses || populated != tc.populated {
						t.Errorf("the workspace entity cache is listed=%v populated=%v, want listed=%v populated=%v:\n%s",
							listed, populated, tc.workspaceClasses, tc.populated, out)
					}
					if listed, _ := classListsElements(out, "compiled-module cache"); !listed {
						t.Errorf("the listing does not carry the machine-wide compiled-module cache:\n%s", out)
					}
				case "prune":
					if err == nil || !strings.Contains(err.Error(), tc.pruneRefusal) {
						t.Errorf("cache prune returned %v, want the refusal containing %q", err, tc.pruneRefusal)
					}
				case "evict":
					if err != nil {
						t.Fatalf("cache evict failed: %v\nstderr:\n%s", err, errOut)
					}
					if !strings.Contains(out, "nothing evicted") {
						t.Errorf("cache evict under a cap the cache fits did not report a no-op:\n%s", out)
					}
				case "clear":
					if err == nil || !strings.Contains(err.Error(), "non-interactive stdin without --yes") {
						t.Errorf("cache clear without --yes returned %v, want the confirmation's refusal", err)
					}
				}
			})
		}
	}
}
