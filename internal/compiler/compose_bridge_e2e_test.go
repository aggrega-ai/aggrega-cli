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
	"encoding/base64"
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

	// Assertion 3: the boundary->component env channel rewrote api.DB_HOST to
	// the gateway at the edge-derived port. db provides port 5432 and there is
	// no collision, so the listen port (and thus the rewrite) is 5432.
	if !strings.Contains(merged, "DB_HOST: aggrega-gw:5432") {
		t.Errorf("expected api's DB_HOST rewritten to 'aggrega-gw:5432' (edge-derived port):\n%s", merged)
	}

	// Assertion 4: the gateway carries the compiler-generated nginx stream
	// config, base64-delivered via command: — one server block listening on the
	// assigned port and proxy_pass-ing the edge-derived upstream (db:5432). This
	// is the functional bridge: a revert of the generation would leave aggrega-gw
	// a bare nginx with no stream config.
	conf := decodeBridgeStreamConfig(t, merged)
	for _, want := range []string{"stream {", "listen 5432;", "proxy_pass db:5432;"} {
		if !strings.Contains(conf, want) {
			t.Errorf("gateway stream config missing %q; decoded config:\n%s", want, conf)
		}
	}
}

// decodeBridgeStreamConfig pulls the base64-encoded nginx config out of the
// aggrega-gw service's generated command and decodes it.
func decodeBridgeStreamConfig(t *testing.T, compose string) string {
	t.Helper()
	const echoMarker = "echo "
	i := strings.Index(compose, echoMarker)
	if i < 0 {
		t.Fatalf("no bridge gateway command (echo <b64>) in compose:\n%s", compose)
		return ""
	}
	rest := compose[i+len(echoMarker):]
	j := strings.Index(rest, " | base64 -d")
	if j < 0 {
		t.Fatalf("malformed bridge gateway command (no base64 decode) in compose:\n%s", compose)
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest[:j]))
	if err != nil {
		t.Fatalf("bridge gateway command base64 decode: %v", err)
		return ""
	}
	return string(decoded)
}
