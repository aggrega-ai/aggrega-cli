// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a scope-attached feature's
// `uses` block projects through the full Step 1-5 pipeline to rendered,
// injected provides.

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

// TestScopeFeatureUses_RendersInjectedProvides closes the render-level loop
// for the projected case: a feature attached subsystem-wide carries a `uses`
// edge (backend → db, injecting db's `host` provides as DB_HOST), and that
// edge must project to the subsystem's component (api) all the way to
// rendered output — api's DB_HOST environment variable carries db's host.
//
// This complements the topology-level projection proof (which asserts the
// edge is produced from YAML) and the per-component compose-bridge E2E
// (which renders injected provides for a uses authored directly on the
// component). Here the uses is authored on the scope feature, not on api,
// and the value reaches the rendered artifact — the projected-case loop.
//
// The api-recipe renders DB_HOST from `.context.inject.env.DB_HOST`; with no
// bridge FR in this fixture, the value is db's plain host provides
// (db's instance id, "db"), proving the injection came from the projected
// edge rather than any per-consumer rewrite. Neutralizing the scope
// projection drops the edge, so DB_HOST renders empty — the green is
// non-vacuous.
func TestScopeFeatureUses_RendersInjectedProvides(t *testing.T) {
	t.Parallel()

	cat, err := catalog.LoadAll("",
		"testdata/scope-feature-uses/catalog/base",
	)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: "testdata/scope-feature-uses/aggregate.yaml",
		ReleasePath:   "testdata/scope-feature-uses/release.yaml",
		ManifestPath:  "testdata/scope-feature-uses/manifest.yaml",
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

	for _, svcName := range []string{"api:", "db:"} {
		if !strings.Contains(merged, svcName) {
			t.Errorf("merged docker-compose.yaml missing service %q\n--- body ---\n%s", svcName, merged)
		}
	}

	// The projected edge injected db's host provides into api's DB_HOST. With
	// no bridge rewrite, the value is db's instance id ("db"). An empty/absent
	// DB_HOST would mean the projection did not reach render.
	if !strings.Contains(merged, "DB_HOST: db") {
		t.Errorf("expected api's DB_HOST to be injected with db's host provides ('db') via the subsystem feature's projected uses edge:\n%s", merged)
	}
}
