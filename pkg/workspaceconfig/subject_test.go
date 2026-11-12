// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// noWorkspaceFolder returns a fresh directory that anchors no workspace, and
// proves it does not. Every ancestor is walked during resolution, so a marker
// anywhere above the system temporary directory would make every row below pass
// or fail for a reason that has nothing to do with the rule under test.
func noWorkspaceFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	if rw.Marker() != workspaceconfig.MarkerCWD {
		t.Fatalf("a workspace resolves from %s, anchored by %v at %s, so it cannot stand for a folder holding none",
			dir, rw.Marker(), rw.Root())
	}
	return dir
}

// TestForSubjectResolvesFromTheSubjectsFolderOnlyWhenNothingAnchored drives the
// one rule deciding whether a run about a named file uses the workspace that
// file's folder resolves, across every condition the rule reads.
//
// The first two rows are the case: nothing named a root or a start, the folder
// the process runs in anchored nothing, and the subject is absolute and sits in
// a folder that is there — the workspace is then the subject's, whether a
// workspace contains it or its own folder is the fallback. Every other row
// breaks exactly one of those conditions, and the resolution comes back
// unchanged: the same value, not an equal one.
//
// Not parallel: a resolution given no start begins at the process working
// directory, which is the condition under test, so each row moves it.
func TestForSubjectResolvesFromTheSubjectsFolderOnlyWhenNothingAnchored(t *testing.T) {
	ws := t.TempDir()
	write(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace:\n  catalog_dirs:\n    - catalog\n")
	release := filepath.Join(ws, "releases", "release.yaml")
	write(t, release, "kind: Release\n")

	// The loose Release's name begins with none of the heuristic root markers'
	// prefixes (aggregate, release, manifest): such a file would anchor its own
	// folder, and the row this Release serves is about a folder that anchors
	// nothing, so the second pass falls back to that folder.
	loose := noWorkspaceFolder(t)
	looseRelease := filepath.Join(loose, "production.yaml")
	write(t, looseRelease, "kind: Release\n")

	other := t.TempDir()
	write(t, filepath.Join(other, workspaceconfig.ConfigFilename), "workspace: {}\n")

	empty := noWorkspaceFolder(t)
	named := noWorkspaceFolder(t)
	relative, err := filepath.Rel(empty, release)
	if err != nil {
		t.Fatalf("spell %s relative to %s: %v", release, empty, err)
	}

	lookup := func(string) string { return "" }
	for _, tc := range []struct {
		name string
		// runIn is the process working directory; start and flagRoot are what the
		// resolution was given.
		runIn, start, flagRoot string
		subject                string
		// moved says whether the subject's workspace is the answer; wantRoot is the
		// root of the answer either way.
		moved      bool
		wantRoot   string
		wantMarker workspaceconfig.Marker
	}{
		{
			name:  "nothing named and nothing anchored, an absolute Release inside a workspace",
			runIn: empty, subject: release,
			moved: true, wantRoot: ws, wantMarker: workspaceconfig.MarkerConfig,
		},
		{
			name:  "nothing named and nothing anchored, an absolute Release no workspace contains",
			runIn: empty, subject: looseRelease,
			moved: true, wantRoot: loose, wantMarker: workspaceconfig.MarkerCWD,
		},
		{
			name:  "the folder it runs in is a workspace",
			runIn: other, subject: release,
			wantRoot: other, wantMarker: workspaceconfig.MarkerConfig,
		},
		{
			name:  "a root is named",
			runIn: empty, flagRoot: named, subject: release,
			wantRoot: named, wantMarker: workspaceconfig.MarkerNone,
		},
		{
			name:  "where resolution starts is named",
			runIn: empty, start: named, subject: release,
			wantRoot: named, wantMarker: workspaceconfig.MarkerCWD,
		},
		{
			name:  "a relative subject",
			runIn: empty, subject: relative,
			wantRoot: empty, wantMarker: workspaceconfig.MarkerCWD,
		},
		{
			name:     "no subject",
			runIn:    empty,
			wantRoot: empty, wantMarker: workspaceconfig.MarkerCWD,
		},
		{
			name:  "an absolute subject in a folder that is not there",
			runIn: empty, subject: filepath.Join(loose, "missing", "release.yaml"),
			wantRoot: empty, wantMarker: workspaceconfig.MarkerCWD,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(tc.runIn)
			rw, err := workspaceconfig.ResolveOn(machinedirs.Host(), tc.start, tc.flagRoot, lookup)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			got, err := rw.ForSubject(tc.subject)
			if err != nil {
				t.Fatalf("ForSubject(%q): %v", tc.subject, err)
			}
			if moved := got != rw; moved != tc.moved {
				t.Errorf("ForSubject(%q) moved the workspace: %v, want %v (root %s)", tc.subject, moved, tc.moved, got.Root())
			}
			if got.Root() != tc.wantRoot {
				t.Errorf("root = %s, want %s", got.Root(), tc.wantRoot)
			}
			if got.Marker() != tc.wantMarker {
				t.Errorf("marker = %v, want %v", got.Marker(), tc.wantMarker)
			}
		})
	}
}

