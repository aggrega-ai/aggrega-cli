// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestPinGateSuppressesExternalShadow proves the registry-pin gate in the shared
// closure resolver: a top-level Aggregate that the lockfile pins with a real
// resolved version must NOT be seeded as an external entity, because the external
// tier would shadow it with a version-less pin and drift against the lockfile.
// The un-gated resolve (Lockfile: nil) is the pre-unification behavior every read
// path had; the gated resolve (Lockfile: lf) is the correct superset the CLI
// build always had. The test resolves both ways over one fixture and compares the
// lockfile drift verdict.
func TestPinGateSuppressesExternalShadow(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteAggregateFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	// Move both aggregates into the content cache (present-in-cache, absent-from-
	// catalog) and promote the top-level pin to a real resolved version — the
	// post-pull shape of a registry-pulled top-level aggregate. A Tier-1 local pull
	// records a version-less pin, which the gate treats as external; a registry pull
	// records a real version, which is what the gate suppresses from the external
	// seed so it resolves through the pinned tier instead.
	relocateAggregateToCache(t, root, ".aggrega/cache", "myapp")
	relocateAggregateToCache(t, root, ".aggrega/cache", "mysub")
	for i := range lf.Entities {
		if lf.Entities[i].URI == "myapp" {
			lf.Entities[i].ResolvedVersion = "1.0.0"
		}
	}
	for _, e := range lf.Entities {
		if e.URI == "myapp" {
			t.Logf("lockfile pin for myapp (promoted): resolvedVersion=%q", e.ResolvedVersion)
		}
	}

	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := workspaceconfig.ResolveCatalogDirs([]string{catalogDir}, wsCfg, root)

	resolve := func(withLockfile *lockfile.Lockfile) (compilerops.ResolvedClosure, error) {
		view := catalog.NewLazyLookup(root, cacheDir, dirs, nil)
		view.SetPins(catalog.PinsFromLockfile(lf))
		resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
			View:            view,
			ReleasePath:     releasePath,
			WorkspaceRoot:   root,
			WorkspaceConfig: wsCfg,
			Lockfile:        withLockfile,
			Augment:         true,
		})
		if cerr != nil {
			return compilerops.ResolvedClosure{}, cerr
		}
		return *resolved, nil
	}

	ungated, uerr := resolve(nil)
	if uerr != nil {
		t.Fatalf("un-gated resolve: %v", uerr)
	}
	gated, gerr := resolve(lf)
	if gerr != nil {
		t.Fatalf("gated resolve: %v", gerr)
	}

	// Un-gated: the registry-pinned top-level is seeded external, so the closure
	// records it version-less and drifts against the real-version pin.
	ungatedDrift := lockfile.Verify(lf, ungated.Closure)
	if ungatedDrift == nil {
		t.Fatalf("un-gated resolve did not drift; the pin gate would be a no-op — the scenario failed to reproduce the bug")
	}
	t.Logf("UN-GATED drift (reproduced bug): %v", ungatedDrift)

	// Gated: the external seed is suppressed, so the top-level resolves through the
	// pinned tier and the closure matches the lockfile.
	if gatedDrift := lockfile.Verify(lf, gated.Closure); gatedDrift != nil {
		t.Fatalf("gated resolve drifted; the pin gate did not fix it: %v", gatedDrift)
	}
	t.Logf("GATED drift: <nil> (fix confirmed)")
}
