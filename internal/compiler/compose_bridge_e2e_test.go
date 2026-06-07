// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end catalog-driven proof of the
// core/compose-bridge Feature + FeatureRecipe.
//
// This test depends on both aggrega-cli AND aggrega-hub submodules being
// initialized in the umbrella checkout. Contributors running tests via
// `cd aggrega-cli && go test ./...` without `git submodule update --init`
// from the umbrella will see this test fail with a missing-catalog-entry
// error. Run from the umbrella or initialise submodules first.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// TestComposeBridge_CatalogDriven_E2E exercises the canonical catalog entries
// for core/compose-bridge end-to-end through the full Step 1-5 pipeline. Closes
// the inject-path verification gap that the renderer-level proof case at
// boundary_fr_proof_test.go does not cover.
//
// It compiles through the build-faithful harness (pipelinetest.Build), so it
// runs the real pipeline with the CLI build's Options — including Step 4c
// resolution-completeness. The bridge feature `core/compose-bridge` therefore
// resolves to its real shipped FeatureRecipe (loaded from aggrega-hub below),
// which renders the gateway service and rewrites the consumer's DB_HOST — the
// rendered output this test asserts on, not a hand-built graph.
//
// Fixture shape: a Release with parent boundary "cluster" attaching the bridge
// feature, two child boundaries "services-net" (api) and "data-net" (db)
// declaring directory: "{{ .parent_boundary.directory }}". All contributors
// merge at the parent's evaluated path.
//
// Asserts:
//  1. One docker-compose.yaml at dist/cluster/compose/.
//  2. That file contains services 'api', 'db', and 'aggrega-gw'.
//  3. The 'api' service's environment.DB_HOST equals 'aggrega-gw:15432'
//     (the per-consumer injectMapping rewrite — the value-add over the
//     renderer-level proof case, which doesn't exercise injectMapping at all).
func TestComposeBridge_CatalogDriven_E2E(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath: "testdata/compose-bridge/release.yaml",
		CatalogPaths: []string{
			"testdata/compose-bridge/catalog/base",
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components.yaml",
		},
	})

	// Assertion 1: exactly one docker-compose.yaml at the merged path.
	const wantPath = "dist/cluster/compose/docker-compose.yaml"
	body, ok := result.Artifacts[wantPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		t.Fatalf("expected merged compose at %q; got paths %v", wantPath, paths)
	}

	merged := string(body)

	// Assertion 2: services api + db + aggrega-gw merged.
	for _, svcName := range []string{"api:", "db:", "aggrega-gw:"} {
		if !strings.Contains(merged, svcName) {
			t.Errorf("merged docker-compose.yaml missing service %q\n--- body ---\n%s", svcName, merged)
		}
	}
	if !strings.Contains(merged, "nginx:alpine") {
		t.Errorf("expected nginx:alpine (bridge image) in merged compose:\n%s", merged)
	}

	// Assertion 3: per-consumer injectMapping rewrote api.DB_HOST to
	// "aggrega-gw:15432". This is the inject-path verification — the
	// renderer-level proof case at boundary_fr_proof_test.go does NOT
	// exercise injectMapping; this is the first test to cover it.
	if !strings.Contains(merged, "DB_HOST") {
		t.Fatalf("expected DB_HOST env var in merged compose (injectMapping should have written it):\n%s", merged)
	}
	if !strings.Contains(merged, "aggrega-gw:15432") {
		t.Errorf("expected api's DB_HOST to be rewritten to 'aggrega-gw:15432' (per FR injectMapping with .infra.targetVar=DB_HOST + .infra.mappedPort=15432):\n%s", merged)
	}
}
