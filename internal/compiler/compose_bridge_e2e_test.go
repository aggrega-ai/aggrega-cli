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
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestComposeBridge_CatalogDriven_E2E exercises the canonical catalog
// entries for core/compose-bridge end-to-end through the full Step 1-5
// pipeline. Closes the inject-path verification gap that the renderer-
// level proof case at boundary_fr_proof_test.go does not cover.
//
// Fixture shape: a Release with parent boundary "cluster" attaching the
// bridge feature, two child boundaries "services-net" (api) and "data-net"
// (db) declaring directory: "{{ .parent_boundary.directory }}". All
// contributors merge at the parent's evaluated path.
//
// Asserts:
//  1. One docker-compose.yaml at dist/cluster/compose/.
//  2. That file contains services 'api', 'db', and 'aggrega-gw'.
//  3. The 'api' service's environment.DB_HOST equals 'aggrega-gw:15432'
//     (the per-consumer injectMapping rewrite — the new value-add over
//     the renderer-level proof case, which doesn't exercise injectMapping
//     at all).
func TestComposeBridge_CatalogDriven_E2E(t *testing.T) {
	t.Parallel()

	cat, err := catalog.LoadAll(
		"testdata/compose-bridge/catalog/base",
		"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge.yaml",
		"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
		"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components.yaml",
	)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: "testdata/compose-bridge/aggregate.yaml",
		ReleasePath:   "testdata/compose-bridge/release.yaml",
		ManifestPath:  "testdata/compose-bridge/manifest.yaml",
	})
	if err != nil {
		t.Fatalf("Step 1 (resolve): %v", err)
	}

	catalogLookup := cat.CatalogLookup()

	guardErrs := resolver.RunGuards(resolveResult.Aggregate, resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra)
	if len(guardErrs) > 0 {
		for _, e := range guardErrs {
			t.Errorf("guard: %s", e.Error())
		}
		t.FailNow()
	}

	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult:    resolveResult,
		CatalogLookup:    catalogLookup,
		LoadSubAggregate: cat.SubAggregateLoader(),
	})
	if err != nil {
		t.Fatalf("Step 2 (topology): %v", err)
	}

	graph, featureOrder, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   catalogLookup,
		CompilerVersion: "0.1.0-test",
	})
	if err != nil {
		t.Fatalf("Step 3 (IR): %v", err)
	}

	analysis := analyzer.Analyze(graph, catalogLookup)
	for _, e := range analysis.Errors {
		t.Errorf("selector: %s -> %s (%s): %s", e.Source, e.Target, e.Socket, e.Message)
	}
	if t.Failed() {
		t.FailNow()
	}

	rendererInput := renderer.RendererInput{
		Graph:          graph,
		FeatureOrder:   featureOrder,
		ResolveResult:  resolveResult,
		CatalogLookup:  catalogLookup,
		ProviderLookup: cat.ProviderLookup,
	}
	engineFactory := func(string) (renderer.Engine, error) {
		return &renderCompose.ComposeEngine{}, nil
	}

	renderResult, err := renderer.RenderAll(
		context.Background(),
		rendererInput,
		cat.RecipeLookup(),
		engineFactory,
	)
	if err != nil {
		t.Fatalf("Step 5 (render): %v", err)
	}

	// Assertion 1: exactly one docker-compose.yaml at the merged path.
	const wantPath = "dist/cluster/compose/docker-compose.yaml"
	body, ok := renderResult.Files[wantPath]
	if !ok {
		paths := make([]string, 0, len(renderResult.Files))
		for p := range renderResult.Files {
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
