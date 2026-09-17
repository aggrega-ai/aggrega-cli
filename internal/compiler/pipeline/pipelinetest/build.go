// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pipelinetest is the single sanctioned way an example/end-to-end test
// compiles a workspace. It runs the real compilation spine
// (pipeline.RunPipeline) with the SAME Options the CLI build uses — crucially
// the recipe-resolution verifier (resolution-completeness) — and the
// SAME catalog-driven Wasm provider dispatch. A test built through this harness
// renders the way `aggrega build` renders and is gated the way a real build is
// gated, so it cannot pass on input a real build would reject.
//
// Hand-assembling the pipeline (resolver.Resolve -> topology.Build ->
// ir.BuildIRContext -> renderer.RenderAll) in a test skips the verifier and any other
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
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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

	// TraceUnredacted turns off the trace's withholding of declared-sensitive
	// data and stamps each collected entry with the announcing header, matching
	// what `aggrega build --trace --trace-unredacted` writes. Meaningful only
	// alongside IncludeTrace. Off by default, which is the withholding shape.
	TraceUnredacted bool

	// PluginPathLookup, when set, overrides the build's pipeline plugin-path
	// resolver entirely, bypassing the production entity-front. Reserved for a
	// negative-path test that needs to inject a canned resolver; an entity-aware
	// fixture leaves it nil and stages its Plugin .wasm co-located beside the
	// entity's plugin.yaml in one of CatalogPaths, so the build's real entity-front
	// (catalog Plugin entity → spec.plugin → co-located tier) fires.
	PluginPathLookup pluginpath.Lookup
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
// build-faithful pipeline (resolution-completeness on) but returns the first error instead of
// failing the test. A negative-path test uses BuildE to assert on the error a
// real build produces — at catalog load, resolution, resolution-completeness, or render — via
// errors.Is/message inspection, rather than treating any failure as a test
// fault. It is build-faithful, not a bypass: the full pipeline runs, so the
// same error surfaces in the same place a real build would surface it.
func BuildE(t *testing.T, in Input) (*pipeline.Result, error) {
	t.Helper()
	return compile(in)
}

// Tier4E runs Steps 1-4 over the same workspace and the same loaded inputs Build
// compiles, through pipeline.RunTier4Pipeline — the entry point the validation
// surfaces use (`aggrega validate --release`, the machine-readable tool surface,
// and the editor daemon all reach it through one semantic-validation core). It is
// the sibling of BuildE for caller-coverage tests: a check whose inputs are
// complete by Step 4 must produce the same verdict on both entry points, and the
// only way to assert that is to run one fixture through both.
//
// It deliberately shares loadInputs with the build path, so the two verdicts
// cannot differ because the harness fed them different documents.
func Tier4E(t *testing.T, in Input) (*pipeline.Tier4Result, error) {
	t.Helper()
	loaded, err := loadInputs(in)
	if err != nil {
		return nil, err
	}
	return pipeline.RunTier4Pipeline(
		context.Background(),
		loaded.inputs,
		loaded.catalogLookup,
		loaded.recipeLookup.Recipe,
		loaded.subAggregateLoader,
	)
}

// loadedWorkspace bundles what both entry points need from one workspace load.
type loadedWorkspace struct {
	inputs             pipeline.Inputs
	catalogLookup      resolver.CatalogLookup
	recipeLookup       renderer.RecipeLookup
	subAggregateLoader topology.SubAggregateLoader
	recipeVerifier     func(*types.Manifest, *types.IRGraph) error
}

// loadInputs loads the catalog and derives the Aggregate and Manifest from the
// Release's spec pointers, exactly as the CLI build does, and assembles the
// pipeline inputs. Shared by every entry point in this package so a build-path
// and a validation-path run over one fixture are guaranteed to see identical
// documents.
func loadInputs(in Input) (*loadedWorkspace, error) {
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
	manifest, err := resolver.ResolveManifestExtends(releaseDir, manRes)
	if err != nil {
		return nil, fmt.Errorf("compose manifest extends chain: %w", err)
	}
	// Gate on Manifest mapping-table consistency exactly as cmd/aggrega/build.go
	// does — same method, same join-and-halt — so a build-faithful test halts
	// wherever a real build would.
	if err := cat.AssertManifestImplementsConsistency(manifest, manRes.Path, cat.ComponentAncestry); err != nil {
		return nil, err
	}
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, cat.AggregateLookup(), nil)
	if err != nil {
		return nil, fmt.Errorf("load aggregate: %w", err)
	}

	// A fixture workspace carries no configuration file, so the defaults are the
	// configuration in force. Supplied explicitly: the graph entry refuses an
	// unsupplied configuration, and a fixture that ran against no configuration at
	// all would be running a different composition from the build it stands in for.
	wsCfg := workspaceconfig.Default()
	return &loadedWorkspace{
		inputs: pipeline.Inputs{
			Release:            rel,
			Manifest:           manifest,
			AggregateRawBytes:  aggRes.Bytes,
			AggregatePath:      aggRes.Path,
			MappingLibraries:   cat.MappingLibraries,
			WorkloadLibraries:  cat.WorkloadLibraries,
			ArtifactsLibraries: cat.ArtifactsLibraries,
			// Arbitrary fixed fixture, not a real version: production derives the
			// compiler-version from the build, but tests inject a constant so the
			// provenance stamp (and the aggrega.ai/compiler-version label it renders
			// into artifacts) stays build-independent and golden output is stable.
			CompilerVersion: "0.1.0",
			// Fixture override (rare): when set, replaces the renderer's pipeline
			// resolver wholesale. Left nil, the build's entity-front fires from the
			// loaded catalog's Plugin accessor, resolving spec.plugin through the
			// cascade — including the co-located tier a fixture stages beside its
			// Plugin entity in the catalog.
			PluginPathLookup: in.PluginPathLookup,
			WorkspaceConfig:  &wsCfg,
			// A fixture workspace has no recorded closure, so no recorded identity
			// was ever going to govern what it resolves; its guests come from the
			// embedded set or from beside their catalog entities. Written rather
			// than omitted, for the same reason the configuration above is: a
			// fixture that stated nothing would be standing in for a build that
			// cannot exist.
			PluginPins: pluginpath.NoPins(),
		},
		catalogLookup:      cat.CatalogLookup(),
		recipeLookup:       cat.RecipeLookupWithManifest(manifest),
		subAggregateLoader: cat.SubAggregateLoader(),
		recipeVerifier:     cat.VerifyRecipeResolution,
	}, nil
}

// compile is the shared core of Build and BuildE. It loads the workspace through
// loadInputs and runs the real pipeline with the recipe-resolution verifier on.
// Returns the first error encountered; the two exported entry points choose
// whether to fail the test or surface it.
func compile(in Input) (*pipeline.Result, error) {
	loaded, err := loadInputs(in)
	if err != nil {
		return nil, err
	}
	outcome, err := pipeline.RunPipeline(
		context.Background(),
		loaded.inputs,
		loaded.catalogLookup,
		loaded.recipeLookup,
		loaded.subAggregateLoader,
		pipeline.Options{
			// The build-faithful bit: resolution-completeness ON,
			// exactly as cmd/aggrega/build.go runs it.
			RecipeResolutionVerifier: loaded.recipeVerifier,
			IncludeTrace:             in.IncludeTrace,
			TraceUnredacted:          in.TraceUnredacted,
		},
	)
	if err != nil {
		return nil, err
	}
	// A harness asks for the build a command line would have produced, so it
	// reports from the finished payload and nothing else: a test handed a partial
	// would assert over an artifact set the run never emitted.
	return outcome.Completed(), nil
}
