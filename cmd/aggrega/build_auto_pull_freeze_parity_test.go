// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// TestBuildAutoPullFreezeMatchesStandalonePullFreeze proves that the content-pin
// freeze performed by `build --auto-pull`'s pull phase is byte-for-byte
// identical to the freeze a standalone `pull` writes over the same unpinned
// recipe input. Both paths route through the same freeze logic; this is the
// cross-side parity guard that keeps them unified — a divergence in either path
// (a different digest, a different serialization, one path skipping the freeze)
// fails the byte-equality assertion.
//
// What each path writes is the lock beside the entity, so that is what the two
// sides are compared on. It carries no path and no timestamp, which is what lets
// two workspaces in different directories be held to byte-equality at all.
func TestBuildAutoPullFreezeMatchesStandalonePullFreeze(t *testing.T) {
	// One deterministic chart blob, served once. Both workspaces point at the
	// same URL so the frozen digest is identical across paths.
	blob := pinChartTgz(t)
	srv := serveChart(t, blob)
	chartURL := srv.URL + "/pinchart.tgz"
	wantDigest := sha256OCI(blob)

	// Workspace A: standalone `pull` freeze.
	rootA := t.TempDir()
	t.Chdir(rootA)
	releaseA := sourcedPinFixture(t, rootA, chartURL)
	lockA := recipepin.LockPath(filepath.Join(rootA, "catalog", "recipes", "server.yaml"))
	if _, err := os.Stat(lockA); !os.IsNotExist(err) {
		t.Fatalf("workspace A carried a lock beside its recipe before pull; stat err = %v", err)
	}
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: rootA}), ReleasePath: releaseA, CatalogDirs: []string{filepath.Join(rootA, "catalog")}}); err != nil {
		t.Fatalf("standalone pullRun: %v", err)
	}
	lockAfterPull := mustReadFile(t, lockA)

	// Workspace B: `build --auto-pull` freeze via the real flag dispatch path.
	rootB := t.TempDir()
	t.Chdir(rootB)
	releaseB := sourcedPinFixture(t, rootB, chartURL)
	lockB := recipepin.LockPath(filepath.Join(rootB, "catalog", "recipes", "server.yaml"))
	if _, err := os.Stat(lockB); !os.IsNotExist(err) {
		t.Fatalf("workspace B carried a lock beside its recipe before build; stat err = %v", err)
	}
	var diagBuf, outBuf bytes.Buffer
	if err := dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: rootB}), releaseB, []string{filepath.Join(rootB, "catalog")}, false,
		buildDiagOptions{}, buildops.RunOptions{AutoPull: true}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build --auto-pull: %v", err)
	}
	lockAfterBuild := mustReadFile(t, lockB)

	// Both paths must have actually frozen — carry sourceDigests, the fetched
	// digest, and the source URL. This catches a false-pass where neither path
	// wrote a pin (two empty-but-equal locks would otherwise satisfy the
	// byte-equality assertion below).
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"standalone pull", lockAfterPull},
		{"build --auto-pull", lockAfterBuild},
	} {
		s := string(tc.body)
		if !strings.Contains(s, "sourceDigests") {
			t.Fatalf("%s did not freeze sourceDigests into the lock:\n%s", tc.name, s)
		}
		if !strings.Contains(s, wantDigest) {
			t.Errorf("%s lock missing fetched digest %s:\n%s", tc.name, wantDigest, s)
		}
		if !strings.Contains(s, chartURL) {
			t.Errorf("%s lock missing source url %s:\n%s", tc.name, chartURL, s)
		}
	}

	// The parity proof: the two frozen locks are byte-identical.
	if !bytes.Equal(lockAfterPull, lockAfterBuild) {
		t.Errorf("the content-pin freeze diverged between standalone pull and build --auto-pull:\n--- after standalone pull ---\n%s\n--- after build --auto-pull ---\n%s",
			lockAfterPull, lockAfterBuild)
	}
}
