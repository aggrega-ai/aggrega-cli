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
// claims end-to-end build fidelity. Use Build for a positive build, BuildE for
// a negative-path test that asserts on the error a real build produces, and
// set Input.IncludeTrace to assert on per-pass trace layout via Result.Trace.
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
	"fmt"
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
//
// IncludeTrace, when true, turns on per-pass trace collection: the returned
// Result.Trace carries the same relative-path-keyed entries the filesystem
// trace writer would emit (boundaries/<boundaryPath>/<nodeID>/...), so a test
// can assert on trace layout by reading Result.Trace rather than walking a
// temp directory. Off by default — most tests assert only on Artifacts.
type Input struct {
	ReleasePath  string
	CatalogPaths []string
	IncludeTrace bool
}

// Build compiles the workspace named by in through the real pipeline with the
// CLI build's Options (recipe-resolution verifier ON) and the shared provider
// dispatch, returning the pipeline Result (rendered Artifacts + the IR graph,
// plus Trace when in.IncludeTrace is set). It fails the test on any error —
// including a Step-4c resolution-completeness failure, which is the whole
// point: a build-invalid workspace fails here the way it fails a real build.
func Build(t *testing.T, in Input) *pipeline.Result {
	t.Helper()
	result, err := compile(in)
	if err != nil {
		t.Fatalf("pipelinetest.Build: %v", err)
	}
	return result
}

// BuildE is the error-returning sibling of Build: it runs the identical
// build-faithful pipeline (Step 4c on) but returns the first error instead of
// failing the test. A negative-path test uses BuildE to assert on the error a
// real build produces — at catalog load, resolution, Step 4c, or render — via
// errors.Is/message inspection, rather than treating any failure as a test
// fault. It is build-faithful, not a bypass: the full pipeline runs, so the
// same error surfaces in the same place a real build would surface it.
func BuildE(t *testing.T, in Input) (*pipeline.Result, error) {
	t.Helper()
	return compile(in)
}

// compile is the shared core of Build and BuildE. It loads the catalog,
// derives the Aggregate and Manifest from the Release's spec pointers (exactly
// as the CLI build does), and runs the real pipeline with the recipe-
// resolution verifier on. Returns the first error encountered; the two
// exported entry points choose whether to fail the test or surface it.
func compile(in Input) (*pipeline.Result, error) {
	cat, err := catalog.LoadAll("", in.CatalogPaths...)
	if err != nil {
		return nil, fmt.Errorf("catalog load: %w", err)
	}

	rel, err := resolver.LoadRelease(in.ReleasePath)
	if err != nil {
		return nil, fmt.Errorf("load release: %w", err)
	}
	releaseDir := filepath.Dir(in.ReleasePath)
	manRes, err := resolver.ResolveManifest(releaseDir, rel.Spec.Manifest)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	manifest, err := resolver.ResolveManifestExtends(releaseDir, manRes.Manifest)
	if err != nil {
		return nil, fmt.Errorf("compose manifest extends chain: %w", err)
	}
	// Gate on Manifest mapping-table consistency exactly as cmd/aggrega/build.go
	// does — same method, same join-and-halt — so a build-faithful test halts
	// wherever a real build would.
	if err := cat.AssertManifestImplementsConsistency(manifest, manRes.Path); err != nil {
		return nil, err
	}
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, cat.AggregateLookup(), nil)
	if err != nil {
		return nil, fmt.Errorf("load aggregate: %w", err)
	}
	aggBytes := aggRes.Bytes

	buildLookup := cat.CatalogLookup()
	buildLookup.ProviderInfraSchema = cat.ProviderInfraSchemaResolver(manifest)
	result, err := pipeline.RunPipeline(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     aggRes.Path,
			MappingLibraries:  cat.MappingLibraries,
			// Arbitrary fixed fixture, not a real version: production derives the
			// compiler-version from the build, but tests inject a constant so the
			// provenance stamp (and the aggrega.ai/compiler-version label it renders
			// into artifacts) stays build-independent and golden output is stable.
			CompilerVersion: "0.1.0",
		},
		buildLookup,
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		enginefactory.Dispatch,
		pipeline.Options{
			// The build-faithful bit: Step 4c (resolution-completeness) ON,
			// exactly as cmd/aggrega/build.go runs it.
			RecipeResolutionVerifier: cat.VerifyRecipeResolution,
			IncludeTrace:             in.IncludeTrace,
		},
	)
	if err != nil {
		return nil, err
	}
	return result, nil
}
