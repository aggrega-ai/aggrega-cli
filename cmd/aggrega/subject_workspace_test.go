// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// folderNoWorkspaceResolvesFrom returns a fresh directory that anchors no
// workspace, and proves it does not: every ancestor is walked during
// resolution, so a marker anywhere above the system temporary directory would
// make the folder resolve and leave every row below passing or failing for a
// reason unrelated to the rule under test.
func folderNoWorkspaceResolvesFrom(t *testing.T) string {
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

// subjectFixture lays down a copy of the workspace whose configuration gives
// release-own.yaml a catalog directory of its own, and returns its root and that
// Release's absolute path. That directory is the only place the Release's
// Component is declared, so a pull of it completes only in that workspace.
//
// It is called before any row moves the working directory, because the tracked
// fixture is located relative to this package's directory.
func subjectFixture(t *testing.T) (root, release string) {
	t.Helper()
	src, err := filepath.Abs(releaseCatalogFixture)
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	root = releaseCatalogWorkspace(t, src, "")
	return root, filepath.Join(root, "release-own.yaml")
}

// TestAPullNamingAnAbsoluteReleaseFromAFolderNoWorkspaceResolvesUsesTheReleasesWorkspace
// runs `aggrega pull` through the real command tree from a folder that anchors
// no workspace, with nothing naming a root, over a Release named by its absolute
// path inside a workspace.
//
// The pull completes only where it ran in the Release's own workspace, whose
// configuration is what gives the Release its catalog directory; answered from
// the folder it was run in, the closure finds nothing for the Component and the
// pull halts. It writes the lockfile beside the Release and nothing in the
// folder it ran from, and the resolution that command reads — through the same
// resolver, for the same subject — places the workspace and its cache at the
// Release's workspace root.
//
// Not parallel: the command resolves from the process working directory, which
// is the condition under test, and its diagnostics are read from the process's
// standard error.
func TestAPullNamingAnAbsoluteReleaseFromAFolderNoWorkspaceResolvesUsesTheReleasesWorkspace(t *testing.T) {
	root, release := subjectFixture(t)
	startedIn := folderNoWorkspaceResolvesFrom(t)
	t.Chdir(startedIn)

	var out string
	var err error
	stderr := captureStderr(t, func() { out, err = runAggrega(t, "pull", "--release", release) })
	if err != nil {
		t.Fatalf("aggrega pull --release %s from %s failed: %v\noutput:\n%s\nstderr:\n%s", release, startedIn, err, out, stderr)
	}
	readLockfile(t, release)

	entries, err := os.ReadDir(startedIn)
	if err != nil {
		t.Fatalf("read %s: %v", startedIn, err)
	}
	for _, e := range entries {
		t.Errorf("the pull created %s in the folder it was run from", e.Name())
	}

	ws, err := workspaceForSubject(withWorkspaceResolver(context.Background(), "", nil), release)
	if err != nil {
		t.Fatalf("resolve the workspace for %s: %v", release, err)
	}
	if ws.Root() != root {
		t.Errorf("the workspace for %s is %s, want the Release's own %s", release, ws.Root(), root)
	}
	cache := ws.Config().ResolvedArtifactCacheDir()
	if !filepath.IsAbs(cache) {
		cache = filepath.Join(ws.Root(), cache)
	}
	if !strings.HasPrefix(cache, root+string(filepath.Separator)) {
		t.Errorf("the workspace cache is %s, want it under the Release's workspace %s", cache, root)
	}
}

// TestAPullNamingAnAbsoluteReleaseResolvesAsBeforeWhereAWorkspaceWasNamedOrFound
// is the negative half on the command line: every way a workspace is settled
// before the Release is read keeps that workspace, so the same pull is refused as
// it is refused today. The workspace each row settles on gives the Release no
// catalog directory, so no local directory holds the Recipe the Release's
// Manifest maps its Component to; the reference is then looked for in the
// registry, and binding it to a registry location is refused because it names no
// provider, before the registry is asked for it.
//
// That is the refusal with the offline posture off, which every row pins: with
// the posture on there is no registry to look in, and the same reference is
// refused as one nothing resolves instead, so leaving the posture to the ambient
// environment would let it choose which refusal a row sees.
//
// Each row breaks one condition of the rule, so a rule that stopped reading that
// condition would run the row in the Release's workspace and the pull would
// complete.
//
// The relative-path row runs in the folder it was started in, which anchors
// nothing and was named by nobody, so its pull is refused before anything is
// fetched rather than at the registry: no artifact cache is written in a folder
// that is not a workspace. That refusal still says the row was not answered in
// the Release's workspace, where the pull would have completed.
//
// Not parallel, for the reasons the positive test gives; every row also sets the
// process environment.
func TestAPullNamingAnAbsoluteReleaseResolvesAsBeforeWhereAWorkspaceWasNamedOrFound(t *testing.T) {
	for _, tc := range []struct {
		name string
		// setup moves the process into the folder the row runs from, sets whatever
		// the row names, and returns the arguments the pull runs with.
		setup func(t *testing.T, release string) []string
		// cacheRefused is set on the row whose resolution found no workspace and
		// was handed none, whose pull is refused for the cache it would write.
		cacheRefused bool
	}{
		{
			name: "the folder it runs in is a workspace",
			setup: func(t *testing.T, release string) []string {
				other := t.TempDir()
				writeFixture(t, filepath.Join(other, workspaceconfig.ConfigFilename), "workspace: {}\n")
				t.Chdir(other)
				return []string{"pull", "--release", release}
			},
		},
		{
			name: "--workspace-root names a root",
			setup: func(t *testing.T, release string) []string {
				named := folderNoWorkspaceResolvesFrom(t)
				t.Chdir(folderNoWorkspaceResolvesFrom(t))
				return []string{"--workspace-root", named, "pull", "--release", release}
			},
		},
		{
			name: "AGGREGA_WORKSPACE_ROOT names a root",
			setup: func(t *testing.T, release string) []string {
				t.Setenv(workspaceconfig.EnvWorkspaceRoot, folderNoWorkspaceResolvesFrom(t))
				t.Chdir(folderNoWorkspaceResolvesFrom(t))
				return []string{"pull", "--release", release}
			},
		},
		{
			name: "the Release is named by a relative path",
			setup: func(t *testing.T, release string) []string {
				startedIn := folderNoWorkspaceResolvesFrom(t)
				relative, err := filepath.Rel(startedIn, release)
				if err != nil {
					t.Fatalf("spell %s relative to %s: %v", release, startedIn, err)
				}
				t.Chdir(startedIn)
				return []string{"pull", "--release", relative}
			},
			cacheRefused: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGGREGA_OFFLINE", "")
			_, release := subjectFixture(t)
			args := tc.setup(t, release)
			var err error
			stderr := captureStderr(t, func() { _, err = runAggrega(t, args...) })
			if err == nil {
				t.Fatalf("aggrega %s completed, so it ran in the Release's own workspace rather than the one this "+
					"row settles on\nstderr:\n%s", strings.Join(args, " "), stderr)
			}
			if tc.cacheRefused {
				var refused *workspaceconfig.CacheWriteRefusedError
				if !errors.As(err, &refused) {
					t.Fatalf("aggrega %s refused, but not for the cache it would write in a folder that is no "+
						"workspace: %v", strings.Join(args, " "), err)
				}
				assertNoLockfile(t, release, "the refused pull")
				return
			}
			if !errors.Is(err, catalog.ErrUnscopedRecipeReference()) {
				t.Fatalf("aggrega %s refused, but not as a Recipe reference no local catalog directory holds is "+
					"refused at the registry: %v", strings.Join(args, " "), err)
			}
			assertNoLockfile(t, release, "the refused pull")
		})
	}
}

