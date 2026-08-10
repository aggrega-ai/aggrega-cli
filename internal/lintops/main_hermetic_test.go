// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestMain isolates the per-user homes for this test binary. Linting resolves a
// workspace, and every workspace resolution loads the global configuration layer
// beneath the tree's own files — so without this the suite would merge the
// developer's own configuration under its temporary fixtures, and a lint verdict
// would depend on the machine it ran on.
func TestMain(m *testing.M) {
	os.Exit(hometest.RunHermetic(m))
}

// TestConfigHomeRedirectIsInEffect is the passing control for the redirect: it
// proves the production resolver reads the redirected configuration home, not
// merely that the environment points somewhere temporary. A redirect that is
// wired but ineffective — because the code reads a third location, or reads
// nothing at all — passes an environment-level assertion and fails this one.
//
// It plants a distinguishable value in the redirected global configuration file
// and asserts a production resolve over a freshly constructed workspace returns
// it, then removes the file and asserts the value reverts. The second half is
// what makes the first non-vacuous: without it, a resolver that happened to
// return the planted number for an unrelated reason would look like proof.
//
// The planted key caps the size of an in-memory parse cache. It is chosen
// because it changes no lint, render or validation outcome, so the plant cannot
// perturb a concurrently running case in this binary while it is in place.
func TestConfigHomeRedirectIsInEffect(t *testing.T) {
	// Not parallel: it writes to the configuration home the whole binary shares.
	hometest.AssertRedirected(t)

	const plantedMaxEntries = 424242

	globalDir := filepath.Join(hometest.ConfigHome(), "aggrega")
	if err := os.MkdirAll(globalDir, 0o700); err != nil {
		t.Fatalf("create the redirected global config directory: %v", err)
	}
	globalPath := filepath.Join(globalDir, "config.yaml")

	// A constructed workspace, so the resolve under test reaches nothing the
	// developer owns: the marker caps root discovery at this directory.
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("seed the constructed workspace marker: %v", err)
	}

	baseline, err := workspaceconfig.Resolve(ws, "")
	if err != nil {
		t.Fatalf("resolve before planting: %v", err)
	}
	if got := baseline.Config().ParseCache.MaxEntries; got == plantedMaxEntries {
		t.Fatalf("the default parse-cache entry cap is already %d; the planted value cannot distinguish the redirected home from anything", got)
	}

	if err := os.WriteFile(globalPath, []byte("parse_cache:\n  max_entries: 424242\n"), 0o600); err != nil {
		t.Fatalf("plant the distinguishable global config: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(globalPath) })

	planted, err := workspaceconfig.Resolve(ws, "")
	if err != nil {
		t.Fatalf("resolve after planting: %v", err)
	}
	if got := planted.Config().ParseCache.MaxEntries; got != plantedMaxEntries {
		t.Fatalf("parse-cache entry cap = %d, want the planted %d; the production resolver did not read the redirected configuration home at %s", got, plantedMaxEntries, globalPath)
	}

	if err := os.Remove(globalPath); err != nil {
		t.Fatalf("remove the planted global config: %v", err)
	}
	reverted, err := workspaceconfig.Resolve(ws, "")
	if err != nil {
		t.Fatalf("resolve after removing the plant: %v", err)
	}
	if got := reverted.Config().ParseCache.MaxEntries; got == plantedMaxEntries {
		t.Fatalf("parse-cache entry cap is still the planted %d after the file was removed; the assertion above proves nothing about where the value came from", got)
	}
}
