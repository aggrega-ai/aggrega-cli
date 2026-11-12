// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// releaseCatalogWorkspace copies the shared fixture whose two Releases build
// against different catalog directories into a fresh workspace and returns its
// root. The copy is what every call reads, so the tracked fixture is never
// driven in place.
//
// A non-empty config replaces the fixture's own workspace configuration. That is
// how a case builds the same files under a configuration that lists one
// directory for every Release.
func releaseCatalogWorkspace(t *testing.T, config string) string {
	t.Helper()
	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "release-catalog-dirs")
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
			t.Fatalf("write the workspace configuration: %v", err)
		}
	}
	return root
}

// buildToolFiles calls the build tool for release in the workspace at root the
// way an assistant calls it — the Release and the workspace named, no catalog
// directories — and returns every rendered file with its content.
func buildToolFiles(t *testing.T, root, release string) map[string]string {
	t.Helper()
	res := callTool(t, newTestServer(t), "build", buildArgs{Release: release, WorkspaceRoot: root})
	if res.IsError {
		t.Fatalf("the build of %s in %s was refused: %s", release, root, res.Content[0].Text)
	}
	files := map[string]string{}
	for _, f := range unmarshalBuildPayload(t, res).Files {
		files[f.Path] = f.Content
	}
	if len(files) == 0 {
		t.Fatalf("the build of %s in %s rendered no file, so an equality with it would prove nothing", release, root)
	}
	return files
}

// TestBuildToolReadsTheCatalogDirectoriesTheConfigurationGivesTheRelease pins
// that the build tool renders a Release from the catalog directories the
// workspace configuration gives that Release, and a Release it gives none from
// the workspace's own list.
//
// Each row renders one Release twice: in the fixture workspace, whose
// configuration gives release-own.yaml a directory of its own, and in a
// reference workspace holding the same files under a configuration that gives no
// Release anything and lists, for all of them, the one directory this Release
// has to be rendered from. The two answers must be equal, file for file and byte
// for byte.
//
// The fixture's two directories declare no Component in common, so a build of
// release-own.yaml that read the workspace list in place of its own entry cannot
// resolve the Release at all: the call is refused, and the first row fails on
// that refusal. The second row is the control — a Release no entry names renders
// from the workspace list — so a failure of the first row is about the entry
// rather than about the fixture.
func TestBuildToolReadsTheCatalogDirectoriesTheConfigurationGivesTheRelease(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		release string
		// reference is the one directory the reference workspace lists for every
		// Release: the directory this Release has to have been rendered from.
		reference string
	}{
		{
			name:      "a Release the configuration names renders from its own directories",
			release:   "release-own.yaml",
			reference: "catalog-own",
		},
		{
			name:      "a Release the configuration does not name renders from the workspace list",
			release:   "release-shared.yaml",
			reference: "catalog-shared",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildToolFiles(t, releaseCatalogWorkspace(t, ""), tc.release)
			want := buildToolFiles(t, releaseCatalogWorkspace(t, "workspace:\n  catalog_dirs:\n    - "+tc.reference+"\n"), tc.release)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s rendered differently from the same Release in a workspace listing only %s\n got: %v\nwant: %v",
					tc.release, tc.reference, got, want)
			}
		})
	}
}
