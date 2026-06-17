// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// writeWorkspaceConfig writes a minimal `.aggrega-config.yaml` at
// the workspace root with the supplied workspace block fragment so
// integration tests can flip the auto_pull / offline dials per case.
func writeWorkspaceConfig(t *testing.T, root, workspaceBlock string) {
	t.Helper()
	content := "workspace:\n" + workspaceBlock + "\n"
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), content)
}

func TestBuildAutoPullsWhenLockfileMissingAndAutoPullTrue(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	writeWorkspaceConfig(t, root, "  auto_pull: true\n")

	if err := dispatchBuild(releasePath, []string{filepath.Join(root, "catalog")}, false); err != nil {
		t.Fatalf("dispatchBuild: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "release.lock")); err != nil {
		t.Errorf("expected auto-pull to write release.lock; got %v", err)
	}
}

func TestBuildHaltsWhenLockfileMissingAndAutoPullFalse(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	writeWorkspaceConfig(t, root, "  auto_pull: false\n")

	err := dispatchBuild(releasePath, []string{filepath.Join(root, "catalog")}, false)
	if err == nil {
		t.Fatal("expected halt on missing-lockfile + auto_pull:false; got nil")
	}
	if !strings.Contains(err.Error(), "auto_pull") || !strings.Contains(err.Error(), "aggrega pull") {
		t.Errorf("error should name auto_pull and the remediation; got %v", err)
	}
}

func TestBuildOfflineWithMissingLockfileHaltsWithFetchNotFound(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	writeWorkspaceConfig(t, root, "  offline: true\n")

	err := dispatchBuild(releasePath, []string{filepath.Join(root, "catalog")}, false)
	if err == nil {
		t.Fatal("expected halt on offline + missing lockfile; got nil")
	}
	if !errors.Is(err, oci.ErrFetchNotFound) {
		t.Errorf("error should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestBuildVerifiesLockfileAndHaltsOnDrift(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	// auto_pull is FALSE: the build is frozen against the committed lockfile and
	// verifies it. (Under auto_pull:true the build's honoring auto-pull reconciles —
	// a mutable local entity re-resolves from its authoritative Tier-1 source and
	// re-pins, so a local drift never reaches this frozen drift-halt; registry-pin
	// drift halts earlier via ErrFetchIntegrity. The drift-halt below is the
	// auto_pull:false frozen-lockfile guarantee.)
	writeWorkspaceConfig(t, root, "  auto_pull: false\n")

	// Prime the cache + lockfile by running an initial explicit pull.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}

	// Tamper with the lockfile's recorded digest for the server entity
	// so the next build's freshly-computed closure no longer matches.
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	for i := range lf.Entities {
		if lf.Entities[i].URI == "server" {
			lf.Entities[i].Digest = "sha256:" + strings.Repeat("0", 64)
		}
	}
	if err := lockfile.WriteLockfile(filepath.Join(root, "release.lock"), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	err = dispatchBuild(releasePath, []string{filepath.Join(root, "catalog")}, false)
	if err == nil {
		t.Fatal("expected ErrLockfileDrift; got nil")
	}
	if !errors.Is(err, lockfile.ErrLockfileDrift) {
		t.Errorf("error should wrap ErrLockfileDrift; got %v", err)
	}
}

func TestBuildSucceedsWhenLockfileMatchesClosure(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	writeWorkspaceConfig(t, root, "  auto_pull: true\n")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}

	// Second build with unchanged catalog + unchanged lockfile must pass
	// the verification step. The build itself may halt on later steps
	// (this fixture has no Manifest mapping that the renderer requires);
	// the test only asserts the lockfile verification gate is cleared.
	err := dispatchBuild(releasePath, []string{filepath.Join(root, "catalog")}, false)
	if err != nil && errors.Is(err, lockfile.ErrLockfileDrift) {
		t.Errorf("unexpected drift on unchanged fixture: %v", err)
	}
}
