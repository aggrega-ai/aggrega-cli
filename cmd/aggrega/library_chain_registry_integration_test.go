// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
)

// libraryChainPublisher lays every entity, library and realization the rows reach
// into a publisher tree laid out by kind and version, ready to push, and returns the
// tree and the paths to push in a fixed order.
func libraryChainPublisher(t *testing.T, rows ...libraryChainRow) (root string, entries []string) {
	t.Helper()
	root = t.TempDir()
	files := map[string]string{}
	for _, row := range rows {
		for rel, body := range libraryChainEntities(row) {
			files[rel] = body
		}
		for rel, body := range libraryChainRealizations(row) {
			files[rel] = body
		}
	}
	for rel, body := range files {
		writeFixture(t, filepath.Join(root, filepath.FromSlash(rel)), body)
		entries = append(entries, filepath.FromSlash(rel))
	}
	sort.Strings(entries)
	return root, entries
}

// TestIntegration_ALibraryReachedThroughAnotherResolvesFromARegistryAtAnyDepth
// publishes the Component, every Feature, every library and every realization the
// rows reach to an in-process OCI registry and runs each row's Release in a
// workspace that reads no local catalog, through the real command tree: `aggrega
// pull`, then `aggrega build`, then `aggrega pull --verify`, then a second
// `aggrega pull`, each exiting 0. The cold pull pins every library of the chain
// with the edge that reached it; the build renders the value only the chain's last
// library declares; the second pull rewrites nothing. Which source served each
// library is read where the report can be captured, in the test of the same rows
// against an in-process registry.
//
// A chain that loops back on itself is the last row: its pull exits non-zero
// naming the loop.
func TestIntegration_ALibraryReachedThroughAnotherResolvesFromARegistryAtAnyDepth(t *testing.T) {
	// Not parallel: every command runs from the workspace it builds, which moves the
	// process working directory, and the commands' notices are read from the process
	// stderr.
	host := startRegistry(t, nil)
	pub, entries := libraryChainPublisher(t, append(append([]libraryChainRow{}, libraryChainRows...), libraryChainCycleRow)...)
	publisher := resolveTempWorkspace(t, tempWorkspaceOptions{Root: pub})
	for _, rel := range entries {
		if err := pushRun(t.Context(), pushConfig{Workspace: publisher, EntityPath: filepath.Join(pub, rel), Registry: host}); err != nil {
			t.Fatalf("push %s: %v", rel, err)
		}
	}
	config := "workspace:\n  catalog_dirs: []\ndefault_registry: \"" + host + "\"\noffline: false\nauto_pull: false"

	for _, row := range libraryChainRows {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			releasePath := libraryChainRelease(t, ws, row)
			writeWorkspaceConfig(t, ws, config)
			lockPath := lockfile.PathFor(releasePath)
			t.Chdir(ws)

			text, err := runFromTheCommandLine(t, ws, "pull", "--release", releasePath)
			if err != nil {
				t.Fatalf("aggrega pull exited non-zero: %v\n%s\n%s", err, formatCompilerError(err), text)
			}
			for _, marker := range renderStoppedMarkers {
				if strings.Contains(text, marker) {
					t.Errorf("the pull's own render of the Release did not finish (%q):\n%s", marker, text)
				}
			}
			assertChainPinned(t, lockPath, row)
			first := mustReadFile(t, lockPath)

			if text, err := runFromTheCommandLine(t, ws, "build", "--release", releasePath); err != nil {
				t.Fatalf("aggrega build exited non-zero over the lockfile its pull wrote: %v\n%s\n%s", err, formatCompilerError(err), text)
			}
			if rendered := readDistTree(t, filepath.Join(ws, "dist")); !strings.Contains(rendered, "LEVEL: from-ChainC") {
				t.Errorf("the build did not render the value the chain's last library declares (LEVEL: from-ChainC); got:\n%s", rendered)
			}

			if text, err := runFromTheCommandLine(t, ws, "pull", "--release", releasePath, "--verify"); err != nil {
				t.Errorf("aggrega pull --verify exited non-zero over the lockfile the pull wrote: %v\n%s", err, text)
			}
			if text, err := runFromTheCommandLine(t, ws, "pull", "--release", releasePath); err != nil {
				t.Fatalf("second aggrega pull exited non-zero: %v\n%s", err, text)
			}
			if second := mustReadFile(t, lockPath); string(second) != string(first) {
				t.Errorf("a second pull over an unchanged workspace rewrote the lockfile\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}

	t.Run(libraryChainCycleRow.name, func(t *testing.T) {
		ws := t.TempDir()
		releasePath := libraryChainRelease(t, ws, libraryChainCycleRow)
		writeWorkspaceConfig(t, ws, config)
		t.Chdir(ws)

		text, err := runFromTheCommandLine(t, ws, "pull", "--release", releasePath)
		if err == nil {
			t.Fatalf("aggrega pull accepted a chain of libraries that loops back on itself:\n%s", text)
		}
		printed := formatCompilerError(err)
		if !errors.Is(err, refcycle.ErrCycle()) {
			t.Errorf("aggrega pull halted, but not on the loop:\n%s", printed)
		}
		if !strings.Contains(printed, libraryChainCycle) {
			t.Errorf("the halt does not name the loop %q:\n%s", libraryChainCycle, printed)
		}
	})
}