// TestTheAdvisoriesShownAreThoseOfTheWorkspaceTheCommandUses pins that the
// resolution's advisories are written once, for the workspace a command runs
// in, when the folder it was run from is set aside for the Release's workspace.
//
// Both folders carry a CI-owned overlay setting a key such an overlay may not
// set, which each resolution reports as a warning naming that overlay. The
// Release's overlay is named exactly once; the overlay of the folder the command
// was run from, whose resolution the command does not use, is never named.
//
// Not parallel, for the reasons the positive test above gives.
func TestTheAdvisoriesShownAreThoseOfTheWorkspaceTheCommandUses(t *testing.T) {
	root, release := subjectFixture(t)
	subjectOverlay := filepath.Join(root, workspaceconfig.AppendCIConfigFilename)
	writeFixture(t, subjectOverlay, "default_registry: subject.example\n")
	startedIn := folderNoWorkspaceResolvesFrom(t)
	startOverlay := filepath.Join(startedIn, workspaceconfig.AppendCIConfigFilename)
	writeFixture(t, startOverlay, "default_registry: start.example\n")
	t.Chdir(startedIn)

	var out string
	var err error
	stderr := captureStderr(t, func() { out, err = runAggrega(t, "pull", "--release", release) })
	if err != nil {
		t.Fatalf("aggrega pull --release %s failed: %v\noutput:\n%s\nstderr:\n%s", release, err, out, stderr)
	}
	if n := strings.Count(stderr, subjectOverlay); n != 1 {
		t.Errorf("the Release's workspace overlay %s is named %d times, want once:\n%s", subjectOverlay, n, stderr)
	}
	if strings.Contains(stderr, startOverlay) {
		t.Errorf("the overlay of the folder the command was run from, %s, is named although that resolution "+
			"was set aside:\n%s", startOverlay, stderr)
	}
}

// TestNamingTheSubjectAfterTheWorkspaceWasReadIsRefused pins the wiring guard:
// a command that names the file it is about after something has already read the
// workspace is refused, since part of its run would have used a workspace other
// than the subject's. Naming the subject first, or naming the same one again, is
// answered with the one workspace the invocation resolves.
//
// Not parallel: the resolver resolves from the process working directory.
func TestNamingTheSubjectAfterTheWorkspaceWasReadIsRefused(t *testing.T) {
	_, release := subjectFixture(t)
	t.Chdir(folderNoWorkspaceResolvesFrom(t))

	late := withWorkspaceResolver(context.Background(), "", nil)
	if _, err := workspaceFromContext(late); err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	if _, err := workspaceForSubject(late, release); err == nil {
		t.Error("a subject named after the workspace was read was accepted")
	}

	first := withWorkspaceResolver(context.Background(), "", nil)
	named, err := workspaceForSubject(first, release)
	if err != nil {
		t.Fatalf("name the subject: %v", err)
	}
	again, err := workspaceFromContext(first)
	if err != nil {
		t.Fatalf("read the workspace again: %v", err)
	}
	if again != named {
		t.Error("a later read of the same invocation returned a different workspace from the one its subject resolved")
	}
	if _, err := workspaceForSubject(first, release); err != nil {
		t.Errorf("naming the same subject again was refused: %v", err)
	}
}
