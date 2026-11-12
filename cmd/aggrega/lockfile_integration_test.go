// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// writeWorkspaceConfig writes a .aggrega-config.yaml whose full body is
// configBody (verbatim). Top-level settings (default_registry, offline,
// auto_pull, artifact_cache_dir) sit at the root; catalog_dirs go under a
// `workspace:` block.
func writeWorkspaceConfig(t *testing.T, root, configBody string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), configBody+"\n")
}

func TestBuildAutoPullsWhenLockfileMissingAndAutoPullTrue(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: true\n")

	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{filepath.Join(root, "catalog")}, false, os.Stderr, os.Stdout); err != nil {
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
	writeWorkspaceConfig(t, root, "auto_pull: false\n")

	err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{filepath.Join(root, "catalog")}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("expected halt on missing-lockfile + auto_pull:false; got nil")
	}
	if !strings.Contains(err.Error(), "auto_pull") || !strings.Contains(err.Error(), "aggrega pull") {
		t.Errorf("error should name auto_pull and the remediation; got %v", err)
	}
}

// TestBuildDefaultReadonlyHaltsAndWritesNothing is the core read-only-default
// proof: with no auto_pull configured (the default) and no lockfile present, a
// plain build halts with the pull-first remediation and mutates nothing — it
// writes no lockfile and leaves the recipe source byte-identical. Build is a
// reader of pins, never a writer.
func TestBuildDefaultReadonlyHaltsAndWritesNothing(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	// Empty config: the default posture applies (auto_pull off).
	writeWorkspaceConfig(t, root, "")

	recipePath := filepath.Join(root, "catalog", "recipes", "server.yaml")
	lockfilePath := filepath.Join(root, "release.lock")
	recipeBefore, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatalf("read recipe source: %v", err)
	}

	err = dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{filepath.Join(root, "catalog")}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("expected read-only halt on missing lockfile under the default posture; got nil")
	}
	if !strings.Contains(err.Error(), "aggrega pull") {
		t.Errorf("halt should name the `aggrega pull` remediation; got %v", err)
	}

	// The build must have written NOTHING: no lockfile, and the recipe source
	// is untouched (no frozen pins). Pull is the sole writer of source pins.
	if _, statErr := os.Stat(lockfilePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("default build must not create a lockfile; stat err = %v", statErr)
	}
	recipeAfter, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatalf("re-read recipe source: %v", err)
	}
	if string(recipeAfter) != string(recipeBefore) {
		t.Errorf("default build mutated the recipe source; it must be byte-identical")
	}
}

// TestBuildDefaultReadonlyWithLockfileWritesNoSource proves the read-only
// default builds against a pre-existing complete lockfile without falling into
// the missing-lockfile halt and without mutating source: the lockfile and the
// recipe source are byte-identical across the build.
func TestBuildDefaultReadonlyWithLockfileWritesNoSource(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	// Empty config: the default posture applies (auto_pull off).
	writeWorkspaceConfig(t, root, "")

	// Create the lockfile out-of-band via an explicit pull (the sole writer).
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}

	recipePath := filepath.Join(root, "catalog", "recipes", "server.yaml")
	lockfilePath := filepath.Join(root, "release.lock")
	recipeBefore, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatalf("read recipe source: %v", err)
	}
	lockBefore, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}

	// The default build must use the existing lockfile read-only. It must not
	// hit the missing-lockfile halt and must not report drift.
	err = dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{filepath.Join(root, "catalog")}, false, os.Stderr, os.Stdout)
	if err != nil && strings.Contains(err.Error(), "lockfile") && strings.Contains(err.Error(), "missing") {
		t.Fatalf("default build with a present lockfile must not halt as missing; got %v", err)
	}
	if err != nil && errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Errorf("unexpected drift on unchanged fixture: %v", err)
	}

	recipeAfter, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatalf("re-read recipe source: %v", err)
	}
	lockAfter, err := os.ReadFile(lockfilePath)
	if err != nil {
		t.Fatalf("re-read lockfile: %v", err)
	}
	if string(recipeAfter) != string(recipeBefore) {
		t.Errorf("default build mutated the recipe source; it must be byte-identical")
	}
	if string(lockAfter) != string(lockBefore) {
		t.Errorf("default build mutated the lockfile; it must be byte-identical")
	}
}

// TestBuildOfflineWithMissingLockfileHaltsNamingThePullThatFillsIt holds a build
// asked for no fill to the read-only default's halt under the offline posture
// too. The posture adds no halt of its own: the remedy is the same pull, or
// --auto-pull, which under the posture fills from what the machine already holds
// — and nothing is fetched or written on the way to saying so.
func TestBuildOfflineWithMissingLockfileHaltsNamingThePullThatFillsIt(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := authoringFixture(t, root)
	writeWorkspaceConfig(t, root, "offline: true\n")

	err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{filepath.Join(root, "catalog")}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("expected halt on offline + missing lockfile; got nil")
	}
	if !strings.Contains(err.Error(), "aggrega pull --release") || !strings.Contains(err.Error(), "--auto-pull") {
		t.Errorf("error should name the pull that fills the lockfile and --auto-pull; got %v", err)
	}
	if errors.Is(err, oci.ErrFetchNotFound()) {
		t.Errorf("error carries a fetch that found nothing, but no fill ran and nothing was fetched; got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "release.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a build asked for no fill must not create a lockfile; stat err = %v", statErr)
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
	writeWorkspaceConfig(t, root, "auto_pull: false\n")

	// Prime the cache + lockfile by running an initial explicit pull.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
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

	err = dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{filepath.Join(root, "catalog")}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("expected ErrLockfileDrift; got nil")
	}
	if !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Errorf("error should wrap ErrLockfileDrift; got %v", err)
	}
}

func TestBuildSucceedsWhenLockfileMatchesClosure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath := authoringFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: true\n")

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{filepath.Join(root, "catalog")}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}

	// Second build with unchanged catalog + unchanged lockfile must pass
	// the verification step. The build itself may halt on later steps
	// (this fixture has no Manifest mapping that the renderer requires);
	// the test only asserts the lockfile verification gate is cleared.
	err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{filepath.Join(root, "catalog")}, false, os.Stderr, os.Stdout)
	if err != nil && errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Errorf("unexpected drift on unchanged fixture: %v", err)
	}
}
