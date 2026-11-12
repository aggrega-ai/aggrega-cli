// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci/ocitest"
)

// registryReferencePublisher lays every entity the Release reaches into a
// publisher tree laid out by kind and version, ready to push.
func registryReferencePublisher(t *testing.T) (root string, entities []string) {
	t.Helper()
	root = t.TempDir()
	for key, body := range registryOnlyEntities {
		kind, identity, _ := strings.Cut(key, " ")
		rel := filepath.Join(strings.ToLower(kind), filepath.FromSlash(identity), registryReferenceVersion, strings.ToLower(kind)+".yaml")
		writeFixture(t, filepath.Join(root, rel), body)
		entities = append(entities, rel)
	}
	for rel, body := range registryReferenceRealizations {
		writeFixture(t, filepath.Join(root, filepath.FromSlash(rel)), body)
		entities = append(entities, filepath.FromSlash(rel))
	}
	return root, entities
}

// runFromTheCommandLine runs one command through the real command tree from ws,
// with the process stderr captured, and returns everything it printed and its
// error; a nil error is the exit status 0 the command returns.
func runFromTheCommandLine(t *testing.T, ws string, args ...string) (string, error) {
	t.Helper()
	var out string
	var err error
	stderr := captureStderr(t, func() { out, err = runAggrega(t, append([]string{"--workspace-root", ws}, args...)...) })
	return out + stderr, err
}

// TestIntegration_AReferenceWrittenWithoutItsVersionPullsAndBuildsFromARegistry
// publishes every Component, Feature, Recipe and FeatureRecipe a Release reaches
// to an in-process OCI registry and builds the Release in a workspace that reads
// no local catalog, through the real command tree: `aggrega pull`, then `aggrega
// build` (`--offline` on the row that asks for it), then `aggrega pull --verify`,
// then a second `aggrega pull`, each exiting 0, for each way a row writes its
// references. A Feature attached on a connection and written without its version
// is one row; a Component reference written without its version is another.
//
// After the cold pull each reference written without its version is pinned under
// that form at the version the registry served, its own render of the Release
// finished, and the entity is cached where a read of its pin opens — so the build,
// which fetches nothing, reads it there, and the second pull finds it there and
// rewrites nothing.
func TestIntegration_AReferenceWrittenWithoutItsVersionPullsAndBuildsFromARegistry(t *testing.T) {
	// Not parallel: every command runs from the workspace it builds, which moves the
	// process working directory, and the commands' notices are read from the process
	// stderr.
	host := startRegistry(t, nil)
	pub, entities := registryReferencePublisher(t)
	publisher := resolveTempWorkspace(t, tempWorkspaceOptions{Root: pub})
	for _, rel := range entities {
		if err := pushRun(t.Context(), pushConfig{Workspace: publisher, EntityPath: filepath.Join(pub, rel), Registry: host}); err != nil {
			t.Fatalf("push %s: %v", rel, err)
		}
	}

	for _, row := range registryReferenceRows {
		t.Run(row.name, func(t *testing.T) {
			ws := t.TempDir()
			releasePath := registryReferenceRelease(t, ws, row.forms)
			writeWorkspaceConfig(t, ws, "workspace:\n  catalog_dirs: []\ndefault_registry: \""+host+"\"\noffline: false\nauto_pull: false")
			lockPath := lockfile.PathFor(releasePath)
			t.Chdir(ws)

			text, err := runFromTheCommandLine(t, ws, "pull", "--release", releasePath)
			if err != nil {
				t.Fatalf("aggrega pull exited non-zero: %v\n%s", err, text)
			}
			for _, marker := range renderStoppedMarkers {
				if strings.Contains(text, marker) {
					t.Errorf("the pull's own render of the Release did not finish (%q):\n%s", marker, text)
				}
			}
			assertPinnedAtTheResolvedVersion(t, lockPath, row.forms.writtenBare())
			for _, uri := range row.forms.writtenBare() {
				slot := ocitest.MustCachePath(ws, ".aggrega/cache", writtenBareKind(uri), uri+":"+registryReferenceVersion, registryReferenceVersion)
				if _, statErr := os.Stat(filepath.Join(slot, "blob")); statErr != nil {
					t.Errorf("%s is not cached where a read of its pin opens: %v", uri, statErr)
				}
			}
			first := mustReadFile(t, lockPath)

			buildArgs := []string{"build", "--release", releasePath}
			if row.offlineBuild {
				buildArgs = append(buildArgs, "--offline")
			}
			if text, err := runFromTheCommandLine(t, ws, buildArgs...); err != nil {
				t.Fatalf("aggrega %s exited non-zero over the lockfile its pull wrote: %v\n%s", strings.Join(buildArgs, " "), err, text)
			}
			rendered := readDistTree(t, filepath.Join(ws, "dist"))
			for _, image := range []string{"caller:1", "peer:1"} {
				if !strings.Contains(rendered, image) {
					t.Errorf("the build did not render %q; got:\n%s", image, rendered)
				}
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
}
