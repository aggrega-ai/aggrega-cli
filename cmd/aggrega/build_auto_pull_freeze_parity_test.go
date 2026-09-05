// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
)

// TestBuildAutoPullFreezeMatchesStandalonePullFreeze proves that the recipe-pin
// freeze performed by `build --auto-pull`'s pull phase is byte-for-byte
// identical to the freeze a standalone `pull` writes over the same unpinned
// recipe input. Both paths route through the same freeze logic; this is the
// cross-side parity guard that keeps them unified — a divergence in either path
// (a different digest, a different serialization, one path skipping the freeze)
// fails the byte-equality assertion.
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
	recipeA := filepath.Join(rootA, "catalog", "recipes", "server.yaml")
	if strings.Contains(string(mustReadFile(t, recipeA)), "sourceDigests") {
		t.Fatalf("workspace A recipe carried sourceDigests before pull")
	}
	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: rootA}), ReleasePath: releaseA, CatalogDirs: []string{filepath.Join(rootA, "catalog")}}); err != nil {
		t.Fatalf("standalone pullRun: %v", err)
	}
	recipeAfterPull := mustReadFile(t, recipeA)

	// Workspace B: `build --auto-pull` freeze via the real flag dispatch path.
	rootB := t.TempDir()
	t.Chdir(rootB)
	releaseB := sourcedPinFixture(t, rootB, chartURL)
	recipeB := filepath.Join(rootB, "catalog", "recipes", "server.yaml")
	if strings.Contains(string(mustReadFile(t, recipeB)), "sourceDigests") {
		t.Fatalf("workspace B recipe carried sourceDigests before build")
	}
	var diagBuf, outBuf bytes.Buffer
	if err := dispatchBuildWithDiag(resolveTempWorkspace(t, tempWorkspaceOptions{Root: rootB}), releaseB, []string{filepath.Join(rootB, "catalog")}, false,
		buildDiagOptions{}, buildops.RunOptions{AutoPull: true}, &diagBuf, &outBuf); err != nil {
		t.Fatalf("build --auto-pull: %v", err)
	}
	recipeAfterBuild := mustReadFile(t, recipeB)

	// Both paths must have actually frozen — carry sourceDigests, the fetched
	// digest, and the source URL. This catches a false-pass where neither path
	// wrote a pin (two empty-but-equal recipes would otherwise satisfy the
	// byte-equality assertion below).
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"standalone pull", recipeAfterPull},
		{"build --auto-pull", recipeAfterBuild},
	} {
		s := string(tc.body)
		if !strings.Contains(s, "sourceDigests") {
			t.Fatalf("%s did not freeze sourceDigests into the recipe:\n%s", tc.name, s)
		}
		if !strings.Contains(s, wantDigest) {
			t.Errorf("%s recipe missing fetched digest %s:\n%s", tc.name, wantDigest, s)
		}
		if !strings.Contains(s, chartURL) {
			t.Errorf("%s recipe missing source url %s:\n%s", tc.name, chartURL, s)
		}
	}

	// The parity proof: the two frozen recipes are byte-identical.
	if !bytes.Equal(recipeAfterPull, recipeAfterBuild) {
		t.Errorf("recipe-pin freeze diverged between standalone pull and build --auto-pull:\n--- after standalone pull ---\n%s\n--- after build --auto-pull ---\n%s",
			recipeAfterPull, recipeAfterBuild)
	}
}
