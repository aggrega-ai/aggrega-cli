// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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

// TestOptionalDiscriminator_E2E exercises the optional template helper
// through the full Step 1-5 pipeline against three Release variants:
//
//   - absent: the Release omits infra.replicas entirely; the rendered
//     output carries the fallback value (1).
//   - zero: the Release authors infra.replicas: 0; the rendered output
//     carries 0 — the case that `| default 1` would have silently
//     overridden to 1.
//   - three: the Release authors infra.replicas: 3; the rendered output
//     carries 3 (positive control for the present-and-non-zero path).
//
// The Recipe template uses `optional .infra "replicas" 1` rather than
// `.infra.replicas | default 1`. The discriminator behavior is what the
// helper exists for; this test pins it end-to-end.
func TestOptionalDiscriminator_E2E(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		releaseFile  string
		wantReplicas string
	}{
		{
			name:         "absent_falls_back",
			releaseFile:  "testdata/optional-discriminator/release-absent.yaml",
			wantReplicas: "REPLICA_COUNT: \"1\"",
		},
		{
			name:         "authored_zero_preserved",
			releaseFile:  "testdata/optional-discriminator/release-zero.yaml",
			wantReplicas: "REPLICA_COUNT: \"0\"",
		},
		{
			name:         "authored_three_preserved",
			releaseFile:  "testdata/optional-discriminator/release-three.yaml",
			wantReplicas: "REPLICA_COUNT: \"3\"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := runOptionalDiscriminatorPipeline(t, tc.releaseFile)
			merged := joinFiles(files)
			if !strings.Contains(merged, tc.wantReplicas) {
				t.Errorf("expected %q in rendered output; got:\n%s", tc.wantReplicas, merged)
			}
		})
	}
}

// runOptionalDiscriminatorPipeline runs Step 1-5 for one Release variant
// against the optional-discriminator fixture and returns the rendered
// file set.
func runOptionalDiscriminatorPipeline(t *testing.T, releasePath string) map[string][]byte {
	t.Helper()

	cat, err := catalog.LoadAll("", 
		"testdata/optional-discriminator/catalog/components",
		"testdata/optional-discriminator/catalog/recipes",
		"testdata/optional-discriminator/catalog/providers",
	)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: "testdata/optional-discriminator/aggregate.yaml",
		ReleasePath:   releasePath,
		ManifestPath:  "testdata/optional-discriminator/manifest.yaml",
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

	return renderResult.Files
}

// joinFiles concatenates all rendered file bodies into a single string
// for substring grepping in assertions.
func joinFiles(files map[string][]byte) string {
	var b strings.Builder
	for _, body := range files {
		b.Write(body)
		b.WriteString("\n")
	}
	return b.String()
}
