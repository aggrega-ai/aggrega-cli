// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// releaseCatalogFixture is the shared fixture whose two Releases build against
// different catalog directories, relative to this package's directory.
const releaseCatalogFixture = "../../internal/compiler/testdata/release-catalog-dirs"

// releaseCatalogWorkspace copies the fixture at src into a fresh workspace and
// returns its root. The copy is what every command reads and writes, so the
// tracked fixture is never driven in place. src is absolute, because the
// commands below run from inside the workspaces they build.
//
// A non-empty config replaces the fixture's own workspace configuration. That is
// how a case builds the same files under a configuration that lists one
// directory for every Release.
func releaseCatalogWorkspace(t *testing.T, src, config string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	if config != "" {
		writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), config)
	}
	return root
}

// buildFromTheCommandLine runs `aggrega pull` and then `aggrega build` for
// release from the root of the workspace at root, the way a person at a terminal
// does — no --catalog flag — and returns every file the build wrote.
//
// The pull is there because a build needs a lockfile. It reads the Release's
// catalog directories through its own call, so what the returned files say about
// which directories were read is what the build itself read. The build is not
// asked to fill the lockfile for the same reason: a build that fills presents
// the render the fill performed rather than performing its own.
func buildFromTheCommandLine(t *testing.T, root, release string) map[string]string {
	t.Helper()
	t.Chdir(root)
	for _, args := range [][]string{
		{"pull", "--release", release},
		{"build", "--release", release},
	} {
		var out string
		var err error
		stderr := captureStderr(t, func() { out, err = runAggrega(t, args...) })
		if err != nil {
			t.Fatalf("aggrega %s in %s failed: %v\ncommand output:\n%s\nstderr:\n%s",
				strings.Join(args, " "), root, err, out, stderr)
		}
	}
	return renderedTree(t, filepath.Join(root, artifactout.DefaultOutputDir))
}

// TestBuildCmdReadsTheCatalogDirectoriesTheConfigurationGivesTheRelease pins
// that `aggrega build` builds a Release from the catalog directories the
// workspace configuration gives that Release, and a Release it gives none from
// the workspace's own list, with no flag typed.
//
// Each row builds one Release twice: in the fixture workspace, whose
// configuration gives release-own.yaml a directory of its own, and in a
// reference workspace holding the same files under a configuration that gives no
// Release anything and lists, for all of them, the one directory this Release
// has to be built from. The two rendered trees must be equal, file for file and
// byte for byte.
//
// The fixture's two directories declare no Component in common, so a build of
// release-own.yaml that read the workspace list in place of its own entry cannot
// resolve the Release at all: the command fails, and the first row fails on that
// failure. The second row is the control — a Release no entry names builds from
// the workspace list — so a failure of the first row is about the entry rather
// than about the fixture.
func TestBuildCmdReadsTheCatalogDirectoriesTheConfigurationGivesTheRelease(t *testing.T) {
	// Not parallel: every command runs from the workspace it builds, which moves
	// the process working directory, and its diagnostics are read from the process
	// stderr.
	src, err := filepath.Abs(releaseCatalogFixture)
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	for _, tc := range []struct {
		name    string
		release string
		// reference is the one directory the reference workspace lists for every
		// Release: the directory this Release has to have been built from.
		reference string
	}{
		{
			name:      "a Release the configuration names builds from its own directories",
			release:   "release-own.yaml",
			reference: "catalog-own",
		},
		{
			name:      "a Release the configuration does not name builds from the workspace list",
			release:   "release-shared.yaml",
			reference: "catalog-shared",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Both workspaces are laid down before either build moves the working
			// directory into one of them.
			mapped := releaseCatalogWorkspace(t, src, "")
			reference := releaseCatalogWorkspace(t, src, "workspace:\n  catalog_dirs:\n    - "+tc.reference+"\n")
			got := buildFromTheCommandLine(t, mapped, tc.release)
			want := buildFromTheCommandLine(t, reference, tc.release)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s built differently from the same Release in a workspace listing only %s\n got: %v\nwant: %v",
					tc.release, tc.reference, got, want)
			}
		})
	}
}
