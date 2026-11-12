// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// releaseCatalogWorkspace copies the shared fixture whose two Releases build
// against different catalog directories into a fresh workspace and returns its
// root. The copy is what every request reads and writes, so the tracked fixture
// is never driven in place.
//
// A non-empty config replaces the fixture's own workspace configuration. That is
// how a case builds the same files under a configuration that lists one
// directory for every Release.
//
// Either way the configuration the copy ends with names a registry the test
// owns, appended to the copy and never to the tracked fixture, so a resolution
// over it asks that registry for whatever no local tier holds.
func releaseCatalogWorkspace(t *testing.T, config string) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "release-catalog-dirs"))
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	configPath := filepath.Join(root, workspaceconfig.ConfigFilename)
	if config == "" {
		own, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read the fixture's own workspace configuration: %v", err)
		}
		config = string(own)
	}
	if n := len(config); n > 0 && config[n-1] != '\n' {
		config += "\n"
	}
	if err := os.WriteFile(configPath, []byte(config+testOwnedRegistryConfig(t)), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return root
}

// daemonBuildOutput resolves release's dependencies and then builds it, the way
// an editor does, and returns the answer's file names with the bytes of every
// file the build wrote.
//
// The workspace is opened the way the daemon opens any workspace a request
// names, through the session's own resolution, because that is the one place a
// workspace's per-Release catalog lookups are set up; a Server assembled by hand
// has none, and would answer every Release from the workspace list whatever the
// configuration says. The dependencies are resolved by their own request so the
// build then renders for itself — a build asked to fill would instead present
// the render the resolution performed, and the build's own reading of the
// Release's directories would go unexercised.
func daemonBuildOutput(t *testing.T, root, release string) ([]string, map[string]string) {
	t.Helper()
	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}

	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/resolveDependencies", resolveDependenciesParams{Release: release}))
	if resolved, _ := runOneRequestPastPushes(t, srv, &in, &out); resolved.Error != nil {
		t.Fatalf("resolving the dependencies of %s in %s was refused: %d %s",
			release, root, resolved.Error.Code, resolved.Error.Message)
	}

	built, wire := driveBuildOn(t, srv, release)
	if built.Error != nil {
		t.Fatalf("the build of %s in %s was refused: %d %s\nwire:\n%s",
			release, root, built.Error.Code, built.Error.Message, wire)
	}
	written := map[string]string{}
	for _, name := range built.Result.Files {
		content, err := os.ReadFile(filepath.Join(built.Result.OutputDirectory, name))
		if err != nil {
			t.Fatalf("the build of %s reported %s, which cannot be read: %v", release, name, err)
		}
		written[name] = string(content)
	}
	if len(written) == 0 {
		t.Fatalf("the build of %s in %s wrote no file, so an equality with it would prove nothing\nwire:\n%s",
			release, root, wire)
	}
	return built.Result.Files, written
}

// TestTheDaemonBuildReadsTheCatalogDirectoriesTheConfigurationGivesTheRelease
// pins that aggrega/build renders a Release from the catalog directories the
// workspace configuration gives that Release, and a Release it gives none from
// the workspace's own list.
//
// Each row builds one Release twice: in the fixture workspace, whose
// configuration gives release-own.yaml a directory of its own, and in a
// reference workspace holding the same files under a configuration that gives no
// Release anything and lists, for all of them, the one directory this Release
// has to be built from. The two builds must report the same files and write the
// same bytes.
//
// The fixture's two directories declare no Component in common, so a build of
// release-own.yaml answered from the workspace list in place of its own entry
// cannot resolve the Release at all: the request is refused, and the first row
// fails on that refusal. The second row is the control — a Release no entry
// names builds from the workspace list — so a failure of the first row is about
// the entry rather than about the fixture.
func TestTheDaemonBuildReadsTheCatalogDirectoriesTheConfigurationGivesTheRelease(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			gotFiles, got := daemonBuildOutput(t, releaseCatalogWorkspace(t, ""), tc.release)
			wantFiles, want := daemonBuildOutput(t,
				releaseCatalogWorkspace(t, "workspace:\n  catalog_dirs:\n    - "+tc.reference+"\n"), tc.release)
			if !reflect.DeepEqual(gotFiles, wantFiles) {
				t.Errorf("%s reported %v; the same Release in a workspace listing only %s reported %v",
					tc.release, gotFiles, tc.reference, wantFiles)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s wrote different bytes from the same Release in a workspace listing only %s\n got: %v\nwant: %v",
					tc.release, tc.reference, got, want)
			}
		})
	}
}
