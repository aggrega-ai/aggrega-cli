// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// loadFullstackInputs loads the fullstack-app fixture into the RunPipeline
// input shape the production build path uses. Shared by the skip-render
// proofs so both runs feed byte-identical inputs.
func loadFullstackInputs(t *testing.T) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, *catalog.Catalog) {
	t.Helper()
	rel, err := resolver.LoadRelease(fullstackReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fullstackManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(fullstackAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.LoadAll("", fullstackCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d resolution error(s); first: %v", len(extErrs), extErrs[0])
	}
	inputs := pipeline.Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: aggBytes,
		AggregatePath:     fullstackAggregatePath,
		WorkspaceConfig:   workspaceconfig.Default(),
		CompilerVersion:   fixtureCompilerVersion,
	}
	return inputs, wiredLookup(cat, manifest), cat.RecipeLookupWithManifest(manifest), cat
}

// TestRunPipelineSkipRenderMatchesFullRenderGraph is the load-bearing
// proof for the render-skip dial: for identical inputs, the IR graph
// RunPipeline returns under SkipRender is byte-identical to the graph a
// full render returns (render never mutates the graph), and SkipRender
// never enters the provider render stage — the sole path to rendering-plugin
// (wasm) compilation, load, and artifact emission. The observable proof is
// that a full render emits artifacts while SkipRender emits none, from the
// same inputs and the same graph.
func TestRunPipelineSkipRenderMatchesFullRenderGraph(t *testing.T) {
	t.Parallel()

	// Full render: the render stage runs and emits artifacts.
	inFull, lookupFull, recipeFull, catFull := loadFullstackInputs(t)
	full, err := pipeline.RunPipeline(
		context.Background(), inFull, lookupFull, recipeFull,
		catFull.SubAggregateLoader(), pipeline.Options{},
	)
	if err != nil {
		t.Fatalf("RunPipeline (full render): %v", err)
	}

	// Skip render: the render stage must not run. The run stops after Step 4b,
	// so no artifacts are emitted.
	inSkip, lookupSkip, recipeSkip, catSkip := loadFullstackInputs(t)
	skip, err := pipeline.RunPipeline(
		context.Background(), inSkip, lookupSkip, recipeSkip,
		catSkip.SubAggregateLoader(), pipeline.Options{SkipRender: true},
	)
	if err != nil {
		t.Fatalf("RunPipeline (SkipRender): %v", err)
	}

	if len(full.Artifacts) == 0 {
		t.Fatal("full render produced no artifacts; fixture does not exercise render — the skip proof would be vacuous")
	}
	if len(skip.Artifacts) != 0 {
		t.Errorf("SkipRender produced %d artifact(s); render (the only rendering-plugin load path) must not run", len(skip.Artifacts))
	}

	fullBytes, err := json.Marshal(full.IRGraph)
	if err != nil {
		t.Fatalf("marshal full IR graph: %v", err)
	}
	skipBytes, err := json.Marshal(skip.IRGraph)
	if err != nil {
		t.Fatalf("marshal skip IR graph: %v", err)
	}
	if !bytes.Equal(fullBytes, skipBytes) {
		t.Errorf("IR graph differs between full render and SkipRender:\n full: %s\n skip: %s", fullBytes, skipBytes)
	}
}
