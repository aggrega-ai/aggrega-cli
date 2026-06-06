// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pipelinetest is the single sanctioned way an example/end-to-end test
// compiles a workspace. It runs the real compilation spine
// (pipeline.RunPipeline) with the SAME Options the CLI build uses — crucially
// the recipe-resolution verifier (resolution-completeness, "Step 4c") — and the
// SAME provider dispatch (enginefactory.Dispatch). A test built through this
// harness renders the way `aggrega build` renders and is gated the way a real
// build is gated, so it cannot pass on input a real build would reject.
//
// Hand-assembling the pipeline (resolver.Resolve -> topology.Build ->
// ir.BuildIR -> renderer.RenderAll) in a test skips the verifier and any other
// gate RunPipeline runs between IR and render; do not do that for a test that
// claims end-to-end build fidelity. Use Build.
//
// Scope: this harness loads a self-contained, local catalog (catalog.LoadAll)
// and passes a nil Lockfile / empty WorkspaceRoot — the local/sourceless path
// every example fixture under internal/compiler uses. The lockfile,
// auto-pull, and remote-library-bridging machinery the CLI build also runs is
// out-of-closure-resilience and remote-source resolution — orthogonal to the
// Step-4c rendering fidelity this harness guarantees. A fixture needing a
// pinned remote source is out of scope for this helper.
package pipelinetest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// Input identifies a workspace to compile. ReleasePath is the Release YAML;
// the Aggregate and Manifest are derived from the Release's spec.aggregate /
// spec.manifest names (resolved against the Release's directory), exactly as
// the CLI build derives them. CatalogPaths are the catalog directories/files
// to load (passed to catalog.LoadAll).
type Input struct {
	ReleasePath  string
	CatalogPaths []string
}

// Build compiles the workspace named by in through the real pipeline with the
// CLI build's Options (recipe-resolution verifier ON) and the shared provider
// dispatch, returning the pipeline Result (rendered Artifacts + the IR graph).
// It fails the test on any error — including a Step-4c resolution-completeness
// failure, which is the whole point: a build-invalid workspace fails here the
// way it fails a real build.
func Build(t *testing.T, in Input) *pipeline.Result {
	t.Helper()

	cat, err := catalog.LoadAll("", in.CatalogPaths...)
	if err != nil {
		t.Fatalf("pipelinetest.Build: catalog load: %v", err)
	}

	rel, err := resolver.LoadRelease(in.ReleasePath)
	if err != nil {
		t.Fatalf("pipelinetest.Build: load release: %v", err)
	}
	releaseDir := filepath.Dir(in.ReleasePath)
	aggPath := filepath.Join(releaseDir, rel.Spec.Aggregate+".yaml")
	manifestPath := filepath.Join(releaseDir, rel.Spec.Manifest+".yaml")

	manifest, err := resolver.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("pipelinetest.Build: load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("pipelinetest.Build: read aggregate: %v", err)
	}

	result, err := pipeline.RunPipeline(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     aggPath,
			MappingLibraries:  cat.MappingLibraries,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		enginefactory.Dispatch,
		pipeline.Options{
			// The build-faithful bit: Step 4c (resolution-completeness) ON,
			// exactly as cmd/aggrega/build.go runs it.
			RecipeResolutionVerifier: cat.VerifyRecipeResolution,
		},
	)
	if err != nil {
		t.Fatalf("pipelinetest.Build: RunPipeline (build-faithful, Step 4c on): %v", err)
	}
	return result
}
