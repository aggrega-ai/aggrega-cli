// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// unlaunchedServer is a server built the way a launch naming no workspace builds
// one: no root, so a call naming none resolves from the process working
// directory. root, where given, is the root the launch named instead.
func unlaunchedServer(root string) *server {
	return &server{out: io.Discard, logw: io.Discard, serverVersion: "test", launchRoot: root, transport: transportStdio}
}

// assertNothingCreatedIn fails where dir holds anything: the folder a server was
// started in is not where a call about a Release elsewhere writes.
func assertNothingCreatedIn(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		t.Errorf("%s was created in the folder the server was started in", e.Name())
	}
}

// TestAToolNamingAnAbsoluteReleaseFromAFolderNoWorkspaceResolvesUsesTheReleasesWorkspace
// drives the server's own tools, launched with no root in a folder that anchors
// no workspace, over a Release named by its absolute path inside a workspace whose
// configuration gives that Release a catalog directory of its own.
//
// That directory is the only place the Release's Component is declared, so each
// call can only succeed where the workspace it ran in is the Release's: answered
// from the folder the server was started in, the closure finds nothing for the
// Component and the call is refused.
//
// Not parallel: a server launched with no root resolves from the process working
// directory, which is the condition under test, so each row moves it.
func TestAToolNamingAnAbsoluteReleaseFromAFolderNoWorkspaceResolvesUsesTheReleasesWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(t *testing.T, s *server, release string)
	}{
		{
			name: "pull writes the lockfile beside the Release",
			call: func(t *testing.T, s *server, release string) {
				res := callTool(t, s, "pull", pullArgs{Release: release})
				if res.IsError {
					t.Fatalf("pull was refused: %s", res.Content[0].Text)
				}
				if _, err := os.Stat(lockfile.PathFor(release)); err != nil {
					t.Errorf("no lockfile beside the Release after the pull: %v", err)
				}
			},
		},
		{
			name: "build renders the Release",
			call: func(t *testing.T, s *server, release string) {
				res := callTool(t, s, "build", buildArgs{Release: release})
				if res.IsError {
					t.Fatalf("build was refused: %s", res.Content[0].Text)
				}
				if files := unmarshalBuildPayload(t, res).Files; len(files) == 0 {
					t.Error("the build rendered no file")
				}
			},
		},
		{
			name: "validate resolves the Release's closure",
			call: func(t *testing.T, s *server, release string) {
				res := callTool(t, s, "validate", validateArgs{Release: release})
				if res.IsError {
					t.Fatalf("validate was refused: %s", res.Content[0].Text)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := filepath.Join(releaseCatalogWorkspace(t, ""), "release-own.yaml")
			startedIn := aDirectoryNoWorkspaceResolvesFrom(t)
			t.Chdir(startedIn)
			tc.call(t, unlaunchedServer(""), release)
			assertNothingCreatedIn(t, startedIn)
		})
	}
}

// TestAToolNamingAnAbsoluteReleaseResolvesAsBeforeWhereAWorkspaceWasNamedOrFound
// is the negative half: every way a workspace is settled before the Release is
// read keeps that workspace, so the same pull over the same absolute Release is
// refused as it is refused today — the workspace each row settles on gives the
// Release no catalog directory.
//
// Each row breaks one condition of the rule, so a rule that stopped reading that
// condition would answer the row from the Release's workspace, and the pull would
// complete.
func TestAToolNamingAnAbsoluteReleaseResolvesAsBeforeWhereAWorkspaceWasNamedOrFound(t *testing.T) {
	for _, tc := range []struct {
		name string
		// setup returns the folder the server runs in, the root its launch named,
		// and the arguments the call sends for the Release at release.
		setup func(t *testing.T, release string) (startedIn, launchRoot string, args pullArgs)
	}{
		{
			name: "the folder the server runs in is a workspace",
			setup: func(t *testing.T, release string) (string, string, pullArgs) {
				other := t.TempDir()
				if err := os.WriteFile(filepath.Join(other, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
					t.Fatalf("write the workspace configuration: %v", err)
				}
				return other, "", pullArgs{Release: release}
			},
		},
		{
			name: "the call names its workspace",
			setup: func(t *testing.T, release string) (string, string, pullArgs) {
				return aDirectoryNoWorkspaceResolvesFrom(t), "",
					pullArgs{Release: release, WorkspaceRoot: aDirectoryNoWorkspaceResolvesFrom(t)}
			},
		},
		{
			name: "the server was launched with a root",
			setup: func(t *testing.T, release string) (string, string, pullArgs) {
				return aDirectoryNoWorkspaceResolvesFrom(t), aDirectoryNoWorkspaceResolvesFrom(t), pullArgs{Release: release}
			},
		},
		{
			name: "the Release is named by a relative path",
			setup: func(t *testing.T, release string) (string, string, pullArgs) {
				startedIn := aDirectoryNoWorkspaceResolvesFrom(t)
				relative, err := filepath.Rel(startedIn, release)
				if err != nil {
					t.Fatalf("spell %s relative to %s: %v", release, startedIn, err)
				}
				return startedIn, "", pullArgs{Release: relative}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := filepath.Join(releaseCatalogWorkspace(t, ""), "release-own.yaml")
			startedIn, launchRoot, args := tc.setup(t, release)
			t.Chdir(startedIn)
			res := callTool(t, unlaunchedServer(launchRoot), "pull", args)
			if !res.IsError {
				t.Errorf("the pull completed, so it was answered from the Release's own workspace rather than the one "+
					"this row settles on: %s", res.Content[0].Text)
			}
			if _, err := os.Stat(lockfile.PathFor(release)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a lockfile was written beside the Release (stat: %v)", err)
			}
		})
	}
}