// TestForSubjectResolvesTheSubjectsFolderWithTheResolutionsOwnInputs pins that
// the resolution taken from the subject's folder reads what the one it replaces
// read: the global file the environment names, or none where the resolution was
// made without the global layer, and the command-line layer laid over the result.
//
// The global file turns the offline posture on, which makes each input visible
// in one setting: a second resolution that dropped the environment would read it
// off, one that read the global layer the first had left out would read it on,
// and one that dropped the command-line layer would read the flag's value lost.
func TestForSubjectResolvesTheSubjectsFolderWithTheResolutionsOwnInputs(t *testing.T) {
	ws := t.TempDir()
	write(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace: {}\n")
	release := filepath.Join(ws, "release.yaml")
	write(t, release, "kind: Release\n")
	global := filepath.Join(t.TempDir(), "config.yaml")
	write(t, global, workspaceconfig.OfflineSettingKey+": true\n")
	lookup := func(name string) string {
		if name == workspaceconfig.EnvConfigOverride {
			return global
		}
		return ""
	}
	off := false

	for _, tc := range []struct {
		name    string
		resolve func() (*workspaceconfig.ResolvedWorkspace, error)
		offline bool
	}{
		{
			name: "the global file the environment names is read",
			resolve: func() (*workspaceconfig.ResolvedWorkspace, error) {
				return workspaceconfig.ResolveOn(machinedirs.Host(), "", "", lookup)
			},
			offline: true,
		},
		{
			name: "a resolution made without the global layer is taken again without it",
			resolve: func() (*workspaceconfig.ResolvedWorkspace, error) {
				return workspaceconfig.ResolveWithoutGlobalOn(machinedirs.Host(), "", "", lookup)
			},
			offline: false,
		},
		{
			name: "the command-line layer is laid over the result",
			resolve: func() (*workspaceconfig.ResolvedWorkspace, error) {
				rw, err := workspaceconfig.ResolveOn(machinedirs.Host(), "", "", lookup)
				if err != nil {
					return nil, err
				}
				return rw.WithFlags(workspaceconfig.FlagOverlay{Offline: &off}), nil
			},
			offline: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(noWorkspaceFolder(t))
			rw, err := tc.resolve()
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			got, err := rw.ForSubject(release)
			if err != nil {
				t.Fatalf("ForSubject: %v", err)
			}
			if got.Root() != ws {
				t.Fatalf("root = %s, want the subject's workspace %s", got.Root(), ws)
			}
			if got.Config().Offline != tc.offline {
				t.Errorf("offline = %v, want %v", got.Config().Offline, tc.offline)
			}
		})
	}
}
